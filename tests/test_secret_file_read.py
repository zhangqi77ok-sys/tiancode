"""Credential-shaped workspace files are not opened by read, search, or edit tools.

The canary is local fake text. The test does not use a credential or the network.
"""

from __future__ import annotations

import json
from pathlib import Path

from tiancode.core.context_mentions import resolve_file_mentions
from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.search import GrepOptions, read_file_paginated, search_workspace
from tiancode.tools.execution import create_execution_tools
from tiancode.tools.file_ops import create_file_tools


def test_credential_files_stay_unread_while_normal_files_still_open(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    secret = work / ".env"
    secret.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    notes = work / "notes.txt"
    notes.write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    (work / "alias.txt").symlink_to(Path(".env"))
    (work / "notes-link.txt").symlink_to(Path("notes.txt"))
    (work / ".env.local").write_text("CANARY-LOCAL\n", encoding="utf-8")
    (work / "id_rsa").write_text("CANARY-KEY\n", encoding="utf-8")
    (work / "server.pem").write_text("CANARY-PEM\n", encoding="utf-8")
    store = InMemorySnapshotStore(work)
    tools = {tool.name: tool for tool in create_file_tools(store, work, auto_heal=False)}
    runner = {tool.name: tool for tool in create_execution_tools(work)}

    read = read_file_paginated(work, ".env")
    alias = read_file_paginated(work, "alias.txt")
    grep = search_workspace(work, GrepOptions(pattern="CANARY", directory="."), set())
    mention = resolve_file_mentions("see @.env and @alias.txt", work)
    edited = tools["edit_file"].invoke({"filename": ".env", "old_code": "NOPE", "new_code": "X\n"})
    written = tools["write_file"].invoke({"filename": ".env", "content": "NEW\n"})
    deleted = tools["delete_file"].invoke({"filename": ".env"})
    ran = runner["run_python"].invoke({"filename": ".env"})

    for result in (read, alias, grep, mention.expanded_prompt, edited, written, deleted, ran):
        assert "CANARY-OUTSIDE" not in result
    assert "已跳过" in read
    assert "已跳过" in alias
    assert any("安全拦截敏感凭证或 Git 内部文件引用" in item for item in mention.warnings)
    assert "CANARY-OUTSIDE" not in json.dumps(store.export_snapshots())
    assert secret.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert (work / "alias.txt").is_symlink()
    assert "KEEP-IMPORTANT" in read_file_paginated(work, "notes.txt")
    assert "KEEP-IMPORTANT" in read_file_paginated(work, "notes-link.txt")
    kept = search_workspace(work, GrepOptions(pattern="KEEP-IMPORTANT", directory="."), set())
    assert "notes.txt" in kept
    assert "KEEP-IMPORTANT" in kept
    for name in (".env.local", "id_rsa", "server.pem"):
        shown = read_file_paginated(work, name)
        assert "CANARY" not in shown
        assert "已跳过" in shown
        assert (work / name).read_text(encoding="utf-8").startswith("CANARY")
