"""AST-based repository topology map generator for TianCode."""

import ast
import os
from pathlib import Path
from typing import Any, Dict, List, Union


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
    """Parse python code and extract top-level classes and functions."""
    try:
        tree = ast.parse(code)
    except SyntaxError:
        return [{"type": "error", "name": "(语法解析失败)", "methods": []}]

    symbols = []
    for node in tree.body:
        if isinstance(node, ast.ClassDef):
            bases = [ast.unparse(b) for b in node.bases]
            class_name = f"{node.name}({', '.join(bases)})" if bases else node.name
            methods = []
            for item in node.body:
                if isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    methods.append(format_signature(item))
            symbols.append({
                "type": "class",
                "name": class_name,
                "methods": methods,
            })
        elif isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            symbols.append({
                "type": "function",
                "name": format_signature(node),
                "methods": [],
            })
    return symbols


def generate_repo_map(work_dir: Path, max_depth: int = 4) -> str:
    """Scan work_dir and format an indented AST code topology map."""
    resolved_dir = work_dir.resolve()
    ignored_dirs = {
        ".git", ".pytest_cache", ".venv", "venv", "env", "build",
        "dist", "__pycache__", ".idea", ".vscode", "tiancode.egg-info"
    }

    lines = ["🗺️ ====== [TianCode 项目代码全景图 (Repo Map)] ======"]
    has_files = False

    for root, dirs, files in os.walk(resolved_dir):
        # Exclude ignored directories in-place
        dirs[:] = [d for d in dirs if d not in ignored_dirs and not d.startswith(".")]

        rel_root = Path(root).relative_to(resolved_dir)
        if len(rel_root.parts) > max_depth:
            continue

        py_files = sorted([f for f in files if f.endswith(".py")])
        if not py_files:
            continue

        has_files = True
        folder_label = f"📁 {rel_root}" if str(rel_root) != "." else "📁 根目录 /"
        lines.append(f"\n{folder_label}")

        for py_file in py_files:
            file_path = Path(root) / py_file
            try:
                content = open(file_path, "r", encoding="utf-8", errors="replace").read()
                symbols = extract_file_symbols(content)
            except Exception:
                symbols = []

            lines.append(f"  📄 {py_file}")
            for sym in symbols:
                if sym["type"] == "class":
                    lines.append(f"    class {sym['name']}:")
                    for method in sym["methods"]:
                        lines.append(f"      def {method}")
                elif sym["type"] == "function":
                    lines.append(f"    def {sym['name']}")
                elif sym["type"] == "error":
                    lines.append(f"    ⚠️ {sym['name']}")

    lines.append("\n======================================================\n")
    return "\n".join(lines) if has_files else "💡 当前项目未检测到有效的 Python 代码文件。"
