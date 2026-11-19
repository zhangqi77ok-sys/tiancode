"""Synchronous MCP transports (stdio JSON-RPC lines & Streamable HTTP/SSE)."""

from __future__ import annotations

import json
import os
import queue
import shutil
import subprocess
import sys
import threading
from collections import deque
from typing import Any, Deque, Dict, List, Optional
from urllib.parse import urljoin, urlparse

import httpx

from tiancode.core.mcp_auth import (
    McpAuthError,
    OAuthTokenProvider,
    build_auth_headers,
    format_401_hint,
)

# Prefer newest known revision; still accept earlier negotiated versions.
_MCP_PROTOCOL_VERSION = "2025-06-18"
_SUPPORTED_PROTOCOL_VERSIONS = frozenset({"2025-06-18", "2025-03-26", "2024-11-05"})
_DEFAULT_TIMEOUT_SECONDS = 30
_SENSITIVE_ENV_KEYS = frozenset({"TIANCODE_API_KEY", "DEEPSEEK_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"})
_SAFE_BASE_ENV = frozenset({
    "PATH", "SYSTEMROOT", "SYSTEMDRIVE", "COMSPEC", "TEMP", "TMP", "HOME", "USERPROFILE",
    "APPDATA", "LOCALAPPDATA", "HOMEDRIVE", "HOMEPATH", "PROGRAMDATA", "OS",
    "PATHEXT", "WINDIR", "LANG", "NUMBER_OF_PROCESSORS",
})


def _build_scrubbed_env(server_env: Optional[Dict[str, str]] = None) -> Dict[str, str]:
    """Build subprocess env without leaking parent LLM API credentials."""
    clean_env: Dict[str, str] = {}
    for key, value in os.environ.items():
        upper = key.upper()
        if upper in _SENSITIVE_ENV_KEYS or upper.endswith(("_API_KEY", "_SECRET", "_TOKEN")):
            continue
        if upper in _SAFE_BASE_ENV or upper.startswith(("PYTHON", "LC_")):
            clean_env[key] = value
    if server_env:
        clean_env.update({str(k): str(v) for k, v in server_env.items()})
    return clean_env


def _init_params(protocol_version: str = _MCP_PROTOCOL_VERSION) -> Dict[str, Any]:
    return {
        "protocolVersion": protocol_version,
        "capabilities": {},
        "clientInfo": {"name": "tiancode", "version": "0.1.0"},
    }


def _negotiate_protocol_version(result: Any) -> str:
    """Validate initialize result protocolVersion against client support set."""
    if not isinstance(result, dict):
        raise RuntimeError("MCP initialize 响应无效：缺少 result 对象")
    version = str(result.get("protocolVersion") or "").strip()
    if not version:
        raise RuntimeError("MCP initialize 响应缺少 protocolVersion")
    if version not in _SUPPORTED_PROTOCOL_VERSIONS:
        supported = ", ".join(sorted(_SUPPORTED_PROTOCOL_VERSIONS, reverse=True))
        raise RuntimeError(f"MCP 协议版本不兼容: server={version}；客户端支持: {supported}")
    return version


def _unwrap_rpc_result(msg: Any) -> Any:
    if not isinstance(msg, dict):
        raise RuntimeError("MCP 通道已关闭或响应格式无效")
    if msg.get("error"):
        err = msg["error"]
        raise RuntimeError(err.get("message") if isinstance(err, dict) else str(err))
    return msg.get("result")


def _extract_tool_text(result: Any) -> str:
    """Convert MCP tools/call response payload into readable text."""
    if not isinstance(result, dict):
        return str(result or "")
    parts = [
        str(item["text"]) if isinstance(item, dict) and item.get("text") is not None else json.dumps(item, ensure_ascii=False)
        for item in (result.get("content") or [])
    ]
    text = "\n".join(p for p in parts if p).strip() or json.dumps(result, ensure_ascii=False)
    return f"❌ MCP 工具返回错误: {text}" if result.get("isError") and not text.startswith("❌") else text


def _parse_sse_payload(text: str, req_id: Any) -> Dict[str, Any]:
    """Extract the matching JSON-RPC envelope from an SSE event stream."""
    last_obj: Dict[str, Any] = {}
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("data:") or stripped[5:].strip() in ("", "[DONE]"):
            continue
        try:
            obj = json.loads(stripped[5:].strip())
        except json.JSONDecodeError:
            continue
        if isinstance(obj, dict):
            last_obj = obj
            if obj.get("id") == req_id or "result" in obj or "error" in obj:
                return obj
    if not last_obj:
        raise RuntimeError("MCP SSE 响应中未找到有效 JSON-RPC 数据")
    return last_obj


