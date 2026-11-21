"""A symlinked project rule must not be opened or copied into the prompt.

The canary is local fake text. The test does not use a credential or the network.
"""

from __future__ import annotations

from pathlib import Path

from tiancode.core.project_rules import ProjectRulesLoader


def test_regular_rule_loads_and_symlinks_do_not(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    secret = work / ".env"
    secret.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    (work / "AGENTS.md").symlink_to(Path(".env"))
    (work / "TIANCODE.md").write_text("KEEP-RULE\n", encoding="utf-8")
    inside = work / "local-rules.md"
    inside.write_text("KEEP-INSIDE\n", encoding="utf-8")
    nested = work / ".tiancode"
    nested.mkdir()
    (nested / "AGENTS.md").symlink_to(Path("../local-rules.md"))
    loader = ProjectRulesLoader(global_rules_dir=tmp_path / "no-rules")

    text = loader.format_prompt_rules(work)

    assert "KEEP-RULE" in text
    assert "CANARY-OUTSIDE" not in text
    assert "KEEP-INSIDE" not in text
    assert ".env" not in text
    assert secret.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert (work / "AGENTS.md").is_symlink()
    assert (nested / "AGENTS.md").is_symlink()
    assert inside.read_text(encoding="utf-8") == "KEEP-INSIDE\n"
