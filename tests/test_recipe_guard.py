"""make, just, and tox recipes are checked before the tool runs.

The marker is a local file. These tests do not use the network or a credential.
"""

from __future__ import annotations

import os
from pathlib import Path

from tiancode.core.recipe_guard import recipe_block_reason
from tiancode.tools.execution import execute_shell_command


def _dangerous(marker: Path) -> str:
    return f"touch {marker} && chmod 777 {marker}"


def test_make_recipe_does_not_start_when_it_is_dangerous(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text(
        f"test:\n\t{_dangerous(marker)}\n",
        encoding="utf-8",
    )

    result = execute_shell_command("make test", work_dir=tmp_path, timeout=5)

    assert result.exit_code == 126
    assert "配方" in result.stderr
    assert not marker.exists()


def test_make_expands_a_variable_and_a_prerequisite(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text(
        "CMD = " + _dangerous(marker) + "\n"
        "test: wipe\n\t@echo ok\n"
        "wipe:\n\t$(CMD)\n",
        encoding="utf-8",
    )

    reason = recipe_block_reason("make test", tmp_path)

    assert reason is not None
    assert "wipe" in reason
    assert not marker.exists()


def test_make_shell_function_runs_even_for_another_target(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text(
        "X := $(shell " + _dangerous(marker) + ")\n"
        "help:\n\t@echo ok\n",
        encoding="utf-8",
    )

    reason = recipe_block_reason("make help", tmp_path)

    assert reason is not None
    assert "$(shell)" in reason
    assert recipe_block_reason("make -n help", tmp_path) is not None


def test_make_dry_run_skips_a_recipe_it_would_not_execute(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text(
        f"test:\n\t{_dangerous(marker)}\n",
        encoding="utf-8",
    )

    assert recipe_block_reason("make -n test", tmp_path) is None
    assert not marker.exists()


def test_safe_make_recipe_stays_allowed(tmp_path: Path) -> None:
    (tmp_path / "Makefile").write_text("test:\n\t@echo ok\n", encoding="utf-8")

    assert recipe_block_reason("make test", tmp_path) is None
    assert recipe_block_reason("echo make test", tmp_path) is None
    result = execute_shell_command("make test", work_dir=tmp_path, timeout=5)
    assert result.exit_code == 0
    assert "ok" in result.stdout


def test_make_refuses_a_linked_makefile_and_an_outside_include(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    outside = tmp_path / "outside.mk"
    outside.write_text(f"test:\n\t{_dangerous(marker)}\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    (work / "Makefile").symlink_to(outside)

    linked = recipe_block_reason("make test", work)
    assert linked is not None and "链接" in linked

    plain = tmp_path / "plain"
    plain.mkdir()
    (plain / "Makefile").write_text("include ../outside.mk\ntest:\n\t@echo ok\n", encoding="utf-8")
    escaped = recipe_block_reason("make test", plain)
    assert escaped is not None and "工作区外" in escaped
    assert recipe_block_reason("make -C /tmp test", plain) is not None
    assert not marker.exists()


def test_make_refuses_a_hardlinked_makefile(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    real = tmp_path / "real.mk"
    real.write_text(f"test:\n\t{_dangerous(marker)}\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    os.link(real, work / "Makefile")

    reason = recipe_block_reason("make test", work)

    assert reason is not None and "链接" in reason
    assert not marker.exists()


def test_just_recipe_and_import_are_checked(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "justfile").write_text(
        "cmd := \"" + _dangerous(marker) + "\"\n"
        "test:\n"
        "    {{cmd}}\n",
        encoding="utf-8",
    )

    reason = recipe_block_reason("just test", tmp_path)

    assert reason is not None and "just" in reason
    assert recipe_block_reason("just --list", tmp_path) is None
    safe = tmp_path / "safe"
    safe.mkdir()
    (safe / "justfile").write_text("test:\n    echo ok\n", encoding="utf-8")
    assert recipe_block_reason("just test", safe) is None

    nested = tmp_path / "nested"
    nested.mkdir()
    (nested / "lib.just").write_text("wipe:\n    " + _dangerous(marker) + "\n", encoding="utf-8")
    (nested / "justfile").write_text("import 'lib.just'\n", encoding="utf-8")
    imported_recipe = recipe_block_reason("just wipe", nested)
    assert imported_recipe is not None and "wipe" in imported_recipe

    outside = tmp_path / "out.just"
    outside.write_text("wipe:\n    " + _dangerous(marker) + "\n", encoding="utf-8")
    imported = tmp_path / "with-import"
    imported.mkdir()
    (imported / "justfile").write_text("import '../out.just'\n", encoding="utf-8")
    escaped = recipe_block_reason("just", imported)
    assert escaped is not None and "工作区外" in escaped
    assert not marker.exists()


def test_tox_commands_are_checked_for_the_selected_env(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "tox.ini").write_text(
        "[tox]\nenvlist = safe,evil\n"
        "[testenv:safe]\ncommands = pytest -q\n"
        "[testenv:evil]\ncommands =\n    " + _dangerous(marker) + "\n",
        encoding="utf-8",
    )

    evil = recipe_block_reason("tox -e evil", tmp_path)
    assert evil is not None and "evil" in evil
    assert recipe_block_reason("tox -e safe", tmp_path) is None
    assert recipe_block_reason("tox exec -- echo ok", tmp_path) is None
    assert recipe_block_reason("tox --help", tmp_path) is None
    assert not marker.exists()


def test_tox_refuses_a_linked_config(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    outside = tmp_path / "tox.ini"
    outside.write_text("[testenv]\ncommands = " + _dangerous(marker) + "\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    (work / "tox.ini").symlink_to(outside)

    reason = recipe_block_reason("tox", work)

    assert reason is not None and "链接" in reason
    assert not marker.exists()
