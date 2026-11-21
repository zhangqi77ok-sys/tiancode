"""Cross-platform sandboxed code search (grep) and paginated file reader for TianCode."""

import fnmatch
import os
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterator, List, Optional, Set
from langchain_core.tools import BaseTool, tool

MAX_READ_LINES = 300
MAX_READ_CHARS = 24_000
MAX_GREP_RESULTS = 50
MAX_LINE_SNIPPET_CHARS = 240
MAX_SEARCH_FILE_BYTES = 2 * 1024 * 1024  # 2 MB safety threshold


@dataclass
class GrepOptions:
    """Configuration options for workspace-scoped code search."""

    pattern: str
    directory: str = "."
    file_glob: str = "*"
    is_regex: bool = False
    case_sensitive: bool = True
    max_results: int = MAX_GREP_RESULTS
    ignored_dirs: Set[str] = field(default_factory=set)


def _is_binary_file(path: Path) -> bool:
    """Inspect initial bytes to detect binary files safely without decoding full payloads."""
    try:
        with open(path, "rb") as f:
            return b"\x00" in f.read(1024)
    except OSError:
        return True


def _compile_pattern(options: GrepOptions) -> re.Pattern:
    """Compile search pattern uniformly for both literal and regex modes."""
    flags = 0 if options.case_sensitive else re.IGNORECASE
    raw = options.pattern if options.is_regex else re.escape(options.pattern)
    return re.compile(raw, flags)


def _match_glob(rel_posix: str, filename: str, file_glob: str) -> bool:
    """Match either basename or relative path against the glob filter."""
    if not file_glob or file_glob == "*":
        return True
    if "/" in file_glob or "\\" in file_glob:
        return fnmatch.fnmatch(rel_posix, file_glob.replace("\\", "/"))
    return fnmatch.fnmatch(filename, file_glob)


def _is_safe_file(full_p: Path, resolved_root: Path, file_glob: str) -> bool:
    """Verify physical containment, regular file status, size budget, and glob match."""
    from tiancode.core.path_safety import is_credential_path, is_shared_hardlink

    if is_shared_hardlink(full_p):
        return False
    try:
        if is_credential_path(full_p.relative_to(resolved_root)):
            return False
        real_p = full_p.resolve()
        if not real_p.is_relative_to(resolved_root) or not real_p.is_file():
            return False
        if is_credential_path(real_p.relative_to(resolved_root)):
            return False
        if real_p.stat().st_size > MAX_SEARCH_FILE_BYTES:
            return False
        rel_posix = full_p.relative_to(resolved_root).as_posix()
        return _match_glob(rel_posix, full_p.name, file_glob)
    except (OSError, ValueError):
        return False


def _filter_safe_dirs(root: str, dirs: List[str], resolved_root: Path, ignored: Set[str]) -> List[str]:
    """Prune ignored directories and symlinks escaping the workspace root."""
    safe: List[str] = []
    for d in sorted(dirs):
        if d.lower() in ignored or d.startswith("."):
            continue
        try:
            if Path(root, d).resolve().is_relative_to(resolved_root):
                safe.append(d)
        except OSError:
            continue
    return safe


def _iter_safe_files(target: Path, resolved_root: Path, options: GrepOptions) -> Iterator[Path]:
    """Yield workspace-contained, non-oversized files while pruning heavy or escaping dirs."""
    if target.is_file():
        if _is_safe_file(target, resolved_root, options.file_glob):
            yield target
        return

    for root, dirs, files in os.walk(target):
        dirs[:] = _filter_safe_dirs(root, dirs, resolved_root, options.ignored_dirs)
        for fname in sorted(files):
            full_p = Path(root, fname)
            if _is_safe_file(full_p, resolved_root, options.file_glob):
                yield full_p


def _scan_file_lines(file_path: Path, resolved_root: Path, regex: re.Pattern, *, remaining: int) -> List[str]:
    """Scan a single text file line-by-line and format clipped matching snippets."""
    if remaining <= 0 or _is_binary_file(file_path):
        return []

    rel_path = file_path.relative_to(resolved_root).as_posix()
    hits: List[str] = []
    try:
        with open(file_path, "r", encoding="utf-8", errors="replace") as f:
            for lineno, line in enumerate(f, start=1):
                if not regex.search(line):
                    continue
                clean = line.rstrip("\r\n")
                if len(clean) > MAX_LINE_SNIPPET_CHARS:
                    clean = clean[:MAX_LINE_SNIPPET_CHARS] + "... [已截断]"
                hits.append(f"{rel_path}:{lineno}: {clean}")
                if len(hits) >= remaining:
                    break
    except OSError:
        return []
    return hits


def search_workspace(work_dir: Path, options: GrepOptions, ignored_dirs: Set[str]) -> str:
    """Execute sandboxed content search across workspace text files."""
    if not options.pattern:
        return "❌ 搜索关键词不能为空！"
    try:
        regex = _compile_pattern(options)
    except re.error as err:
        return f"❌ 无效的正则表达式 '{options.pattern}': {err}"

    from tiancode.tools.file_ops import resolve_safe_path

    try:
        target = resolve_safe_path(options.directory, work_dir)
    except PermissionError as err:
        return f"❌ {str(err)}"
    if not target.exists():
        return f"❌ 搜索路径 '{options.directory}' 不存在！"

    resolved_root = work_dir.resolve()
    scoped_opts = GrepOptions(
        pattern=options.pattern,
        directory=options.directory,
        file_glob=options.file_glob,
        is_regex=options.is_regex,
        case_sensitive=options.case_sensitive,
        max_results=max(1, min(options.max_results, 200)),
        ignored_dirs=options.ignored_dirs or ignored_dirs,
    )
    matches: List[str] = []
    for fpath in _iter_safe_files(target, resolved_root, scoped_opts):
        matches.extend(_scan_file_lines(fpath, resolved_root, regex, remaining=scoped_opts.max_results - len(matches)))
        if len(matches) >= scoped_opts.max_results:
            break

    if not matches:
        return f"🔍 未在 '{options.directory}' (过滤: {options.file_glob}) 中找到匹配 '{options.pattern}' 的内容。"
    header = f"🔍 搜索 '{options.pattern}' 共匹配 {len(matches)} 处"
    if len(matches) >= scoped_opts.max_results:
        header += f" (已达单次上限 {scoped_opts.max_results} 条，请缩小 directory 或 file_glob 范围)"
    return header + "：\n" + "\n".join(matches)


