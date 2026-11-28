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
    import shutil

    marker = tmp_path / "ran"
    if shutil.which("vim"):
        proved = _run(["vim", "-u", "NONE", "-es", "-c", "!touch ran", "-c", "qa!"], tmp_path)
        assert proved.returncode == 0 and marker.exists(), proved.stderr
        marker.unlink()
    if shutil.which("emacs"):
        proved = _run(["emacs", "--batch", "--eval", '(shell-command "touch ran")'], tmp_path)
        assert proved.returncode == 0 and marker.exists(), proved.stderr
        marker.unlink()
    if shutil.which("groff"):
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

    real = tmp_path / "real_config"
    real.write_text("Host *\n  ProxyCommand echo\n", encoding="utf-8")
    linked = tmp_path / "linked_config"
    linked.symlink_to(real)
    reason = launch_block_reason(f"ssh -F {linked} -G example.invalid", tmp_path)
    assert reason is not None and "符号链接" in reason

    # A distro include is a symlink from ssh_config.d to /usr/lib. The system
    # reader follows it. A user `-F` file does not.
    from tiancode.core.launch_guard import _SshScan, _ssh_read, _ssh_system_path

    assert _ssh_system_path(Path("/etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf"))
    assert not _ssh_system_path(linked)

    lib = tmp_path / "lib"
    lib.mkdir()
    target = lib / "20.conf"
    target.write_text("Host *\n  ProxyCommand sudo reboot\n", encoding="utf-8")
    dropin = tmp_path / "ssh_config.d"
    dropin.mkdir()
    (dropin / "20.conf").symlink_to(target)
    main = tmp_path / "main_config"
    main.write_text(f"Include {dropin}/*.conf\n", encoding="utf-8")
    followed = _SshScan(command_sets_proxy=False)
    _ssh_read(main, "example.invalid", None, followed, depth=0, seen=set(), follow=True)
    assert followed.error == ""
    assert followed.proxy.startswith("sudo")
    refused = _SshScan(command_sets_proxy=False)
    _ssh_read(main, "example.invalid", None, refused, depth=0, seen=set(), follow=False)
    assert refused.error and "符号链接" in refused.error


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


def _git(repo: Path, args: list[str], **extra: str) -> subprocess.CompletedProcess[str]:
    env = {
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_AUTHOR_NAME": "t",
        "GIT_AUTHOR_EMAIL": "t@example.com",
        "GIT_COMMITTER_NAME": "t",
        "GIT_COMMITTER_EMAIL": "t@example.com",
    }
    env.update(extra)
    return _run(["git", *args], repo, env)


