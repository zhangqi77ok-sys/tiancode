"""Subprocess execution tools with timeout, safety guardrails, env scrubbing, and output capture."""

import os
import re
import subprocess
import sys
import time
from pathlib import Path
from typing import Any, Dict, List
from langchain_core.tools import BaseTool, tool
from tiancode.domain.models import ExecutionResult

DANGEROUS_PATTERNS = [
    # rm with recursive/force flags targeting whole-repo, root, home, or current directory
    r"\brm\s+-[a-zA-Z]*[rRfF][a-zA-Z]*\s+(--\s+)?(\.|\.\/|\*|\/\*|\/|~|~\/)(\s|$)",
    # rm with recursive/force flags targeting traversal
    r"\brm\s+-[a-zA-Z]*[rRfF][a-zA-Z]*\s+(--\s+)?\.\.(\/.*|\s|$)",
    # PowerShell recursive/force Remove-Item targeting root, workspace dot, wildcard, or parent traversal
    r"\b(remove-item|ri)\b(?=.*-(r|recurse|f|force|fo)\b).*(\s|['\"])(\.|\.[\\/]\*?|\*|[/\\]|~|\.\.([\\/].*)?)['\"]?(\s|$)",
    r"\bdel\s+/[sSqQ]*\s+([cC]:\\|\\|\.|\*)(\s|$)",
    r"\bformat\s+[a-zA-Z]:",                                 # format C:
    r"\bmkfs(\.[a-z0-9]+)?\b",                               # mkfs / mkfs.ext4
    r"\bdd\b.*\bof=/dev/(sd[a-z]|nvme\d|hd[a-z]|vd[a-z]|mmcblk\d|disk)",  # raw disk overwrite
    r"\bdiskpart\b",                                         # Windows diskpart
    r"\bgit\s+reset\s+--hard\b",                             # git reset --hard
    r"\bgit\s+clean\s+-[a-zA-Z]*[fF]",                       # git clean -xdf, -fd, -f
    r"\bgit\s+(checkout|restore)\s+(\.|--\s*\.|\*)(\s|$)",   # git restore ., git checkout -- ., git restore *
    r"\b(curl|wget)\b.*\|\s*(/usr/bin/|/bin/)?(ba)?sh\b",    # curl | /bin/sh or bash
    r"\bchmod\s+-R\s+777\b",                                 # chmod -R 777
    r"\bfind\b.*\s+-delete\b",                               # find . -delete
    r":\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:",            # fork bomb
]

# Applied after quotes and newlines are stripped. Each pattern must be a command,
# not a mention of the word inside echo or a commit message.
_CMD = r"(?:^|&&|\|\||;|\|)\s*"
_EXTRA_DANGEROUS_PATTERNS = [
    _CMD + r"sudo\b",
    _CMD + r"(?:shutdown|reboot|poweroff|halt)\b",
    r"\bsystemctl\s+(?:poweroff|reboot|halt|suspend|hibernate|kexec)\b",
    r"\binit\s+[06]\b",
    _CMD + r"git\s+push\b.*(?:\s--force(?:-with-lease)?\b|\s-f\b)",
    _CMD + r"rm\b[^|;]*\s/(?:etc|usr|bin|boot|home|root|sys|proc|dev|sbin|lib|lib64|opt|var|run)(?:/|\s|$)",
    r"\b(?:curl|wget)\b.*\|\s*(?:/usr/bin/|/bin/)?(?:python3?|perl|ruby|node|php|zsh|ksh)\b",
    r"\bbase64\s+(?:-d|--decode)\b.*\|\s*(?:/usr/bin/|/bin/)?(?:python3?|perl|ruby|node|bash|sh|zsh|ksh)\b",
    _CMD + r"shred\b",
    _CMD + r"chmod\b.*\b777\b",
    _CMD + r"find\b.*\s-exec\s+rm\b",
    r">\s*/dev/(?:sd[a-z]|nvme|hd[a-z]|vd[a-z]|mmcblk|disk)\b",
    r"shutil\.rmtree\(\s*/",
    _CMD + r"(?:nc|ncat|netcat)\b.*\s-e\s",
]


def _normalize_command(command: str) -> str:
    """Collapse whitespace and quotes so `rm -rf "/"` matches the same rule as `rm -rf /`."""
    text = str(command).replace("\x00", " ")
    text = re.sub(r"[\r\n\t]+", " ", text)
    text = text.replace("$(", " ").replace("`", " ")
    text = text.replace("'", "").replace('"', "")
    return re.sub(r"\s+", " ", text).strip()

