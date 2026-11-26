"""Read-only Git status and diff inspection tools with credential and path-safety guards."""

import shutil
import subprocess
import sys
from pathlib import Path
from typing import List

from langchain_core.tools import BaseTool, tool

from tiancode.core.git_store import git_workspace_env
from tiancode.core.path_safety import is_credential_path, is_shared_hardlink, names_credential_file
from tiancode.tools.execution import truncate_output
from tiancode.tools.file_ops import resolve_safe_path


def _run_readonly_git(work_dir: Path, args: List[str], timeout: int = 8) -> tuple[int, str, str]:
    """Execute read-only git command safely inside work_dir."""
    if not shutil.which("git"):
        return -1, "", "系统未安装 git 命令。"
    try:
        creationflags = 0x08000000 if sys.platform == "win32" else 0
        res = subprocess.run(
            ["git", *args],
            cwd=str(work_dir),
            env=git_workspace_env(),
            capture_output=True,
            timeout=timeout,
            creationflags=creationflags,
        )
        out = res.stdout.decode("utf-8", errors="replace") if res.stdout else ""
        err = res.stderr.decode("utf-8", errors="replace") if res.stderr else ""
        return res.returncode, out, err
    except Exception as exc:
        return -1, "", str(exc)


def _redact_credential_diffs(diff_text: str) -> str:
    """Strip diff sections belonging to credential or internal .git paths."""
    if not diff_text:
        return ""
    blocks = diff_text.split("diff --git ")
    kept: List[str] = []
    for idx, blk in enumerate(blocks):
        if idx == 0:
            if blk.strip():
                kept.append(blk)
            continue
        header = blk.splitlines()[0] if blk else ""
        tokens = header.split()
        raw_path = tokens[-1][2:] if tokens and tokens[-1].startswith("b/") else (tokens[-1] if tokens else "")
        if raw_path and is_credential_path(raw_path):
            kept.append(f"a/{raw_path} b/{raw_path}\n[已跳过敏感凭据文件 Diff]")
        else:
            kept.append(blk)
    return "diff --git ".join(kept) if any(idx > 0 for idx in range(len(blocks))) else diff_text


def create_git_tools(work_dir: Path) -> List[BaseTool]:
    """Create read-only git_status and git_diff tools bound to work_dir."""
    resolved_root = work_dir.resolve()

    @tool
    def git_status() -> str:
        """只读查看当前工作区的 Git 分支与变更文件摘要（可在只读模式下安全调用）。"""
        code, out, err = _run_readonly_git(resolved_root, ["status", "--short", "--branch"])
        if code != 0:
            return f"ℹ️ 无法获取 Git 状态: {err.strip() or '当前目录不是 Git 仓库'}"
        clean = out.strip()
        return f"📂 当前 Git 工作区状态：\n{truncate_output(clean, max_length=4000)}" if clean else "📂 Git 工作区干净，无输出。"

    @tool
    def git_diff(filename: str = "", staged: bool = False) -> str:
        """只读查看工作区或暂存区的代码变更 Diff（支持指定 filename，可在只读模式下调用）。"""
        args = ["diff", "--staged"] if staged else ["diff"]
        if filename:
            try:
                target = resolve_safe_path(filename, resolved_root)
            except PermissionError as err:
                return f"❌ {str(err)}"
            if is_shared_hardlink(target):
                return f"❌ 文件 '{filename}' 是硬链接，已跳过读取其 Diff。"
            if names_credential_file(filename, target, resolved_root):
                return f"❌ 文件 '{filename}' 已跳过：疑似密钥或 Git 内部文件，禁止读取 Diff。"
            try:
                rel = target.relative_to(resolved_root).as_posix()
            except ValueError:
                rel = filename
            args.extend(["--", rel])

        code, out, err = _run_readonly_git(resolved_root, args)
        if code != 0:
            return f"ℹ️ 无法获取 Git Diff: {err.strip() or '当前目录不是 Git 仓库'}"
        redacted = _redact_credential_diffs(out).strip()
        if not redacted:
            scope = f"暂存区 ({filename or '全部'})" if staged else f"工作区 ({filename or '全部'})"
            return f"ℹ️ {scope} 当前没有已追踪文件的变更 Diff。"
        return f"🔍 Git Diff ({'staged' if staged else 'working tree'}):\n{truncate_output(redacted, max_length=8000)}"

    return [git_status, git_diff]
