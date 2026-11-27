"""File operations tools with visual diff rendering, snapshot rollback, and post-edit self-healing."""

import difflib
import os
from pathlib import Path
from typing import Dict, List, Optional, Tuple
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


def _try_relaxed_replace(src: str, old_code: str, new_code: str) -> Optional[str]:
    """Match ignoring trailing spaces and CRLF/LF differences when exactly one window matches."""
    src_lines = src.splitlines(keepends=True)
    old_norm = [ln.rstrip() for ln in old_code.replace("\r\n", "\n").splitlines()]
    if not old_norm or len(old_norm) > len(src_lines):
        return None

    hits: List[int] = []
    win = len(old_norm)
    for start in range(len(src_lines) - win + 1):
        if [src_lines[start + k].rstrip() for k in range(win)] == old_norm:
            hits.append(start)
            if len(hits) > 1:
                return None

    if len(hits) != 1:
        return None

    idx = hits[0]
    eol = "\r\n" if "\r\n" in src else "\n"
    new_norm = new_code.replace("\r\n", "\n")
    if src_lines[idx + win - 1].endswith(("\n", "\r")) and not new_norm.endswith("\n"):
        new_norm += "\n"
    replacement = new_norm.replace("\n", eol)
    return "".join(src_lines[:idx]) + replacement + "".join(src_lines[idx + win :])


