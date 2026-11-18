"""AST-based repository topology map generator with line ranges and budget guards."""

import ast
import os
from pathlib import Path
from typing import Any, Dict, Iterator, List, Set, Tuple, Union

DEFAULT_MAX_REPO_FILES = 120
DEFAULT_MAX_REPO_CHARS = 16_000
_EXTRA_IGNORED_DIRS: Set[str] = {"env", "tiancode.egg-info"}


def _format_line_range(node: ast.AST) -> str:
    """Format inclusive AST line range like 'L12-28' cleanly."""
    start = getattr(node, "lineno", None)
    if not start:
        return ""
    end = getattr(node, "end_lineno", None) or start
    return f"L{start}-{end}" if end >= start else f"L{start}"


def format_signature(node: Union[ast.FunctionDef, ast.AsyncFunctionDef]) -> str:
    """Extract a concise function or method signature without excessive tokens."""
    args_list = []
    for arg in node.args.args:
        if arg.arg in ("self", "cls"):
            continue
        arg_str = arg.arg
        if arg.annotation:
            arg_str += f": {ast.unparse(arg.annotation)}"
        args_list.append(arg_str)

    sig = f"{node.name}({', '.join(args_list)})"
    if node.returns:
        sig += f" -> {ast.unparse(node.returns)}"
    return sig


def extract_file_symbols(code: str) -> List[Dict[str, Any]]:
    """Parse python code and extract top-level classes and functions with line ranges."""
    try:
        tree = ast.parse(code)
    except SyntaxError:
        return [{"type": "error", "name": "(语法解析失败)", "methods": []}]

    symbols: List[Dict[str, Any]] = []
    for node in tree.body:
        if isinstance(node, ast.ClassDef):
            bases = [ast.unparse(b) for b in node.bases]
            class_name = f"{node.name}({', '.join(bases)})" if bases else node.name
            methods: List[str] = []
            method_details: List[Dict[str, str]] = []
            for item in node.body:
                if isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    sig = format_signature(item)
                    methods.append(sig)
                    method_details.append({"sig": sig, "line_range": _format_line_range(item)})
            symbols.append({
                "type": "class",
                "name": class_name,
                "line_range": _format_line_range(node),
                "methods": methods,
                "method_details": method_details,
            })
        elif isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            symbols.append({
                "type": "function",
                "name": format_signature(node),
                "line_range": _format_line_range(node),
                "methods": [],
            })
    return symbols


def _filter_safe_dirs(root: str, dirs: List[str], resolved_dir: Path, ignored: Set[str]) -> List[str]:
    """Prune ignored directories and symlinks pointing outside work_dir."""
    safe: List[str] = []
    for d in sorted(dirs):
        if d.lower() in ignored or d.startswith("."):
            continue
        try:
            if Path(root, d).resolve().is_relative_to(resolved_dir):
                safe.append(d)
        except OSError:
            continue
    return safe


def _render_file_block(py_file: str, real_file: Path) -> List[str]:
    """Read a Python file and format its AST symbol block with jumpable line ranges."""
    try:
        content = real_file.read_text(encoding="utf-8", errors="replace")
        symbols = extract_file_symbols(content)
    except Exception:
        symbols = []

    block = [f"  📄 {py_file}"]
    for sym in symbols:
        lr = f" [{sym['line_range']}]" if sym.get("line_range") else ""
        if sym["type"] == "class":
            block.append(f"    class {sym['name']}:{lr}")
            details = sym.get("method_details") or [{"sig": m, "line_range": ""} for m in sym["methods"]]
            for md in details:
                mlr = f" [{md['line_range']}]" if md.get("line_range") else ""
                block.append(f"      def {md['sig']}{mlr}")
        elif sym["type"] == "function":
            block.append(f"    def {sym['name']}{lr}")
        elif sym["type"] == "error":
            block.append(f"    ⚠️ {sym['name']}")
    return block


def _iter_valid_py_files(resolved_dir: Path, max_depth: int, ignored: Set[str]) -> Iterator[Tuple[str, str, Path]]:
    """Yield (folder_header, filename, resolved_path) for workspace-contained Python files."""
    for root, dirs, files in os.walk(resolved_dir):
        dirs[:] = _filter_safe_dirs(root, dirs, resolved_dir, ignored)
        rel_root = Path(root).relative_to(resolved_dir)
        if len(rel_root.parts) > max_depth:
            dirs.clear()
            continue

        folder_header = f"\n📁 {rel_root}" if str(rel_root) != "." else "\n📁 根目录 /"
        for py_file in sorted(f for f in files if f.endswith(".py")):
            try:
                real_file = Path(root, py_file).resolve()
                if real_file.is_relative_to(resolved_dir) and real_file.is_file():
                    yield folder_header, py_file, real_file
            except OSError:
                continue


def _clip_first_block(pending: List[str], available_chars: int) -> List[str]:
    """Ensure even a single giant Python file respects the max_chars safety budget."""
    clipped: List[str] = []
    used = 0
    for item in pending:
        if len(clipped) >= 2 and used + len(item) + 1 > available_chars:
            break
        clipped.append(item)
        used += len(item) + 1
    return clipped


def generate_repo_map(
    work_dir: Path,
    max_depth: int = 4,
    *,
    max_files: int = DEFAULT_MAX_REPO_FILES,
    max_chars: int = DEFAULT_MAX_REPO_CHARS,
) -> str:
    """Scan work_dir and format a bounded AST code topology map with line numbers."""
    from tiancode.tools.file_ops import IGNORED_DIR_NAMES

    resolved_dir = work_dir.resolve()
    ignored = {d.lower() for d in IGNORED_DIR_NAMES} | _EXTRA_IGNORED_DIRS
    lines = ["🗺️ ====== [TianCode 项目代码全景图 (Repo Map)] ======"]
    char_count = len(lines[0]) + 1
    rendered_files = 0
    last_folder = ""
    truncated_reason = ""

    for folder_header, py_file, real_file in _iter_valid_py_files(resolved_dir, max_depth, ignored):
        if rendered_files >= max_files:
            truncated_reason = f"已达文件数上限 {max_files}"
            break

        block = _render_file_block(py_file, real_file)
        pending = ([folder_header] if folder_header != last_folder else []) + block
        pending_chars = sum(len(x) + 1 for x in pending)
        if char_count + pending_chars + 100 > max_chars:
            if rendered_files == 0:
                pending = _clip_first_block(pending, max(0, max_chars - char_count - 100))
                lines.extend(pending)
                rendered_files = 1
            truncated_reason = f"已达字符预算上限 {max_chars}"
            break

        lines.extend(pending)
        char_count += pending_chars
        last_folder = folder_header
        rendered_files += 1

    if not rendered_files:
        return "💡 当前项目未检测到有效的 Python 代码文件。"
    if truncated_reason:
        lines.append(f"\n⚠️ [全景图输出已截断 ({truncated_reason}，共展示 {rendered_files} 个文件)；请使用 grep_search 或 list_files 按子目录精准定位]")
    lines.append("\n======================================================\n")
    return "\n".join(lines)
