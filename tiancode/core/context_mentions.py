"""Workspace @file fuzzy path matcher and sandboxed code snippet injection."""

from dataclasses import dataclass, field
import os
from pathlib import Path
import re
from typing import List, Optional, Sequence, Tuple, Union

from tiancode.core.search import MAX_SEARCH_FILE_BYTES, _filter_safe_dirs, _is_binary_file

MAX_ATTACHMENT_LINES = 250
MAX_ATTACHMENT_FILE_CHARS = 8_000
MAX_TOTAL_MENTION_CHARS = 16_000
_IGNORED_MENTION_DIRS = frozenset({
    ".git", ".hg", ".svn", ".venv", "venv", "env", "node_modules",
    "__pycache__", ".pytest_cache", "target", "dist", "build", ".idea", ".vscode",
})
_SENSITIVE_NAMES = frozenset({".env", "id_rsa", "id_ed25519"})
_SENSITIVE_SUFFIXES = (".pem", ".key", ".p12", ".pfx")
_MENTION_RE = re.compile(r"(?:^|(?<=[\s(\[\"'，、。：；（]))@([^\s@\"'<>]+)")
_LINE_RANGE_RE = re.compile(r"^(.+?)#L(\d+)(?:-(\d+))?$")


@dataclass(frozen=True)
class FileAttachment:
    """Resolved workspace file excerpt injected into user prompt context."""

    rel_path: str
    start_line: int
    end_line: int
    snippet: str


@dataclass(frozen=True)
class MentionResolution:
    """Result of resolving all @file mentions inside a user prompt."""

    expanded_prompt: str
    attachments: List[FileAttachment] = field(default_factory=list)
    warnings: List[str] = field(default_factory=list)


def list_workspace_files(work_dir: Union[Path, str], limit: int = 800) -> List[str]:
    """List workspace-contained regular non-hidden files as POSIX relative paths."""
    root = Path(work_dir).expanduser().resolve()
    if not root.is_dir():
        return []
    collected: List[str] = []
    for cur_root, dirs, files in os.walk(root):
        dirs[:] = _filter_safe_dirs(cur_root, dirs, root, _IGNORED_MENTION_DIRS)
        for fname in sorted(files):
            full_p = Path(cur_root, fname)
            try:
                real_p = full_p.resolve()
                if not real_p.is_relative_to(root) or not real_p.is_file():
                    continue
                if real_p.stat().st_size > MAX_SEARCH_FILE_BYTES:
                    continue
                collected.append(real_p.relative_to(root).as_posix())
            except (OSError, ValueError):
                continue
            if len(collected) >= limit:
                return collected
    return collected


def _fuzzy_score(query: str, rel_path: str) -> Optional[float]:
    q = query.lower()
    if not q:
        return 1.0
    rel_low = rel_path.lower()
    base = rel_low.rsplit("/", 1)[-1]
    if base.startswith(q):
        return 100.0 - len(rel_low) * 0.01
    if q in base:
        return 80.0 - base.index(q) - len(rel_low) * 0.01
    if q in rel_low:
        return 60.0 - rel_low.index(q) * 0.1 - len(rel_low) * 0.01

    pos, gaps = 0, 0
    for ch in q:
        idx = rel_low.find(ch, pos)
        if idx == -1:
            return None
        gaps += idx - pos
        pos = idx + 1
    return 30.0 - gaps * 0.2 - len(rel_low) * 0.01


def fuzzy_match_files(query: str, rel_paths: Sequence[str], limit: int = 12) -> List[str]:
    """Rank workspace file paths against a partial query using prefix/substring/subsequence scores."""
    scored: List[Tuple[float, str]] = []
    for p in rel_paths:
        sc = _fuzzy_score(query, p)
        if sc is not None:
            scored.append((sc, p))
    scored.sort(key=lambda item: (-item[0], item[1]))
    return [p for _, p in scored[:max(1, limit)]]


def _parse_mention_token(raw_token: str) -> Tuple[str, Optional[int], Optional[int], bool]:
    cleaned = raw_token.rstrip(".,;:!?，、。；：！？)）")
    has_range = False
    m = _LINE_RANGE_RE.match(cleaned)
    if m:
        has_range = True
        path_part = m.group(1)
        start = max(1, int(m.group(2)))
        end = max(start, int(m.group(3))) if m.group(3) else start
        return path_part, start, end, has_range
    return cleaned, None, None, False