def _slice_file_lines(target: Path, s_line: int, capped_end: int) -> tuple[List[str], int, int]:
    """Stream lines in [s_line, capped_end] within MAX_READ_CHARS budget and count total lines."""
    collected: List[str] = []
    char_count = 0
    total_lines = 0
    actual_end = s_line - 1
    hit_char_cap = False

    with open(target, "r", encoding="utf-8", errors="replace") as f:
        for lineno, raw in enumerate(f, start=1):
            total_lines = lineno
            if lineno < s_line or lineno > capped_end or hit_char_cap:
                continue

            formatted = f"{lineno:4d} | {raw.rstrip(chr(13) + chr(10))}"
            if collected and char_count + len(formatted) + 1 > MAX_READ_CHARS:
                hit_char_cap = True
                continue
            if len(formatted) > MAX_READ_CHARS:
                formatted = formatted[:MAX_READ_CHARS] + "... [单行超长已截断]"
                hit_char_cap = True

            collected.append(formatted)
            actual_end = lineno
            char_count += len(formatted) + 1

    return collected, total_lines, actual_end


def read_file_paginated(
    work_dir: Path,
    filename: str,
    *,
    start_line: Optional[int] = None,
    end_line: Optional[int] = None,
) -> str:
    """Read a workspace text file safely with line numbers and strict slice caps."""
    from tiancode.tools.file_ops import resolve_safe_path

    from tiancode.core.path_safety import is_shared_hardlink, names_credential_file

    try:
        target = resolve_safe_path(filename, work_dir)
    except PermissionError as err:
        return f"❌ {str(err)}"

    if is_shared_hardlink(target):
        return f"❌ 文件 '{filename}' 是硬链接，未读取其内容。"
    if names_credential_file(filename, target, work_dir):
        return f"❌ 文件 '{filename}' 已跳过：疑似密钥或 Git 内部文件，未读取内容。"
    if not target.exists():
        parent = target.parent if target.parent.is_dir() else work_dir
        nearby = [f for f in os.listdir(parent) if f.endswith(".py")] if parent.is_dir() else []
        return f"❌ 文件 '{filename}' 不存在！在同目录下发现的文件有：{nearby}。请核实文件名！"
    if target.is_dir():
        return f"❌ '{filename}' 是目录而非文件，请调用 list_files 查看！"
    if _is_binary_file(target):
        return f"❌ 文件 '{filename}' 似乎是二进制文件，无法按纯文本源码读取。"

    s_line = max(1, start_line) if start_line is not None else 1
    if end_line is not None and end_line < s_line:
        return f"❌ 参数无效：end_line ({end_line}) 不能小于 start_line ({s_line})。"

    capped_end = min(end_line, s_line + MAX_READ_LINES - 1) if end_line is not None else (s_line + MAX_READ_LINES - 1)
    collected, total_lines, actual_end = _slice_file_lines(target, s_line, capped_end)

    if total_lines == 0:
        return f"📄 文件 '{filename}' 为空 (0 行)。"
    if s_line > total_lines:
        return f"ℹ️ 文件 '{filename}' 共 {total_lines} 行，请求的起始行 {s_line} 超出范围。"

    header = f"📄 {filename} (第 {s_line}-{actual_end} 行 / 共 {total_lines} 行)：\n"
    footer = ""
    if actual_end < total_lines:
        next_start = actual_end + 1
        next_end = min(total_lines, next_start + MAX_READ_LINES - 1)
        footer = (
            f"\n... [已截断，还有 {total_lines - actual_end} 行未显示。"
            f"继续阅读请调用 read_file(filename='{filename}', start_line={next_start}, end_line={next_end})] ..."
        )
    return header + "\n".join(collected) + footer


def create_search_tools(work_dir: Path, ignored_dirs: Set[str]) -> List[BaseTool]:
    """Create shared read-only search and paginated file reader tools for master and subagents."""

    @tool
    def grep_search(
        pattern: str,
        directory: str = ".",
        file_glob: str = "*",
        is_regex: bool = False,
        max_results: int = MAX_GREP_RESULTS,
    ) -> str:
        """在工作区代码或日志文件中按关键字或正则表达式精准搜索，返回 '文件:行号: 内容'。在查找符号引用、报错堆栈或跨文件调用时优先调用！"""
        options = GrepOptions(
            pattern=pattern,
            directory=directory,
            file_glob=file_glob,
            is_regex=is_regex,
            max_results=max_results,
            ignored_dirs=ignored_dirs,
        )
        return search_workspace(work_dir, options, ignored_dirs)

    @tool
    def read_file(
        filename: str,
        start_line: Optional[int] = None,
        end_line: Optional[int] = None,
    ) -> str:
        """带行号分段读取文件源码或日志内容（支持 start_line/end_line，单次最多 300 行）。调用 edit_file 时 old_code 仅填纯正文，切勿包含行号前缀！"""
        return read_file_paginated(work_dir, filename, start_line=start_line, end_line=end_line)

    return [grep_search, read_file]
