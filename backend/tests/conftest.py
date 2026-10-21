"""测试进程使用固定的非公开密钥，并在每条用例前清空内存状态。"""
from __future__ import annotations

import os

import pytest

# 必须在导入 app 之前写好。pytest 先执行本文件，再导入测试模块。
os.environ["JWT_SECRET"] = "test-jwt-secret-for-local-suite-32bytes!"
os.environ["ADMIN_USERNAME"] = "admin"
os.environ["ADMIN_PASSWORD"] = "TestAdminPassword!"


@pytest.fixture(autouse=True)
def _reset_in_memory_state():
    from app.api.v1.auth import (
        _FAMILIES,
        _INVITE_TO_FAMILY,
        _LOGIN_FAILS,
        _PHONE_TO_USER,
        _USERS,
    )
    from app.core.ai_config import ai_config_manager
    from app.plugins.registry import plugin_registry

    _FAMILIES.clear()
    _USERS.clear()
    _PHONE_TO_USER.clear()
    _INVITE_TO_FAMILY.clear()
    _LOGIN_FAILS.clear()
    plugin_registry._global_enabled.clear()
    plugin_registry._invocations.clear()
    ai_config_manager.reset()
    yield
