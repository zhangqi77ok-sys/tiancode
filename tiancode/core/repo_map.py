"""AST-based repository topology map generator with line ranges and budget guards."""

import ast
import os
import re
from pathlib import Path
from typing import Any, Dict, Iterator, List, Set, Tuple, Union

DEFAULT_MAX_REPO_FILES = 120
DEFAULT_MAX_REPO_CHARS = 16_000
_EXTRA_IGNORED_DIRS: Set[str] = {"env", "tiancode.egg-info"}
_JS_TS_EXTS: Tuple[str, ...] = (".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs")
_TS_DECL_PATTERNS: Tuple[Tuple[str, re.Pattern[str]], ...] = (
    ("class", re.compile(r"^\s*(?:export\s+(?:default\s+)?)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)")),
    ("interface", re.compile(r"^\s*(?:export\s+)?interface\s+([A-Za-z_$][\w$]*)")),
    ("type", re.compile(r"^\s*(?:export\s+)?type\s+([A-Za-z_$][\w$]*)\s*=")),
    ("enum", re.compile(r"^\s*(?:export\s+)?enum\s+([A-Za-z_$][\w$]*)")),
    ("function", re.compile(r"^\s*(?:export\s+(?:default\s+)?)?(?:async\s+)?function\*?\s+([A-Za-z_$][\w$]*)\s*\(([^)]*)")),
    ("function", re.compile(r"^\s*(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>")),
)


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


def extract_ts_symbols(code: str) -> List[Dict[str, Any]]:
    """Extract top-level classes, interfaces, types, enums, and functions from TS/JS code."""
    symbols: List[Dict[str, Any]] = []
    for lineno, line in enumerate(code.splitlines(), start=1):
        stripped = line.lstrip()
        if not stripped or stripped.startswith(("//", "/*", "*")):
            continue
        for kind, pattern in _TS_DECL_PATTERNS:
            m = pattern.match(line)
            if not m:
                continue
            name = m.group(1)
            if kind == "function" and m.lastindex and m.lastindex >= 2:
                params = " ".join(m.group(2).split())[:40]
                name = f"{name}({params})"
            elif kind == "function":
                name = f"{name}(...)"
            symbols.append({"type": kind, "name": name, "line_range": f"L{lineno}", "methods": []})
            break
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


def _render_file_block(src_file: str, real_file: Path) -> List[str]:
    """Read a source file and format its AST/regex symbol block with jumpable line ranges."""
    is_py = src_file.endswith(".py")
    try:
        content = real_file.read_text(encoding="utf-8", errors="replace")
        symbols = extract_file_symbols(content) if is_py else extract_ts_symbols(content)
    except Exception:
        symbols = []

    block = [f"  📄 {src_file}"]
    for sym in symbols:
        lr = f" [{sym['line_range']}]" if sym.get("line_range") else ""
        stype = sym["type"]
        if stype == "class":
            suffix = ":" if is_py else ""
            block.append(f"    class {sym['name']}{suffix}{lr}")
            details = sym.get("method_details") or [{"sig": m, "line_range": ""} for m in sym["methods"]]
            for md in details:
                mlr = f" [{md['line_range']}]" if md.get("line_range") else ""
                block.append(f"      def {md['sig']}{mlr}")
        elif stype == "function":
            prefix = "def" if is_py else "fn"
            block.append(f"    {prefix} {sym['name']}{lr}")
        elif stype in ("interface", "type", "enum"):
            block.append(f"    {stype} {sym['name']}{lr}")
        elif stype == "error":
            block.append(f"    ⚠️ {sym['name']}")
    return block


def _iter_valid_source_files(resolved_dir: Path, max_depth: int, ignored: Set[str]) -> Iterator[Tuple[str, str, Path]]:
    """Yield (folder_header, filename, resolved_path) for workspace-contained Python/TS/JS files."""
    supported = (".py",) + _JS_TS_EXTS
    for root, dirs, files in os.walk(resolved_dir):
        dirs[:] = _filter_safe_dirs(root, dirs, resolved_dir, ignored)
        rel_root = Path(root).relative_to(resolved_dir)
        if len(rel_root.parts) > max_depth:
            dirs.clear()
            continue

        folder_header = f"\n📁 {rel_root}" if str(rel_root) != "." else "\n📁 根目录 /"
        for src_file in sorted(f for f in files if f.endswith(supported) and not f.endswith(".d.ts")):
            try:
                real_file = Path(root, src_file).resolve()
                if real_file.is_relative_to(resolved_dir) and real_file.is_file():
                    yield folder_header, src_file, real_file
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

    for folder_header, src_file, real_file in _iter_valid_source_files(resolved_dir, max_depth, ignored):
        if rendered_files >= max_files:
            truncated_reason = f"已达文件数上限 {max_files}"
            break

        block = _render_file_block(src_file, real_file)
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
        return "💡 当前项目未检测到有效的源代码文件 (.py / .ts / .js)。"
    if truncated_reason:
        lines.append(f"\n⚠️ [全景图输出已截断 ({truncated_reason}，共展示 {rendered_files} 个文件)；请使用 grep_search 或 list_files 按子目录精准定位]")
    lines.append("\n======================================================\n")
    return "\n".join(lines)
