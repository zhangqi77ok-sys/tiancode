"""PostgreSQL 存储。家庭、成员、设置、插件开关、熔断、登录失败计数和注销名单都在这里。

连接串用 TIANCODE_DATABASE_URL，没有则用 DATABASE_URL，必须是 postgresql://。
不读取 TIANCODE_DB，也不会退回 SQLite。启动时执行 schema.sql（CREATE TABLE IF NOT EXISTS）。
旧的 SQLite 文件不会被导入。

同一条连接会复用。连接已关闭、连接串变了，或请求遇到断线（OperationalError /
InterfaceError）时会丢掉这条连接并重试一次。重试只包住这一次存储调用。
"""
from __future__ import annotations

import json
import os
import threading
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, TypeVar
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

_RETRYABLE = (psycopg.OperationalError, psycopg.InterfaceError)
_T = TypeVar("_T")

# 50 条 × 2000 字，再留一点 JSON 开销。超过就拒绝，避免一行进度撑满内存。
MAX_PLUGIN_STATE_CHARS = 150_000


class DuplicateRecord(Exception):
    """唯一约束冲突。constraint 是 PostgreSQL 的约束名，调用方据此返回 409。"""

    def __init__(self, constraint: str) -> None:
        self.constraint = constraint
        super().__init__(constraint)


class PluginStateError(Exception):
    """插件进度无法写入。路由把它收成 400，不落成 500。"""


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


def _encode_plugin_state(state: Dict[str, Any]) -> str:
    if not isinstance(state, dict):
        raise PluginStateError("插件进度必须是对象")
    payload = json.dumps(state, ensure_ascii=False)
    if len(payload) > MAX_PLUGIN_STATE_CHARS:
        raise PluginStateError("插件进度超出保存上限")
    return payload


def _loads_object(raw: Any) -> Dict[str, Any]:
    """把 JSON 文本收成字典。损坏、空值或非对象都当成空字典，避免一条坏数据打垮请求。"""
    if isinstance(raw, dict):
        return raw
    if not isinstance(raw, str) or not raw.strip():
        return {}
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        return {}
    return data if isinstance(data, dict) else {}


