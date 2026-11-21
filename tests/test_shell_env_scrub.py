"""Shell and test subprocesses must not inherit token-like environment variables.

The canaries are fake. The test does not read user config or call a network.
"""

from __future__ import annotations

import os
import subprocess

from tiancode.tools.execution import execute_shell_command, get_sanitized_env


def test_sanitized_env_drops_token_names_and_keeps_path(monkeypatch) -> None:
    monkeypatch.setenv("AWS_ACCESS_KEY_ID", "ISOLATED-CANARY")
    monkeypatch.setenv("GH_TOKEN", "ISOLATED-GH")
    monkeypatch.setenv("TIANCODE_API_KEY", "ISOLATED-TC")
    monkeypatch.setenv("LESSKEY", "keep-lesskey")
    monkeypatch.setenv("PATH", os.environ.get("PATH", "/usr/bin"))

    env = get_sanitized_env()

    assert "AWS_ACCESS_KEY_ID" not in env
    assert "GH_TOKEN" not in env
    assert "TIANCODE_API_KEY" not in env
    assert env.get("LESSKEY") == "keep-lesskey"
    assert env.get("PATH")


def test_git_config_key_slot_stays_so_git_can_start(tmp_path, monkeypatch) -> None:
    monkeypatch.setenv("AWS_ACCESS_KEY_ID", "ISOLATED-CANARY")
    monkeypatch.setenv("GIT_CONFIG_COUNT", "1")
    monkeypatch.setenv("GIT_CONFIG_KEY_0", "core.quotePath")
    monkeypatch.setenv("GIT_CONFIG_VALUE_0", "false")
    repo = tmp_path / "repo"
    repo.mkdir()
    subprocess.run(["git", "init", "-q"], cwd=repo, check=True)

    env = get_sanitized_env()
    result = execute_shell_command("git status --short", work_dir=repo, timeout=5)

    assert "AWS_ACCESS_KEY_ID" not in env
    assert env.get("GIT_CONFIG_KEY_0") == "core.quotePath"
    assert env.get("GIT_CONFIG_VALUE_0") == "false"
    assert result.exit_code == 0
    assert "missing config key" not in result.stderr
    assert "ISOLATED-CANARY" not in result.stdout
    assert "ISOLATED-CANARY" not in result.stderr


def test_shell_output_does_not_contain_inherited_canary(tmp_path, monkeypatch) -> None:
    monkeypatch.setenv("AWS_ACCESS_KEY_ID", "ISOLATED-CANARY")
    monkeypatch.setenv("GH_TOKEN", "ISOLATED-GH")
    monkeypatch.setenv("PATH", os.environ.get("PATH", "/usr/bin"))

    result = execute_shell_command(
        "printenv AWS_ACCESS_KEY_ID GH_TOKEN PATH",
        work_dir=tmp_path,
        timeout=5,
    )

    assert "ISOLATED-CANARY" not in result.stdout
    assert "ISOLATED-GH" not in result.stdout
    assert "/" in result.stdout
