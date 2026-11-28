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


def test_unshare_setarch_capsh_script_and_choom(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    proved = _run(["unshare", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("unshare sudo reboot", tmp_path)

    proved = _run(["linux64", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("linux64 sudo reboot", tmp_path)
    refused = _run(["setarch", "sudo", "reboot"], tmp_path)
    assert refused.returncode != 0 and not marker.exists()

    proved = _run(["capsh", "--", "-c", "touch ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("capsh -- -c 'sudo reboot'", tmp_path)

    proved = _run(["script", "-qc", "touch ran", "/dev/null"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("script -qc 'sudo reboot' /dev/null", tmp_path)

    proved = _run(["choom", "-n", "0", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("choom -n 0 sudo reboot", tmp_path)


def test_ssh_proxy_tar_program_and_cmake_env(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "file.txt").write_text("x\n", encoding="utf-8")
    proved = _run(
        [
            "ssh", "-o", "ProxyCommand=touch ran", "-o", "BatchMode=yes",
            "-o", "ConnectTimeout=1", "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null", "127.0.0.1",
        ],
        tmp_path,
    )
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("ssh -o ProxyCommand='sudo reboot' 127.0.0.1", tmp_path)

    archived = _run(["tar", "-cf", "a.tar", "file.txt"], tmp_path)
    assert archived.returncode == 0, archived.stderr
    proved = _run(["tar", "--to-command=touch ran", "-xf", "a.tar"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("tar --to-command='sudo reboot' -xf a.tar", tmp_path)
    proved = _run(["tar", "-I", "touch ran; cat", "-cf", "b.tar", "file.txt"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("tar -I 'sudo reboot' -cf b.tar file.txt", tmp_path)

    proved = _run(["cmake", "-E", "env", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("cmake -E env sudo reboot", tmp_path)
    nested = tmp_path / "nested"
    nested.mkdir()
    proved = _run(["cmake", "-E", "chdir", str(nested), "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and (nested / "ran").exists(), proved.stderr
    _blocked("cmake -E chdir . sudo reboot", tmp_path)
    folder = tmp_path / "build"
    folder.mkdir()
    removed = execute_shell_command("cmake -E remove_directory build", work_dir=tmp_path, timeout=5)
    assert removed.exit_code == 0 and not folder.exists()
    _blocked("cmake -E remove_directory /", tmp_path)


def test_interpreter_stdin_is_the_program(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    proved = _run(["sh", "-c", "echo 'open(\"ran\",\"w\").close()' | python3"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("echo 'import os; os.system(\"sudo reboot\")' | python3", tmp_path)
    assert launch_block_reason("echo data | python3 -c 'print(1)'", tmp_path) is None


def test_env_chdir_split_string_and_display_wrappers(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    nested = tmp_path / "nested"
    nested.mkdir()
    proved = _run(["env", "-C", str(nested), "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and (nested / "ran").exists(), proved.stderr
    proved = _run(["env", "-S", "touch ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    proved = _run(["env", "--split-string=touch ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    proved = _run(["stdbuf", "-i", "0", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists()
    marker.unlink()
    proved = _run(["nice", "--adjustment", "0", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists()
    marker.unlink()
    _blocked("env -C /tmp sudo reboot", tmp_path)
    _blocked("env --split-string='rm -rf /'", tmp_path)
    _blocked("stdbuf --output 0 sudo reboot", tmp_path)
    _blocked("nice --adjustment 0 chmod 777 /", tmp_path)


def test_dbus_session_and_xvfb_run_the_command(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    proved = _run(["dbus-run-session", "--", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    proved = _run(["xvfb-run", "-a", "touch", "ran"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("dbus-run-session sudo reboot", tmp_path)
    _blocked("xvfb-run -s '-screen 0 640x480x8' sudo reboot", tmp_path)


def test_tar_checkpoint_exec_and_sort_compress_program(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    (tmp_path / "file.txt").write_text("x\n", encoding="utf-8")
    proved = _run(
        ["tar", "--checkpoint=1", "--checkpoint-action=exec=touch ran", "-cf", "a.tar", "file.txt"],
        tmp_path,
    )
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("tar --checkpoint=1 --checkpoint-action=exec='sudo reboot' -cf a.tar file.txt", tmp_path)

    script = tmp_path / "spill.sh"
    script.write_text("#!/bin/sh\ntouch ran\ncat\n", encoding="utf-8")
    script.chmod(0o755)
    proved = _run(
        ["sh", "-c", f"seq 1 30000 | sort -S 1k --compress-program={script}"],
        tmp_path,
    )
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("sort --compress-program=sudo data", tmp_path)


def test_editors_and_unsafe_roff_run_a_shell(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    proved = _run(["vim", "-u", "NONE", "-es", "-c", "!touch ran", "-c", "qa!"], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    proved = _run(["emacs", "--batch", "--eval", '(shell-command "touch ran")'], tmp_path)
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    proved = _run(
        ["sh", "-c", "printf '.sy touch ran\\n' | groff -U -Tascii >/dev/null"],
        tmp_path,
    )
    assert proved.returncode == 0 and marker.exists(), proved.stderr
    marker.unlink()
    _blocked("vim -u NONE -es -c '!sudo reboot' -c qa!", tmp_path)
    _blocked("emacs --batch --eval '(call-process \"sudo\" nil nil nil \"reboot\")'", tmp_path)
    _blocked("printf '.sy sudo reboot\\n' | groff -U -Tascii", tmp_path)
    named = tmp_path / "doc.roff"
    named.write_text(".sy touch ran\n", encoding="utf-8")
    assert launch_block_reason("groff -U doc.roff", tmp_path) is None


def test_ssh_config_match_exec_and_proxy_command(tmp_path: Path) -> None:
    marker = tmp_path / "ran"
    config = tmp_path / "ssh_config"
    config.write_text('Match exec "touch ran"\n', encoding="utf-8")
    proved = _run(["ssh", "-F", str(config), "-G", "example.invalid"], tmp_path)
    assert marker.exists(), proved.stderr
    marker.unlink()
    config.write_text('Match exec "sudo reboot"\n', encoding="utf-8")
    _blocked(f"ssh -F {config} -G example.invalid", tmp_path)
    assert launch_block_reason(f"ssh -V -F {config}", tmp_path) is None
    config.write_text('Match host nomatch exec "sudo reboot"\n', encoding="utf-8")
    assert launch_block_reason(f"ssh -F {config} -G example.invalid", tmp_path) is None
    config.write_text('Match exec "sudo reboot" host nomatch\n', encoding="utf-8")
    reason = launch_block_reason(f"ssh -F {config} -G example.invalid", tmp_path)
    assert reason is not None and "Match exec" in reason

    config.write_text("Host *\n  ProxyCommand touch ran\n", encoding="utf-8")
    proved = _run(
        [
            "ssh", "-F", str(config), "-o", "BatchMode=yes", "-o", "ConnectTimeout=1",
            "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
            "127.0.0.1",
        ],
        tmp_path,
    )
    assert marker.exists(), proved.stderr
    marker.unlink()
    config.write_text("Host *\n  ProxyCommand sudo reboot\n", encoding="utf-8")
    reason = launch_block_reason(f"ssh -F {config} example.invalid", tmp_path)
    assert reason is not None and "ProxyCommand" in reason
    assert launch_block_reason(
        f"ssh -F {config} -o ProxyCommand=echo example.invalid", tmp_path
    ) is None
    (tmp_path / "a.txt").write_text("a\n", encoding="utf-8")
    (tmp_path / "b.txt").write_text("b\n", encoding="utf-8")
    assert launch_block_reason(f"scp -F {config} a.txt b.txt", tmp_path) is None
    remote = launch_block_reason(f"scp -F {config} host:a b.txt", tmp_path)
    assert remote is not None and "ProxyCommand" in remote
    assert launch_block_reason("ssh -G example.invalid", tmp_path) is None

    program = tmp_path / "scp-ssh.sh"
    program.write_text("#!/bin/sh\ntouch ran\nexit 1\n", encoding="utf-8")
    program.chmod(0o755)
    proved = _run(
        ["scp", "-S", str(program), "-o", "BatchMode=yes", "a.txt", "127.0.0.1:b.txt"],
        tmp_path,
    )
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("scp -S sudo host:a b.txt", tmp_path)


def test_git_external_diff_and_credential_helper(tmp_path: Path) -> None:
    repo = tmp_path / "repo"
    repo.mkdir()
    assert _run(["git", "init"], repo).returncode == 0
    (repo / "a.txt").write_text("a\n", encoding="utf-8")
    assert _run(["git", "add", "a.txt"], repo).returncode == 0
    committed = _run(
        ["git", "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-m", "m"],
        repo,
    )
    assert committed.returncode == 0, committed.stderr
    (repo / "a.txt").write_text("b\n", encoding="utf-8")
    script = repo / "ext.sh"
    script.write_text("#!/bin/sh\ntouch ran\nexit 0\n", encoding="utf-8")
    script.chmod(0o755)
    proved = _run(["git", "-c", f"diff.external={script}", "diff"], repo)
    assert (repo / "ran").exists(), proved.stderr
    (repo / "ran").unlink()
    _run(["git", "config", "diff.external", "sudo reboot"], repo)
    reason = launch_block_reason("git diff", repo)
    assert reason is not None and "高危" in reason
    assert launch_block_reason("GIT_EXTERNAL_DIFF= git diff", repo) is None
    assert launch_block_reason("git status --short", repo) is None
    assert launch_block_reason("export GIT_EXTERNAL_DIFF='sudo reboot'; git diff", repo) is not None

    _run(["git", "config", "--unset-all", "credential.helper"], repo)
    helper = _run(["git", "config", "--add", "credential.helper", "!touch ran"], repo)
    assert helper.returncode == 0, helper.stderr
    proved = _run(
        ["sh", "-c", "printf 'protocol=https\\nhost=example.invalid\\n\\n' | git credential fill"],
        repo,
    )
    assert (repo / "ran").exists(), proved.stderr
    (repo / "ran").unlink()
    _run(["git", "config", "--unset-all", "credential.helper"], repo)
    _run(["git", "config", "--add", "credential.helper", "!sudo reboot"], repo)
    _run(["git", "config", "--add", "credential.helper", "!chmod 777 /"], repo)
    reason = launch_block_reason("git credential fill", repo)
    assert reason is not None
    assert launch_block_reason("git -c credential.helper= credential fill", repo) is None
    assert launch_block_reason("git status --short", repo) is None
