"""make, just, and tox recipes are checked before the tool runs.

The marker is a local file. These tests do not use the network or a credential.
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import pytest

from tiancode.core.recipe_guard import recipe_block_reason
from tiancode.tools.execution import execute_shell_command


def _run_make(work: Path, command: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=work,
        shell=True,
        text=True,
        capture_output=True,
        timeout=20,
        check=False,
    )


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


def test_gmake_and_bmake_share_the_make_recipe_check(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text(
        f"test:\n\t{_dangerous(marker)}\n",
        encoding="utf-8",
    )

    for program in ("gmake", "bmake"):
        reason = recipe_block_reason(f"{program} test", tmp_path)
        assert reason is not None and "配方" in reason

    safe = tmp_path / "safe"
    safe.mkdir()
    (safe / "Makefile").write_text("test:\n\t@echo ok\n", encoding="utf-8")
    assert recipe_block_reason("gmake test", safe) is None
    assert recipe_block_reason("bmake test", safe) is None


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


def test_make_suffix_rule_runs_when_the_prerequisite_exists(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    recipe = _dangerous(marker)
    (tmp_path / "Makefile").write_text(f".sh:\n\t{recipe}\n", encoding="utf-8")
    (tmp_path / "foo.sh").write_text("echo hi\n", encoding="utf-8")

    reason = recipe_block_reason("make foo", tmp_path)

    assert reason is not None and "foo" in reason
    cleared = tmp_path / "cleared"
    cleared.mkdir()
    (cleared / "Makefile").write_text(f".SUFFIXES:\n.sh:\n\t{recipe}\n", encoding="utf-8")
    (cleared / "foo.sh").write_text("echo hi\n", encoding="utf-8")
    assert recipe_block_reason("make foo", cleared) is None

    added = tmp_path / "added"
    added.mkdir()
    (added / "Makefile").write_text(f".foo:\n\t{recipe}\n.SUFFIXES: .foo\n", encoding="utf-8")
    (added / "x.foo").write_text("echo hi\n", encoding="utf-8")
    assert recipe_block_reason("make x", added) is not None

    explicit = tmp_path / "explicit"
    explicit.mkdir()
    (explicit / "Makefile").write_text(f"foo:\n\t@echo ok\n.sh:\n\t{recipe}\n", encoding="utf-8")
    (explicit / "foo.sh").write_text("echo hi\n", encoding="utf-8")
    assert recipe_block_reason("make foo", explicit) is None
    assert not marker.exists()


def test_make_double_suffix_uses_the_suffix_list_order(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text(
        ".c.o:\n\t" + _dangerous(marker) + "\n.s.o:\n\t@echo other\n",
        encoding="utf-8",
    )
    (tmp_path / "foo.c").write_text("int x;\n", encoding="utf-8")
    (tmp_path / "foo.s").write_text("nop\n", encoding="utf-8")

    reason = recipe_block_reason("make foo.o", tmp_path)

    assert reason is not None and "foo.o" in reason
    assert not marker.exists()


def test_make_eval_call_and_foreach_expand_before_exec(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    body = _dangerous(marker)
    (tmp_path / "Makefile").write_text(
        "define recipe\n"
        "$(1):\n"
        f"\t{body}\n"
        "endef\n"
        "$(eval $(call recipe,test))\n",
        encoding="utf-8",
    )

    reason = recipe_block_reason("make test", tmp_path)

    assert reason is not None and "test" in reason
    nested = tmp_path / "nested"
    nested.mkdir()
    (nested / "Makefile").write_text(
        "define recipe\n"
        "$(1):\n"
        f"\t{body}\n"
        "endef\n"
        "$(foreach t,test,$(eval $(call recipe,$(t))))\n",
        encoding="utf-8",
    )
    assert recipe_block_reason("make test", nested) is not None
    braced = tmp_path / "braced"
    braced.mkdir()
    (braced / "Makefile").write_text(
        "X := ${shell " + body + "}\nhelp:\n\t@echo ok\n",
        encoding="utf-8",
    )
    assert recipe_block_reason("make -n help", braced) is not None
    safe = tmp_path / "safe"
    safe.mkdir()
    (safe / "Makefile").write_text(
        "define CMD\necho ok\nendef\ntest:\n\t$(call CMD)\n",
        encoding="utf-8",
    )
    assert recipe_block_reason("make test", safe) is None
    assert not marker.exists()


def test_makefiles_variable_runs_before_the_makefile(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text("all:\n\t@echo ok\n", encoding="utf-8")
    (tmp_path / "evil.mk").write_text("X := $(shell touch ran)\n", encoding="utf-8")
    proved = _run_make(tmp_path, "MAKEFILES=evil.mk make")
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    (tmp_path / "evil.mk").write_text(
        "X := $(shell " + _dangerous(marker) + ")\n",
        encoding="utf-8",
    )

    blocked = execute_shell_command("MAKEFILES=evil.mk make", work_dir=tmp_path, timeout=5)
    assert blocked.exit_code == 126
    assert "$(shell)" in blocked.stderr
    assert not marker.exists()
    assert recipe_block_reason("make MAKEFILES=evil.mk", tmp_path) is not None
    assert recipe_block_reason("export MAKEFILES=evil.mk; make", tmp_path) is not None
    assert recipe_block_reason("env MAKEFILES=evil.mk make", tmp_path) is not None
    assert recipe_block_reason("MAKEFILES=evil.mk make MAKEFILES=", tmp_path) is None
    assert recipe_block_reason("MAKEFILES=missing.mk make", tmp_path) is None

    monkeypatch.setenv("MAKEFILES", "evil.mk")
    parent = execute_shell_command("make", work_dir=tmp_path, timeout=5)
    assert parent.exit_code == 126
    assert not marker.exists()
    monkeypatch.setenv("MAKEFILES", "safe-missing.mk")
    assert recipe_block_reason("make", tmp_path) is None

    outside = recipe_block_reason("MAKEFILES=/etc/passwd make", tmp_path)
    assert outside is not None and "MAKEFILES" in outside
    link = tmp_path / "link.mk"
    link.symlink_to(tmp_path / "evil.mk")
    linked = recipe_block_reason("MAKEFILES=link.mk make", tmp_path)
    assert linked is not None and "链接" in linked


def test_makefiles_suffix_rule_runs_for_the_main_target(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "foo.c").write_text("int x;\n", encoding="utf-8")
    (tmp_path / "suf.mk").write_text(".c.o:\n\ttouch ran\n", encoding="utf-8")
    (tmp_path / "Makefile").write_text("all: foo.o\n", encoding="utf-8")
    proved = _run_make(tmp_path, "MAKEFILES=suf.mk make")
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    (tmp_path / "foo.o").unlink(missing_ok=True)
    (tmp_path / "suf.mk").write_text(".c.o:\n\t" + _dangerous(marker) + "\n", encoding="utf-8")

    blocked = execute_shell_command("MAKEFILES=suf.mk make", work_dir=tmp_path, timeout=5)

    assert blocked.exit_code == 126
    assert not marker.exists()


def test_make_command_line_eval_and_immediate_assignment(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text("all:\n\t@echo ok\n", encoding="utf-8")
    proved = _run_make(tmp_path, "make --eval='$(shell touch ran)'")
    assert marker.exists(), proved.stderr
    marker.unlink()
    proved = _run_make(tmp_path, "make -E '$(shell touch ran)'")
    assert marker.exists(), proved.stderr
    marker.unlink()
    proved = _run_make(tmp_path, "make 'X:=$(shell touch ran)'")
    assert marker.exists(), proved.stderr
    marker.unlink()
    proved = _run_make(tmp_path, "make 'X!=touch ran'")
    assert marker.exists(), proved.stderr
    marker.unlink()

    blocked = execute_shell_command(
        "make --eval='$(shell chmod 777 /)'", work_dir=tmp_path, timeout=5,
    )
    assert blocked.exit_code == 126
    assert not marker.exists()
    assert recipe_block_reason("make -E '$(shell sudo reboot)'", tmp_path) is not None
    assert recipe_block_reason("make 'X:=$(shell sudo reboot)'", tmp_path) is not None
    assert recipe_block_reason("make 'X::=$(shell sudo reboot)'", tmp_path) is not None
    assert recipe_block_reason("make 'X!=sudo reboot'", tmp_path) is not None
    assert recipe_block_reason("make 'X=$(shell sudo reboot)'", tmp_path) is None
    assert recipe_block_reason("make --eval='CFLAGS+=-Wall'", tmp_path) is None


def test_make_recursive_assignment_runs_when_a_recipe_expands_it(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text("all:\n\t$(X)\n", encoding="utf-8")
    proved = _run_make(tmp_path, "make 'X=$(shell touch ran)'")
    assert marker.exists(), proved.stderr
    marker.unlink()
    (tmp_path / "Makefile").write_text(
        "all:\n\t$(X)\n",
        encoding="utf-8",
    )
    reason = recipe_block_reason("make 'X=$(shell sudo reboot)'", tmp_path)
    assert reason is not None
    assert not marker.exists()


def test_makeflags_eval_runs_at_startup(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "Makefile").write_text("all:\n\t@echo ok\n", encoding="utf-8")
    proved = _run_make(tmp_path, "MAKEFLAGS='--eval=$(shell touch ran)' make")
    assert marker.exists(), proved.stderr
    marker.unlink()
    proved = _run_make(tmp_path, "GNUMAKEFLAGS='--eval=$(shell touch ran)' make")
    assert marker.exists(), proved.stderr
    marker.unlink()

    blocked = execute_shell_command(
        "MAKEFLAGS='--eval=$(shell sudo reboot)' make",
        work_dir=tmp_path,
        timeout=5,
    )
    assert blocked.exit_code == 126
    assert not marker.exists()
    assert recipe_block_reason("GNUMAKEFLAGS='--eval=$(shell rm -rf /)' make", tmp_path) is not None
    assert recipe_block_reason("MAKEFLAGS= make", tmp_path) is None

    monkeypatch.setenv("MAKEFLAGS", "--eval=$(shell sudo reboot)")
    parent = execute_shell_command("make", work_dir=tmp_path, timeout=5)
    assert parent.exit_code == 126
    assert not marker.exists()
    assert recipe_block_reason("MAKEFLAGS= make", tmp_path) is None


def test_env_chdir_make_uses_that_directory(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    nested = tmp_path / "nested"
    nested.mkdir()
    (nested / "Makefile").write_text("all:\n\ttouch ran\n", encoding="utf-8")
    (tmp_path / "Makefile").write_text("all:\n\t@echo root\n", encoding="utf-8")
    proved = _run_make(tmp_path, "env -C nested make")
    assert proved.returncode == 0 and (nested / "ran").exists(), proved.stderr
    (nested / "Makefile").write_text("X := $(shell sudo reboot)\nall:\n\t@echo ok\n", encoding="utf-8")
    reason = recipe_block_reason("env -C nested make", tmp_path)
    assert reason is not None
    assert recipe_block_reason("env -C /tmp make", tmp_path) is not None
    assert not marker.exists()

