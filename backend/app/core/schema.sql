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

CREATE INDEX IF NOT EXISTS idx_users_family ON users (family_id);

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

CREATE TABLE IF NOT EXISTS revoked_jti (
    jti TEXT PRIMARY KEY,
    exp BIGINT NOT NULL
);