SENSITIVE_ENV_KEYS = {
    "TIANCODE_API_KEY",
    "OPENAI_API_KEY",
    "ANTHROPIC_API_KEY",
    "GEMINI_API_KEY",
    "DEEPSEEK_API_KEY",
    "AWS_SECRET_ACCESS_KEY",
    "GITHUB_TOKEN",
    "GIT_TOKEN",
}

# Names the fixed list misses, such as AWS_ACCESS_KEY_ID and GH_TOKEN.
# Match a secret word as its own env-name token so PATH and LESSKEY stay.
_SENSITIVE_ENV_NAME = re.compile(
    r"(^|_)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PWD)($|_)",
    re.IGNORECASE,
)
# Git injects config as GIT_CONFIG_KEY_<n>=core.quotePath. The slot name is not a secret.
_GIT_CONFIG_KEY_SLOT = re.compile(r"^GIT_CONFIG_KEY_\d+$")


def is_dangerous_command(command: str | List[str]) -> bool:
    """Check if command matches high-risk destructive signatures.

    The raw text is checked first so patterns that look for quotes still match.
    A second pass strips quotes and newlines, which is how `rm -rf "/"` and a
    command split across lines reach the same rules.
    """
    raw = " ".join(command) if isinstance(command, (list, tuple)) else str(command)
    patterns = (*DANGEROUS_PATTERNS, *_EXTRA_DANGEROUS_PATTERNS)
    for text in (raw, _normalize_command(raw)):
        for pattern in patterns:
            if re.search(pattern, text, re.IGNORECASE):
                return True
    return False


def _is_sensitive_env_name(name: str) -> bool:
    """Return whether a subprocess should not inherit this environment variable."""
    if _GIT_CONFIG_KEY_SLOT.fullmatch(name):
        return False
    return name in SENSITIVE_ENV_KEYS or _SENSITIVE_ENV_NAME.search(name) is not None


def get_sanitized_env() -> Dict[str, str]:
    """Scrub credentials and token-like variables from the subprocess environment."""
    return {key: value for key, value in os.environ.items() if not _is_sensitive_env_name(key)}


_ERROR_SIGNAL_MARKERS = (
    "ERROR", "FAILED", "Traceback", "Exception", "AssertionError",
    "SyntaxError", "error TS", "FATAL", "Caused by:",
)


def _decode_process_bytes(raw: bytes | str | None) -> str:
    """Decode subprocess bytes cleanly, falling back from UTF-8 to Windows GBK/CP936."""
    if raw is None:
        return ""
    if isinstance(raw, str):
        return raw
    for enc in ("utf-8", "gb18030", "cp936") if sys.platform == "win32" else ("utf-8",):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            continue
    return raw.decode("utf-8", errors="replace")


def truncate_output(output: str, max_length: int = 8000) -> str:
    """Keep head, tail, and high-signal middle error lines when exceeding length."""
    if len(output) <= max_length:
        return output
    head_len = max_length // 4
    tail_len = max_length // 2
    middle = output[head_len : len(output) - tail_len]
    signals: List[str] = []
    for ln in middle.splitlines():
        clean = ln.strip()
        if clean and any(tok in clean for tok in _ERROR_SIGNAL_MARKERS):
            signals.append(clean[:180])
            if len(signals) >= 12:
                break
    mid_block = (
        "\n\n... [输出过长已截断，抽取中段关键报错行] ...\n" + "\n".join(signals) + "\n...\n\n"
        if signals
        else "\n\n... [输出过长已截断，保留头部与尾部关键信息] ...\n\n"
    )
    return f"{output[:head_len]}{mid_block}{output[-tail_len:]}"


def kill_process_tree(proc: subprocess.Popen) -> None:
    """Recursively kill the entire process tree to eliminate orphan background processes."""
    if proc.poll() is not None:
        return

    if sys.platform == "win32":
        try:
            subprocess.run(
                ["taskkill", "/F", "/T", "/PID", str(proc.pid)],
                capture_output=True,
                check=False,
                creationflags=0x08000000,
            )
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass
    else:
        try:
            import signal
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass


