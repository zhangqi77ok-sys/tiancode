"""Skill discovery, frontmatter parsing, and prompt formatting."""

import os
from pathlib import Path
from typing import Dict, List, Optional
import yaml

from tiancode.skills.models import Skill


class SkillLoader:
    """Discovers, parses, and formats pluggable skills from workspace and global directories."""

    def __init__(
        self,
        workspace_dir: Optional[Path] = None,
        global_skills_dir: Optional[Path] = None,
    ) -> None:
        self.workspace_dir = (workspace_dir or Path(".")).resolve()
        self.global_skills_dir = (
            global_skills_dir or (Path.home() / ".tiancode" / "skills")
        ).resolve()

    def parse_skill_file(self, skill_file: Path, is_global: bool = False) -> Optional[Skill]:
        """Parse a SKILL.md file, extracting YAML frontmatter and markdown body."""
        from tiancode.core.path_safety import is_shared_hardlink

        if is_shared_hardlink(skill_file) or not skill_file.is_file():
            return None

        try:
            content = skill_file.read_text(encoding="utf-8", errors="replace")
        except Exception:
            return None

        fallback_name = skill_file.parent.name
        fallback_desc = f"Domain skill for {fallback_name}"

        name = fallback_name
        description = fallback_desc

        if content.startswith("---"):
            parts = content.split("---", 2)
            if len(parts) >= 3:
                yaml_block = parts[1].strip()
                try:
                    meta = yaml.safe_load(yaml_block)
                    if isinstance(meta, dict):
                        name = str(meta.get("name", fallback_name)).strip()
                        description = str(meta.get("description", fallback_desc)).strip()
                except Exception:
                    pass

        return Skill(
            name=name,
            description=description,
            file_path=skill_file.resolve(),
            is_global=is_global,
            raw_content=content,
        )

    def _scan_directory(self, base_dir: Path, is_global: bool) -> List[Skill]:
        """Scan subdirectories of base_dir for SKILL.md files."""
        if not base_dir.exists() or not base_dir.is_dir():
            return []

        skills: List[Skill] = []
        for child in base_dir.iterdir():
            if child.is_dir():
                skill_path = child / "SKILL.md"
                if skill_path.is_file():
                    skill = self.parse_skill_file(skill_path, is_global=is_global)
                    if skill:
                        skills.append(skill)
        return skills

    def load_all_skills(self) -> List[Skill]:
        """Load all skills with workspace-level skills overriding global ones."""
        skills_map: Dict[str, Skill] = {}

        # 1. Global skills
        for s in self._scan_directory(self.global_skills_dir, is_global=True):
            skills_map[s.name] = s

        # 2. Workspace skills (.tiancode/skills)
        ws_skills_dir = self.workspace_dir / ".tiancode" / "skills"
        for s in self._scan_directory(ws_skills_dir, is_global=False):
            skills_map[s.name] = s

        return sorted(skills_map.values(), key=lambda x: x.name)

    def format_prompt_catalog(self) -> str:
        """Render a concise markdown index of available skills for prompt injection."""
        skills = self.load_all_skills()
        if not skills:
            return ""

        lines = [
            "\n【可用扩展技能 (Skills)】",
            "以下为当前项目已安装的专家技能。当任务涉及对应领域时，请主动调用 read_file 工具查阅对应文件以获取专家级规范：",
        ]
        for s in skills:
            rel_or_abs = (
                os.path.relpath(s.file_path, self.workspace_dir)
                if not s.is_global
                else str(s.file_path)
            )
            lines.append(f"- **{s.name}** (`{rel_or_abs}`): {s.description}")

        return "\n".join(lines)
