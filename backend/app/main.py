"""伴暖 API 入口。

路由挂在应用根上（/healthz、/api/v1、/）。反向代理若使用子路径，
应剥掉前缀再转发。对外 OpenAPI 前缀由 uvicorn `--root-path "$ROOT_PATH"` 注入，
这里不再传 FastAPI(root_path=...)，避免前缀被加两次。
"""
from __future__ import annotations

import os
from pathlib import Path

from fastapi import FastAPI, HTTPException
from fastapi.staticfiles import StaticFiles

from app.api.v1.admin import router as admin_router
from app.api.v1.auth import router as auth_router
from app.api.v1.plugins import router as plugins_router
from app.plugins.loader import load_builtin_plugins
from app.plugins.registry import plugin_registry


def prototype_directory() -> Path | None:
    """定位 prototype/index.html。镜像里用 PROTOTYPE_DIR，源码树则向上查找。"""
    override = os.getenv("PROTOTYPE_DIR", "").strip()
    if override:
        path = Path(override)
        if (path / "index.html").is_file():
            return path
    for parent in Path(__file__).resolve().parents:
        candidate = parent / "prototype"
        if (candidate / "index.html").is_file():
            return candidate
    return None


def create_app() -> FastAPI:
    load_builtin_plugins()
    app = FastAPI(title="伴暖 WarmCompanion", version="0.1.0")

    @app.get("/healthz")
    def healthz() -> dict:
        return {"status": "ok"}

    @app.get("/readyz")
    def readyz() -> dict:
        if not plugin_registry.list_manifests():
            raise HTTPException(status_code=503, detail="builtin plugins not ready")
        return {"status": "ready"}

    app.include_router(auth_router)
    app.include_router(plugins_router)
    app.include_router(admin_router)

    proto = prototype_directory()
    if proto is not None:
        # 必须放在 API 路由之后：未匹配的路径才落到静态原型（/ 与 /admin.html）。
        app.mount("/", StaticFiles(directory=proto, html=True), name="prototype")

    return app


app = create_app()
