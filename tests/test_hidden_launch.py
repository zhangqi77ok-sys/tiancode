"""Hidden launchers run a command the outer words do not show.

A marker proves the unguarded tool really executes. The guard is then given
the same shape with sudo or chmod 777, which must exit 126 before the marker
exists. These tests do not use the network.
"""

from __future__ import annotations

import os
import stat
import subprocess
from pathlib import Path

from tiancode.core.launch_guard import launch_block_reason
from tiancode.tools.execution import execute_shell_command


def _run(argv: list[str], cwd: Path, env: dict | None = None) -> subprocess.CompletedProcess[str]:
    merged = os.environ.copy()
    if env:
        merged.update(env)
    return subprocess.run(
        argv,
        cwd=cwd,
        env=merged,
        text=True,
        capture_output=True,
        timeout=20,
        check=False,
    )


def _blocked(command: str, work: Path) -> None:
    marker = work / "ran"
    result = execute_shell_command(command, work_dir=work, timeout=5)
    assert result.exit_code == 126, result.stderr
    assert "安全拦截" in result.stderr
    assert not marker.exists()


def test_timeout_runs_the_later_command_and_sudo_is_blocked(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    proved = _run(["timeout", "5", "touch", str(marker)], tmp_path)
    assert proved.returncode == 0
    assert marker.exists()
    marker.unlink()
    _blocked(f"timeout 5 sudo touch {marker}", tmp_path)


def test_awk_system_and_sed_e_and_sqlite_shell(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    awk = _run(["awk", "BEGIN { system(\"touch ran\") }"], tmp_path)
    assert awk.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("awk 'BEGIN { system(\"sudo reboot\") }'", tmp_path)

    sed = _run(["sh", "-c", "echo | sed 'etouch ran'"], tmp_path)
    assert sed.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("sed 'esudo reboot'", tmp_path)

    sql = _run(["sqlite3", ":memory:", ".shell touch ran"], tmp_path)
    assert sql.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("sqlite3 :memory: '.shell sudo reboot'", tmp_path)


def test_find_exec_and_xargs_run_their_utility(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    found = _run(["find", ".", "-exec", "touch", "ran", ";"], tmp_path)
    assert found.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("find . -exec sudo reboot \\;", tmp_path)

    listed = _run(["sh", "-c", "printf 'ran\\n' | xargs touch"], tmp_path)
    assert listed.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("xargs sudo reboot", tmp_path)


def test_git_fsmonitor_alias_editor_and_hook(tmp_path: Path) -> None:
    repo = tmp_path / "repo"
    repo.mkdir()
    assert _run(["git", "init", "-q"], repo).returncode == 0
    config = repo / ".git" / "config"
    original = config.read_text(encoding="utf-8")
    config.write_text(original + "[core]\n\tfsmonitor = touch ran\n", encoding="utf-8")
    proved = _run(["git", "status", "--short"], repo)
    assert (repo / "ran").exists(), proved.stderr
    (repo / "ran").unlink()
    config.write_text(original + "[core]\n\tfsmonitor = sudo reboot\n", encoding="utf-8")
    _blocked("git status --short", repo)
    config.write_text(original, encoding="utf-8")
    assert launch_block_reason("git status --short", repo) is None

    hook = repo / ".git" / "hooks" / "pre-commit"
    hook.write_text("#!/bin/sh\ntouch ran\n", encoding="utf-8")
    hook.chmod(hook.stat().st_mode | stat.S_IEXEC)
    identity = {
        "GIT_AUTHOR_NAME": "Tian",
        "GIT_AUTHOR_EMAIL": "tian@example.com",
        "GIT_COMMITTER_NAME": "Tian",
        "GIT_COMMITTER_EMAIL": "tian@example.com",
    }
    committed = _run(["git", "commit", "--allow-empty", "-m", "m"], repo, identity)
    assert (repo / "ran").exists(), committed.stderr
    (repo / "ran").unlink()
    hook.write_text("#!/bin/sh\nchmod 777 secrets.txt\n", encoding="utf-8")
    hook.chmod(hook.stat().st_mode | stat.S_IEXEC)
    _blocked("git commit --allow-empty -m m", repo)

    hook.unlink()
    sample = repo / ".git" / "hooks" / "pre-commit"
    sample.write_text("#!/bin/sh\nchmod 777 secrets.txt\n", encoding="utf-8")
    assert launch_block_reason("git commit -m m", repo) is None
    outside = tmp_path / "outside-hook"
    outside.write_text("#!/bin/sh\necho hi\n", encoding="utf-8")
    sample.unlink()
    sample.symlink_to(outside)
    reason = launch_block_reason("git commit -m m", repo)
    assert reason is not None and "符号链接" in reason


def test_parent_git_config_slot_is_part_of_the_command(tmp_path: Path, monkeypatch) -> None:
    repo = tmp_path / "repo"
    repo.mkdir()
    assert _run(["git", "init", "-q"], repo).returncode == 0
    monkeypatch.setenv("GIT_CONFIG_COUNT", "1")
    monkeypatch.setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
    monkeypatch.setenv("GIT_CONFIG_VALUE_0", "sudo reboot")
    _blocked("git status --short", repo)


def test_cmake_execute_process_go_generate_and_cargo_wrapper(tmp_path: Path) -> None:
    script = tmp_path / "s.cmake"
    script.write_text("execute_process(COMMAND touch ran)\n", encoding="utf-8")
    proved = _run(["cmake", "-P", "s.cmake"], tmp_path)
    assert proved.returncode == 0 and (tmp_path / "ran").exists(), proved.stderr
    (tmp_path / "ran").unlink()
    script.write_text("execute_process(COMMAND sudo reboot)\n", encoding="utf-8")
    _blocked("cmake -P s.cmake", tmp_path)
    script.write_text(
        "# execute_process(COMMAND sudo reboot)\nexecute_process(COMMAND echo hi)\n",
        encoding="utf-8",
    )
    assert launch_block_reason("cmake -P s.cmake", tmp_path) is None
    assert launch_block_reason("cmake -E echo hi", tmp_path) is None

    (tmp_path / "go.mod").write_text("module example.com/p\n\ngo 1.22\n", encoding="utf-8")
    go_file = tmp_path / "main.go"
    go_file.write_text("package main\n//go:generate touch ran\nfunc main() {}\n", encoding="utf-8")
    generated = _run(["go", "generate", "."], tmp_path)
    assert generated.returncode == 0 and (tmp_path / "ran").exists(), generated.stderr
    (tmp_path / "ran").unlink()
    go_file.write_text("package main\n//go:generate sudo reboot\nfunc main() {}\n", encoding="utf-8")
    _blocked("go generate .", tmp_path)
    assert launch_block_reason("go generate -n .", tmp_path) is None

    package = tmp_path / "Cargo.toml"
    package.write_text(
        '[package]\nname = "p"\nversion = "0.1.0"\nedition = "2021"\n',
        encoding="utf-8",
    )
    (tmp_path / "src").mkdir()
    (tmp_path / "src" / "main.rs").write_text("fn main() {}\n", encoding="utf-8")
    wrapper = tmp_path / "wrap.sh"
    wrapper.write_text("#!/bin/sh\ntouch ran\nexit 0\n", encoding="utf-8")
    wrapper.chmod(0o755)
    cargo_dir = tmp_path / ".cargo"
    cargo_dir.mkdir()
    cargo_config = cargo_dir / "config.toml"
    cargo_config.write_text('[build]\nrustc-wrapper = "./wrap.sh"\n', encoding="utf-8")
    built = _run(["cargo", "build", "--offline"], tmp_path)
    assert (tmp_path / "ran").exists(), built.stderr
    (tmp_path / "ran").unlink()
    cargo_config.write_text('[build]\nrustc-wrapper = "sudo reboot"\n', encoding="utf-8")
    _blocked("cargo build --offline", tmp_path)
    assert launch_block_reason("cargo --version", tmp_path) is None
    cargo_config.write_text('[build]\nrustc-wrapper = "sccache"\n', encoding="utf-8")
    assert launch_block_reason("cargo build --offline", tmp_path) is None


def test_perl5opt_scans_the_loaded_module_not_a_sub(tmp_path: Path) -> None:
    module = tmp_path / "evil.pm"
    module.write_text('system("touch ran");\n1;\n', encoding="utf-8")
    proved = _run(["perl", "-e", "print 1"], tmp_path, {"PERL5OPT": "-I. -Mevil"})
    assert proved.returncode == 0 and (tmp_path / "ran").exists(), proved.stderr
    (tmp_path / "ran").unlink()
    module.write_text('system("chmod 777 secrets.txt");\n1;\n', encoding="utf-8")
    result = execute_shell_command(
        "PERL5OPT='-I. -Mevil' perl -e 'print 1'",
        work_dir=tmp_path,
        timeout=5,
    )
    assert result.exit_code == 126
    assert not (tmp_path / "ran").exists()

    module.write_text('sub later { system("chmod 777 secrets.txt"); }\n1;\n', encoding="utf-8")
    assert launch_block_reason("PERL5OPT='-I. -Mevil' perl -e 'print 1'", tmp_path) is None
    module.write_text('BEGIN { system("chmod 777 secrets.txt"); }\n1;\n', encoding="utf-8")
    reason = launch_block_reason("PERL5OPT='-I. -Mevil' perl -e 'print 1'", tmp_path)
    assert reason is not None and "PERL5OPT" in reason
    module.write_text('# system("chmod 777 secrets.txt")\n1;\n', encoding="utf-8")
    assert launch_block_reason("PERL5OPT='-I. -Mevil' perl -e 'print 1'", tmp_path) is None


def test_ansible_adhoc_runs_the_module_argument(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    proved = _run(
        ["ansible", "localhost", "-i", "localhost,", "-c", "local", "-m", "shell", "-a", "touch ran"],
        tmp_path,
    )
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("ansible localhost -m shell -a 'sudo reboot'", tmp_path)
    assert launch_block_reason("ansible localhost -m ping", tmp_path) is None


def test_node_options_require_runs_before_the_program(tmp_path: Path) -> None:
    (tmp_path / "evil.js").write_text(
        'require("fs").writeFileSync("ran", "")\n',
        encoding="utf-8",
    )
    proved = _run(["node", "-e", "0"], tmp_path, {"NODE_OPTIONS": "--require ./evil.js"})
    assert proved.returncode == 0 and (tmp_path / "ran").exists(), proved.stderr
    (tmp_path / "ran").unlink()
    _blocked("NODE_OPTIONS='--require ./evil.js' node -e 0", tmp_path)


def test_interpreter_stdin_is_the_program(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    proved = _run(["sh", "-c", "echo 'open(\"ran\",\"w\").close()' | python3"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("echo 'import os; os.system(\"sudo reboot\")' | python3", tmp_path)
    assert launch_block_reason("echo data | python3 -c 'print(1)'", tmp_path) is None