def test_pic_sh_and_groff_preprocessor(tmp_path: Path) -> None:
    import shutil

    marker = tmp_path / "ran"
    if shutil.which("pic"):
        proved = _run(
            ["sh", "-c", "printf '.PS\\nsh X touch ran X\\n.PE\\n' | pic -U"],
            tmp_path,
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("printf '.PS\\nsh X sudo reboot X\\n.PE\\n' | pic -U", tmp_path)
    _blocked("pic -U <<'EOF'\n.PS\nsh X rm -rf / X\n.PE\nEOF", tmp_path)
    echoed = execute_shell_command(
        "pic -U <<'EOF'\nbox\nEOF", work_dir=tmp_path, timeout=5
    )
    assert echoed.exit_code != 126
    assert launch_block_reason("pic -U doc.pic", tmp_path) is None
    assert launch_block_reason("printf 'sh X sudo reboot X\\n' | pic -U -S", tmp_path) is None

    if shutil.which("groff"):
        proved = _run(
            ["sh", "-c", "printf '.PS\\nsh X touch ran X\\n.PE\\n' | groff -U -p -Tascii >/dev/null"],
            tmp_path,
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("groff -U -p -Tascii <<'EOF'\n.PS\nsh X chmod 777 / X\n.PE\nEOF", tmp_path)
    named = tmp_path / "doc.roff"
    named.write_text(".PS\nsh X sudo reboot X\n.PE\n", encoding="utf-8")
    assert launch_block_reason(f"groff -U -p -Tascii {named}", tmp_path) is None
    assert launch_block_reason(
        "groff -U -Tascii <<'EOF'\n.PS\nsh X sudo reboot X\n.PE\nEOF", tmp_path
    ) is None


def test_sftp_tar_rsh_zip_and_gcc_wrapper(tmp_path: Path) -> None:
    import shutil

    marker = tmp_path / "ran"
    script = tmp_path / "run.sh"
    script.write_text("#!/bin/sh\ntouch ran\nexit 1\n", encoding="utf-8")
    script.chmod(0o755)
    (tmp_path / "a.txt").write_text("a\n", encoding="utf-8")
    if shutil.which("sftp"):
        proved = _run(
            ["sftp", "-S", str(script), "-o", "BatchMode=yes", "127.0.0.1"],
            tmp_path,
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
        proved = _run(["sftp", "-D", str(script)], tmp_path)
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("sftp -S sudo 127.0.0.1", tmp_path)
    _blocked("sftp -D sudo", tmp_path)
    assert launch_block_reason("sftp -S /usr/bin/ssh 127.0.0.1", tmp_path) is None

    if shutil.which("tar"):
        proved = _run(
            ["tar", f"--rsh-command={script}", "-cf", "localhost:out.tar", "a.txt"],
            tmp_path,
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("tar --rsh-command=sudo -cf host:a.tar a.txt", tmp_path)
    assert launch_block_reason("tar --rsh-command=ssh -cf host:a.tar a.txt", tmp_path) is None
    assert launch_block_reason(
        "tar --rsh-command=sudo --force-local -cf host:a.tar a.txt", tmp_path
    ) is None
    assert launch_block_reason("tar --rsh-command=sudo -cf local.tar a.txt", tmp_path) is None

    if shutil.which("zip"):
        proved = _run(["zip", "-T", "-TT", "touch ran", "out.zip", "a.txt"], tmp_path)
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("zip -T -TT 'sudo reboot' out.zip a.txt", tmp_path)
    _blocked("zip -T -TT 'rm -rf /' out.zip a.txt", tmp_path)
    assert launch_block_reason("zip -TT 'sudo reboot' out.zip a.txt", tmp_path) is None
    assert launch_block_reason("zip -T -TT 'unzip -tqq' out.zip a.txt", tmp_path) is None

    if shutil.which("gcc"):
        wrap = tmp_path / "wrap.sh"
        wrap.write_text("#!/bin/sh\ntouch ran\nexit 0\n", encoding="utf-8")
        wrap.chmod(0o755)
        (tmp_path / "t.c").write_text("int main(void){return 0;}\n", encoding="utf-8")
        proved = _run(["gcc", "-wrapper", str(wrap), "-c", "t.c"], tmp_path)
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("gcc -wrapper sudo,reboot -c t.c", tmp_path)
    _blocked("g++ -wrapper chmod,777,/ -c t.cpp", tmp_path)
    assert launch_block_reason("gcc -wrapper echo -c t.c", tmp_path) is None


def test_sg_and_start_stop_daemon(tmp_path: Path) -> None:
    import shutil

    marker = tmp_path / "ran"
    if shutil.which("sg"):
        group = _run(["id", "-gn"], tmp_path).stdout.strip()
        proved = _run(["sg", group, "-c", "touch ran"], tmp_path)
        assert marker.exists(), proved.stderr + proved.stdout
        marker.unlink()
    _blocked("sg users -c 'sudo reboot'", tmp_path)
    _blocked("sg users -c 'chmod 777 /'", tmp_path)
    assert launch_block_reason("sg users -c 'pytest -q'", tmp_path) is None
    assert launch_block_reason("sg users rm -rf /", tmp_path) is None

    if shutil.which("start-stop-daemon"):
        proved = _run(
            [
                "start-stop-daemon", "--start", "--oknodo",
                "--exec", "/usr/bin/touch", "--", str(marker),
            ],
            tmp_path,
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("start-stop-daemon --start --exec sudo -- reboot", tmp_path)
    _blocked("start-stop-daemon --start --exec rm -- -rf /", tmp_path)
    assert launch_block_reason(
        "start-stop-daemon --stop --exec sudo -- reboot", tmp_path
    ) is None
    assert launch_block_reason(
        "start-stop-daemon --test --exec sudo -- reboot", tmp_path
    ) is None


def test_perl_debugger_and_python_startup(tmp_path: Path, monkeypatch) -> None:
    import shutil

    marker = tmp_path / "ran"
    if shutil.which("perl"):
        proved = _run(
            ["perl", "-d", "-e", "1"],
            tmp_path,
            env={"PERL5DB": 'system("touch ran");'},
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked('PERL5DB=\'system("sudo reboot");\' perl -d -e 1', tmp_path)
    _blocked('PERL5OPT=-d PERL5DB=\'system("chmod 777 /");\' perl -e 1', tmp_path)
    assert launch_block_reason("perl -d -e 1", tmp_path) is None
    assert launch_block_reason('PERL5DB=\'system("sudo reboot");\' perl -e 1', tmp_path) is None
    monkeypatch.setenv("PERL5DB", 'system("rm -rf /");')
    reason = launch_block_reason("perl -d -e 1", tmp_path)
    assert reason is not None and "PERL5DB" in reason
    monkeypatch.delenv("PERL5DB", raising=False)
    assert launch_block_reason("perl -d -e 1", tmp_path) is None

    site = tmp_path / "site"
    site.mkdir()
    (site / "sitecustomize.py").write_text("open('ran','w').close()\n", encoding="utf-8")
    proved = _run(
        ["python3", "-c", "print(1)"],
        tmp_path,
        env={"PYTHONPATH": str(site), "PYTHONSAFEPATH": "1"},
    )
    assert marker.exists(), proved.stderr
    marker.unlink()
    skipped = _run(
        ["python3", "-S", "-c", "print(1)"],
        tmp_path,
        env={"PYTHONPATH": str(site)},
    )
    assert not marker.exists(), skipped.stderr
    (site / "sitecustomize.py").write_text(
        'import os\nos.system("sudo reboot")\n', encoding="utf-8"
    )
    _blocked(f"PYTHONPATH={site} python3 -c 'print(1)'", tmp_path)
    assert launch_block_reason(f"PYTHONPATH={site} python3 -S -c 'print(1)'", tmp_path) is None
    assert launch_block_reason(f"PYTHONPATH={site} python3 -E -c 'print(1)'", tmp_path) is None
    assert launch_block_reason(f"PYTHONPATH={site} python3 -I -c 'print(1)'", tmp_path) is None

    (site / "sitecustomize.py").write_text("print('hi')\n", encoding="utf-8")
    (site / "usercustomize.py").write_text("open('ran','w').close()\n", encoding="utf-8")
    proved = _run(["python3", "-c", "print(1)"], tmp_path, env={"PYTHONPATH": str(site)})
    assert marker.exists(), proved.stderr
    marker.unlink()
    skipped = _run(["python3", "-s", "-c", "print(1)"], tmp_path, env={"PYTHONPATH": str(site)})
    assert not marker.exists(), skipped.stderr
    (site / "usercustomize.py").write_text(
        'import os\nos.system("rm -rf /")\n', encoding="utf-8"
    )
    _blocked(f"PYTHONPATH={site} python3 -c 'print(1)'", tmp_path)
    assert launch_block_reason(f"PYTHONPATH={site} python3 -s -c 'print(1)'", tmp_path) is None
    monkeypatch.setenv("PYTHONPATH", "/tmp/outside-pythonpath")
    assert launch_block_reason("python3 -c 'print(1)'", tmp_path) is None
    assert launch_block_reason("cd /tmp && python3 -c 'print(1)'", tmp_path) is None


def test_make_stdin_and_ctest_add_test(tmp_path: Path) -> None:
    import shutil

    from tiancode.core.recipe_guard import recipe_block_reason

    marker = tmp_path / "ran"
    if shutil.which("make"):
        proved = _run(
            ["sh", "-c", "printf 'all:\\n\\ttouch ran\\n' | make -f /dev/stdin"],
            tmp_path,
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
        proved = _run(
            ["sh", "-c", "printf 'all:\\n\\ttouch ran\\n' | make -f /dev/fd/0"],
            tmp_path,
        )
        assert marker.exists(), proved.stderr
        marker.unlink()
    _blocked("printf 'all:\\n\\ttouch ran\\n' | make -f /dev/stdin", tmp_path)
    _blocked("printf 'all:\\n\\tsudo reboot\\n' | make -f /dev/fd/0", tmp_path)
    _blocked("make --file=/proc/self/fd/0", tmp_path)
    (tmp_path / "Makefile").write_text("all:\n\ttrue\n", encoding="utf-8")
    assert recipe_block_reason("make -f Makefile", tmp_path) is None

    listing = "add_test([=[t]=] [=[/usr/bin/touch]=] [=[ran]=])\n"
    (tmp_path / "CTestTestfile.cmake").write_text(listing, encoding="utf-8")
    if shutil.which("ctest"):
        proved = _run(["ctest", "--output-on-failure"], tmp_path)
        assert marker.exists(), proved.stderr
        marker.unlink()
    (tmp_path / "CTestTestfile.cmake").write_text("add_test(t sudo reboot)\n", encoding="utf-8")
    reason = launch_block_reason("ctest", tmp_path)
    assert reason is not None and "CTest" in reason
    assert launch_block_reason("ctest -N", tmp_path) is None
    (tmp_path / "CTestTestfile.cmake").write_text("add_test(t /bin/true)\n", encoding="utf-8")
    assert launch_block_reason("ctest", tmp_path) is None
    assert launch_block_reason("cd /tmp && ctest", tmp_path) is not None

    child = tmp_path / "child"
    child.mkdir()
    (child / "CTestTestfile.cmake").write_text(
        f"add_test(t /usr/bin/touch {marker})\n", encoding="utf-8"
    )
    (tmp_path / "CTestTestfile.cmake").write_text("subdirs(child)\n", encoding="utf-8")
    if shutil.which("ctest"):
        proved = _run(["ctest", "--output-on-failure"], tmp_path)
        assert marker.exists(), proved.stderr
        marker.unlink()
    (child / "CTestTestfile.cmake").write_text(
        "add_test(t chmod 777 /)\n", encoding="utf-8"
    )
    assert launch_block_reason("ctest", tmp_path) is not None


def test_git_editors_rebase_bisect_difftool_and_signing(tmp_path: Path) -> None:
    repo = tmp_path / "repo"
    repo.mkdir()
    marker = repo / "ran"
    assert _git(repo, ["init"]).returncode == 0
    (repo / "a.txt").write_text("a\n", encoding="utf-8")
    assert _git(repo, ["add", "a.txt"]).returncode == 0
    editor = repo / "ed.sh"
    editor.write_text(
        "#!/bin/sh\ntouch ran\nif [ -n \"$1\" ]; then printf 'm\\n' > \"$1\"; fi\nexit 0\n",
        encoding="utf-8",
    )
    editor.chmod(0o755)
    sequence = repo / "seq.sh"
    sequence.write_text("#!/bin/sh\ntouch ran\nexit 0\n", encoding="utf-8")
    sequence.chmod(0o755)

    proved = _git(repo, ["commit"], GIT_EDITOR=str(editor))
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("GIT_EDITOR='sudo reboot' git commit", repo)
    assert launch_block_reason("GIT_EDITOR='sudo reboot' git commit -m m", repo) is None
    assert launch_block_reason("export GIT_EDITOR='rm -rf /'; git commit", repo) is not None
    assert launch_block_reason("VISUAL='sudo reboot' git commit", repo) is None

    proved = _git(repo, ["tag", "-a", "v1"], GIT_EDITOR=str(editor))
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("GIT_EDITOR='chmod 777 /' git tag -a v2", repo)
    assert launch_block_reason("GIT_EDITOR='sudo reboot' git tag -a v2 -m m", repo) is None

    proved = _git(repo, ["notes", "add"], GIT_EDITOR=str(editor))
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("EDITOR='sudo reboot' git notes add", repo)
    assert launch_block_reason("GIT_EDITOR='sudo reboot' git notes add -m hi", repo) is None

    assert _git(repo, ["commit", "--allow-empty", "-m", "second"], GIT_EDITOR="true").returncode == 0
    proved = _git(repo, ["rebase", "-i", "HEAD~1"], GIT_SEQUENCE_EDITOR=str(sequence))
    assert marker.exists(), proved.stderr
    marker.unlink()
    _git(repo, ["rebase", "--abort"])
    _blocked("GIT_SEQUENCE_EDITOR='sudo reboot' git rebase -i HEAD", repo)
    assert launch_block_reason("git rebase --exec 'pytest -q' HEAD~1", repo) is None
    proved = _git(repo, ["rebase", "--exec", "touch ran", "HEAD~1"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("git rebase --exec 'sudo reboot' HEAD~1", repo)
    _blocked("git rebase -x 'rm -rf /' HEAD~1", repo)

    started = _git(repo, ["bisect", "start", "HEAD", "HEAD~1"])
    assert started.returncode == 0, started.stderr
    proved = _git(repo, ["bisect", "run", "touch", "ran"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _git(repo, ["bisect", "reset"])
    _blocked("git bisect run sudo reboot", repo)
    assert launch_block_reason("git bisect run pytest -q", repo) is None

    (repo / "a.txt").write_text("b\n", encoding="utf-8")
    proved = _git(repo, ["difftool", "-y", "--extcmd", "touch ran"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("git difftool -y -x 'chmod 777 /'", repo)
    assert launch_block_reason("git difftool -x 'sudo reboot'", repo) is None
    proved = _git(
        repo,
        ["-c", "difftool.prompt=false", "difftool", "-x", "touch ran"],
    )
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("git -c difftool.prompt=false difftool -x 'sudo reboot'", repo)

    signer = repo / "gpg.sh"
    signer.write_text("#!/bin/sh\ntouch ran\nexit 0\n", encoding="utf-8")
    signer.chmod(0o755)
    proved = _git(
        repo,
        ["-c", f"gpg.program={signer}", "commit", "-S", "--allow-empty", "-m", "signed"],
    )
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("git -c gpg.program=sudo commit -S -m m", repo)
    assert launch_block_reason("git -c gpg.program=sudo commit -m m", repo) is None
    assert launch_block_reason(
        "git -c gpg.program=sudo commit -S --no-gpg-sign -m m", repo
    ) is None
    assert launch_block_reason(
        "git -c gpg.format=ssh -c gpg.program=sudo commit -S -m m", repo
    ) is None
    proved = _git(repo, ["-c", f"gpg.program={signer}", "tag", "-s", "-m", "t", "vsigned"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _blocked("git -c gpg.program='rm -rf /' tag -s -m m v3", repo)


def test_git_attribute_drivers(tmp_path: Path) -> None:
    repo = tmp_path / "repo"
    repo.mkdir()
    marker = repo / "ran"
    assert _git(repo, ["init"]).returncode == 0
    script = repo / "filt.sh"
    script.write_text("#!/bin/sh\ntouch ran\ncat\n", encoding="utf-8")
    script.chmod(0o755)
    (repo / "a.txt").write_text("a\n", encoding="utf-8")
    assert _git(repo, ["add", "a.txt"]).returncode == 0
    assert _git(repo, ["commit", "-m", "m"], GIT_EDITOR="true").returncode == 0

    assert _git(repo, ["config", "filter.mine.clean", str(script)]).returncode == 0
    (repo / "a.txt").write_text("b\n", encoding="utf-8")
    without = _git(repo, ["add", "a.txt"])
    assert not marker.exists(), without.stderr
    assert launch_block_reason("git add a.txt", repo) is None
    (repo / ".gitattributes").write_text("* filter=mine\n", encoding="utf-8")
    proved = _git(repo, ["add", "a.txt"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    (repo / "a.txt").write_text("c\n", encoding="utf-8")
    proved = _git(repo, ["status"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _git(repo, ["config", "filter.mine.clean", "sudo reboot"])
    reason = launch_block_reason("git add a.txt", repo)
    assert reason is not None and "属性" in reason
    (repo / ".gitattributes").unlink()
    assert launch_block_reason("git status", repo) is None

    (repo / ".gitattributes").write_text("* filter=mine\n", encoding="utf-8")
    assert _git(repo, ["config", "filter.mine.clean", "cat"]).returncode == 0
    assert _git(repo, ["config", "filter.mine.smudge", str(script)]).returncode == 0
    assert _git(repo, ["add", "a.txt"]).returncode == 0
    assert _git(repo, ["commit", "-m", "filt"], GIT_EDITOR="true").returncode == 0
    (repo / "a.txt").unlink()
    proved = _git(repo, ["checkout", "--", "a.txt"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    (repo / "a.txt").unlink()
    proved = _git(repo, ["restore", "--", "a.txt"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _git(repo, ["config", "filter.mine.smudge", "sudo reboot"])
    assert launch_block_reason("git checkout -- a.txt", repo) is not None
    assert launch_block_reason("git restore -- a.txt", repo) is not None

    (repo / ".gitattributes").write_text("* diff=mine\n", encoding="utf-8")
    assert _git(repo, ["config", "diff.mine.textconv", str(script)]).returncode == 0
    (repo / "a.txt").write_text("changed\n", encoding="utf-8")
    proved = _git(repo, ["diff"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _git(repo, ["config", "diff.mine.textconv", "sudo reboot"])
    assert launch_block_reason("git diff", repo) is not None
    assert launch_block_reason("git log", repo) is None
    assert launch_block_reason("git log -p", repo) is not None
    (repo / ".gitattributes").unlink()
    assert launch_block_reason("git diff", repo) is None

    (repo / ".gitattributes").write_text("* diff=mine\n", encoding="utf-8")
    assert _git(repo, ["config", "--unset", "diff.mine.textconv"]).returncode == 0
    assert _git(repo, ["config", "diff.mine.command", str(script)]).returncode == 0
    proved = _git(repo, ["diff"])
    assert marker.exists(), proved.stderr
    marker.unlink()
    _git(repo, ["config", "diff.mine.command", "chmod 777 /"])
    assert launch_block_reason("git diff", repo) is not None
    assert launch_block_reason("git status", repo) is None
