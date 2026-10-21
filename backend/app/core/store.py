"""SQLite 存储。家庭、成员、设置、插件开关和插件进度写在同一个文件里。

路径来自环境变量 TIANCODE_DB。未设置时用当前工作目录下的
data/warmcompanion.sqlite。删掉这个文件再启动，等于清空全部家庭数据。
"""
from __future__ import annotations

import json
import os
import sqlite3
import threading
from pathlib import Path
from typing import Any, Dict, List, Optional


_SCHEMA = """
CREATE TABLE IF NOT EXISTS families (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    invite_code TEXT NOT NULL UNIQUE,
    settings_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    family_id TEXT NOT NULL,
    nickname TEXT NOT NULL,
    role TEXT NOT NULL,
    birth_date TEXT NOT NULL,
    phone TEXT UNIQUE,
    password_hash TEXT,
    pin_hash TEXT
);
CREATE INDEX IF NOT EXISTS idx_users_family ON users(family_id);
CREATE TABLE IF NOT EXISTS kv (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS plugin_state (
    plugin_id TEXT NOT NULL,
    scope_key TEXT NOT NULL,
    state_json TEXT NOT NULL,
    PRIMARY KEY (plugin_id, scope_key)
);
"""


def database_path() -> str:
    return os.environ.get("TIANCODE_DB", "data/warmcompanion.sqlite")


class Store:
    def __init__(self) -> None:
        self._conn: Optional[sqlite3.Connection] = None
        self._lock = threading.Lock()

    def conn(self) -> sqlite3.Connection:
        if self._conn is None:
            path = database_path()
            Path(path).parent.mkdir(parents=True, exist_ok=True)
            self._conn = sqlite3.connect(path, check_same_thread=False)
            self._conn.row_factory = sqlite3.Row
            self._conn.executescript(_SCHEMA)
            self._conn.commit()
        return self._conn

    def reconnect(self) -> None:
        """关掉连接再打开同一文件，用来证明数据在磁盘上而不只在这次连接里。"""
        with self._lock:
            if self._conn is not None:
                self._conn.close()
                self._conn = None

    def wipe(self) -> None:
        with self._lock:
            conn = self.conn()
            for table in ("users", "families", "kv", "plugin_state"):
                conn.execute(f"DELETE FROM {table}")
            conn.commit()

    def upsert_family(self, family: Dict[str, Any]) -> None:
        settings = family.get("settings") or {}
        with self._lock:
            self.conn().execute(
                """
                INSERT INTO families (id, name, invite_code, settings_json, created_at)
                VALUES (?, ?, ?, ?, ?)
                ON CONFLICT(id) DO UPDATE SET
                    name=excluded.name,
                    invite_code=excluded.invite_code,
                    settings_json=excluded.settings_json,
                    created_at=excluded.created_at
                """,
                (
                    family["id"],
                    family["name"],
                    family["invite_code"],
                    json.dumps(settings, ensure_ascii=False),
                    family.get("created_at") or "",
                ),
            )
            self.conn().commit()

    def get_family(self, family_id: str) -> Optional[Dict[str, Any]]:
        with self._lock:
            row = self.conn().execute("SELECT * FROM families WHERE id = ?", (family_id,)).fetchone()
        return _family_from_row(row) if row else None

    def get_family_by_invite(self, invite_code: str) -> Optional[Dict[str, Any]]:
        with self._lock:
            row = self.conn().execute(
                "SELECT * FROM families WHERE invite_code = ?", (invite_code,)
            ).fetchone()
        return _family_from_row(row) if row else None

    def count_families(self) -> int:
        with self._lock:
            row = self.conn().execute("SELECT COUNT(*) AS n FROM families").fetchone()
        return int(row["n"]) if row else 0

    def upsert_user(self, user: Dict[str, Any]) -> None:
        phone = user.get("phone") or None
        with self._lock:
            self.conn().execute(
                """
                INSERT INTO users (id, family_id, nickname, role, birth_date, phone, password_hash, pin_hash)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(id) DO UPDATE SET
                    family_id=excluded.family_id,
                    nickname=excluded.nickname,
                    role=excluded.role,
                    birth_date=excluded.birth_date,
                    phone=excluded.phone,
                    password_hash=excluded.password_hash,
                    pin_hash=excluded.pin_hash
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
            self.conn().commit()

    def get_user(self, user_id: str) -> Optional[Dict[str, Any]]:
        with self._lock:
            row = self.conn().execute("SELECT * FROM users WHERE id = ?", (user_id,)).fetchone()
        return _user_from_row(row) if row else None

    def get_user_by_phone(self, phone: str) -> Optional[Dict[str, Any]]:
        with self._lock:
            row = self.conn().execute("SELECT * FROM users WHERE phone = ?", (phone,)).fetchone()
        return _user_from_row(row) if row else None

    def list_users(self) -> List[Dict[str, Any]]:
        with self._lock:
            rows = self.conn().execute("SELECT * FROM users").fetchall()
        return [_user_from_row(row) for row in rows]

    def delete_user(self, user_id: str) -> None:
        with self._lock:
            self.conn().execute("DELETE FROM users WHERE id = ?", (user_id,))
            self.conn().commit()

    def kv_get(self, key: str) -> Optional[str]:
        with self._lock:
            row = self.conn().execute("SELECT value FROM kv WHERE key = ?", (key,)).fetchone()
        return None if row is None else str(row["value"])

    def kv_set(self, key: str, value: str) -> None:
        with self._lock:
            self.conn().execute(
                """
                INSERT INTO kv (key, value) VALUES (?, ?)
                ON CONFLICT(key) DO UPDATE SET value=excluded.value
                """,
                (key, value),
            )
            self.conn().commit()

    def get_plugin_state(self, plugin_id: str, scope_key: str) -> Optional[Dict[str, Any]]:
        with self._lock:
            row = self.conn().execute(
                "SELECT state_json FROM plugin_state WHERE plugin_id = ? AND scope_key = ?",
                (plugin_id, scope_key),
            ).fetchone()
        if row is None:
            return None
        data = json.loads(row["state_json"])
        return data if isinstance(data, dict) else None

    def set_plugin_state(self, plugin_id: str, scope_key: str, state: Dict[str, Any]) -> None:
        payload = json.dumps(state, ensure_ascii=False)
        with self._lock:
            self.conn().execute(
                """
                INSERT INTO plugin_state (plugin_id, scope_key, state_json)
                VALUES (?, ?, ?)
                ON CONFLICT(plugin_id, scope_key) DO UPDATE SET state_json=excluded.state_json
                """,
                (plugin_id, scope_key, payload),
            )
            self.conn().commit()


def _family_from_row(row: sqlite3.Row) -> Dict[str, Any]:
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


def _user_from_row(row: sqlite3.Row) -> Dict[str, Any]:
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
