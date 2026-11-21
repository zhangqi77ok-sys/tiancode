"""File operations tools with visual diff rendering, snapshot rollback, and post-edit self-healing."""

import os
import subprocess
import sys
from pathlib import Path
from typing import List, Optional
from langchain_core.tools import BaseTool, tool
from tiancode.domain.interfaces import SnapshotStore


def resolve_safe_path(filename: str | Path, work_dir: Path) -> Path:
    """Resolve path and verify it is strictly within work_dir (anti-traversal)."""
    target = Path(filename)
    resolved_work_dir = work_dir.resolve()

    if target.is_absolute():
        resolved_target = target.resolve()
    else:
        resolved_target = (work_dir / target).resolve()

    try:
        resolved_target.relative_to(resolved_work_dir)
    except ValueError:
        raise PermissionError(f"安全拦截：路径 '{filename}' 越界，位于工作区 '{work_dir}' 外部，禁止访问！")

    return resolved_target


IGNORED_DIR_NAMES = {
    ".git",
    "node_modules",
    "appdata",
    ".cache",
    "target",
    "build",
    "dist",
    "__pycache__",
    ".vscode",
    ".idea",
    "venv",
    ".venv",
    ".pytest_cache",
}


def _find_match_lines(src: str, needle: str) -> List[int]:
    """Find 1-based starting line numbers for all non-overlapping occurrences of needle."""
    lines: List[int] = []
    pos = 0
    while True:
        idx = src.find(needle, pos)
        if idx == -1:
            break
        lines.append(src[:idx].count("\n") + 1)
        pos = idx + max(1, len(needle))
    return lines


def _print_visual_diff(diff) -> None:
    """Safely print visual diff via TerminalFormatter without tight coupling."""
    try:
        from tiancode.cli.formatter import TerminalFormatter
        TerminalFormatter.print_diff(diff)
    except Exception:
        pass


def _run_post_edit_verification(work_dir: Path) -> Optional[str]:
    """Helper to detect and run project test suite for self-healing verification with safety and env scrubbing."""
    from tiancode.core.polyglot import detect_project_test_command
    from tiancode.tools.execution import get_sanitized_env, is_dangerous_command

    cmd = detect_project_test_command(work_dir)
    if not cmd:
        return None

    if is_dangerous_command(cmd):
        return "自动化测试安全拦截：检测到高危测试命令，已阻止执行。"

    try:
        creationflags = 0x08000000 if sys.platform == "win32" else 0
        res = subprocess.run(
            cmd,
            cwd=str(work_dir),
            shell=True,
            capture_output=True,
            text=True,
            timeout=8,
            encoding="utf-8",
            errors="replace",
            env=get_sanitized_env(),
            creationflags=creationflags,
        )
        if res.returncode == 0:
            return "单测验证通过 (100% 绿灯)"
        else:
            err_msg = (res.stderr.strip() or res.stdout.strip())[-1000:]
            return f"自动化测试未通过 (Exit {res.returncode})：\n{err_msg}\n请根据上述报错自我修正；若修复陷入死胡同，可调用 'rollback_turn' 安全回滚。"
    except subprocess.TimeoutExpired:
        return "自动化测试超时（超过 8 秒未完成，已终止）"
    except Exception as exc:
        return f"自动化测试跳过: {str(exc)}"


