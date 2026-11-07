"""schema.sql 只能做可重复的加性变更。启动时拒绝会删数据或改写行的语句。"""
from __future__ import annotations

import hashlib
import re

_FORBIDDEN_HEAD = re.compile(
    r"^\s*(DROP|TRUNCATE|DELETE|UPDATE|GRANT|REVOKE)\b",
    re.IGNORECASE,
)
_ALTER_DROP = re.compile(r"^\s*ALTER\b[\s\S]*\bDROP\b", re.IGNORECASE)


def schema_statements(sql: str) -> list[str]:
    """按分号切开，并丢掉整行注释。注释不能藏住被拒绝的语句。"""
    statements: list[str] = []
    for part in sql.split(";"):
        lines = [line for line in part.splitlines() if not line.strip().startswith("--")]
        statement = "\n".join(lines).strip()
        if statement:
            statements.append(statement)
    return statements


def assert_additive_schema(statements: list[str]) -> None:
    """DROP / TRUNCATE / DELETE / UPDATE / GRANT / REVOKE，以及带 DROP 的 ALTER，都拒绝启动。"""
    for statement in statements:
        if _FORBIDDEN_HEAD.match(statement) or _ALTER_DROP.match(statement):
            first = statement.splitlines()[0][:80]
            raise RuntimeError(
                "schema.sql must stay additive. Refused statement: " + first
            )


def schema_checksum(sql: str) -> str:
    return hashlib.sha256(sql.encode("utf-8")).hexdigest()
