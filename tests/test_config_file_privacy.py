"""Config saves stay private and do not follow a symlink.

The key is a fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import os
import stat
from pathlib import Path

from tiancode.config.settings import load_user_config, save_user_config


def _mode(path: Path) -> int:
    return stat.S_IMODE(os.lstat(path).st_mode)


def test_saved_config_is_owner_rw_even_with_permissive_umask(tmp_path: Path) -> None:
    previous = os.umask(0o000)
    try:
        path = tmp_path / "config.json"
        save_user_config({"api_key": "sk-test-fake", "model": "fake"}, path)
        loaded = load_user_config(path)

        assert path.is_file() and not path.is_symlink()
        assert _mode(path) == 0o600
        assert loaded["api_key"] == "sk-test-fake"
        assert list(tmp_path.glob("*.tmp")) == []
    finally:
        os.umask(previous)


def test_config_symlink_is_not_followed(tmp_path: Path, monkeypatch) -> None:
    outside = tmp_path / "outside.txt"
    outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    link = tmp_path / "config.json"
    link.symlink_to(outside)
    monkeypatch.setenv("TIANCODE_CONFIG_PATH", str(link))

    assert load_user_config() == {}
    save_user_config({"api_key": "sk-test-fake"})

    assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert link.is_file() and not link.is_symlink()
    assert _mode(link) == 0o600
    assert load_user_config()["api_key"] == "sk-test-fake"
    assert not (tmp_path / "config.json.tmp").exists()


def test_failed_save_keeps_existing_config(tmp_path: Path, monkeypatch) -> None:
    path = tmp_path / "config.json"
    path.write_text('{"api_key":"KEEP-KEY"}\n', encoding="utf-8")
    os.chmod(path, 0o600)

    def boom(*_args, **_kwargs) -> None:
        raise TypeError("simulated config encode failure")

    monkeypatch.setattr("tiancode.config.settings.json.dump", boom)

    try:
        save_user_config({"api_key": "sk-test-fake"}, path)
    except TypeError:
        pass
    else:
        raise AssertionError("save should fail when encoding fails")

    assert path.read_text(encoding="utf-8") == '{"api_key":"KEEP-KEY"}\n'
    assert _mode(path) == 0o600
    assert not (tmp_path / "config.json.tmp").exists()
