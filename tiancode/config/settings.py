"""Configuration settings with ~/.tiancode/config.json persistence."""

import json
import os
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Dict, Optional


def get_default_config_path() -> Path:
    """Return ~/.tiancode/config.json path or TIANCODE_CONFIG_PATH override.

    The last path component is not resolved, so a symlink is not followed.
    """
    env_override = os.environ.get("TIANCODE_CONFIG_PATH")
    if not env_override:
        return Path.home() / ".tiancode" / "config.json"
    raw = Path(env_override).expanduser()
    if not raw.is_absolute():
        raw = Path.cwd() / raw
    try:
        parent = raw.parent.resolve()
    except OSError:
        parent = raw.parent.absolute()
    return parent / raw.name


def load_user_config(config_path: Optional[Path] = None) -> Dict[str, Any]:
    """Read configuration from a regular file. A symlink is not followed."""
    path = config_path or get_default_config_path()
    if path.is_symlink() or not path.exists():
        return {}
    try:
        with open(path, "r", encoding="utf-8-sig") as handle:
            loaded = json.load(handle)
    except Exception:
        return {}
    return loaded if isinstance(loaded, dict) else {}


def _replace_private_config(path: Path, data: Dict[str, Any]) -> None:
    """Write config as a private regular file. Do not follow a symlink."""
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    if temporary.is_symlink() or temporary.exists():
        temporary.unlink()
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(temporary, flags, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(data, handle, indent=2, ensure_ascii=False)
            handle.flush()
            os.fsync(handle.fileno())
    except Exception:
        temporary.unlink(missing_ok=True)
        raise
    os.replace(temporary, path)


def save_user_config(data: Dict[str, Any], config_path: Optional[Path] = None) -> None:
    """Save configuration into a private regular file."""
    path = config_path or get_default_config_path()
    _replace_private_config(path, data)


@dataclass
class Settings:
    """Application settings with 4-level fallback: CLI > Env > Config File > Default."""

    api_key: str
    base_url: str = "https://api.deepseek.com"
    model: str = "deepseek-chat"
    temperature: float = 0.0
    timeout_seconds: int = 15
    max_history_messages: int = 40
    max_context_chars: int = 60000
    work_dir: Path = Path(".")
    auto_heal: bool = True

    @classmethod
    def load(
        cls,
        api_key: Optional[str] = None,
        base_url: Optional[str] = None,
        model: Optional[str] = None,
        work_dir: Optional[Path] = None,
        config_path: Optional[Path] = None,
    ) -> "Settings":
        """Hierarchical loader: CLI Arg > Env Vars > User Config File > Defaults."""
        user_cfg = load_user_config(config_path)

        resolved_key = (
            api_key
            or os.environ.get("TIANCODE_API_KEY")
            or os.environ.get("DEEPSEEK_API_KEY")
            or user_cfg.get("api_key")
            or ""
        )

        resolved_base_url = (
            base_url
            or os.environ.get("TIANCODE_BASE_URL")
            or os.environ.get("DEEPSEEK_BASE_URL")
            or user_cfg.get("base_url")
            or "https://api.deepseek.com"
        )

        resolved_model = (
            model
            or os.environ.get("TIANCODE_MODEL")
            or os.environ.get("DEEPSEEK_MODEL")
            or user_cfg.get("model")
            or "deepseek-chat"
        )

        resolved_work_dir = work_dir or Path(os.environ.get("TIANCODE_WORK_DIR", ".")).resolve()

        raw_timeout = user_cfg.get("timeout_seconds")
        timeout_seconds = raw_timeout if type(raw_timeout) is int and raw_timeout > 0 else 15

        raw_max_hist = user_cfg.get("max_history_messages")
        max_history = raw_max_hist if type(raw_max_hist) is int and raw_max_hist > 1 else 40

        raw_max_chars = user_cfg.get("max_context_chars")
        max_chars = raw_max_chars if type(raw_max_chars) is int and raw_max_chars >= 500 else 60000

        raw_auto_heal = user_cfg.get("auto_heal")
        auto_heal = raw_auto_heal if isinstance(raw_auto_heal, bool) else True

        return cls(
            api_key=resolved_key,
            base_url=resolved_base_url,
            model=resolved_model,
            timeout_seconds=timeout_seconds,
            max_history_messages=max_history,
            max_context_chars=max_chars,
            auto_heal=auto_heal,
            work_dir=resolved_work_dir,
        )

    def persist(self, config_path: Optional[Path] = None) -> None:
        """Persist current core settings and runtime tuning options to user config file."""
        data = {
            "api_key": self.api_key,
            "base_url": self.base_url,
            "model": self.model,
            "timeout_seconds": self.timeout_seconds,
            "max_history_messages": self.max_history_messages,
            "max_context_chars": self.max_context_chars,
            "auto_heal": self.auto_heal,
        }
        save_user_config(data, config_path=config_path)


def prompt_setup_wizard(current_settings: Settings, config_path: Optional[Path] = None) -> Settings:
    """Interactive wizard to guide user setting up API Key when missing."""
    print("\n" + "=" * 60)
    print("🔑 未检测到已配置的 API Key！首次使用请配置大模型连接信息：")
    print("=" * 60)

    # 1. API Key
    while True:
        key = input("👉 请输入 API Key (如 sk-...): ").strip()
        if key:
            current_settings.api_key = key
            break
        print("⚠️ API Key 不能为空，请重新输入！")

    # 2. Base URL
    default_url = current_settings.base_url or "https://api.deepseek.com"
    url_input = input(f"👉 请输入 Base URL [直接回车默认: {default_url}]: ").strip()
    if url_input:
        current_settings.base_url = url_input

    # 3. Model
    default_model = current_settings.model or "deepseek-chat"
    model_input = input(f"👉 请输入模型名称 [直接回车默认: {default_model}]: ").strip()
    if model_input:
        current_settings.model = model_input

    # Save to ~/.tiancode/config.json
    current_settings.persist(config_path=config_path)
    target_path = config_path or get_default_config_path()
    print(f"\n✅ 配置已永久保存至: {target_path}\n")

    return current_settings
