"""Put snapshot bytes back on a workspace directory entry, not through a link.

Undo remembers a path and later writes the original text there. Opening that
path follows a symlink or a hard link, and a saved absolute path can sit
outside the work directory. Both stores restore through this helper so the
write stays on the workspace name.
"""

from __future__ import annotations

import os
import stat
from pathlib import Path


class WorkspacePathError(PermissionError):
    """The snapshot path is not a directory entry inside the work directory."""


def workspace_directory_entry(work_dir: Path, filename: str) -> Path:
    """Return the workspace path for `filename` without following a final symlink."""
    root = work_dir.resolve()
    raw = Path(filename)
    if not str(raw).strip():
        raise WorkspacePathError("回滚路径为空")
    candidate = raw if raw.is_absolute() else root / raw
    name = candidate.name
    if name in ("", ".", ".."):
        raise WorkspacePathError(f"回滚路径越界: {filename}")
    parent = candidate.parent.resolve()
    try:
        parent.relative_to(root)
    except ValueError as exc:
        raise WorkspacePathError(f"回滚路径越界: {filename}") from exc
    return parent / name


def _regular_file_mode(entry: Path) -> int | None:
    """Permission bits of a regular file. A symlink is not followed and not copied."""
    try:
        info = os.lstat(entry)
    except OSError:
        return None
    if not stat.S_ISREG(info.st_mode):
        return None
    return stat.S_IMODE(info.st_mode)


def restore_workspace_text(entry: Path, content: str) -> None:
    """Replace `entry` with a regular file. A symlink or hard link is not followed."""
    parent = entry.parent
    parent.mkdir(parents=True, exist_ok=True)
    kept_mode = _regular_file_mode(entry)
    temporary = parent / f".{entry.name}.tiancode-restore"
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    if temporary.exists() or temporary.is_symlink():
        if temporary.is_symlink():
            raise WorkspacePathError(f"回滚临时文件是符号链接: {temporary.name}")
        temporary.unlink()
    fd = os.open(temporary, flags, 0o644)
    fd_closed = False
    try:
        if kept_mode is not None and hasattr(os, "fchmod"):
            os.fchmod(fd, kept_mode)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            fd_closed = True
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        if kept_mode is not None and not hasattr(os, "fchmod"):
            try:
                os.chmod(temporary, kept_mode)
            except OSError:
                pass
    except Exception:
        if not fd_closed:
            try:
                os.close(fd)
            except OSError:
                pass
        temporary.unlink(missing_ok=True)
        raise
    os.replace(temporary, entry)


def remove_workspace_file(entry: Path) -> bool:
    """Remove a file or symlink at `entry`. Do not remove a directory or follow a link."""
    if entry.is_symlink() or (entry.is_file() and not entry.is_symlink()):
        entry.unlink()
        return True
    return False


def restore_workspace_symlink(entry: Path, link_text: str) -> None:
    """Put a symlink back at `entry`. Do not create, open, or delete its target.

    The link is created under a sibling name and renamed into place. A failed
    undo leaves the current directory entry and does not keep that sibling.
    """
    if not isinstance(link_text, str) or link_text == "" or "\x00" in link_text:
        raise WorkspacePathError(f"符号链接目标无效: {entry.name}")
    if entry.exists() and not entry.is_symlink() and not entry.is_file():
        raise WorkspacePathError(f"不能在目录上还原符号链接: {entry.name}")
    temp = entry.with_name(f".{entry.name}.tiancode-restore-link")
    if temp.is_symlink() or temp.exists():
        raise WorkspacePathError(f"还原符号链接的临时名字已被占用: {temp.name}")
    try:
        os.symlink(link_text, temp)
        os.replace(temp, entry)
    except OSError as exc:
        if temp.is_symlink():
            try:
                temp.unlink()
            except OSError:
                pass
        raise WorkspacePathError(f"还原符号链接失败: {entry.name}") from exc


def restore_snapshot_entry(
    entry: Path,
    *,
    link_target: str | None,
    original_content: str,
    is_created_new: bool,
) -> str:
    """Restore one snapshot onto a workspace name.

    A recorded symlink is recreated from its raw link text. The target is not opened.
    """
    if link_target:
        restore_workspace_symlink(entry, link_target)
        return "已还原符号链接"
    if is_created_new:
        if remove_workspace_file(entry):
            return "已删除新建文件"
        return "新建文件已不存在"
    restore_workspace_text(entry, original_content)
    return "已还原原内容"


_CREDENTIAL_NAMES = frozenset({".env", "id_rsa", "id_ed25519"})
_CREDENTIAL_SUFFIXES = (".pem", ".key", ".p12", ".pfx")


def is_credential_path(path: Path | str) -> bool:
    """True when this path text names a credential or a Git internal file.

    Only the path text is inspected. The file is not opened and links are not followed.
    """
    raw = path.as_posix() if isinstance(path, Path) else str(path).replace("\\", "/")
    parts = [part for part in raw.split("/") if part not in ("", ".")]
    while parts and parts[0] == "..":
        parts.pop(0)
    if not parts:
        return False
    lowered = [part.lower() for part in parts]
    if ".git" in lowered:
        return True
    name = lowered[-1]
    return name in _CREDENTIAL_NAMES or name.startswith(".env.") or name.endswith(_CREDENTIAL_SUFFIXES)


def names_credential_file(filename: str, entry: Path, work_dir: Path) -> bool:
    """True when the requested name or the workspace entry names a credential file."""
    if is_credential_path(filename):
        return True
    try:
        relative = entry.resolve().relative_to(work_dir.resolve())
    except (OSError, ValueError):
        relative = Path(entry.name)
    return is_credential_path(relative)


def is_shared_hardlink(path: Path) -> bool:
    """True when this name is a regular file with another directory entry.

    The other name may sit outside the workspace. Callers must not open the file.
    """
    try:
        info = os.lstat(path)
    except OSError:
        return False
    return stat.S_ISREG(info.st_mode) and info.st_nlink > 1


def read_workspace_text(entry: Path) -> str:
    """Read a regular file. Refuse a symlink or hard link instead of returning the other file."""
    if entry.is_symlink():
        raise WorkspacePathError(f"回滚读取已跳过符号链接: {entry.name}")
    if is_shared_hardlink(entry):
        raise WorkspacePathError(f"回滚读取已跳过硬链接: {entry.name}")
    if not entry.exists():
        return ""
    if not entry.is_file():
        return ""
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(entry, flags)
    except OSError as exc:
        raise WorkspacePathError(f"回滚读取失败: {entry.name}") from exc
    with os.fdopen(fd, "r", encoding="utf-8", errors="replace") as handle:
        return handle.read()
