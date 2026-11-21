"""Polyglot project tech stack and test runner auto-detection."""

from dataclasses import dataclass
import json
import os
from pathlib import Path
from typing import Optional


@dataclass(frozen=True)
class ProjectProfile:
    """Detected project language stack, package/build manager, and test command."""

    language: str
    package_manager: str
    test_command: Optional[str]


def _has_real_node_test_script(pkg_json: Path) -> bool:
    """Return True if package.json defines a non-stub scripts.test entry."""
    from tiancode.core.path_safety import is_shared_hardlink

    if pkg_json.is_symlink() or is_shared_hardlink(pkg_json):
        return False
    try:
        data = json.loads(pkg_json.read_text(encoding="utf-8", errors="replace"))
    except Exception:
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
    if (work_dir / "bun.lockb").is_file() or (work_dir / "bun.lock").is_file():
        mgr, cmd = "bun", "bun test"
    elif (work_dir / "pnpm-lock.yaml").is_file():
        mgr, cmd = "pnpm", "pnpm test"
    elif (work_dir / "yarn.lock").is_file():
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

    if (work_dir / "pom.xml").is_file():
        mvnw = work_dir / ("mvnw.cmd" if os.name == "nt" else "mvnw")
        cmd = f"{mvnw.name} test" if mvnw.is_file() else "mvn test"
        return ProjectProfile("Java", "mvnw" if mvnw.is_file() else "maven", cmd)

    if (work_dir / "build.gradle").is_file() or (work_dir / "build.gradle.kts").is_file():
        gradlew = work_dir / ("gradlew.bat" if os.name == "nt" else "gradlew")
        cmd = f"{gradlew.name} test" if gradlew.exists() else "gradle test"
        return ProjectProfile("Java/Kotlin", "gradlew" if gradlew.exists() else "gradle", cmd)

    package_json = work_dir / "package.json"
    if not package_json.is_symlink() and package_json.is_file():
        return _detect_node_profile(work_dir)

    if (work_dir / "go.mod").is_file():
        return ProjectProfile("Go", "go", "go test ./...")

    if (work_dir / "Cargo.toml").is_file():
        return ProjectProfile("Rust", "cargo", "cargo test")

    py_markers = ("pytest.ini", "pyproject.toml", "setup.py", "tests")
    if any((work_dir / m).exists() for m in py_markers) or any(work_dir.glob("*.py")):
        mgr = "uv" if (work_dir / "uv.lock").is_file() else ("poetry" if (work_dir / "poetry.lock").is_file() else "pip/pytest")
        return ProjectProfile("Python", mgr, "pytest")

    if (work_dir / "CMakeLists.txt").is_file():
        return ProjectProfile("C/C++", "cmake", "ctest")
    if (work_dir / "Makefile").is_file():
        return ProjectProfile("C/C++", "make", "make test")

    return ProjectProfile("Unknown", "none", None)


def detect_project_test_command(work_dir: Path) -> Optional[str]:
    """Return the standard automated test command for the detected project stack."""
    return detect_project_profile(work_dir).test_command