def _is_sensitive_or_git_file(rel_posix: str) -> bool:
    parts = rel_posix.lower().split("/")
    if ".git" in parts:
        return True
    name = parts[-1]
    return name in _SENSITIVE_NAMES or name.startswith(".env.") or name.endswith(_SENSITIVE_SUFFIXES)


def _extract_snippet(resolved_file: Path, start_line: Optional[int], end_line: Optional[int]) -> Tuple[int, int, str]:
    s = max(1, start_line or 1)
    target_end = min(end_line, s + MAX_ATTACHMENT_LINES - 1) if end_line is not None else (s + MAX_ATTACHMENT_LINES - 1)
    selected: List[str] = []
    total_seen = 0
    has_more = False

    with open(resolved_file, "r", encoding="utf-8", errors="replace") as f:
        for lineno, raw_line in enumerate(f, start=1):
            total_seen = lineno
            if lineno < s:
                continue
            if lineno > target_end:
                has_more = True
                break
            selected.append(f"  {lineno} | {raw_line.rstrip(chr(13) + chr(10))}")

    if total_seen == 0:
        return 1, 1, "(空文件)"
    if not selected:
        return s, s, f"(文件共 {total_seen} 行，请求起始行 L{s} 超出范围)"

    actual_end = s + len(selected) - 1
    numbered = "\n".join(selected)
    if len(numbered) > MAX_ATTACHMENT_FILE_CHARS:
        numbered = numbered[:MAX_ATTACHMENT_FILE_CHARS] + "\n  ... (单文件超出字符预算已截断)"
    if end_line is None and has_more:
        numbered += f"\n  ... (未全量展示，当前仅附 L{s}-L{actual_end})"
    return s, actual_end, numbered


def resolve_file_mentions(
    text: str,
    work_dir: Union[Path, str],
    *,
    max_total_chars: int = MAX_TOTAL_MENTION_CHARS,
) -> MentionResolution:
    """Safely resolve @file and @file#Lstart-end mentions inside work_dir sandbox."""
    if "@" not in text:
        return MentionResolution(expanded_prompt=text)
    root = Path(work_dir).expanduser().resolve()
    if not root.is_dir():
        return MentionResolution(expanded_prompt=text)

    attachments: List[FileAttachment] = []
    warnings: List[str] = []
    seen_keys: set = set()
    used_chars = 0

    for match in _MENTION_RE.finditer(text):
        path_str, req_start, req_end, has_range = _parse_mention_token(match.group(1))
        if not path_str:
            continue
        looks_like_path = has_range or any(ch in path_str for ch in ("/", "\\", "."))
        try:
            candidate = (root / path_str).resolve()
        except OSError:
            if looks_like_path:
                warnings.append(f"路径解析失败: @{path_str}")
            continue

        if not candidate.is_relative_to(root):
            warnings.append(f"安全拦截越界引用: @{path_str} (禁止访问工作区外部路径)")
            continue
        if not candidate.is_file():
            if looks_like_path:
                warnings.append(f"未找到引用的文件: @{path_str}")
            continue
        from tiancode.core.path_safety import is_shared_hardlink

        if is_shared_hardlink(candidate):
            warnings.append(f"已跳过硬链接，未读取内容: @{path_str}")
            continue
        rel_posix = candidate.relative_to(root).as_posix()
        if _is_sensitive_or_git_file(rel_posix):
            warnings.append(f"安全拦截敏感凭证或 Git 内部文件引用: @{rel_posix}")
            continue
        if _is_binary_file(candidate):
            warnings.append(f"已跳过二进制文件引用: @{path_str}")
            continue

        dedup_key = (rel_posix, req_start, req_end)
        if dedup_key in seen_keys:
            continue
        seen_keys.add(dedup_key)

        s, e, snippet = _extract_snippet(candidate, req_start, req_end)
        if used_chars + len(snippet) > max_total_chars and attachments:
            warnings.append(f"超出总上下文直注预算 ({max_total_chars} 字)，已跳过 @{rel_posix}")
            continue
        used_chars += len(snippet)
        attachments.append(FileAttachment(rel_path=rel_posix, start_line=s, end_line=e, snippet=snippet))

    if not attachments:
        return MentionResolution(expanded_prompt=text, warnings=warnings)

    blocks = [f"### 📎 {a.rel_path} (L{a.start_line}-{a.end_line})\n```\n{a.snippet}\n```" for a in attachments]
    expanded = f"{text}\n\n【随附用户 @file 直注源码上下文】\n" + "\n\n".join(blocks)
    return MentionResolution(expanded_prompt=expanded, attachments=attachments, warnings=warnings)
