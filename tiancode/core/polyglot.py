"""Polyglot project tech stack and test runner auto-detection."""

import os
from pathlib import Path
from typing import Optional


def detect_project_test_command(work_dir: Path) -> Optional[str]:
    """Inspect directory markers and return the standard test command for the project.

    Priority:
    1. Java Maven: pom.xml -> mvn test
    2. Java/Kotlin Gradle: build.gradle / build.gradle.kts -> gradlew test
    3. Node / Frontend: package.json -> pnpm/yarn/npm test
    4. Golang: go.mod -> go test ./...
    5. Rust: Cargo.toml -> cargo test
    6. Python: pyproject.toml, pytest.ini, setup.py, tests/ -> pytest
    7. C/C++: CMakeLists.txt, Makefile -> ctest / make test
    """
    if not work_dir.exists():
        return None

    # 1. Java Maven
    if (work_dir / "pom.xml").is_file():
        return "mvn test"

    # 2. Java / Android Gradle
    if (work_dir / "build.gradle").is_file() or (work_dir / "build.gradle.kts").is_file():
        gradlew = work_dir / ("gradlew.bat" if os.name == "nt" else "gradlew")
        if gradlew.exists():
            return f"{gradlew.name} test"
        return "gradle test"

    # 3. Node.js / Frontend
    if (work_dir / "package.json").is_file():
        if (work_dir / "pnpm-lock.yaml").is_file():
            return "pnpm test"
        if (work_dir / "yarn.lock").is_file():
            return "yarn test"
        return "npm test"

    # 4. Golang
    if (work_dir / "go.mod").is_file():
        return "go test ./..."

    # 5. Rust
    if (work_dir / "Cargo.toml").is_file():
        return "cargo test"

    # 6. Python
    if any((work_dir / marker).exists() for marker in ("pytest.ini", "pyproject.toml", "setup.py", "tests")):
        return "pytest"
    if any(work_dir.glob("*.py")):
        return "pytest"

    # 7. C/C++
    if (work_dir / "CMakeLists.txt").is_file():
        return "ctest"
    if (work_dir / "Makefile").is_file():
        return "make test"

    return None
