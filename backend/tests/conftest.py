"""测试进程使用固定的非公开密钥，并在每条用例前清空 SQLite。"""
from __future__ import annotations

import os
import tempfile
from datetime import datetime

import pytest

from app.core.curfew import SHANGHAI, set_now

# 必须在导入 app 之前写好。pytest 先执行本文件，再导入测试模块。
os.environ["JWT_SECRET"] = "test-jwt-secret-for-local-suite-32bytes!"
os.environ["ADMIN_USERNAME"] = "admin"
os.environ["ADMIN_PASSWORD"] = "TestAdminPassword!"
os.environ["TIANCODE_DB"] = tempfile.mktemp(suffix=".sqlite")


@pytest.fixture(autouse=True)
def _reset_stored_state():
    from app.core.store import store
    from app.plugins.registry import plugin_registry

    store.wipe()
    set_now(datetime(2026, 6, 15, 12, 0, tzinfo=SHANGHAI))
    plugin_registry._global_enabled.clear()
    plugin_registry._invocations.clear()
    yield
    set_now(None)
