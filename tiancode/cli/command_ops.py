"""Rich UI panel helpers and config, cost, memory, workspace, and rules slash command handlers."""

from typing import TYPE_CHECKING, Any, Dict, Sequence, Tuple

from rich import box
from rich.panel import Panel
from rich.table import Table
from rich.text import Text

from tiancode.cli.formatter import TerminalFormatter
from tiancode.cli.theme import (
    ACCENT_COLOR,
    BORDER_MUTED,
    BRAND_COLOR,
    SUCCESS_COLOR,
    TEXT_MUTED,
    TEXT_PRIMARY,
    TEXT_SUBTLE,
    get_console,
)
from tiancode.config.settings import get_default_config_path

if TYPE_CHECKING:
    from tiancode.core.engine import TianCodeEngine


def print_kv_panel(title: str, rows: Sequence[Tuple[str, str]], *, border_style: str = BORDER_MUTED) -> None:
    """Render aligned two-column key/value metadata inside a rounded card."""
    grid = Table.grid(padding=(0, 2))
    grid.add_column(style=TEXT_MUTED, no_wrap=True)
    grid.add_column(style=TEXT_PRIMARY)
    for key, value in rows:
        grid.add_row(key, Text(str(value), style=TEXT_PRIMARY))
    console = get_console()
    console.print()
    console.print(Panel(grid, title=Text(title, style=f"bold {BRAND_COLOR}"), title_align="left", box=box.ROUNDED, border_style=border_style, padding=(0, 2)))
    console.print()


def print_table(title: str, headers: Sequence[Tuple[str, Dict[str, Any]]], rows: Sequence[Sequence[Any]]) -> None:
    """Render a rounded Rich table uniformly across list commands."""
    table = Table(title=title, box=box.ROUNDED, border_style=BORDER_MUTED, title_style=f"bold {BRAND_COLOR}")
    for col_name, col_opts in headers:
        table.add_column(col_name, **col_opts)
    for r in rows:
        table.add_row(*r)
    console = get_console()
    console.print()
    console.print(table)


def apply_config_set(engine: "TianCodeEngine", prop: str, val: str) -> None:
    """Validate, persist, and hot-reload a /config set property update."""
    s = engine.settings
    str_map = {"key": "api_key", "api_key": "api_key", "url": "base_url", "base_url": "base_url", "model": "model", "model_name": "model"}
    if prop in str_map:
        setattr(s, str_map[prop], val)
        s.persist()
        engine.reload_llm()
        TerminalFormatter.print_success(f"已更新 {str_map[prop]} 并持久化保存。")
        return

    int_limits = {
        "max_chars": ("max_context_chars", 500), "max_context_chars": ("max_context_chars", 500),
        "max_msgs": ("max_history_messages", 2), "max_history_messages": ("max_history_messages", 2),
        "timeout": ("timeout_seconds", 1), "timeout_seconds": ("timeout_seconds", 1),
    }
    if prop in int_limits:
        attr, min_val = int_limits[prop]
        try:
            num = int(val)
        except ValueError:
            TerminalFormatter.print_warning(f"参数值 '{val}' 必须为正整数。")
            return
        if num < min_val:
            TerminalFormatter.print_warning(f"参数 {prop} 不能小于 {min_val}: {num}")
            return
        setattr(s, attr, num)
        s.persist()
        if attr == "timeout_seconds":
            engine.reload_llm()
        TerminalFormatter.print_success(f"已更新 {attr} = {num} 并持久化保存。")
        return

    if prop in ("auto_heal", "heal"):
        low = val.lower()
        if low not in ("true", "false", "1", "0", "on", "off", "yes", "no"):
            TerminalFormatter.print_warning("布尔值仅支持 true/false/on/off。")
            return
        s.auto_heal = low in ("true", "1", "on", "yes")
        s.persist()
        engine.reload_llm()
        TerminalFormatter.print_success(f"已更新 auto_heal = {s.auto_heal} 并持久化保存。")
        return

    TerminalFormatter.print_warning(f"未知属性 '{prop}'。支持: key, url, model, max_chars, max_msgs, timeout, auto_heal")


def show_config_panel(engine: "TianCodeEngine") -> None:
    """Display active engine settings inside a rounded card."""
    s = engine.settings
    masked_key = f"****{s.api_key[-4:]}" if len(s.api_key) >= 4 else "(未配置)"
    rows = [
        ("API Key", masked_key),
        ("Base URL", s.base_url),
        ("Model", s.model),
        ("预算阈值", f"{getattr(s, 'max_history_messages', 12)} 条 / {getattr(s, 'max_context_chars', 40000):,} 字符"),
        ("自愈与超时", f"{'开启' if getattr(s, 'auto_heal', True) else '关闭'} | {getattr(s, 'timeout_seconds', 15)}s"),
    ]
    guard = getattr(engine, "permission_guard", None)
    if guard is not None and isinstance(getattr(getattr(guard, "mode", None), "value", None), str):
        rows.append(("权限模式", guard.mode.value))
    rows.append(("配置文件", str(get_default_config_path())))
    print_kv_panel("⚙️ TianCode 配置信息", rows)