def create_file_tools(store: SnapshotStore, work_dir: Path, auto_heal: bool = True) -> List[BaseTool]:
    """Factory to create file operation tools bound to a specific snapshot store and directory."""

    def _resolve(filename: str) -> Path:
        return resolve_safe_path(filename, work_dir)

    @tool
    def list_files(directory: str = ".") -> str:
        """列出指定目录下的代码和配置文件。不了解项目结构时优先调用。"""
        try:
            target = _resolve(directory)
        except PermissionError as err:
            return f"❌ {str(err)}"

        if not target.exists():
            return f"❌ 目录 {directory} 不存在！"
        exts = (".py", ".json", ".txt", ".md", ".toml", ".yaml", ".yml", ".java", ".go", ".rs", ".js", ".ts")
        matched = []
        for root, dirs, files in os.walk(target):
            # Prune heavy directories in-place to stop traversal instantly
            dirs[:] = [d for d in dirs if d.lower() not in IGNORED_DIR_NAMES and not d.startswith(".")]
            rel_depth = len(Path(root).relative_to(target).parts)
            if rel_depth > 3:
                dirs.clear()
                continue

            for f in files:
                if f.endswith(exts):
                    rel = Path(root, f).relative_to(target)
                    matched.append(str(rel))
                    if len(matched) >= 100:
                        break
            if len(matched) >= 100:
                break

        if not matched:
            return "目录为空或未发现代码文件。"
        trunc_note = " (已显示前 100 个文件)" if len(matched) >= 100 else ""
        return f"当前项目文件{trunc_note}：{matched}"

    @tool
    def get_repo_map() -> str:
        """获取当前工程的代码拓扑全景图（包含全部模块的类、函数与方法签名）。在定位跨模块调用或不熟悉大型项目结构时优先调用此工具。"""
        from tiancode.core.repo_map import generate_repo_map
        return generate_repo_map(work_dir)

    from tiancode.core.search import create_search_tools
    search_tools = create_search_tools(work_dir, IGNORED_DIR_NAMES)

    def _persist_change(
        target: Path,
        original: str,
        modified: str,
        *,
        filename: str,
        is_new: bool,
        action_label: str,
    ) -> str:
        """Validate syntax, record snapshot, replace the file in place, and run self-heal."""
        from tiancode.core.path_safety import WorkspacePathError, restore_workspace_text
        from tiancode.core.syntax_guard import SyntaxGuard
        check_res = SyntaxGuard().validate(filename, modified)
        if not check_res.is_valid:
            return check_res.error_message or "❌ 语法校验失败，已阻断写入！"

        from tiancode.core.diff import compute_text_diff
        diff = compute_text_diff(filename, original, modified)
        _print_visual_diff(diff)

        store.record_snapshot(str(target), original, is_new=is_new)
        try:
            restore_workspace_text(target, modified)
        except (OSError, UnicodeError, WorkspacePathError) as exc:
            return f"❌ 写入已中止，原文件保持不变 ({type(exc).__name__}: {exc})"

        summary = f"✅ 文件 '{filename}' {action_label} (+{diff.additions} / -{diff.deletions})"
        if auto_heal:
            test_res = _run_post_edit_verification(work_dir)
            if test_res:
                summary += f"，{test_res}"
        return summary

    @tool
    def write_file(filename: str, content: str) -> str:
        """全量新建或覆盖写入文件（自动渲染 Visual Diff 与安全快照）。"""
        try:
            target = _resolve(filename)
        except PermissionError as err:
            return f"❌ {str(err)}"

        is_new = not target.exists()
        original = "" if is_new else target.read_text(encoding="utf-8", errors="replace")
        return _persist_change(target, original, content, filename=filename, is_new=is_new, action_label="写入成功")

    @tool
    def edit_file(filename: str, old_code: str, new_code: str, replace_all: bool = False) -> str:
        """局部修改文件：将原代码中唯一的 old_code 精准替换为 new_code；若存在多处匹配需补充上下文或显式传 replace_all=True。"""
        if not old_code:
            return "❌ 替换失败：old_code 不能为空字符串！若需新建或全量覆盖文件，请调用 write_file。"
        try:
            target = _resolve(filename)
        except PermissionError as err:
            return f"❌ {str(err)}"

        if not target.exists():
            return f"❌ 文件 '{filename}' 不存在，请先确认路径！"
        src = target.read_text(encoding="utf-8", errors="replace")

        match_lines = _find_match_lines(src, old_code)
        if not match_lines:
            return f"❌ 替换失败：文件中未找到完全匹配的旧代码！当前源码如下，请对照后再修改：\n{src}"
        if len(match_lines) > 1 and not replace_all:
            return (
                f"❌ 替换拦截：old_code 在 '{filename}' 中匹配到 {len(match_lines)} 处（位于第 {match_lines} 行），"
                f"存在歧义风险！请补充更多上下文使其唯一，或显式传入 replace_all=True。"
            )

        modified = src.replace(old_code, new_code) if replace_all else src.replace(old_code, new_code, 1)
        return _persist_change(target, src, modified, filename=filename, is_new=False, action_label="局部替换成功")

    @tool
    def delete_file(filename: str) -> str:
        """安全删除文件：删除前自动捕获全文快照，支持随时通过 /undo 或 rollback_turn 无损恢复。"""
        try:
            target = _resolve(filename)
        except PermissionError as err:
            return f"❌ {str(err)}"

        if not target.exists() and not target.is_symlink():
            return f"❌ 文件 '{filename}' 不存在，无法删除！"
        if target.is_dir():
            return f"❌ '{filename}' 是目录而非文件，为防止误删已拦截！"

        original = target.read_text(encoding="utf-8", errors="replace") if target.is_file() else ""
        store.record_snapshot(str(target), original, is_new=False)
        target.unlink()
        return f"✅ 已安全删除文件 '{filename}' (已记录快照，可随时通过 /undo 无损恢复)。"

    @tool
    def rollback_turn() -> str:
        """当代码修改或单测修复陷入死胡同、无法通过测试时，主动调用此工具将工作区代码安全回滚至修改前状态。"""
        reverted = store.rollback()
        if reverted:
            return "⏪ 已完成工作区安全回滚：\n" + "\n".join(f"  • {item}" for item in reverted)
        return "ℹ️ 当前没有可回滚的修改。"

    return [list_files, get_repo_map, *search_tools, write_file, edit_file, delete_file, rollback_turn]
