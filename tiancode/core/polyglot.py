"""Polyglot project tech stack and test runner auto-detection."""

from dataclasses import dataclass
import json
import os
import re
from pathlib import Path
from typing import Optional


@dataclass(frozen=True)
class ProjectProfile:
    """Detected project language stack, package/build manager, and test command."""

    language: str
    package_manager: str
    test_command: Optional[str]


def _owned_marker(path: Path, *, directory: bool = False) -> bool:
    """True when this path is a regular workspace entry, not a link to another file."""
    from tiancode.core.path_safety import is_shared_hardlink

    if path.is_symlink() or is_shared_hardlink(path):
        return False
    return path.is_dir() if directory else path.is_file()


def _read_owned_text(path: Path) -> str | None:
    """Read a regular file without following a link. None when that open fails.

    The check and the read are the same open: a directory entry swapped to a
    symlink between `is_file` and `read_text` is not followed.
    """
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(path, flags)
    except OSError:
        return None
    try:
        with os.fdopen(fd, "r", encoding="utf-8", errors="replace") as handle:
            return handle.read()
    except OSError:
        return None


def _has_regular_python_file(work_dir: Path) -> bool:
    """True when the workspace root has a regular Python file of its own."""
    return any(_owned_marker(path) for path in work_dir.glob("*.py"))


_SCRIPT_RUN = re.compile(
    r"(?:^|&&|\|\||;)\s*(?:npm|pnpm|yarn|bun)\s+run\s+([A-Za-z0-9_.:/-]+)"
)


def _script_bodies(scripts: dict, roots: list[str]) -> list[tuple[str, str]]:
    """The named scripts and the `npm run` chain they reach, stopping on cycles."""
    from tiancode.tools.execution import _expand_literal_assignments, _normalize_command

    ordered: list[tuple[str, str]] = []
    seen: set[str] = set()

    def add(name: str) -> None:
        if name in seen or len(ordered) >= 8:
            return
        body = scripts.get(name)
        if not isinstance(body, str) or not body.strip():
            seen.add(name)
            return
        seen.add(name)
        ordered.append((name, body))
        visible = _normalize_command(_expand_literal_assignments(body))
        for match in _SCRIPT_RUN.finditer(visible):
            add(match.group(1))

    for name in roots:
        add(name)
    return ordered


def package_scripts_block_reason(work_dir: Path, names: list[str]) -> Optional[str]:
    """Why these package.json scripts must not start, or None.

    A symlink or hard link is not opened: the package manager would follow it.
    Each named script and the short `npm run` chain it reaches are checked
    with the same dangerous-command rules as the shell.
    """
    from tiancode.core.path_safety import is_shared_hardlink
    from tiancode.tools.execution import is_dangerous_command

    pkg = work_dir / "package.json"
    if pkg.is_symlink() or is_shared_hardlink(pkg):
        return "package.json 是符号链接或硬链接，已拒绝执行其中的脚本"
    if not pkg.is_file():
        return None
    text = _read_owned_text(pkg)
    if text is None:
        return "package.json 无法作为普通文件读取，已拒绝执行其中的脚本"
    try:
        data = json.loads(text)
    except json.JSONDecodeError:
        return None
    scripts = data.get("scripts") if isinstance(data, dict) else None
    if not isinstance(scripts, dict):
        return None
    for name, body in _script_bodies(scripts, names):
        if is_dangerous_command(body):
            return f"package.json scripts.{name} 含高危命令，已拒绝执行"
    return None


def package_test_block_reason(work_dir: Path) -> Optional[str]:
    """Why `npm test` / `pnpm test` / `yarn test` must not start, or None."""
    return package_scripts_block_reason(work_dir, ["pretest", "test", "posttest"])


