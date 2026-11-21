"""Shell and test subprocesses must not inherit token-like environment variables.

The canaries are fake. The test does not read user config or call a network.
"""

from __future__ import annotations

import os

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
