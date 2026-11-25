"""Pre-write JIT AST and format validation guard for Python, JSON, TOML, and XML."""

import ast
from dataclasses import dataclass
import json
from pathlib import Path
import re
try:
    import tomllib
except ImportError:  # Python 3.10 fallback
    try:
        import tomli as tomllib  # type: ignore[no-redef]
    except ImportError:
        tomllib = None  # type: ignore[assignment]
from typing import Callable, Dict, Optional, Union
import xml.parsers.expat as expat


@dataclass
class SyntaxCheckResult:
    """Outcome of a pre-write syntax validation check."""

    is_valid: bool
    error_message: Optional[str] = None


class _DisallowedXmlDtdError(ValueError):
    """Raised when an XML payload declares DOCTYPE or ENTITY (XXE/DTD bomb protection)."""


class SyntaxGuard:
    """Performs millisecond-level static validation on file content prior to disk mutation."""

    def validate(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        """Validate syntax of code/config files via uniform extension dispatch."""
        ext = Path(filename).suffix.lower()
        validators: Dict[str, Callable[[Union[str, Path], str], SyntaxCheckResult]] = {
            ".py": self._validate_python,
            ".json": self._validate_json,
            ".toml": self._validate_toml,
            ".xml": self._validate_xml,
            ".yaml": self._validate_yaml,
            ".yml": self._validate_yaml,
        }
        validator = validators.get(ext)
        if validator is None:
            return SyntaxCheckResult(is_valid=True)
        return validator(filename, content)

    def _build_error_result(
        self,
        filename: Union[str, Path],
        content: str,
        *,
        error_type: str,
        lineno: int,
        colno: int,
        msg: str,
    ) -> SyntaxCheckResult:
        """Format unified, high-signal static syntax diagnostic blocking disk write."""
        snippet = self._format_snippet(content, max(1, lineno), max(1, colno))
        err_msg = (
            f"❌ 语法守卫拦截 (Pre-Write Syntax Guard)：\n"
            f"文件 '{filename}' 存在静态语法/格式错误，已阻断写入以保护工作区！\n"
            f"• 错误类型: {error_type} (第 {lineno} 行, 第 {colno} 列)\n"
            f"• 错误原因: {msg}\n"
            f"• 代码片段诊断:\n{snippet}\n"
            f"👉 请根据上述定位修正错误后重新提交！"
        )
        return SyntaxCheckResult(is_valid=False, error_message=err_msg)

    def _validate_python(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        try:
            ast.parse(content, filename=str(filename))
            return SyntaxCheckResult(is_valid=True)
        except (SyntaxError, IndentationError) as e:
            return self._build_error_result(
                filename,
                content,
                error_type=type(e).__name__,
                lineno=getattr(e, "lineno", 1) or 1,
                colno=getattr(e, "offset", 1) or 1,
                msg=getattr(e, "msg", str(e)) or str(e),
            )

    def _validate_json(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        try:
            json.loads(content)
            return SyntaxCheckResult(is_valid=True)
        except json.JSONDecodeError as e:
            return self._build_error_result(
                filename,
                content,
                error_type="JSONDecodeError",
                lineno=e.lineno,
                colno=e.colno,
                msg=e.msg,
            )

    def _validate_toml(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        if tomllib is None:
            return SyntaxCheckResult(is_valid=True)
        try:
            tomllib.loads(content)
            return SyntaxCheckResult(is_valid=True)
        except tomllib.TOMLDecodeError as e:
            raw = str(e)
            match = re.search(r"\(at line (\d+), column (\d+)\)", raw)
            lineno = int(match.group(1)) if match else 1
            colno = int(match.group(2)) if match else 1
            return self._build_error_result(
                filename,
                content,
                error_type="TOMLDecodeError",
                lineno=lineno,
                colno=colno,
                msg=raw,
            )

    def _validate_xml(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        parser = expat.ParserCreate()

        def _reject_dtd(*_args: object) -> None:
            raise _DisallowedXmlDtdError("出于 XXE 与 DTD 炸弹安全防护，禁止在 XML 中声明 <!DOCTYPE> 或 <!ENTITY>。")

        parser.StartDoctypeDeclHandler = _reject_dtd
        parser.EntityDeclHandler = _reject_dtd
        try:
            parser.Parse(content.encode("utf-8"), True)
            return SyntaxCheckResult(is_valid=True)
        except _DisallowedXmlDtdError as e:
            return self._build_error_result(
                filename,
                content,
                error_type="XMLSecurityGuard",
                lineno=1,
                colno=1,
                msg=str(e),
            )
        except expat.ExpatError as e:
            return self._build_error_result(
                filename,
                content,
                error_type="XMLParseError",
                lineno=getattr(e, "lineno", 1) or 1,
                colno=(getattr(e, "offset", 0) or 0) + 1,
                msg=str(e),
            )

    def _validate_yaml(self, filename: Union[str, Path], content: str) -> SyntaxCheckResult:
        """Validate YAML indentation (no leading tabs) and parse with PyYAML if installed."""
        for idx, line in enumerate(content.splitlines(), start=1):
            stripped = line.lstrip(" ")
            if stripped.startswith("\t"):
                col = len(line) - len(stripped) + 1
                return self._build_error_result(
                    filename, content, error_type="YAMLTabIndentError",
                    lineno=idx, colno=col, msg="YAML 规范禁止使用 Tab(\\t) 作为缩进，请改为空格。",
                )
        try:
            import yaml  # type: ignore[import-untyped]
            list(yaml.safe_load_all(content))
            return SyntaxCheckResult(is_valid=True)
        except ImportError:
            return SyntaxCheckResult(is_valid=True)
        except Exception as e:
            mark = getattr(e, "problem_mark", None)
            lineno = (getattr(mark, "line", 0) or 0) + 1
            colno = (getattr(mark, "column", 0) or 0) + 1
            return self._build_error_result(
                filename, content, error_type="YAMLParseError",
                lineno=lineno, colno=colno, msg=getattr(e, "problem", None) or str(e),
            )

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
