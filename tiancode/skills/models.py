"""Domain models for dynamic skills and extensions."""

from dataclasses import dataclass
from pathlib import Path


@dataclass
class Skill:
    """Represents a pluggable on-demand domain skill."""

    name: str
    description: str
    file_path: Path
    is_global: bool = False
    raw_content: str = ""

    def get_body(self) -> str:
        """Extract markdown instruction body content below YAML frontmatter."""
        if not self.raw_content:
            try:
                self.raw_content = self.file_path.read_text(encoding="utf-8", errors="replace")
            except Exception:
                return ""

        parts = self.raw_content.split("---", 2)
        if len(parts) >= 3:
            return parts[2].strip()
        return self.raw_content.strip()