class StdioMcpTransport:
    """Synchronous JSON-RPC 2.0 client over subprocess stdin/stdout lines."""

    def __init__(
        self,
        command: str,
        args: Optional[List[str]] = None,
        env: Optional[Dict[str, str]] = None,
        timeout_seconds: int = _DEFAULT_TIMEOUT_SECONDS,
        cwd: Optional[str] = None,
    ) -> None:
        self.command = command
        self.args = list(args or [])
        self.env = dict(env or {})
        self.timeout_seconds = max(1, int(timeout_seconds))
        self.cwd = cwd
        self._proc: Optional[subprocess.Popen[str]] = None
        self._lock = threading.Lock()
        self._pending: Dict[int, queue.Queue[Any]] = {}
        self._next_id = 0
        self._stderr_tail: Deque[str] = deque(maxlen=15)
        self._stderr_done = threading.Event()
        self.protocol_version: str = _MCP_PROTOCOL_VERSION

    def connect(self) -> None:
        """Start subprocess and execute MCP initialize handshake."""
        cmd_bin = shutil.which(self.command) or self.command
        flags = 0x08000000 if sys.platform == "win32" else 0
        self._stderr_tail.clear()
        self._stderr_done.clear()
        self._proc = subprocess.Popen(
            [cmd_bin, *self.args],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            bufsize=1,
            env=_build_scrubbed_env(self.env),
            cwd=self.cwd,
            creationflags=flags,
            start_new_session=(sys.platform != "win32"),
        )
        threading.Thread(target=self._reader_loop, daemon=True).start()
        threading.Thread(target=self._drain_stderr, daemon=True).start()
        try:
            init_result = self._request("initialize", _init_params())
            self.protocol_version = _negotiate_protocol_version(init_result)
            self._send_line({"jsonrpc": "2.0", "method": "notifications/initialized"})
        except Exception:
            self.close()
            raise

    def list_tools(self) -> List[Dict[str, Any]]:
        return _list_tools_paginated(self._request)

    def call_tool(self, tool_name: str, arguments: Dict[str, Any]) -> str:
        return _extract_tool_text(self._request("tools/call", {"name": tool_name, "arguments": dict(arguments or {})}))

    def _send_line(self, payload: Dict[str, Any]) -> None:
        if not self._proc or not self._proc.stdin:
            raise RuntimeError("MCP stdio 进程未启动")
        self._proc.stdin.write(json.dumps(payload, ensure_ascii=False) + "\n")
        self._proc.stdin.flush()

    def _request(self, method: str, params: Dict[str, Any]) -> Any:
        with self._lock:
            self._next_id += 1
            req_id = self._next_id
            waiter: queue.Queue[Any] = queue.Queue(maxsize=1)
            self._pending[req_id] = waiter
        try:
            self._send_line({"jsonrpc": "2.0", "id": req_id, "method": method, "params": params})
            msg = waiter.get(timeout=self.timeout_seconds)
            if msg is None:
                self._stderr_done.wait(0.25)
                tail = " | ".join(self._stderr_tail)
                raise RuntimeError(f"MCP 通道已关闭: {tail}" if tail else "MCP 通道已关闭或响应格式无效")
            return _unwrap_rpc_result(msg)
        except queue.Empty as exc:
            self.close()
            tail = " | ".join(self._stderr_tail)
            detail = f" ({tail})" if tail else ""
            raise TimeoutError(f"MCP 请求超时 ({self.timeout_seconds}s): {method}{detail}") from exc
        finally:
            with self._lock:
                self._pending.pop(req_id, None)

    def _reader_loop(self) -> None:
        proc = self._proc
        if not proc or not proc.stdout:
            return
        try:
            for line in proc.stdout:
                raw = line.strip()
                if not raw:
                    continue
                try:
                    msg = json.loads(raw)
                except json.JSONDecodeError:
                    continue
                if isinstance(msg, dict) and "id" in msg:
                    with self._lock:
                        waiter = self._pending.get(msg["id"])
                    if waiter is not None:
                        waiter.put_nowait(msg)
        finally:
            self._wake_pending()

    def _drain_stderr(self) -> None:
        proc = self._proc
        try:
            if proc and proc.stderr:
                for line in proc.stderr:
                    text = line.strip()
                    if text:
                        self._stderr_tail.append(text[:240])
        finally:
            self._stderr_done.set()

    def _wake_pending(self) -> None:
        with self._lock:
            waiters = list(self._pending.values())
            self._pending.clear()
        for waiter in waiters:
            try:
                waiter.put_nowait(None)
            except queue.Full:
                pass

    def close(self) -> None:
        """Terminate subprocess cleanly before closing stdout/stderr pipes."""
        proc, self._proc = self._proc, None
        self._wake_pending()
        if not proc:
            return
        try:
            if proc.stdin:
                proc.stdin.close()
        except OSError:
            pass
        if proc.poll() is None:
            try:
                proc.wait(timeout=0.35)
            except subprocess.TimeoutExpired:
                from tiancode.tools.execution import kill_process_tree
                kill_process_tree(proc)
        for stream in (proc.stdout, proc.stderr):
            try:
                if stream:
                    stream.close()
            except OSError:
                pass


