"""Configuration settings with ~/.tiancode/config.json persistence."""

import json
import os
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Dict, Optional


def get_default_config_path() -> Path:
    """Return ~/.tiancode/config.json path or TIANCODE_CONFIG_PATH override."""
    env_override = os.environ.get("TIANCODE_CONFIG_PATH")
    if env_override:
        return Path(env_override).resolve()
    return Path.home() / ".tiancode" / "config.json"


def load_user_config(config_path: Optional[Path] = None) -> Dict[str, Any]:
    """Read configuration from persistent file."""
    path = config_path or get_default_config_path()
    if path.exists():
        try:
            with open(path, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            return {}
    return {}


def save_user_config(data: Dict[str, Any], config_path: Optional[Path] = None) -> None:
    """Save configuration dictionary into persistent file."""
    path = config_path or get_default_config_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)


@dataclass
class Settings:
    """Application settings with 4-level fallback: CLI > Env > Config File > Default."""

    api_key: str
    base_url: str = "https://api.deepseek.com"
    model: str = "deepseek-chat"
    temperature: float = 0.0
    timeout_seconds: int = 15
    max_history_messages: int = 12
    max_context_chars: int = 40000
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
        max_history = raw_max_hist if type(raw_max_hist) is int and raw_max_hist > 1 else 12

        raw_max_chars = user_cfg.get("max_context_chars")
        max_chars = raw_max_chars if type(raw_max_chars) is int and raw_max_chars >= 500 else 40000

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
