"""Project rules loader (AGENTS.md / TIANCODE.md) and deterministic /init guide generator."""

from dataclasses import dataclass
import os
from pathlib import Path
import tempfile
from typing import List, Optional, Sequence, Set, Tuple, Union

from tiancode.core.path_safety import WorkspacePathError, restore_workspace_text, workspace_directory_entry
from tiancode.core.polyglot import detect_project_profile

DEFAULT_MAX_RULES_CHARS = 6000
_GLOBAL_CANDIDATES: Tuple[str, ...] = ("AGENTS.md", "TIANCODE.md")
_WORKSPACE_CANDIDATES: Tuple[str, ...] = (
    "AGENTS.md",
    "TIANCODE.md",
    ".tiancode/AGENTS.md",
    ".tiancode/TIANCODE.md",
)
_IGNORED_SCAN_DIRS = frozenset({
    ".git", ".hg", ".svn", ".venv", "venv", "env", "node_modules",
    "__pycache__", ".pytest_cache", "target", "dist", "build", ".idea", ".vscode",
})
_KEY_CONFIG_FILES: Tuple[str, ...] = (
    "pom.xml", "build.gradle", "build.gradle.kts", "pyproject.toml",
    "package.json", "go.mod", "Cargo.toml", "Makefile", ".gitmodules",
)


def get_default_global_rules_dir() -> Path:
    """Resolve global rules directory with strict pytest isolation when env var is unset."""
    if env_dir := os.environ.get("TIANCODE_RULES_PATH"):
        return Path(env_dir).resolve()
    if os.environ.get("PYTEST_CURRENT_TEST"):
        return (Path(tempfile.gettempdir()) / f"tiancode_pytest_{os.getpid()}_rules").resolve()
    return (Path.home() / ".tiancode").resolve()


@dataclass(frozen=True)
class ProjectRuleDoc:
    """A validated project rule document loaded from global or workspace scope."""

    scope: str
    filename: str
    path: Path
    content: str
    truncated: bool = False


class ProjectRulesLoader:
    """Discovers and formats hierarchical AGENTS.md / TIANCODE.md guides safely."""

    def __init__(self, global_rules_dir: Optional[Path] = None) -> None:
        self.global_rules_dir = (global_rules_dir or get_default_global_rules_dir()).resolve()

    @staticmethod
    def _collect_from_root(
        root_dir: Path,
        scope: str,
        candidates: Sequence[str],
        seen_paths: Set[str],
        out: List[ProjectRuleDoc],
    ) -> None:
        if not root_dir.exists() or not root_dir.is_dir():
            return
        resolved_root = root_dir.resolve()
        for rel in candidates:
            candidate = root_dir / rel
            try:
                resolved = candidate.resolve()
                # Block symlinks escaping the declared root directory
                if not resolved.is_relative_to(resolved_root) or not resolved.is_file():
                    continue
                norm_key = os.path.normcase(str(resolved))
                if norm_key in seen_paths:
                    continue
                text = resolved.read_text(encoding="utf-8", errors="replace").strip()
            except Exception:
                continue
            if text:
                seen_paths.add(norm_key)
                out.append(ProjectRuleDoc(scope=scope, filename=rel, path=resolved, content=text))

    def load_rules(self, work_dir: Union[Path, str]) -> List[ProjectRuleDoc]:
        """Load global rules first, then workspace rules, deduplicating by resolved path."""
        docs: List[ProjectRuleDoc] = []
        seen: Set[str] = set()
        self._collect_from_root(self.global_rules_dir, "global", _GLOBAL_CANDIDATES, seen, docs)
        self._collect_from_root(Path(work_dir), "workspace", _WORKSPACE_CANDIDATES, seen, docs)
        return docs

    def format_prompt_rules(self, work_dir: Union[Path, str], *, max_chars: int = DEFAULT_MAX_RULES_CHARS) -> str:
        """Format discovered rules within strict character budget for SystemMessage injection."""
        docs = self.load_rules(work_dir)
        if not docs or max_chars <= 0:
            return ""

        header = "【已加载工程规范与开发守则 (Project Rules)】"
        if len(header) >= max_chars:
            return header[:max_chars]

        chunks: List[str] = [header]
        used = len(header)

        for doc in docs:
            title = f"\n## [{doc.scope}] {doc.filename} ({doc.path})\n"
            remaining = max_chars - used
            if remaining <= len(title) + 24:
                break
            body_budget = remaining - len(title)
            if len(doc.content) <= body_budget:
                section = title + doc.content
            else:
                note = f"\n... (超出长度限制已截断，完整细则请调用 read_file 查阅 {doc.path})"
                keep = max(0, body_budget - len(note))
                if keep <= 0:
                    break
                section = title + doc.content[:keep].rstrip() + note
            chunks.append(section)
            used += len(section)

        return "".join(chunks)[:max_chars]