def _has_real_node_test_script(pkg_json: Path) -> bool:
    """Return True if package.json defines a non-stub scripts.test entry."""
    from tiancode.core.path_safety import is_shared_hardlink

    if pkg_json.is_symlink() or is_shared_hardlink(pkg_json):
        return False
    text = _read_owned_text(pkg_json)
    if text is None:
        return False
    try:
        data = json.loads(text)
    except json.JSONDecodeError:
        return False
    scripts = data.get("scripts")
    if not isinstance(scripts, dict):
        return False
    test_script = scripts.get("test")
    if not isinstance(test_script, str) or not test_script.strip():
        return False
    return "no test specified" not in test_script.lower()


def _detect_node_profile(work_dir: Path) -> ProjectProfile:
    """Detect JS/TS package manager and test command if test script exists."""
    has_test = _has_real_node_test_script(work_dir / "package.json")
    # `bun test` is Bun's own runner and ignores scripts.test. The script,
    # including pretest and posttest, runs only as `bun run test`.
    if _owned_marker(work_dir / "bun.lockb") or _owned_marker(work_dir / "bun.lock"):
        mgr, cmd = "bun", "bun run test"
    elif _owned_marker(work_dir / "pnpm-lock.yaml"):
        mgr, cmd = "pnpm", "pnpm test"
    elif _owned_marker(work_dir / "yarn.lock"):
        mgr, cmd = "yarn", "yarn test"
    else:
        mgr, cmd = "npm", "npm test"
    return ProjectProfile(
        language="JavaScript/TypeScript",
        package_manager=mgr,
        test_command=cmd if has_test else None,
    )


def detect_project_profile(work_dir: Path) -> ProjectProfile:
    """Inspect workspace markers and return detected ProjectProfile."""
    if not work_dir.exists():
        return ProjectProfile("Unknown", "none", None)

    if _owned_marker(work_dir / "pom.xml"):
        mvnw = work_dir / ("mvnw.cmd" if os.name == "nt" else "mvnw")
        uses_wrapper = _owned_marker(mvnw)
        cmd = f"{mvnw.name} test" if uses_wrapper else "mvn test"
        return ProjectProfile("Java", "mvnw" if uses_wrapper else "maven", cmd)

    if _owned_marker(work_dir / "build.gradle") or _owned_marker(work_dir / "build.gradle.kts"):
        gradlew = work_dir / ("gradlew.bat" if os.name == "nt" else "gradlew")
        uses_wrapper = _owned_marker(gradlew)
        cmd = f"{gradlew.name} test" if uses_wrapper else "gradle test"
        return ProjectProfile("Java/Kotlin", "gradlew" if uses_wrapper else "gradle", cmd)

    package_json = work_dir / "package.json"
    if not package_json.is_symlink() and package_json.is_file():
        return _detect_node_profile(work_dir)

    if _owned_marker(work_dir / "go.mod"):
        return ProjectProfile("Go", "go", "go test ./...")

    if _owned_marker(work_dir / "Cargo.toml"):
        return ProjectProfile("Rust", "cargo", "cargo test")

    py_markers = ("pytest.ini", "pyproject.toml", "setup.py")
    has_python = any(_owned_marker(work_dir / name) for name in py_markers)
    has_python = has_python or _owned_marker(work_dir / "tests", directory=True) or _has_regular_python_file(work_dir)
    if has_python:
        if _owned_marker(work_dir / "uv.lock"):
            mgr = "uv"
        elif _owned_marker(work_dir / "poetry.lock"):
            mgr = "poetry"
        else:
            mgr = "pip/pytest"
        return ProjectProfile("Python", mgr, "pytest")

    if _owned_marker(work_dir / "CMakeLists.txt"):
        return ProjectProfile("C/C++", "cmake", "ctest")
    if _owned_marker(work_dir / "Makefile"):
        return ProjectProfile("C/C++", "make", "make test")

    return ProjectProfile("Unknown", "none", None)


def detect_project_test_command(work_dir: Path) -> Optional[str]:
    """Return the standard automated test command for the detected project stack."""
    return detect_project_profile(work_dir).test_command
