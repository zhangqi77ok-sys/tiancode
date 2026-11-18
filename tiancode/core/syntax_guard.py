"""Pre-write JIT AST and format validation guard for TianCode."""

import ast
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Optional, Union


@dataclass
class SyntaxCheckResult:
    """Outcome of a pre-write syntax validation check."""

    is_valid: bool
    error_message: Optional[str] = None


class SyntaxGuard:
    """Performs millisecond-level static validation on file content prior to disk mutation."""

    def validate(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        """Validate syntax of code/config files. Returns validation result."""
        path = Path(filename)
        ext = path.suffix.lower()

        if ext == ".py":
            return self._validate_python(filename, content)
        if ext == ".json":
            return self._validate_json(filename, content)

        # Non-code or unsupported formats are passed gracefully
        return SyntaxCheckResult(is_valid=True)

    def _validate_python(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        try:
            ast.parse(content, filename=str(filename))
            return SyntaxCheckResult(is_valid=True)
        except (SyntaxError, IndentationError) as e:
            error_type = type(e).__name__
            lineno = getattr(e, "lineno", 1) or 1
            offset = getattr(e, "offset", 1) or 1
            msg = getattr(e, "msg", str(e)) or str(e)

            snippet = self._format_snippet(content, lineno, offset)
            err_msg = (
                f"❌ 语法守卫拦截 (Pre-Write Syntax Guard)：\n"
                f"文件 '{filename}' 存在静态语法错误，已阻断写入以保护工作区！\n"
                f"• 错误类型: {error_type} (第 {lineno} 行, 第 {offset} 列)\n"
                f"• 错误原因: {msg}\n"
                f"• 代码片段诊断:\n{snippet}\n"
                f"👉 请根据上述定位修正语法错误后重新提交！"
            )
            return SyntaxCheckResult(is_valid=False, error_message=err_msg)

    def _validate_json(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        try:
            json.loads(content)
            return SyntaxCheckResult(is_valid=True)
        except json.JSONDecodeError as e:
            lineno = e.lineno
            colno = e.colno
            msg = e.msg

            snippet = self._format_snippet(content, lineno, colno)
            err_msg = (
                f"❌ 语法守卫拦截 (Pre-Write Syntax Guard)：\n"
                f"文件 '{filename}' 存在静态 JSON 格式错误，已阻断写入以保护工作区！\n"
                f"• 错误类型: JSONDecodeError (第 {lineno} 行, 第 {colno} 列)\n"
                f"• 错误原因: {msg}\n"
                f"• 代码片段诊断:\n{snippet}\n"
                f"👉 请根据上述定位修正 JSON 格式后重新提交！"
            )
            return SyntaxCheckResult(is_valid=False, error_message=err_msg)

    def _format_snippet(self, content: str, error_line: int, error_col: int) -> str:
        """Render snippet with line numbers and a pointer column."""
        lines = content.splitlines()
        if not lines:
            return ""

        start_line = max(0, error_line - 2)
        end_line = min(len(lines), error_line + 1)
        snippet_lines = []

        for idx in range(start_line, end_line):
            cur_no = idx + 1
            prefix = f"> {cur_no:4d} | " if cur_no == error_line else f"  {cur_no:4d} | "
            line_str = lines[idx] if idx < len(lines) else ""
            snippet_lines.append(prefix + line_str)
            if cur_no == error_line:
                pointer = " " * len(prefix) + " " * max(0, error_col - 1) + "^"
                snippet_lines.append(pointer)

        return "\n".join(snippet_lines)
