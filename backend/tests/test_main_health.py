"""应用入口：健康检查、原型页面，以及 ROOT_PATH 不改写实际路由。"""
from fastapi.testclient import TestClient

from app.main import create_app


def test_health_ready_prototype_and_api_not_shadowed():
    client = TestClient(create_app())

    health = client.get("/healthz")
    assert health.status_code == 200
    assert health.json() == {"status": "ok"}

    ready = client.get("/readyz")
    assert ready.status_code == 200
    assert ready.json()["status"] == "ready"

    index = client.get("/")
    assert index.status_code == 200
    assert "伴暖" in index.text

    admin = client.get("/admin.html")
    assert admin.status_code == 200
    assert "平台管理" in admin.text

    # 静态挂载在 / 上，不能吃掉 API。
    assert client.get("/api/v1/auth/me").status_code == 401


def test_routes_are_not_mounted_under_prefix():
    client = TestClient(create_app())
    assert client.get("/readyz").status_code == 200
    # nginx 剥掉 /tiancode。应用自己不提供 /tiancode/readyz。
    assert client.get("/tiancode/readyz").status_code == 404


def test_scope_root_path_advertises_openapi_server():
    # uvicorn --root-path 写入 ASGI scope。TestClient 用同一字段模拟。
    client = TestClient(create_app(), root_path="/tiancode")
    assert client.get("/readyz").status_code == 200
    servers = client.get("/openapi.json").json().get("servers", [])
    assert "/tiancode" in [item.get("url") for item in servers]