class Store:
    def __init__(self) -> None:
        self._conn: Optional[psycopg.Connection] = None
        self._url: Optional[str] = None
        self._lock = threading.Lock()

    def _discard_unlocked(self) -> None:
        conn = self._conn
        self._conn = None
        self._url = None
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass

    def _connect_unlocked(self) -> psycopg.Connection:
        url = database_url()
        if self._conn is not None and not self._conn.closed and self._url == url:
            return self._conn
        self._discard_unlocked()
        conn = psycopg.connect(
            url,
            row_factory=dict_row,
            connect_timeout=10,
            keepalives=1,
            keepalives_idle=30,
            keepalives_interval=10,
            keepalives_count=3,
        )
        try:
            _apply_schema(conn)
            conn.commit()
        except Exception:
            try:
                conn.rollback()
            except Exception:
                pass
            conn.close()
            raise
        self._conn = conn
        self._url = url
        return conn

    def _run(self, fn: Callable[[psycopg.Connection], _T], *, retry: bool = True) -> _T:
        """在同一条连接、同一事务里执行 fn，成功后提交。

        retry 为真时，断线丢掉连接并重试一次。追加口述这类非幂等写入要传 retry=False，
        避免提交其实已经成功、客户端却又写了一遍。
        """
        last: Optional[BaseException] = None
        attempts = 2 if retry else 1
        for _attempt in range(attempts):
            with self._lock:
                try:
                    conn = self._connect_unlocked()
                except _RETRYABLE as exc:
                    last = exc
                    self._discard_unlocked()
                    if not retry:
                        raise
                    continue
                try:
                    result = fn(conn)
                    conn.commit()
                    return result
                except _RETRYABLE as exc:
                    last = exc
                    try:
                        conn.rollback()
                    except Exception:
                        pass
                    self._discard_unlocked()
                    if not retry:
                        raise
                    continue
                except Exception:
                    try:
                        conn.rollback()
                    except Exception:
                        pass
                    raise
        assert last is not None
        raise last

    def ensure_schema(self) -> None:
        database_url()
        self._run(lambda conn: None)

    def ping(self) -> None:
        self._run(lambda conn: conn.execute("SELECT 1"))

    def reconnect(self) -> None:
        """关掉这条连接。下一笔请求会重新连上同一库，用来证明数据不在连接对象里。"""
        with self._lock:
            self._discard_unlocked()

    def wipe(self) -> None:
        self._run(lambda conn: conn.execute("TRUNCATE users, families, kv, plugin_state, revoked_jti"))

    def upsert_family(self, family: Dict[str, Any]) -> None:
        self._run(lambda conn: _upsert_family(conn, family))

    def insert_family_with_user(self, family: Dict[str, Any], user: Dict[str, Any]) -> None:
        """同一事务插入家庭和守护者。唯一约束冲突抛 DuplicateRecord，不留下孤儿家庭。"""

        def op(conn: psycopg.Connection) -> None:
            _insert_family(conn, family)
            _insert_user(conn, user)

        try:
            self._run(op)
        except psycopg.errors.UniqueViolation as exc:
            diag = getattr(exc, "diag", None)
            constraint = str(getattr(diag, "constraint_name", "") or "")
            raise DuplicateRecord(constraint) from exc

    def get_family(self, family_id: str) -> Optional[Dict[str, Any]]:
        def op(conn: psycopg.Connection) -> Optional[Dict[str, Any]]:
            row = conn.execute("SELECT * FROM families WHERE id = %s", (family_id,)).fetchone()
            return _family_from_row(row) if row else None

        return self._run(op)

    def get_family_by_invite(self, invite_code: str) -> Optional[Dict[str, Any]]:
        def op(conn: psycopg.Connection) -> Optional[Dict[str, Any]]:
            row = conn.execute(
                "SELECT * FROM families WHERE invite_code = %s",
                (invite_code,),
            ).fetchone()
            return _family_from_row(row) if row else None

        return self._run(op)

    def count_families(self) -> int:
        def op(conn: psycopg.Connection) -> int:
            row = conn.execute("SELECT COUNT(*) AS n FROM families").fetchone()
            return int(row["n"]) if row else 0

        return self._run(op)

    def count_users_by_role(self) -> Dict[str, int]:
        """按角色计数。大盘不再把全表用户拉进进程。"""

        def op(conn: psycopg.Connection) -> Dict[str, int]:
            rows = conn.execute("SELECT role, COUNT(*) AS n FROM users GROUP BY role").fetchall()
            return {str(row["role"]): int(row["n"]) for row in rows}

        return self._run(op)

    def upsert_user(self, user: Dict[str, Any]) -> None:
        self._run(lambda conn: _upsert_user(conn, user))

    def get_user(self, user_id: str) -> Optional[Dict[str, Any]]:
        def op(conn: psycopg.Connection) -> Optional[Dict[str, Any]]:
            row = conn.execute("SELECT * FROM users WHERE id = %s", (user_id,)).fetchone()
            return _user_from_row(row) if row else None

        return self._run(op)

    def get_user_by_phone(self, phone: str) -> Optional[Dict[str, Any]]:
        def op(conn: psycopg.Connection) -> Optional[Dict[str, Any]]:
            row = conn.execute("SELECT * FROM users WHERE phone = %s", (phone,)).fetchone()
            return _user_from_row(row) if row else None

        return self._run(op)

    def list_users(self) -> List[Dict[str, Any]]:
        def op(conn: psycopg.Connection) -> List[Dict[str, Any]]:
            rows = conn.execute("SELECT * FROM users").fetchall()
            return [_user_from_row(row) for row in rows]

        return self._run(op)

    def list_users_by_family(self, family_id: str) -> List[Dict[str, Any]]:
        """只读一个家庭的成员。Pin 校验和成员列表都走这里，不再扫全表。"""

        def op(conn: psycopg.Connection) -> List[Dict[str, Any]]:
            rows = conn.execute(
                "SELECT * FROM users WHERE family_id = %s ORDER BY id",
                (family_id,),
            ).fetchall()
            return [_user_from_row(row) for row in rows]

        return self._run(op)

    def delete_user(self, user_id: str) -> None:
        """删除成员，并删掉 scope_key 为「家庭:用户」的插件进度。"""

        def op(conn: psycopg.Connection) -> None:
            row = conn.execute("SELECT family_id FROM users WHERE id = %s", (user_id,)).fetchone()
            conn.execute("DELETE FROM users WHERE id = %s", (user_id,))
            if row is not None:
                conn.execute(
                    "DELETE FROM plugin_state WHERE scope_key = %s",
                    (f"{row['family_id']}:{user_id}",),
                )

        self._run(op)

    def kv_get(self, key: str) -> Optional[str]:
        def op(conn: psycopg.Connection) -> Optional[str]:
            row = conn.execute("SELECT value FROM kv WHERE key = %s", (key,)).fetchone()
            return None if row is None else str(row["value"])

        return self._run(op)

    def kv_get_many(self, keys: List[str]) -> Dict[str, str]:
        """一次取出多个键。空列表不访问数据库。"""
        if not keys:
            return {}

        def op(conn: psycopg.Connection) -> Dict[str, str]:
            rows = conn.execute(
                "SELECT key, value FROM kv WHERE key = ANY(%s)",
                (list(keys),),
            ).fetchall()
            return {str(row["key"]): str(row["value"]) for row in rows}

        return self._run(op)

    def kv_delete(self, key: str) -> None:
        self._run(lambda conn: conn.execute("DELETE FROM kv WHERE key = %s", (key,)))

    def kv_set(self, key: str, value: str) -> None:
        def op(conn: psycopg.Connection) -> None:
            conn.execute(
                """
                INSERT INTO kv (key, value) VALUES (%s, %s)
                ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
                """,
                (key, value),
            )

        self._run(op)

    def kv_add_int(self, key: str, delta: int = 1) -> int:
        """原子加一。原值不是非负整数时从 0 起算，避免读改写丢掉并发次数。"""

        def op(conn: psycopg.Connection) -> int:
            row = conn.execute(
                """
                INSERT INTO kv (key, value) VALUES (%s, %s)
                ON CONFLICT (key) DO UPDATE SET
                    value = (
                        CASE
                            WHEN kv.value ~ '^[0-9]+$' THEN kv.value::bigint
                            ELSE 0
                        END + %s
                    )::text
                RETURNING value
                """,
                (key, str(delta), delta),
            ).fetchone()
            return int(row["value"]) if row else delta

        return self._run(op)

    def read_login_failures(self, bucket: str, *, window_seconds: float, now: float) -> List[float]:
        """读出窗口内的失败时间。只有过期项被裁掉时才回写。"""
        return self._mutate_login_failures(bucket, window_seconds=window_seconds, now=now, append=False)

    def append_login_failure(self, bucket: str, *, window_seconds: float, now: float) -> List[float]:
        """裁掉过期项并追加一次失败，同一次事务完成。"""
        return self._mutate_login_failures(bucket, window_seconds=window_seconds, now=now, append=True)

    def clear_login_failure_bucket(self, bucket: str) -> None:
        self.kv_delete(f"login_fail:{bucket}")

    def _mutate_login_failures(
        self,
        bucket: str,
        *,
        window_seconds: float,
        now: float,
        append: bool,
    ) -> List[float]:
        storage_key = f"login_fail:{bucket}"

        def op(conn: psycopg.Connection) -> List[float]:
            row = conn.execute("SELECT value FROM kv WHERE key = %s", (storage_key,)).fetchone()
            raw = "" if row is None else str(row["value"])
            kept: List[float] = []
            for part in raw.split(","):
                if not part:
                    continue
                try:
                    stamp = float(part)
                except ValueError:
                    continue
                if now - stamp < window_seconds:
                    kept.append(stamp)
            if append:
                kept.append(now)
            new_raw = ",".join(str(stamp) for stamp in kept)
            if not kept:
                if row is not None:
                    conn.execute("DELETE FROM kv WHERE key = %s", (storage_key,))
            elif row is None or str(row["value"]) != new_raw:
                conn.execute(
                    """
                    INSERT INTO kv (key, value) VALUES (%s, %s)
                    ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
                    """,
                    (storage_key, new_raw),
                )
            return kept

        return self._run(op)

    def get_plugin_state(self, plugin_id: str, scope_key: str) -> Optional[Dict[str, Any]]:
        def op(conn: psycopg.Connection) -> Optional[Dict[str, Any]]:
            row = conn.execute(
                "SELECT state_json FROM plugin_state WHERE plugin_id = %s AND scope_key = %s",
                (plugin_id, scope_key),
            ).fetchone()
            if row is None:
                return None
            return _loads_object(row["state_json"])

        return self._run(op)

    def revoke_jti(self, jti: str, exp: int) -> None:
        import time

        now = int(time.time())

        def op(conn: psycopg.Connection) -> None:
            conn.execute("DELETE FROM revoked_jti WHERE exp < %s", (now,))
            conn.execute(
                """
                INSERT INTO revoked_jti (jti, exp) VALUES (%s, %s)
                ON CONFLICT (jti) DO UPDATE SET exp = EXCLUDED.exp
                """,
                (jti, int(exp)),
            )

        self._run(op)

    def is_jti_revoked(self, jti: str) -> bool:
        import time

        now = int(time.time())

        def op(conn: psycopg.Connection) -> bool:
            row = conn.execute("SELECT exp FROM revoked_jti WHERE jti = %s", (jti,)).fetchone()
            if row is None:
                return False
            if int(row["exp"]) < now:
                conn.execute("DELETE FROM revoked_jti WHERE jti = %s", (jti,))
                return False
            return True

        return self._run(op)

    def set_plugin_state(self, plugin_id: str, scope_key: str, state: Dict[str, Any]) -> None:
        payload = _encode_plugin_state(state)

        def op(conn: psycopg.Connection) -> None:
            conn.execute(
                """
                INSERT INTO plugin_state (plugin_id, scope_key, state_json)
                VALUES (%s, %s, %s)
                ON CONFLICT (plugin_id, scope_key) DO UPDATE SET state_json = EXCLUDED.state_json
                """,
                (plugin_id, scope_key, payload),
            )

        self._run(op)

    def update_plugin_state(
        self,
        plugin_id: str,
        scope_key: str,
        mutator: Callable[[Dict[str, Any]], Dict[str, Any]],
    ) -> Dict[str, Any]:
        """锁住这一行再改。没有行时先插入空对象，第二个事务会等到第一个提交后再读。

        不重试。断线时调用方看到 503，由用户再提交一次，避免同一段口述被写两遍。
        mutator 收到当前对象（损坏的 JSON 会先变成空对象），必须返回新的对象，且不能再进存储层。
        """

        def op(conn: psycopg.Connection) -> Dict[str, Any]:
            conn.execute(
                """
                INSERT INTO plugin_state (plugin_id, scope_key, state_json)
                VALUES (%s, %s, %s)
                ON CONFLICT (plugin_id, scope_key) DO NOTHING
                """,
                (plugin_id, scope_key, "{}"),
            )
            row = conn.execute(
                """
                SELECT state_json FROM plugin_state
                WHERE plugin_id = %s AND scope_key = %s
                FOR UPDATE
                """,
                (plugin_id, scope_key),
            ).fetchone()
            current = {} if row is None else _loads_object(row["state_json"])
            new_state = mutator(current)
            payload = _encode_plugin_state(new_state)
            conn.execute(
                """
                UPDATE plugin_state SET state_json = %s
                WHERE plugin_id = %s AND scope_key = %s
                """,
                (payload, plugin_id, scope_key),
            )
            return new_state

        return self._run(op, retry=False)