def _run_managed_process(cmd: Any, work_dir: Path, *, use_shell: bool, timeout: int) -> ExecutionResult:
    """Spawn subprocess in work_dir with sanitized env, process-group timeout kill, and error capture."""
    try:
        creationflags = 0x08000000 if sys.platform == "win32" else 0
        proc = subprocess.Popen(
            cmd,
            cwd=str(work_dir),
            shell=use_shell,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=get_sanitized_env(),
            creationflags=creationflags,
            start_new_session=(sys.platform != "win32"),
        )
        try:
            raw_out, raw_err = proc.communicate(timeout=timeout)
            return ExecutionResult(
                exit_code=proc.returncode if proc.returncode is not None else 0,
                stdout=_decode_process_bytes(raw_out),
                stderr=_decode_process_bytes(raw_err),
                timed_out=False,
            )
        except subprocess.TimeoutExpired:
            kill_process_tree(proc)
            try:
                raw_out, raw_err = proc.communicate(timeout=2)
            except Exception:
                raw_out, raw_err = b"", b""
            note = f"执行超时（超过 {timeout} 秒被强制终止）"
            stderr = _decode_process_bytes(raw_err).strip()
            return ExecutionResult(
                exit_code=-1,
                stdout=_decode_process_bytes(raw_out),
                stderr=f"{stderr}\n{note}" if stderr else note,
                timed_out=True,
            )
    except Exception as exc:
        return ExecutionResult(
            exit_code=-1,
            stdout="",
            stderr=f"进程启动失败: {str(exc)}",
            timed_out=False,
        )


def execute_shell_command(command: str, work_dir: Path, timeout: int = 30) -> ExecutionResult:
    """Execute arbitrary shell command in work_dir with timeout, safety checks, and env scrubbing."""
    if is_dangerous_command(command):
        return ExecutionResult(
            exit_code=126,
            stdout="",
            stderr="⚠️ 安全拦截 (Security guardrail)：检测到高危系统破坏性命令，执行已被阻止！",
            timed_out=False,
        )
    return _run_managed_process(command, work_dir, use_shell=True, timeout=timeout)


def execute_python_script(script_path: Path, work_dir: Path, timeout: int = 15) -> ExecutionResult:
    """Execute python script using current interpreter with env scrubbing."""
    return _run_managed_process([sys.executable, str(script_path)], work_dir, use_shell=False, timeout=timeout)


def create_execution_tools(work_dir: Path, timeout: int = 30) -> List[BaseTool]:
    """Factory to create execution tools bound to work directory and timeout."""

    @tool
    def shell(command: str) -> str:
        """在当前项目工作区执行系统终端命令（如 pytest、python、git、pip、npm 等），返回 Exit Code、耗时和标准输出/错误。"""
        t0 = time.perf_counter()
        res = execute_shell_command(command, work_dir=work_dir, timeout=timeout)
        elapsed = time.perf_counter() - t0
        combined_output = ""
        if res.stdout.strip():
            combined_output += f"[STDOUT]\n{res.stdout.strip()}\n"
        if res.stderr.strip():
            combined_output += f"[STDERR]\n{res.stderr.strip()}\n"
        if not combined_output.strip():
            combined_output = "(无终端输出)"

        truncated = truncate_output(combined_output)
        status_label = "成功" if res.is_success else "失败/异常"
        return f"终端执行{status_label} (Exit Code: {res.exit_code}, 耗时: {elapsed:.2f}s):\n{truncated}"

    @tool
    def run_python(filename: str) -> str:
        """在当前工程目录运行 Python 脚本，并捕获运行结果、print 打印或报错 Traceback。"""
        from tiancode.tools.file_ops import resolve_safe_path
        try:
            resolved = resolve_safe_path(filename, work_dir)
        except PermissionError as err:
            return f"❌ {str(err)}"

        from tiancode.core.path_safety import is_shared_hardlink, names_credential_file

        if is_shared_hardlink(resolved):
            return f"❌ 文件 '{filename}' 是硬链接，未读取或运行其内容。"
        if names_credential_file(filename, resolved, work_dir):
            return f"❌ 文件 '{filename}' 已跳过：疑似密钥或 Git 内部文件，未读取或运行其内容。"
        if not resolved.exists():
            return f"❌ 运行失败：文件 '{filename}' 不存在！"

        res = execute_python_script(resolved, work_dir=work_dir, timeout=timeout)
        if res.is_success:
            return f"【运行成功】输出内容：\n{res.stdout.strip() if res.stdout.strip() else '(无任何控制台输出)'}"
        else:
            err_line = res.stderr.strip().splitlines()[-1] if res.stderr.strip() else "未知异常"
            return f"【运行报错！请分析下方 Traceback 并修复代码】：\n{res.stderr}\n核心错误: {err_line}"

    return [shell, run_python]
