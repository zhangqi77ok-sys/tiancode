"""Skill discovery must not follow a symlink into another file.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

from pathlib import Path

from tiancode.skills.loader import SkillLoader


def _skill(path: Path, description: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"---\nname: {path.parent.name}\ndescription: {description}\n---\n", encoding="utf-8")


def _catalog(work: Path, global_dir: Path) -> str:
    return SkillLoader(workspace_dir=work, global_skills_dir=global_dir).format_prompt_catalog()


def test_linked_skills_stay_out_of_the_prompt(tmp_path: Path) -> None:
    outside = tmp_path / "outside-skill.md"
    _skill(outside, "CANARY-OUTSIDE")
    outside_dir = tmp_path / "outside-dir" / "nested"
    _skill(outside_dir / "SKILL.md", "CANARY-DIR")
    outside_skills = tmp_path / "outside-skills" / "planted"
    _skill(outside_skills / "SKILL.md", "CANARY-PARENT")
    outside_tian = tmp_path / "outside-tian" / "skills" / "planted"
    _skill(outside_tian / "SKILL.md", "CANARY-TIAN")
    global_dir = tmp_path / "global-skills"
    global_dir.mkdir()

    work = tmp_path / "work"
    kept = work / ".tiancode" / "skills" / "kept" / "SKILL.md"
    _skill(kept, "KEEP-SKILL")
    leak = work / ".tiancode" / "skills" / "leak"
    leak.mkdir(parents=True)
    (leak / "SKILL.md").symlink_to(outside)
    catalog = _catalog(work, global_dir)

    assert "KEEP-SKILL" in catalog
    assert ".tiancode/skills/kept/SKILL.md" in catalog
    assert "CANARY-OUTSIDE" not in catalog
    assert "../" not in catalog
    assert outside.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1
    assert (leak / "SKILL.md").is_symlink()

    by_dir = tmp_path / "by-dir"
    (by_dir / ".tiancode" / "skills").mkdir(parents=True)
    (by_dir / ".tiancode" / "skills" / "linked").symlink_to(outside_dir, target_is_directory=True)
    assert "CANARY-DIR" not in _catalog(by_dir, global_dir)

    by_skills = tmp_path / "by-skills"
    (by_skills / ".tiancode").mkdir(parents=True)
    (by_skills / ".tiancode" / "skills").symlink_to(outside_skills.parent, target_is_directory=True)
    assert "CANARY-PARENT" not in _catalog(by_skills, global_dir)

    by_tian = tmp_path / "by-tian"
    by_tian.mkdir()
    (by_tian / ".tiancode").symlink_to(outside_tian.parents[1], target_is_directory=True)
    assert "CANARY-TIAN" not in _catalog(by_tian, global_dir)


def test_global_symlink_skill_is_not_loaded(tmp_path: Path) -> None:
    outside = tmp_path / "outside-global.md"
    _skill(outside, "CANARY-GLOBAL")
    global_dir = tmp_path / "global-skills"
    _skill(global_dir / "kept" / "SKILL.md", "KEEP-GLOBAL")
    linked = global_dir / "leak"
    linked.mkdir()
    (linked / "SKILL.md").symlink_to(outside)
    work = tmp_path / "work"
    work.mkdir()

    catalog = _catalog(work, global_dir)

    assert "KEEP-GLOBAL" in catalog
    assert "CANARY-GLOBAL" not in catalog
    assert "../" not in catalog
    assert outside.read_text(encoding="utf-8").count("CANARY-GLOBAL") == 1