def _apply_schema(conn: psycopg.Connection) -> None:
    for part in SCHEMA_SQL.split(";"):
        statement = part.strip()
        if statement:
            conn.execute(statement)


def _family_params(family: Dict[str, Any]) -> tuple:
    settings = family.get("settings") or {}
    return (
        family["id"],
        family["name"],
        family["invite_code"],
        json.dumps(settings, ensure_ascii=False),
        family.get("created_at") or "",
    )


def _user_params(user: Dict[str, Any]) -> tuple:
    return (
        user["id"],
        user["family_id"],
        user["nickname"],
        user["role"],
        user["birth_date"],
        user.get("phone") or None,
        user.get("password_hash"),
        user.get("pin_hash"),
    )


def _insert_family(conn: psycopg.Connection, family: Dict[str, Any]) -> None:
    conn.execute(
        """
        INSERT INTO families (id, name, invite_code, settings_json, created_at)
        VALUES (%s, %s, %s, %s, %s)
        """,
        _family_params(family),
    )


def _upsert_family(conn: psycopg.Connection, family: Dict[str, Any]) -> None:
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
        _family_params(family),
    )


def _insert_user(conn: psycopg.Connection, user: Dict[str, Any]) -> None:
    conn.execute(
        """
        INSERT INTO users (id, family_id, nickname, role, birth_date, phone, password_hash, pin_hash)
        VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
        """,
        _user_params(user),
    )


def _upsert_user(conn: psycopg.Connection, user: Dict[str, Any]) -> None:
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
        _user_params(user),
    )


def _family_from_row(row: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "id": row["id"],
        "name": row["name"],
        "invite_code": row["invite_code"],
        "settings": _loads_object(row["settings_json"]),
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
