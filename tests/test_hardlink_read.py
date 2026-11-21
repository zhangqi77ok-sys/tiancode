"""A hard link is not opened. Its other name may live outside the workspace.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from tiancode.core.context_mentions import resolve_file_mentions
from tiancode.core.git_store import GitSnapshotStore
from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.polyglot import detect_project_profile
from tiancode.core.project_rules import ProjectRulesLoader
from tiancode.core.repo_map import generate_repo_map
from tiancode.core.search import GrepOptions, read_file_paginated, search_workspace
from tiancode.skills.loader import SkillLoader
from tiancode.tools.execution import create_execution_tools
from tiancode.tools.file_ops import create_file_tools


def _link(outside: Path, name: Path) -> None:
    os.link(outside, name)


def test_rules_read_grep_and_mention_do_not_return_hardlink_bytes(tmp_path: Path) -> None:
    outside = tmp_path / "outside.txt"
    outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    (work / "AGENTS.md").write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    (work / "visible.txt").write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    _link(outside, work / "TIANCODE.md")
    _link(outside, work / "notes.txt")
    inode = os.lstat(work / "notes.txt").st_ino

    rules = ProjectRulesLoader(global_rules_dir=tmp_path / "no-rules").format_prompt_rules(work)
    read = read_file_paginated(work, "notes.txt")
    grep = search_workspace(work, GrepOptions(pattern="CANARY", directory="."), set())
    kept = search_workspace(work, GrepOptions(pattern="KEEP-IMPORTANT", directory="."), set())
    mention = resolve_file_mentions("see @notes.txt", work)

    assert "KEEP-IMPORTANT" in rules
    assert "CANARY-OUTSIDE" not in rules
    assert "是硬链接，未读取其内容" in read
    assert "CANARY-OUTSIDE" not in read
    assert "CANARY-OUTSIDE" not in grep
    assert "KEEP-IMPORTANT" in kept
    assert "CANARY-OUTSIDE" not in mention.expanded_prompt
    assert any("未读取内容" in item for item in mention.warnings)
    assert os.lstat(work / "notes.txt").st_ino == inode
    assert os.lstat(work / "notes.txt").st_nlink == os.lstat(outside).st_nlink
    assert os.lstat(work / "notes.txt").st_nlink > 1
    assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"


def test_write_edit_delete_and_run_leave_the_hardlink(tmp_path: Path) -> None:
    outside = tmp_path / "outside.txt"
    outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    script = tmp_path / "ran.py"
    script.write_text("print('CANARY-OUTSIDE')\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    alias = work / "alias.txt"
    _link(outside, alias)
    _link(script, work / "ran.py")
    inode = os.lstat(alias).st_ino
    store = InMemorySnapshotStore(work)
    tools = {tool.name: tool for tool in create_file_tools(store, work, auto_heal=False)}
    runner = {tool.name: tool for tool in create_execution_tools(work)}

    written = tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW-LINE\n"})
    edited = tools["edit_file"].invoke({"filename": "alias.txt", "old_code": "CANARY-OUTSIDE\n", "new_code": "EDIT\n"})
    deleted = tools["delete_file"].invoke({"filename": "alias.txt"})
    ran = runner["run_python"].invoke({"filename": "ran.py"})

    for result in (written, edited, deleted, ran):
        assert "是硬链接" in result
        assert "CANARY-OUTSIDE" not in result
    assert store.export_snapshots() == []
    assert os.lstat(alias).st_ino == inode
    assert os.lstat(alias).st_nlink == 2
    assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert script.read_text(encoding="utf-8") == "print('CANARY-OUTSIDE')\n"


def test_repo_map_skill_package_and_diff_skip_hardlink_bytes(tmp_path: Path) -> None:
    outside_py = tmp_path / "outside.py"
    outside_py.write_text("def canary_marker():\n    return 'CANARY-OUTSIDE'\n", encoding="utf-8")
    outside_skill = tmp_path / "SKILL.md"
    outside_skill.write_text("---\nname: leak\ndescription: CANARY-OUTSIDE\n---\n", encoding="utf-8")
    outside_pkg = tmp_path / "package.json"
    outside_pkg.write_text(
        json.dumps({"scripts": {"test": "python3 -c \"print('CANARY-OUTSIDE')\""}}),
        encoding="utf-8",
    )
    work = tmp_path / "work"
    work.mkdir()
    (work / "kept.py").write_text("def kept_marker():\n    return 1\n", encoding="utf-8")
    _link(outside_py, work / "alias.py")
    skill_dir = work / ".tiancode" / "skills" / "kept"
    skill_dir.mkdir(parents=True)
    (skill_dir / "SKILL.md").write_text("---\nname: kept\ndescription: KEEP-SKILL\n---\n", encoding="utf-8")
    leak_dir = work / ".tiancode" / "skills" / "leak"
    leak_dir.mkdir(parents=True)
    _link(outside_skill, leak_dir / "SKILL.md")
    _link(outside_pkg, work / "package.json")

    repo_map = generate_repo_map(work)
    catalog = SkillLoader(workspace_dir=work, global_skills_dir=tmp_path / "no-skills").format_prompt_catalog()
    profile = detect_project_profile(work)
    note = work / "note.txt"
    note.write_text("ORIGINAL\n", encoding="utf-8")
    store = InMemorySnapshotStore(work)
    store.record_snapshot(str(note), "ORIGINAL\n", is_new=False)
    note.unlink()
    _link(outside_py, note)
    diff_blob = "\n".join(item.diff_text for item in store.compute_diffs())
    untracked = GitSnapshotStore(work)._diff_for_untracked("alias.py")

    assert "kept_marker" in repo_map
    assert "CANARY-OUTSIDE" not in repo_map
    assert "canary_marker" not in repo_map
    assert "KEEP-SKILL" in catalog
    assert "CANARY-OUTSIDE" not in catalog
    assert profile.test_command is None
    assert "CANARY-OUTSIDE" not in diff_blob
    assert untracked is not None
    assert "硬链接，未读取内容" in untracked.diff_text
    assert "CANARY-OUTSIDE" not in untracked.diff_text
    assert outside_py.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1
    assert os.lstat(work / "alias.py").st_ino == os.lstat(outside_py).st_ino
    assert os.lstat(work / "alias.py").st_nlink > 1
