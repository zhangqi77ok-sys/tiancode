"""npm/pnpm/yarn test must not run a dangerous package.json script.

The marker is a local file. These tests do not use the network or a credential.
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from tiancode.core.history import InMemorySnapshotStore
from tiancode.tools.execution import execute_shell_command, package_script_block_reason
from tiancode.tools.file_ops import create_file_tools


def _package(path: Path, scripts: dict[str, str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"scripts": scripts}), encoding="utf-8")


def _dangerous(marker: Path) -> str:
    return f"touch {marker} && chmod 777 {marker}"


def test_npm_test_does_not_start_when_the_script_is_dangerous(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    _package(tmp_path / "package.json", {"test": _dangerous(marker)})

    result = execute_shell_command("npm test", work_dir=tmp_path, timeout=5)
    substituted = package_script_block_reason("$(npm test)", tmp_path)

    assert result.exit_code == 126
    assert "scripts.test" in result.stderr
    assert substituted is not None and "scripts.test" in substituted
    assert not marker.exists()


def test_pretest_and_nested_run_are_inspected(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    _package(
        tmp_path / "package.json",
        {"test": "echo ok", "pretest": _dangerous(marker)},
    )
    pretest = package_script_block_reason("pnpm test", tmp_path)
    assert pretest is not None and "scripts.pretest" in pretest

    _package(
        tmp_path / "package.json",
        {"test": "npm run wipe", "wipe": "npm run deeper", "deeper": _dangerous(marker)},
    )
    nested = package_script_block_reason("yarn run test", tmp_path)
    assert nested is not None and "scripts.deeper" in nested
    assert not marker.exists()


def test_bun_run_test_reads_scripts_test(tmp_path: Path) -> None:
    from tiancode.core.polyglot import detect_project_profile

    marker = tmp_path / "ran.txt"
    _package(tmp_path / "package.json", {"test": "echo ok", "pretest": _dangerous(marker)})
    (tmp_path / "bun.lock").write_text("{}\n", encoding="utf-8")

    profile = detect_project_profile(tmp_path)
    blocked = package_script_block_reason(profile.test_command or "", tmp_path)

    assert profile.package_manager == "bun"
    assert profile.test_command == "bun run test"
    assert blocked is not None and "scripts.pretest" in blocked
    assert package_script_block_reason("bun test", tmp_path) is None
    assert not marker.exists()


def test_env_prefix_does_not_hide_npm_test(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    _package(tmp_path / "package.json", {"test": _dangerous(marker)})

    assigned = package_script_block_reason("FOO=1 npm test", tmp_path)
    wrapped = package_script_block_reason("env npm test", tmp_path)

    assert assigned is not None and "scripts.test" in assigned
    assert wrapped is not None and "scripts.test" in wrapped
    _package(
        tmp_path / "package.json",
        {"test": "A=wipe; npm run $A", "wipe": _dangerous(marker)},
    )
    followed = package_script_block_reason("npm test", tmp_path)
    assert followed is not None and "scripts.wipe" in followed
    assert not marker.exists()


def test_symlinked_bun_lock_does_not_select_bun(tmp_path: Path) -> None:
    from tiancode.core.polyglot import detect_project_profile

    outside = tmp_path / "outside.lock"
    outside.write_text("{}\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    _package(work / "package.json", {"test": "echo ok"})
    (work / "bun.lock").symlink_to(outside)

    profile = detect_project_profile(work)

    assert profile.package_manager == "npm"
    assert profile.test_command == "npm test"


def test_safe_script_and_unrelated_commands_stay_allowed(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    _package(tmp_path / "package.json", {"test": "echo ok"})
    assert package_script_block_reason("npm test", tmp_path) is None
    assert package_script_block_reason("bun test", tmp_path) is None

    _package(tmp_path / "package.json", {"test": _dangerous(marker)})
    assert package_script_block_reason("echo npm test", tmp_path) is None
    assert package_script_block_reason("bun test", tmp_path) is None
    assert package_script_block_reason("pytest -q", tmp_path) is None
    blocked = package_script_block_reason("bun run test", tmp_path)
    assert blocked is not None and "scripts.test" in blocked

    result = execute_shell_command("echo still-ok", work_dir=tmp_path, timeout=5)
    assert result.exit_code == 0
    assert "still-ok" in result.stdout
    assert not marker.exists()


def test_prefix_and_filter_cannot_leave_the_checked_package(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    pkg = tmp_path / "pkg"
    _package(pkg / "package.json", {"test": _dangerous(marker)})
    _package(tmp_path / "package.json", {"test": "echo ok"})

    inside = package_script_block_reason("cd pkg && npm test", tmp_path)
    assert inside is not None and "scripts.test" in inside
    prefixed = package_script_block_reason("npm --prefix pkg test", tmp_path)
    assert prefixed is not None and "scripts.test" in prefixed
    equals = package_script_block_reason("npm --prefix=pkg test", tmp_path)
    assert equals is not None and "scripts.test" in equals
    outside = package_script_block_reason("cd /tmp && npm test", tmp_path)
    assert outside is not None and "越出工作区" in outside
    prefix = package_script_block_reason("npm --prefix ../outside test", tmp_path)
    assert prefix is not None and "越出工作区" in prefix
    filtered = package_script_block_reason("pnpm --filter app test", tmp_path)
    assert filtered is not None and "--filter" in filtered
    assert package_script_block_reason("$(npm test)", tmp_path) is None
    _package(tmp_path / "package.json", {"test": _dangerous(marker)})
    _package(pkg / "package.json", {"test": "echo ok"})
    assert package_script_block_reason("npm --prefix pkg test", tmp_path) is None
    passed_through = package_script_block_reason("npm test -- --prefix pkg", tmp_path)
    assert passed_through is not None and "scripts.test" in passed_through
    assert not marker.exists()


def test_linked_package_json_is_not_executed(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    outside = tmp_path / "outside-package.json"
    _package(outside, {"test": _dangerous(marker)})
    work = tmp_path / "work"
    work.mkdir()
    (work / "package.json").symlink_to(outside)

    result = execute_shell_command("npm test", work_dir=work, timeout=5)

    assert result.exit_code == 126
    assert "链接" in result.stderr
    assert not marker.exists()
    assert outside.read_text(encoding="utf-8").count("chmod") == 1

    hard = tmp_path / "hard"
    hard.mkdir()
    _package(hard / "real.json", {"test": _dangerous(marker)})
    os.link(hard / "real.json", hard / "package.json")
    hard_reason = package_script_block_reason("npm test", hard)
    assert hard_reason is not None and "链接" in hard_reason


def test_auto_heal_does_not_run_a_dangerous_npm_script(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    _package(tmp_path / "package.json", {"test": _dangerous(marker)})
    (tmp_path / "note.txt").write_text("KEEP\n", encoding="utf-8")
    store = InMemorySnapshotStore(tmp_path)
    tools = {tool.name: tool for tool in create_file_tools(store, tmp_path, auto_heal=True)}

    result = tools["write_file"].invoke({"filename": "note.txt", "content": "NEW\n"})

    assert "写入成功" in result
    assert "安全拦截" in result
    assert "scripts.test" in result
    assert not marker.exists()
    assert (tmp_path / "note.txt").read_text(encoding="utf-8") == "NEW\n"
