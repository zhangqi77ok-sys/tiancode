"""PostgreSQL 存储。家庭、成员、设置、插件开关、熔断、登录失败计数和注销名单都在这里。

连接串用 TIANCODE_DATABASE_URL，没有则用 DATABASE_URL，必须是 postgresql://。
不读取 TIANCODE_DB，也不会退回 SQLite。启动时执行 schema.sql（CREATE TABLE IF NOT EXISTS）。
旧的 SQLite 文件不会被导入。
"""
from __future__ import annotations

import json
import os
import threading
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Dict, Iterator, List, Optional
from urllib.parse import unquote, urlparse

import psycopg
from psycopg.rows import dict_row

SCHEMA_SQL = Path(__file__).with_name("schema.sql").read_text(encoding="utf-8")

# 出现在 deploy/.env.example 里的口令不能拿来启动。
INSECURE_DB_PASSWORDS = frozenset(
    {
        "",
        "change-me",
        "change-me-pg-password",
        "postgres",
        "password",
        "tiancode",
    }
)
MIN_DB_PASSWORD_LENGTH = 12


def database_url() -> str:
    """返回 PostgreSQL 连接串。缺失、SQLite 或占位口令都拒绝。"""
    raw = os.environ.get("TIANCODE_DATABASE_URL", "").strip() or os.environ.get("DATABASE_URL", "").strip()
    if not raw:
        raise RuntimeError(
            "TIANCODE_DATABASE_URL or DATABASE_URL is required and must be a postgresql:// URL. "
            "SQLite is not used. TIANCODE_DB is ignored."
        )
    if raw.lower().startswith("sqlite"):
        raise RuntimeError("SQLite is not a supported database. Use a postgresql:// URL.")
    parsed = urlparse(raw)
    if parsed.scheme.lower() not in {"postgresql", "postgres"}:
        raise RuntimeError("Database URL must use the postgresql scheme.")
    password = unquote(parsed.password or "")
    if password in INSECURE_DB_PASSWORDS or len(password) < MIN_DB_PASSWORD_LENGTH:
        raise RuntimeError(
            "Database password is missing, shorter than 12 characters, or a published placeholder. "
            "Set POSTGRES_PASSWORD and put it in TIANCODE_DATABASE_URL."
        )
    return raw