def _format_closest_snippets(src: str, old_code: str, filename: str) -> str:
    """Return top-2 closest matching line ranges instead of dumping the entire file."""
    src_lines = src.splitlines()
    if len(src_lines) <= 40:
        numbered = "\n".join(f"{i:4d} | {line}" for i, line in enumerate(src_lines, start=1))
        return f"❌ 替换失败：'{filename}' 中未找到完全匹配的旧代码。文件较短，当前源码如下：\n{numbered}"

    old_lines = old_code.splitlines()
    win = max(1, min(len(old_lines), len(src_lines)))
    scored: List[tuple[float, int]] = []
    step = 1 if len(src_lines) <= 400 else max(1, win // 3)
    for start in range(0, len(src_lines) - win + 1, step):
        candidate = "\n".join(src_lines[start : start + win])
        ratio = difflib.SequenceMatcher(None, old_code, candidate).quick_ratio()
        if ratio >= 0.45:
            scored.append((ratio, start))

    scored.sort(key=lambda item: item[0], reverse=True)
    if not scored:
        return (
            f"❌ 替换失败：'{filename}' (共 {len(src_lines)} 行) 中未找到完全匹配的旧代码。"
            "请先用 grep_search 或 read_file 核实目标行号与准确文本。"
        )

    blocks: List[str] = []
    used_starts: List[int] = []
    for ratio, start in scored:
        if any(abs(start - u) < win for u in used_starts):
            continue
        used_starts.append(start)
        ctx_start = max(0, start - 1)
        ctx_end = min(len(src_lines), start + win + 1)
        snippet = "\n".join(f"{i + 1:4d} | {src_lines[i]}" for i in range(ctx_start, ctx_end))
        blocks.append(f"候选位置 L{ctx_start + 1}-L{ctx_end} (相似度 {ratio:.0%})：\n{snippet}")
        if len(blocks) >= 2:
            break

    return (
        f"❌ 替换失败：'{filename}' 中未找到完全匹配的旧代码。最接近的片段如下（请复制纯正文，勿带行号）：\n\n"
        + "\n\n".join(blocks)
    )


def _print_visual_diff(diff) -> None:
    """Safely print visual diff via TerminalFormatter without tight coupling."""
    try:
        from tiancode.cli.formatter import TerminalFormatter
        TerminalFormatter.print_diff(diff)
    except Exception:
        pass


def _resolve_targeted_test_cmd(work_dir: Path, base_cmd: str, filename: Optional[str]) -> str:
    """Narrow pytest command to matching module test file when available for faster feedback."""
    if not filename or "pytest" not in base_cmd:
        return base_cmd
    edited = Path(filename)
    if edited.name.startswith("test_") and edited.suffix == ".py" and (work_dir / edited).is_file():
        return f"pytest {edited.as_posix()} -q"
    candidate = work_dir / "tests" / f"test_{edited.stem}.py"
    if candidate.is_file():
        return f"pytest tests/test_{edited.stem}.py -q"
    return base_cmd


def _run_post_edit_verification(work_dir: Path, filename: Optional[str] = None) -> Optional[str]:
    """Helper to detect and run project test suite for self-healing verification with safety and env scrubbing."""
    from tiancode.core.polyglot import detect_project_test_command
    from tiancode.tools.execution import execute_shell_command

    cmd = detect_project_test_command(work_dir)
    if not cmd:
        return None
    cmd = _resolve_targeted_test_cmd(work_dir, cmd, filename)
    try:
        res = execute_shell_command(cmd, work_dir, timeout=8)
    except Exception as exc:
        return f"自动化测试跳过: {str(exc)}"
    if res.exit_code == 126:
        return "自动化测试安全拦截：检测到高危测试命令，已阻止执行。"
    if res.timed_out:
        return "自动化测试超时（超过 8 秒未完成，已终止）"
    if res.is_success:
        return f"已自动运行 '{cmd}' 单测验证通过 (100% 绿灯，无需再重复运行)"
    err_msg = (res.stderr.strip() or res.stdout.strip())[-1000:]
    return (
        f"自动化测试未通过 (Exit {res.exit_code})：\n{err_msg}\n"
        "请根据上述报错自我修正；若修复陷入死胡同，可调用 'rollback_turn' 安全回滚。"
    )


def create_file_tools(store: SnapshotStore, work_dir: Path, auto_heal: bool = True) -> List[BaseTool]:
    """Factory to create file operation tools bound to a specific snapshot store and directory."""

    def _resolve(filename: str) -> Path:
        return resolve_safe_path(filename, work_dir)

    def _mutation_entry(filename: str) -> tuple[Optional[Path], str]:
        """Return the workspace directory entry. Do not follow a final symlink."""
        from tiancode.core.path_safety import WorkspacePathError, workspace_directory_entry

        try:
            return workspace_directory_entry(work_dir, filename), ""
        except WorkspacePathError:
            return None, (
                f"❌ 安全拦截：路径 '{filename}' 越界，位于工作区 '{work_dir}' 外部，禁止访问！"
            )

    def _symlink_text(entry: Path) -> Optional[str]:
        """Read the link text only. Do not open the target."""
        try:
            text = os.readlink(entry)
        except OSError:
            return None
        if not text or "\x00" in text:
            return None
        return text

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
    def get_repo_map(directory: str = ".") -> str:
        """获取当前工程或指定子目录的代码拓扑全景图（包含模块类、函数与方法签名）。定位跨模块调用或不熟悉项目结构时优先调用。"""
        from tiancode.core.repo_map import generate_repo_map
        return generate_repo_map(work_dir, directory=directory)

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
        skip_verify: bool = False,
        line_hint: str = "",
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

        hint_part = f" {line_hint}" if line_hint else ""
        summary = f"✅ 文件 '{filename}' {action_label} (+{diff.additions} / -{diff.deletions}){hint_part}"
        if auto_heal and not skip_verify:
            test_res = _run_post_edit_verification(work_dir, filename)
            if test_res:
                summary += f"，{test_res}"
        return summary

    @tool
    def write_file(filename: str, content: str, skip_verify: bool = False) -> str:
        """全量新建或覆盖写入文件；连续编辑多个关联文件时可传 skip_verify=True 跳过中间态单测。"""
        target, error = _mutation_entry(filename)
        if target is None:
            return error
        if target.is_symlink():
            from tiancode.core.path_safety import WorkspacePathError, restore_workspace_text
            from tiancode.core.syntax_guard import SyntaxGuard

            link_text = _symlink_text(target)
            if link_text is None:
                return f"❌ 文件 '{filename}' 是符号链接，但无法记录链接本身，已拒绝写入，未读取其目标。"
            check_res = SyntaxGuard().validate(filename, content)
            if not check_res.is_valid:
                return check_res.error_message or "❌ 语法校验失败，已阻断写入！"
            try:
                restore_workspace_text(target, content)
            except (OSError, UnicodeError, WorkspacePathError) as exc:
                return f"❌ 写入已中止，符号链接保持不变 ({type(exc).__name__}: {exc})"
            store.record_snapshot(str(target), "", is_new=False, link_target=link_text)
            summary = (
                f"✅ 文件 '{filename}' 写入成功，已用普通文件替换符号链接，未读取或修改链接目标。"
                "/undo 会把该名字还原成原来的符号链接。"
            )
            if auto_heal:
                test_res = _run_post_edit_verification(work_dir)
                if test_res:
                    summary += f"，{test_res}"
            return summary
        if target.is_dir():
            return f"❌ '{filename}' 是目录而非文件，已拒绝写入。"
        from tiancode.core.path_safety import is_shared_hardlink, names_credential_file

        if is_shared_hardlink(target):
            return f"❌ 文件 '{filename}' 是硬链接，未读取或修改其内容。"
        if names_credential_file(filename, target, work_dir):
            return f"❌ 文件 '{filename}' 已跳过：疑似密钥或 Git 内部文件，未读取或修改其内容。"

        is_new = not target.exists()
        original = "" if is_new else target.read_text(encoding="utf-8", errors="replace")
        return _persist_change(
            target, original, content, filename=filename, is_new=is_new,
            action_label="写入成功", skip_verify=skip_verify,
        )

    def _prepare_regular_edit(filename: str) -> Tuple[Optional[Path], str, str]:
        """Validate workspace containment and regular-file safety before editing."""
        target, error = _mutation_entry(filename)
        if target is None:
            return None, "", error
        if target.is_symlink():
            return None, "", f"❌ 文件 '{filename}' 是符号链接，未读取或修改其目标。"
        if not target.exists():
            return None, "", f"❌ 文件 '{filename}' 不存在，请先确认路径！"
        if target.is_dir():
            return None, "", f"❌ '{filename}' 是目录而非文件，已拒绝修改。"
        from tiancode.core.path_safety import is_shared_hardlink, names_credential_file

        if is_shared_hardlink(target):
            return None, "", f"❌ 文件 '{filename}' 是硬链接，未读取或修改其内容。"
        if names_credential_file(filename, target, work_dir):
            return None, "", f"❌ 文件 '{filename}' 已跳过：疑似密钥或 Git 内部文件，未读取或修改其内容。"
        return target, target.read_text(encoding="utf-8", errors="replace"), ""

    def _apply_patch_step(
        src: str, old_code: str, new_code: str, *, filename: str, replace_all: bool = False,
    ) -> Tuple[Optional[str], str, str]:
        """Apply one exact or relaxed patch in memory; return (modified, line_hint, error_msg)."""
        if not old_code:
            return None, "", "❌ 替换失败：old_code 不能为空字符串！若需新建或全量覆盖文件，请调用 write_file。"
        match_lines = _find_match_lines(src, old_code)
        if not match_lines:
            if not replace_all:
                relaxed = _try_relaxed_replace(src, old_code, new_code)
                if relaxed is not None:
                    return relaxed, "", ""
            return None, "", _format_closest_snippets(src, old_code, filename)
        if len(match_lines) > 1 and not replace_all:
            err = (
                f"❌ 替换拦截：old_code 在 '{filename}' 中匹配到 {len(match_lines)} 处（位于第 {match_lines} 行），"
                f"存在歧义风险！请补充更多上下文使其唯一，或显式传入 replace_all=True。"
            )
            return None, "", err
        modified = src.replace(old_code, new_code) if replace_all else src.replace(old_code, new_code, 1)
        total_lines = len(modified.splitlines())
        hint = (
            f"(起于 L{match_lines[0]}，共 {total_lines} 行)"
            if len(match_lines) == 1
            else f"(共替换 {len(match_lines)} 处，共 {total_lines} 行)"
        )
        return modified, hint, ""

    @tool
    def edit_file(filename: str, old_code: str, new_code: str, replace_all: bool = False, skip_verify: bool = False) -> str:
        """局部修改文件：精准替换 old_code 为 new_code；连续重构多文件中间态可传 skip_verify=True。"""
        target, src, err = _prepare_regular_edit(filename)
        if target is None:
            return err
        modified, line_hint, patch_err = _apply_patch_step(src, old_code, new_code, filename=filename, replace_all=replace_all)
        if modified is None:
            return patch_err
        return _persist_change(
            target, src, modified, filename=filename, is_new=False,
            action_label="局部替换成功", skip_verify=skip_verify, line_hint=line_hint,
        )

    @tool
    def multi_edit(filename: str, edits: List[Dict[str, str]], skip_verify: bool = False) -> str:
        """单文件多段原子修改：一次传入多组 {'old_code': ..., 'new_code': ...}，全部匹配成功后再一次性落盘与验证。"""
        if not edits:
            return "❌ 批量修改失败：edits 列表不能为空。"
        target, src, err = _prepare_regular_edit(filename)
        if target is None:
            return err

        curr = src
        for step_idx, item in enumerate(edits, start=1):
            if not isinstance(item, dict) or "old_code" not in item or "new_code" not in item:
                return f"❌ 批量修改中止：第 {step_idx} 项格式无效，需包含 old_code 与 new_code。"
            next_text, _, patch_err = _apply_patch_step(
                curr, str(item["old_code"]), str(item["new_code"]), filename=filename,
            )
            if next_text is None:
                return f"❌ 批量修改已在第 {step_idx}/{len(edits)} 处中止，原文件未发生任何变更：\n{patch_err}"
            curr = next_text

        total_lines = len(curr.splitlines())
        hint = f"(共 {len(edits)} 处连续生效，总 {total_lines} 行)"
        return _persist_change(
            target, src, curr, filename=filename, is_new=False,
            action_label="批量局部替换成功", skip_verify=skip_verify, line_hint=hint,
        )

    @tool
    def delete_file(filename: str) -> str:
        """删除这个名字。普通文件 /undo 还原原文；符号链接 /undo 还原链接，不碰目标。"""
        from tiancode.core.path_safety import remove_workspace_file

        target, error = _mutation_entry(filename)
        if target is None:
            return error
        if target.is_symlink():
            link_text = _symlink_text(target)
            if link_text is None:
                return f"❌ 文件 '{filename}' 是符号链接，但无法记录链接本身，已拒绝删除，未读取其目标。"
            store.record_snapshot(str(target), "", is_new=False, link_target=link_text)
            remove_workspace_file(target)
            return (
                f"✅ 已删除符号链接 '{filename}'，未读取或删除其目标。"
                "/undo 会把该名字还原成原来的符号链接。"
            )
        if not target.exists():
            return f"❌ 文件 '{filename}' 不存在，无法删除！"
        if target.is_dir():
            return f"❌ '{filename}' 是目录而非文件，为防止误删已拦截！"
        from tiancode.core.path_safety import is_shared_hardlink, names_credential_file

        if is_shared_hardlink(target):
            return f"❌ 文件 '{filename}' 是硬链接，未读取或修改其内容。"
        if names_credential_file(filename, target, work_dir):
            return f"❌ 文件 '{filename}' 已跳过：疑似密钥或 Git 内部文件，未读取或修改其内容。"

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

    return [list_files, get_repo_map, *search_tools, write_file, edit_file, multi_edit, delete_file, rollback_turn]
