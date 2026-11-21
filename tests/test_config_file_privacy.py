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


def test_hardlinked_config_is_not_loaded(tmp_path: Path, monkeypatch) -> None:
    outside = tmp_path / "outside.json"
    outside.write_text('{"api_key":"CANARY-KEY","model":"leak-model"}\n', encoding="utf-8")
    linked = tmp_path / "config.json"
    os.link(outside, linked)
    before = outside.read_text(encoding="utf-8")
    inode = os.lstat(outside).st_ino
    for name in ("TIANCODE_API_KEY", "DEEPSEEK_API_KEY", "TIANCODE_MODEL", "DEEPSEEK_MODEL"):
        monkeypatch.delenv(name, raising=False)

    from tiancode.config.settings import Settings
    from tiancode.core.engine import TianCodeEngine

    assert load_user_config(linked) == {}
    settings = Settings.load(config_path=linked)
    assert settings.api_key == ""
    assert settings.model == "deepseek-chat"

    engine = object.__new__(TianCodeEngine)
    engine.settings = Settings(api_key="KEEP-KEY", model="keep-model")
    engine._last_config_mtime = 0.0
    engine.reload_called = False

    def _reload() -> None:
        engine.reload_called = True

    engine.reload_llm = _reload
    assert engine.sync_config_if_changed(linked) is False
    assert engine.settings.api_key == "KEEP-KEY"
    assert engine.reload_called is False
    assert outside.read_text(encoding="utf-8") == before
    assert os.lstat(linked).st_ino == inode
    assert os.lstat(linked).st_nlink > 1

    regular = tmp_path / "regular.json"
    regular.write_text('{"api_key":"KEEP-KEY","model":"keep-model"}\n', encoding="utf-8")
    loaded = Settings.load(config_path=regular)
    assert loaded.api_key == "KEEP-KEY"
    assert loaded.model == "keep-model"


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