class Store:
    def __init__(self) -> None:
        self._conn: Optional[psycopg.Connection] = None
        self._lock = threading.Lock()

    def _connect(self) -> psycopg.Connection:
        if self._conn is not None and not self._conn.closed:
            return self._conn
        conn = psycopg.connect(database_url(), row_factory=dict_row)
        try:
            _apply_schema(conn)
            conn.commit()
        except Exception:
            conn.rollback()
            conn.close()
            raise
        self._conn = conn
        return conn

    @contextmanager
    def _session(self) -> Iterator[psycopg.Connection]:
        with self._lock:
            conn = self._connect()
            try:
                yield conn
                conn.commit()
            except Exception:
                try:
                    conn.rollback()
                except Exception:
                    pass
                try:
                    conn.close()
                except Exception:
                    pass
                self._conn = None
                raise

    def ensure_schema(self) -> None:
        database_url()
        with self._lock:
            self._connect()

    def ping(self) -> None:
        with self._session() as conn:
            conn.execute("SELECT 1")

    def reconnect(self) -> None:
        """关掉这条连接。下一笔请求会重新连上同一库，用来证明数据不在连接对象里。"""
        with self._lock:
            if self._conn is not None and not self._conn.closed:
                self._conn.close()
            self._conn = None

    def wipe(self) -> None:
        with self._session() as conn:
            conn.execute("TRUNCATE users, families, kv, plugin_state, revoked_jti")

    def upsert_family(self, family: Dict[str, Any]) -> None:
        settings = family.get("settings") or {}
        with self._session() as conn:
            conn.execute(
                """
                INSERT INTO families (id, name, invite_code, settings_json, created_at)
                VALUES (%s, %s, %s, %s, %s)
                ON CONFLICT (id) DO UPDATE SET
                    name = EXCLUDED.name,
                    invite_code = EXCLUDED.invite_code,
                    settings_json = EXCLUDED.settings_json,
                    created_at = EXCLUDED.created_at
                """,
                (
                    family["id"],
                    family["name"],
                    family["invite_code"],
                    json.dumps(settings, ensure_ascii=False),
                    family.get("created_at") or "",
                ),
            )

    def get_family(self, family_id: str) -> Optional[Dict[str, Any]]:
        with self._session() as conn:
            row = conn.execute("SELECT * FROM families WHERE id = %s", (family_id,)).fetchone()
        return _family_from_row(row) if row else None

    def get_family_by_invite(self, invite_code: str) -> Optional[Dict[str, Any]]:
        with self._session() as conn:
            row = conn.execute(
                "SELECT * FROM families WHERE invite_code = %s",
                (invite_code,),
            ).fetchone()
        return _family_from_row(row) if row else None

    def count_families(self) -> int:
        with self._session() as conn:
            row = conn.execute("SELECT COUNT(*) AS n FROM families").fetchone()
        return int(row["n"]) if row else 0

    def upsert_user(self, user: Dict[str, Any]) -> None:
        phone = user.get("phone") or None
        with self._session() as conn:
            conn.execute(
                """
                INSERT INTO users (id, family_id, nickname, role, birth_date, phone, password_hash, pin_hash)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (id) DO UPDATE SET
                    family_id = EXCLUDED.family_id,
                    nickname = EXCLUDED.nickname,
                    role = EXCLUDED.role,
                    birth_date = EXCLUDED.birth_date,
                    phone = EXCLUDED.phone,
                    password_hash = EXCLUDED.password_hash,
                    pin_hash = EXCLUDED.pin_hash
                """,
                (
                    user["id"],
                    user["family_id"],
                    user["nickname"],
                    user["role"],
                    user["birth_date"],
                    phone,
                    user.get("password_hash"),
                    user.get("pin_hash"),
                ),
            )

    def get_user(self, user_id: str) -> Optional[Dict[str, Any]]:
        with self._session() as conn:
            row = conn.execute("SELECT * FROM users WHERE id = %s", (user_id,)).fetchone()
        return _user_from_row(row) if row else None

    def get_user_by_phone(self, phone: str) -> Optional[Dict[str, Any]]:
        with self._session() as conn:
            row = conn.execute("SELECT * FROM users WHERE phone = %s", (phone,)).fetchone()
        return _user_from_row(row) if row else None

    def list_users(self) -> List[Dict[str, Any]]:
        with self._session() as conn:
            rows = conn.execute("SELECT * FROM users").fetchall()
        return [_user_from_row(row) for row in rows]

    def delete_user(self, user_id: str) -> None:
        with self._session() as conn:
            conn.execute("DELETE FROM users WHERE id = %s", (user_id,))

    def kv_get(self, key: str) -> Optional[str]:
        with self._session() as conn:
            row = conn.execute("SELECT value FROM kv WHERE key = %s", (key,)).fetchone()
        return None if row is None else str(row["value"])

    def kv_delete(self, key: str) -> None:
        with self._session() as conn:
            conn.execute("DELETE FROM kv WHERE key = %s", (key,))

    def kv_set(self, key: str, value: str) -> None:
        with self._session() as conn:
            conn.execute(
                """
                INSERT INTO kv (key, value) VALUES (%s, %s)
                ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
                """,
                (key, value),
            )

    def get_plugin_state(self, plugin_id: str, scope_key: str) -> Optional[Dict[str, Any]]:
        with self._session() as conn:
            row = conn.execute(
                "SELECT state_json FROM plugin_state WHERE plugin_id = %s AND scope_key = %s",
                (plugin_id, scope_key),
            ).fetchone()
        if row is None:
            return None
        data = json.loads(row["state_json"])
        return data if isinstance(data, dict) else None

    def revoke_jti(self, jti: str, exp: int) -> None:
        import time

        now = int(time.time())
        with self._session() as conn:
            conn.execute("DELETE FROM revoked_jti WHERE exp < %s", (now,))
            conn.execute(
                """
                INSERT INTO revoked_jti (jti, exp) VALUES (%s, %s)
                ON CONFLICT (jti) DO UPDATE SET exp = EXCLUDED.exp
                """,
                (jti, int(exp)),
            )

    def is_jti_revoked(self, jti: str) -> bool:
        import time

        now = int(time.time())
        with self._session() as conn:
            row = conn.execute("SELECT exp FROM revoked_jti WHERE jti = %s", (jti,)).fetchone()
            if row is None:
                return False
            if int(row["exp"]) < now:
                conn.execute("DELETE FROM revoked_jti WHERE jti = %s", (jti,))
                return False
            return True

    def set_plugin_state(self, plugin_id: str, scope_key: str, state: Dict[str, Any]) -> None:
        payload = json.dumps(state, ensure_ascii=False)
        with self._session() as conn:
            conn.execute(
                """
                INSERT INTO plugin_state (plugin_id, scope_key, state_json)
                VALUES (%s, %s, %s)
                ON CONFLICT (plugin_id, scope_key) DO UPDATE SET state_json = EXCLUDED.state_json
                """,
                (plugin_id, scope_key, payload),
            )


def _apply_schema(conn: psycopg.Connection) -> None:
    for part in SCHEMA_SQL.split(";"):
        statement = part.strip()
        if statement:
            conn.execute(statement)


def _family_from_row(row: Dict[str, Any]) -> Dict[str, Any]:
    settings = json.loads(row["settings_json"])
    if not isinstance(settings, dict):
        settings = {}
    return {
        "id": row["id"],
        "name": row["name"],
        "invite_code": row["invite_code"],
        "settings": settings,
        "created_at": row["created_at"],
    }


def _user_from_row(row: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "id": row["id"],
        "family_id": row["family_id"],
        "nickname": row["nickname"],
        "role": row["role"],
        "birth_date": row["birth_date"],
        "phone": row["phone"],
        "password_hash": row["password_hash"],
        "pin_hash": row["pin_hash"],
        "pin_code": None,
    }


store = Store()