def show_cost_panel(engine: "TianCodeEngine") -> None:
    """Display session token usage and estimated cost card."""
    tracker = getattr(engine, "usage_tracker", None)
    if not tracker:
        TerminalFormatter.print_info("当前引擎未启用 Token 计量器。")
        return
    tokens, cost = tracker.get_session_stats()
    model = getattr(getattr(engine, "settings", None), "model", "unknown")
    model = model if isinstance(model, str) else "unknown"
    from tiancode.core.usage_meter import get_model_pricing
    pricing = get_model_pricing(model)
    cost_str = (
        f"¥{cost.total_cost:.4f}"
        + (f" (缓存节省 ¥{cost.saved_by_cache:.4f})" if cost.is_known_pricing and cost.saved_by_cache > 0 else ("" if cost.is_known_pricing else " (无预设费率)"))
    )
    rows = [
        ("当前模型", f"{model} ({pricing.display_name if pricing else '本地/未配置费率'})"),
        ("推理轮次", f"{tokens.turn_count} 轮"),
        ("输入 Tokens", f"{tokens.total_input_tokens:,} (缓存命中: {tokens.cache_read_tokens:,}, 命中率: {tokens.cache_hit_rate:.1f}%)"),
        ("输出 Tokens", f"{tokens.output_tokens:,}"),
        ("累计消耗", f"{tokens.total_tokens:,} Tokens"),
        ("预估总费用", cost_str),
    ]
    print_kv_panel("📊 TianCode 会话 Token 与成本账单", rows, border_style=ACCENT_COLOR)


def handle_cd(engine: "TianCodeEngine", arg: str = "") -> None:
    target = arg.strip().strip("\"'")
    if not target:
        TerminalFormatter.print_info(f"当前工作区: {engine.settings.work_dir} (用法: /cd <目标目录>)")
        return
    if not hasattr(engine, "switch_workspace"):
        TerminalFormatter.print_warning("当前引擎版本不支持动态切换工作区。")
        return
    try:
        new_path = engine.switch_workspace(target)
        TerminalFormatter.print_success(f"工作区已安全切换至: {new_path}")
    except Exception as exc:
        TerminalFormatter.print_warning(f"切换工作区失败: {exc}")


def handle_memory(engine: "TianCodeEngine", arg: str = "") -> None:
    store = getattr(engine, "memory_store", None)
    if store is None:
        TerminalFormatter.print_info("当前未启用跨会话认知记忆存储。")
        return
    if arg.strip().lower() == "clear":
        store.clear()
        TerminalFormatter.print_success("已清空全部跨会话认知记忆档案。")
        return
    rows = [(f"工程:{w.name}", f"{w.path} [{'/'.join(w.tags)}]") for w in list(store.workspaces.values())[:5]]
    rows.extend((f"情景:{ep.project_name}", f"{ep.intent} -> {ep.summary[:80]}") for ep in store.episodes[:5])
    rows.extend((f"认知:{f.scope.value}", f"[{f.key}] {f.content}") for f in store.facts[:8])
    if not rows:
        TerminalFormatter.print_info("当前认知记忆为空，对话后将自动沉淀或用 /remember 添加。")
        return
    print_kv_panel("🧠 TianCode 跨会话认知记忆档案", rows, border_style=ACCENT_COLOR)


def handle_remember(engine: "TianCodeEngine", arg: str = "") -> None:
    store = getattr(engine, "memory_store", None)
    if store is None:
        TerminalFormatter.print_info("当前未启用跨会话认知记忆存储。")
        return
    raw = arg.strip()
    if not raw:
        TerminalFormatter.print_info("用法: /remember [project:]<需要长期记住的规范、别名或事实>")
        return
    scope = "global"
    if raw.lower().startswith("project:"):
        scope = "project"
        raw = raw[8:].strip()
    try:
        saved = store.upsert_fact(
            raw, scope=scope,
            workspace_path=str(engine.settings.work_dir),
            source_session_id=getattr(engine, "active_session_id", ""),
        )
        TerminalFormatter.print_success(f"已写入长期记忆 [{saved.scope.value}/{saved.key}]: {saved.content}")
    except Exception as exc:
        TerminalFormatter.print_warning(f"记忆写入失败: {exc}")


def handle_init(engine: "TianCodeEngine", arg: str = "") -> None:
    from tiancode.core.project_rules import write_project_guide

    force = any(tok in ("--force", "-f") for tok in arg.strip().split())
    try:
        target, written = write_project_guide(engine.settings.work_dir, force=force)
    except Exception as exc:
        TerminalFormatter.print_warning(f"生成工程规约失败: {exc}")
        return
    if written:
        TerminalFormatter.print_success(f"已扫描工程结构并生成规范文件: {target} (可运行 /rules 预览)")
    else:
        TerminalFormatter.print_warning(f"规范文件已存在: {target}，跳过覆写。若需重新生成请执行: /init --force")


def handle_rules(engine: "TianCodeEngine") -> None:
    from tiancode.core.project_rules import ProjectRulesLoader

    loader = getattr(engine, "rules_loader", None) or ProjectRulesLoader()
    docs = loader.load_rules(engine.settings.work_dir)
    if not docs:
        TerminalFormatter.print_info("当前未检测到全局或工程规范文件 (AGENTS.md / TIANCODE.md)，可输入 /init 自动生成。")
        return
    headers = [
        ("层级", {"style": SUCCESS_COLOR, "no_wrap": True}),
        ("文件名", {"style": f"bold {BRAND_COLOR}", "no_wrap": True}),
        ("字符数", {"style": TEXT_MUTED, "justify": "right"}),
        ("物理路径", {"style": TEXT_SUBTLE}),
    ]
    rows = [(d.scope, d.filename, f"{len(d.content):,}", Text(str(d.path))) for d in docs]
    print_table("📜 已自动加载工程规范清单", headers, rows)
