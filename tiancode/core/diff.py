"""Pure utility for unified diff computation and statistics."""

import difflib
from tiancode.domain.models import FileDiff


def credential_file_diff(filename: str) -> FileDiff:
    """Name a credential file in a diff without including its bytes."""
    return FileDiff(
        filename=filename,
        diff_text=f"+++ b/{filename} (疑似密钥或 Git 内部文件，未读取内容)\n",
        additions=0,
        deletions=0,
        is_empty=False,
    )


def compute_text_diff(filename: str, old_text: str, new_text: str) -> FileDiff:
    """Compute unified diff between old_text and new_text with additions/deletions stats."""
    if old_text == new_text:
        return FileDiff(filename=filename, diff_text="", additions=0, deletions=0, is_empty=True)

    old_lines = old_text.splitlines(keepends=False)
    new_lines = new_text.splitlines(keepends=False)

    diff_lines = list(
        difflib.unified_diff(
            old_lines,
            new_lines,
            fromfile=f"a/{filename}",
            tofile=f"b/{filename}",
            lineterm="",
        )
    )

    if not diff_lines:
        return FileDiff(filename=filename, diff_text="", additions=0, deletions=0, is_empty=True)

    additions = sum(1 for line in diff_lines if line.startswith("+") and not line.startswith("+++"))
    deletions = sum(1 for line in diff_lines if line.startswith("-") and not line.startswith("---"))
    diff_text = "\n".join(diff_lines)

    return FileDiff(
        filename=filename,
        diff_text=diff_text,
        additions=additions,
        deletions=deletions,
        is_empty=False,
    )
