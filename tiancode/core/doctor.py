"""Environment self-diagnostics (/doctor) and context window analyzer (/context)."""

from dataclasses import dataclass
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from typing import Any, Dict, List, Optional
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage, ToolMessage


@dataclass(frozen=True)
class DiagnosticCheck:
    """Result of a single environment health check."""

    name: str
    status: str  # "OK" | "WARN" | "ERROR"
    detail: str


def _check_python() -> DiagnosticCheck:
    ver = sys.version.split()[0]
    ok = sys.version_info >= (3, 10)
    return DiagnosticCheck(
        name="Python 运行环境",
        status="OK" if ok else "WARN",
        detail=f"Python {ver} ({sys.executable})" + ("" if ok else " [建议升级至 3.10+]"),
    )


def _check_llm_config(settings: Any) -> DiagnosticCheck:
    key = getattr(settings, "api_key", "") or ""
    url = getattr(settings, "base_url", "") or ""
    model = getattr(settings, "model", "") or ""
    if not isinstance(key, str) or not key.strip():
        return DiagnosticCheck("大模型 API Key", "WARN", "未检测到 API Key，请运行 /config set key <密钥>")
    if not isinstance(url, str) or not url.startswith(("http://", "https://")):
        return DiagnosticCheck("大模型 API Key", "WARN", f"Base URL 格式异常: {url}")
    masked = f"****{key[-4:]}" if len(key) >= 4 else "****"
    return DiagnosticCheck("大模型 API Key", "OK", f"已配置 ({masked}) | 模型: {model} | {url}")


def _check_workdir_writable(work_dir: Path) -> DiagnosticCheck:
    resolved = Path(work_dir).resolve()
    if not resolved.exists() or not resolved.is_dir():
        return DiagnosticCheck("工作区读写权限", "ERROR", f"工作目录不存在: {resolved}")
    try:
        with tempfile.NamedTemporaryFile(dir=resolved, prefix=".tiancode_probe_", delete=True):
            pass
        return DiagnosticCheck("工作区读写权限", "OK", f"可读写: {resolved}")
    except OSError as exc:
        return DiagnosticCheck("工作区读写权限", "ERROR", f"目录不可写 ({resolved}): {exc}")


def _check_git_repo(work_dir: Path) -> DiagnosticCheck:
    if not shutil.which("git"):
        return DiagnosticCheck("Git 工具链", "WARN", "系统未安装 Git，将退回纯内存快照模式")
    try:
        res = subprocess.run(
            ["git", "rev-parse", "--is-inside-work-tree"],
            cwd=str(work_dir),
            capture_output=True,
            text=True,
            timeout=3,
        )
        if res.returncode == 0 and res.stdout.strip() == "true":
            return DiagnosticCheck("Git 工具链", "OK", "Git 可用，当前目录处于受管仓库中")
        return DiagnosticCheck("Git 工具链", "WARN", "Git 可用，当前目录非 Git 仓库（启用内存快照）")
    except Exception as exc:
        return DiagnosticCheck("Git 工具链", "WARN", f"Git 状态检测异常: {exc}")


def _check_pytest_and_sessions(sessions_dir: Optional[Path]) -> DiagnosticCheck:
    has_pytest = bool(shutil.which("pytest"))
    if not has_pytest:
        try:
            res = subprocess.run([sys.executable, "-m", "pytest", "--version"], capture_output=True, timeout=3)
            has_pytest = res.returncode == 0
        except Exception:
            has_pytest = False

    sess_ok = True
    session_problem = ""
    if sessions_dir is not None:
        if sessions_dir.is_symlink():
            sess_ok = False
            session_problem = f"会话目录是符号链接，未使用其目标: {sessions_dir}"
        else:
            try:
                sessions_dir.mkdir(parents=True, exist_ok=True)
            except OSError:
                sess_ok = False
                session_problem = f"会话目录不可写: {sessions_dir}"

    if has_pytest and sess_ok:
        return DiagnosticCheck("测试与持久化组件", "OK", "pytest 就绪且会话目录可写")
    missing = []
    if not has_pytest:
        missing.append("未检测到 pytest（自愈测试不可用）")
    if not sess_ok:
        missing.append(session_problem or f"会话目录不可写: {sessions_dir}")
    return DiagnosticCheck("测试与持久化组件", "WARN", "；".join(missing))


def run_environment_diagnostics(
    work_dir: Path,
    settings: Any,
    *,
    sessions_dir: Optional[Path] = None,
) -> List[DiagnosticCheck]:
    """Run non-destructive local diagnostics covering Python, LLM config, FS, Git, and pytest."""
    return [
        _check_python(),
        _check_llm_config(settings),
        _check_workdir_writable(work_dir),
        _check_git_repo(work_dir),
        _check_pytest_and_sessions(sessions_dir),
    ]


def format_diagnostics_report(checks: List[DiagnosticCheck]) -> str:
    """Format diagnostic checks into a clear ANSI-friendly terminal report."""
    icons = {"OK": "✓", "WARN": "!", "ERROR": "✗"}
    lines = ["\n🩺 ====== [TianCode 环境健康自检 (/doctor)] ======"]
    for chk in checks:
        icon = icons.get(chk.status, "?")
        lines.append(f"  [{icon}] {chk.name:<16} : {chk.detail}")
    lines.append("=================================================\n")
    return "\n".join(lines)


def analyze_context_usage(
    messages: List[BaseMessage],
    *,
    max_retained_messages: int,
    max_context_chars: int,
) -> Dict[str, Any]:
    """Compute role-level character distribution and threshold ratios for active conversation messages."""
    by_role = {"human": 0, "ai": 0, "tool": 0, "system": 0}
    total_chars = 0
    for msg in messages:
        size = len(str(getattr(msg, "content", "")))
        total_chars += size
        if isinstance(msg, HumanMessage):
            by_role["human"] += size
        elif isinstance(msg, AIMessage):
            by_role["ai"] += size
        elif isinstance(msg, ToolMessage):
            by_role["tool"] += size
        elif isinstance(msg, SystemMessage):
            by_role["system"] += size

    char_cap = max(1, max_context_chars)
    msg_cap = max(1, max_retained_messages)
    return {
        "message_count": len(messages),
        "max_messages": msg_cap,
        "msg_percent": min(100.0, len(messages) / msg_cap * 100.0),
        "total_chars": total_chars,
        "max_chars": char_cap,
        "char_percent": min(999.9, total_chars / char_cap * 100.0),
        "by_role": by_role,
    }


def format_context_report(stats: Dict[str, Any]) -> str:
    """Render context window meter bar and role character distribution."""
    pct = stats["char_percent"]
    filled = max(0, min(10, int(round(min(pct, 100.0) / 10.0))))
    bar = "█" * filled + "░" * (10 - filled)
    roles = stats["by_role"]

    lines = [
        "\n🧠 ====== [TianCode 上下文窗口体检 (/context)] ======",
        f"  • 字符预算占用 : [{bar}] {pct:.1f}% ({stats['total_chars']:,} / {stats['max_chars']:,} 字符)",
        f"  • 消息条数占比 : {stats['message_count']} / {stats['max_messages']} 条 ({stats['msg_percent']:.1f}%)",
        f"  • 字符分布明细 : User {roles['human']:,} | AI {roles['ai']:,} | Tool {roles['tool']:,} | System {roles['system']:,}",
    ]
    if pct >= 80.0 or stats["msg_percent"] >= 80.0:
        lines.append("  • ⚠️ 建议提示    : 上下文接近预算上限，推荐执行 /compact 进行智能折叠压缩。")
    lines.append("===================================================\n")
    return "\n".join(lines)