def _list_tools_paginated(request_fn) -> List[Dict[str, Any]]:
    """Collect tools/list pages following optional nextCursor."""
    tools: List[Dict[str, Any]] = []
    cursor: Optional[str] = None
    for _ in range(50):
        params: Dict[str, Any] = {}
        if cursor:
            params["cursor"] = cursor
        result = request_fn("tools/list", params) or {}
        tools.extend(list(result.get("tools") or []))
        cursor = result.get("nextCursor") or result.get("next_cursor")
        if not cursor:
            break
    return tools


class HttpMcpTransport:
    """Synchronous Streamable HTTP / SSE MCP transport backed by httpx."""

    def __init__(
        self,
        url: str,
        headers: Optional[Dict[str, str]] = None,
        timeout_seconds: int = _DEFAULT_TIMEOUT_SECONDS,
        http_client: Any = None,
        auth: Optional[Dict[str, Any]] = None,
        auth_provider: Any = None,
    ) -> None:
        self.url = url
        self.headers = dict(headers or {})
        self.timeout_seconds = max(1, int(timeout_seconds))
        self._auth_cfg = dict(auth or {}) if auth else {}
        self._owns_client = http_client is None
        self._client = http_client or httpx.Client(timeout=self.timeout_seconds)
        self._auth_provider = auth_provider
        self._owns_auth_provider = False
        self.session_id: Optional[str] = None
        self.protocol_version: str = _MCP_PROTOCOL_VERSION
        self._initialized = False
        self._next_id = 0
        self._legacy_sse = False
        self._post_endpoint = self.url

    def connect(self) -> None:
        self._ensure_auth_headers(force_refresh=False)
        try:
            init_result = self._request("initialize", _init_params(), allow_legacy_fallback=True)
        except Exception as first_exc:
            # Streamable HTTP may 401 before/during initialize; refresh once then retry.
            if self._maybe_refresh_auth_from_error(first_exc):
                init_result = self._request("initialize", _init_params(), allow_legacy_fallback=True)
            else:
                raise
        self.protocol_version = _negotiate_protocol_version(init_result)
        self._initialized = True
        self._post({"jsonrpc": "2.0", "method": "notifications/initialized"}, expect_response=False)

    def list_tools(self) -> List[Dict[str, Any]]:
        return _list_tools_paginated(self._request)

    def call_tool(self, tool_name: str, arguments: Dict[str, Any]) -> str:
        return _extract_tool_text(self._request("tools/call", {"name": tool_name, "arguments": dict(arguments or {})}))

    def _ensure_auth_headers(self, *, force_refresh: bool) -> None:
        if self._auth_provider is None and self._auth_cfg:
            try:
                kind = str(self._auth_cfg.get("type") or "bearer").strip().lower()
                if kind in ("client_credentials", "oauth_client_credentials", "oauth"):
                    self._auth_provider = OAuthTokenProvider.from_config(
                        self._auth_cfg, resource_url=self.url, http_client=self._client
                    )
                    self._owns_auth_provider = True
                else:
                    self.headers.update(
                        build_auth_headers(self._auth_cfg, resource_url=self.url, http_client=self._client)
                    )
                    return
            except McpAuthError:
                raise
        if self._auth_provider is not None:
            token = self._auth_provider.get_access_token(force_refresh=force_refresh)
            self.headers["Authorization"] = f"Bearer {token}"

    def _maybe_refresh_auth_from_error(self, exc: Exception) -> bool:
        text = str(exc)
        if "401" not in text:
            return False
        if self._auth_provider is None and not self._auth_cfg:
            return False
        try:
            self._ensure_auth_headers(force_refresh=True)
            return True
        except Exception:
            return False

    def _base_headers(self) -> Dict[str, str]:
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            **self.headers,
        }
        if self._initialized:
            headers["MCP-Protocol-Version"] = self.protocol_version
        if self.session_id:
            headers["Mcp-Session-Id"] = self.session_id
        return headers

    def _capture_session(self, resp: Any) -> None:
        sid = resp.headers.get("Mcp-Session-Id") or resp.headers.get("mcp-session-id")
        if sid:
            self.session_id = sid

    def _raise_http_error(self, resp: Any) -> None:
        status = getattr(resp, "status_code", 0)
        body = getattr(resp, "text", "") or ""
        if status == 401:
            www = resp.headers.get("WWW-Authenticate") or resp.headers.get("www-authenticate") or ""
            raise RuntimeError(format_401_hint(status, www, self.url))
        raise RuntimeError(f"MCP HTTP 错误 ({status}): {body[:200]}")

    def _try_legacy_sse_endpoint(self) -> None:
        """Fall back to 2024-11-05 HTTP+SSE: GET opens stream, first event gives POST endpoint."""
        headers = {"Accept": "text/event-stream", **{k: v for k, v in self.headers.items() if k.lower() != "content-type"}}
        resp = self._client.get(self.url, headers=headers, timeout=self.timeout_seconds)
        if getattr(resp, "status_code", 200) >= 400:
            self._raise_http_error(resp)
        text = getattr(resp, "text", "") or ""
        endpoint = ""
        event_name = ""
        for line in text.splitlines():
            stripped = line.strip()
            if stripped.startswith("event:"):
                event_name = stripped[6:].strip()
            elif stripped.startswith("data:") and event_name == "endpoint":
                endpoint = stripped[5:].strip()
                break
            elif stripped.startswith("data:") and not event_name:
                # Some servers omit event name and only send data as endpoint URI.
                candidate = stripped[5:].strip()
                if candidate.startswith("/") or candidate.startswith("http"):
                    endpoint = candidate
                    break
        if not endpoint:
            raise RuntimeError("MCP 旧版 HTTP+SSE 未返回 endpoint 事件")
        if endpoint.startswith("http://") or endpoint.startswith("https://"):
            self._post_endpoint = endpoint
        elif endpoint.startswith("/"):
            parsed = urlparse(self.url)
            self._post_endpoint = f"{parsed.scheme}://{parsed.netloc}{endpoint}"
        else:
            base = self.url if self.url.endswith("/") else self.url.rsplit("/", 1)[0] + "/"
            self._post_endpoint = urljoin(base, endpoint)
        self._legacy_sse = True

    def _post(self, payload: Dict[str, Any], *, expect_response: bool, allow_legacy_fallback: bool = False) -> Any:
        resp = self._client.post(
            self._post_endpoint,
            json=payload,
            headers=self._base_headers(),
            timeout=self.timeout_seconds,
        )
        status = getattr(resp, "status_code", 200)
        # Session expired → clear and re-init once for non-initialize calls.
        if status == 404 and self.session_id and payload.get("method") != "initialize":
            self.session_id = None
            self._initialized = False
            self.connect()
            resp = self._client.post(
                self._post_endpoint,
                json=payload,
                headers=self._base_headers(),
                timeout=self.timeout_seconds,
            )
            status = getattr(resp, "status_code", 200)
        if status in (404, 405) and allow_legacy_fallback and payload.get("method") == "initialize" and not self._legacy_sse:
            self._try_legacy_sse_endpoint()
            resp = self._client.post(
                self._post_endpoint,
                json=payload,
                headers=self._base_headers(),
                timeout=self.timeout_seconds,
            )
            status = getattr(resp, "status_code", 200)
        if status == 401 and self._auth_provider is not None:
            self._ensure_auth_headers(force_refresh=True)
            resp = self._client.post(
                self._post_endpoint,
                json=payload,
                headers=self._base_headers(),
                timeout=self.timeout_seconds,
            )
            status = getattr(resp, "status_code", 200)
        if status >= 400:
            self._raise_http_error(resp)
        self._capture_session(resp)
        text = (getattr(resp, "text", "") or "").strip()
        if not expect_response or not text or status == 202:
            return None
        ctype = (resp.headers.get("Content-Type") or resp.headers.get("content-type") or "").lower()
        return _parse_sse_payload(text, payload.get("id")) if "text/event-stream" in ctype else json.loads(text)

    def _request(
        self,
        method: str,
        params: Dict[str, Any],
        *,
        allow_legacy_fallback: bool = False,
    ) -> Any:
        self._next_id += 1
        return _unwrap_rpc_result(
            self._post(
                {"jsonrpc": "2.0", "id": self._next_id, "method": method, "params": params},
                expect_response=True,
                allow_legacy_fallback=allow_legacy_fallback,
            )
        )

    def close(self) -> None:
        if self.session_id and not self._legacy_sse:
            try:
                headers = self._base_headers()
                self._client.delete(self.url, headers=headers, timeout=min(5, self.timeout_seconds))
            except Exception:
                pass
        self.session_id = None
        self._initialized = False
        if self._owns_auth_provider and self._auth_provider is not None:
            try:
                self._auth_provider.close()
            except Exception:
                pass
        if self._owns_client and hasattr(self._client, "close"):
            self._client.close()