def _scan_top_modules(work_dir: Path, limit: int = 16) -> List[str]:
    """List primary visible non-ignored subdirectories under work_dir safely."""
    resolved = work_dir.resolve()
    modules: List[str] = []
    if not resolved.is_dir():
        return modules
    for entry in sorted(resolved.iterdir(), key=lambda p: p.name.lower()):
        if not entry.is_dir() or entry.name.startswith(".") or entry.name.lower() in _IGNORED_SCAN_DIRS:
            continue
        try:
            if not entry.resolve().is_relative_to(resolved):
                continue
        except Exception:
            continue
        modules.append(entry.name)
        if len(modules) >= limit:
            break
    return modules


def generate_project_guide(work_dir: Union[Path, str]) -> str:
    """Deterministically inspect workspace facts and synthesize a grounded TIANCODE.md guide."""
    resolved = Path(work_dir).expanduser().resolve()
    profile = detect_project_profile(resolved)
    modules = _scan_top_modules(resolved)
    found_configs = [cfg for cfg in _KEY_CONFIG_FILES if (resolved / cfg).exists()]
    has_submodules = (resolved / ".gitmodules").is_file()

    lines: List[str] = [
        f"# {resolved.name or 'Project'} 开发与协作规范 (TIANCODE.md)",
        "",
        "## 1. 技术栈与验证命令（自动探测）",
        f"- **主要语言**：{profile.language}",
        f"- **构建/包管理**：{profile.package_manager}",
        f"- **标准测试命令**：`{profile.test_command or '未自动探测到测试命令，请手动补充'}`",
        f"- **关键配置清单**：{', '.join(f'`{c}`' for c in found_configs) if found_configs else '暂无标准根配置文件'}",
        "",
        "## 2. 仓库目录模块",
    ]
    if modules:
        lines.extend(f"- `{m}/`" for m in modules)
    else:
        lines.append("- （根目录单模块结构）")

    lines.extend([
        "",
        "## 3. 安全边界与工程纪律",
        "- 修改已有文件前先使用 `read_file` / `get_repo_map` 核实源码上下文，禁止臆测接口签名。",
        "- 优先使用 `edit_file` 执行局部精准替换；完成修改后务必执行对应单测验证后再交付。",
    ])
    if has_submodules:
        lines.append(
            "- **Git 子模块防线（检测到 `.gitmodules`）**：修改跨目录文件前先确认是否属于子模块，未经明确指示严禁在子模块内产生未提交变更。"
        )
    lines.append("")
    return "\n".join(lines)


def write_project_guide(
    work_dir: Union[Path, str],
    *,
    filename: str = "TIANCODE.md",
    force: bool = False,
) -> Tuple[Path, bool]:
    """Generate and atomically write TIANCODE.md unless it already exists without force=True.

    The destination is a directory entry. A symlink or hard link is not opened.
    """
    resolved = Path(work_dir).expanduser().resolve()
    if not resolved.is_dir():
        raise ValueError(f"目标工作区不存在: {work_dir}")
    try:
        target = workspace_directory_entry(resolved, filename)
    except WorkspacePathError as exc:
        raise PermissionError(f"禁止写入工作区外部路径: {filename}") from exc
    if (target.is_symlink() or target.exists()) and not force:
        return target, False
    if not target.is_symlink() and target.is_dir():
        raise PermissionError(f"禁止把工程规范写入目录: {filename}")

    restore_workspace_text(target, generate_project_guide(resolved))
    return target, True
