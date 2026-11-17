"""Subprocess execution tools with timeout, safety guardrails, and output capture."""

import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Dict, List
from langchain_core.tools import BaseTool, tool
from tiancode.domain.models import ExecutionResult

DANGEROUS_PATTERNS = [
    r"\brm\s+-[rRfF]*\s+[/~]",           # rm -rf / or ~
    r"\brm\s+-[rRfF]*\s+[\.\*]",         # rm -rf . or *
    r"\brm\s+-[rRfF]*\s+\.\.",           # rm -rf ..
    r"\bdel\s+/[sSqQ]*\s+[cC]:\\",       # del /s c:\
    r"\bdel\s+/[sSqQ]*\s+[\.\*]",        # del /s . or *
    r"\bformat\s+[a-zA-Z]:",             # format C:
    r"\bmkfs\b",                         # mkfs
    r"\bgit\s+reset\s+--hard\b",         # git reset --hard
    r"\bgit\s+clean\s+-[fF]",            # git clean -f
    r"\bcurl\b.*\|\s*(ba)?sh\b",         # curl | sh
    r"\bwget\b.*\|\s*(ba)?sh\b",         # wget | sh
    r"\bchmod\s+-R\s+777\b",             # chmod -R 777
    r":\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:", # fork bomb
]

SENSITIVE_ENV_KEYS = {
    "TIANCODE_API_KEY",
    "OPENAI_API_KEY",
    "ANTHROPIC_API_KEY",
    "GEMINI_API_KEY",
    "DEEPSEEK_API_KEY",
    "AWS_SECRET_ACCESS_KEY",
}


def is_dangerous_command(command: str) -> bool:
    """Check if command matches high-risk destructive signatures."""
    for pattern in DANGEROUS_PATTERNS:
        if re.search(pattern, command, re.IGNORECASE):
            return True
    return False


def get_sanitized_env() -> Dict[str, str]:
    """Scrub sensitive credentials and API tokens from subprocess environment."""
    env = os.environ.copy()
    for key in SENSITIVE_ENV_KEYS:
        env.pop(key, None)
    return env


def truncate_output(output: str, max_length: int = 8000) -> str:
    """Keep head and tail of output when exceeding length."""
    if len(output) <= max_length:
        return output
    head_len = max_length // 3
    tail_len = max_length - head_len - 100
    return f"{output[:head_len]}\n\n... [输出过长已截断，保留头部与尾部关键信息] ...\n\n{output[-tail_len:]}"


def execute_shell_command(command: str, work_dir: Path, timeout: int = 30) -> ExecutionResult:
    """Execute arbitrary shell command in work_dir with timeout, safety checks, and env scrubbing."""
    if is_dangerous_command(command):
        return ExecutionResult(
            exit_code=126,
            stdout="",
            stderr="⚠️ 安全拦截 (Security guardrail)：检测到高危系统破坏性命令，执行已被阻止！",
            timed_out=False,
        )

    try:
        creationflags = 0x08000000 if sys.platform == "win32" else 0
        proc = subprocess.run(
            command,
            cwd=str(work_dir),
            shell=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=timeout,
            env=get_sanitized_env(),
            creationflags=creationflags,
        )
        return ExecutionResult(
            exit_code=proc.returncode,
            stdout=proc.stdout or "",
            stderr=proc.stderr or "",
            timed_out=False,
        )
    except subprocess.TimeoutExpired as exc:
        stdout_txt = exc.stdout if isinstance(exc.stdout, str) else ""
        return ExecutionResult(
            exit_code=-1,
            stdout=stdout_txt,
            stderr=f"命令执行超时（超过 {timeout} 秒被强制终止）",
            timed_out=True,
        )
    except Exception as exc:
        return ExecutionResult(
            exit_code=-1,
            stdout="",
            stderr=f"终端命令启动失败: {str(exc)}",
            timed_out=False,
        )


def execute_python_script(script_path: Path, work_dir: Path, timeout: int = 15) -> ExecutionResult:
    """Execute python script using current interpreter with env scrubbing."""
    try:
        creationflags = 0x08000000 if sys.platform == "win32" else 0
        proc = subprocess.run(
            [sys.executable, str(script_path)],
            cwd=str(work_dir),
            capture_output=True,
            text=True,
            timeout=timeout,
            env=get_sanitized_env(),
            creationflags=creationflags,
        )
        return ExecutionResult(
            exit_code=proc.returncode,
            stdout=proc.stdout,
            stderr=proc.stderr,
            timed_out=False,
        )
    except subprocess.TimeoutExpired as exc:
        return ExecutionResult(
            exit_code=-1,
            stdout=exc.stdout or "",
            stderr=f"执行超时（超过 {timeout} 秒被强制终止）",
            timed_out=True,
        )
    except Exception as exc:
        return ExecutionResult(
            exit_code=-1,
            stdout="",
            stderr=f"进程启动失败: {str(exc)}",
            timed_out=False,
        )


def create_execution_tools(work_dir: Path, timeout: int = 30) -> List[BaseTool]:
    """Factory to create execution tools bound to work directory and timeout."""

    @tool
    def shell(command: str) -> str:
        """在当前项目工作区执行系统终端命令（如 pytest、python、git、pip、npm 等），返回 Exit Code 和标准输出/错误。
        
        适用场景：
        - 运行测试用例（例如：pytest、python -m unittest）
        - 检查环境与已安装包（例如：pip list、python --version）
        - 编译、构建与运行任意脚本
        """
        res = execute_shell_command(command, work_dir=work_dir, timeout=timeout)
        combined_output = ""
        if res.stdout.strip():
            combined_output += f"[STDOUT]\n{res.stdout.strip()}\n"
        if res.stderr.strip():
            combined_output += f"[STDERR]\n{res.stderr.strip()}\n"
        if not combined_output.strip():
            combined_output = "(无终端输出)"

        truncated = truncate_output(combined_output)
        status_label = "成功" if res.is_success else "失败/异常"
        return f"终端执行{status_label} (Exit Code: {res.exit_code}):\n{truncated}"

    @tool
    def run_python(filename: str) -> str:
        """在当前工程目录运行 Python 脚本，并捕获运行结果、print 打印或报错 Traceback。"""
        target = Path(filename)
        resolved = target if target.is_absolute() else (work_dir / target)
        if not resolved.exists():
            return f"❌ 运行失败：文件 '{filename}' 不存在！"

        res = execute_python_script(resolved, work_dir=work_dir, timeout=timeout)
        if res.is_success:
            return f"【运行成功】输出内容：\n{res.stdout.strip() if res.stdout.strip() else '(无任何控制台输出)'}"
        else:
            err_line = res.stderr.strip().splitlines()[-1] if res.stderr.strip() else "未知异常"
            return f"【运行报错！请分析下方 Traceback 并修复代码】：\n{res.stderr}\n核心错误: {err_line}"

    return [shell, run_python]
