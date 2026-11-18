"""Synchronous MCP transports (stdio JSON-RPC lines & Streamable HTTP/SSE)."""

from __future__ import annotations

import json
import os
import queue
import subprocess
import threading
from typing import Any, Dict, List, Optional

import httpx

_MCP_PROTOCOL_VERSION = "2024-11-05"
_DEFAULT_TIMEOUT_SECONDS = 30
_SENSITIVE_ENV_KEYS = frozenset({"TIANCODE_API_KEY", "DEEPSEEK_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"})
_SAFE_BASE_ENV = frozenset({"PATH", "SYSTEMROOT", "COMSPEC", "TEMP", "TMP", "HOME", "USERPROFILE", "PATHEXT", "WINDIR", "LANG"})


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


def _init_params() -> Dict[str, Any]:
    return {
        "protocolVersion": _MCP_PROTOCOL_VERSION,
        "capabilities": {},
        "clientInfo": {"name": "tiancode", "version": "0.1.0"},
    }


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

    def connect(self) -> None:
        """Start subprocess and execute MCP initialize handshake."""
        self._proc = subprocess.Popen(
            [self.command, *self.args],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            bufsize=1,
            env=_build_scrubbed_env(self.env),
            cwd=self.cwd,
        )
        threading.Thread(target=self._reader_loop, daemon=True).start()
        threading.Thread(target=self._drain_stderr, daemon=True).start()
        self._request("initialize", _init_params())
        self._send_line({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def list_tools(self) -> List[Dict[str, Any]]:
        return list((self._request("tools/list", {}) or {}).get("tools") or [])

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
            return _unwrap_rpc_result(waiter.get(timeout=self.timeout_seconds))
        except queue.Empty as exc:
            self.close()
            raise TimeoutError(f"MCP 请求超时 ({self.timeout_seconds}s): {method}") from exc
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
        if self._proc and self._proc.stderr:
            for _ in self._proc.stderr:
                pass

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
            proc.terminate()
            try:
                proc.wait(timeout=1.0)
            except subprocess.TimeoutExpired:
                proc.kill()
        for stream in (proc.stdout, proc.stderr):
            try:
                if stream:
                    stream.close()
            except OSError:
                pass


class HttpMcpTransport:
    """Synchronous Streamable HTTP / SSE MCP transport backed by httpx."""

    def __init__(
        self,
        url: str,
        headers: Optional[Dict[str, str]] = None,
        timeout_seconds: int = _DEFAULT_TIMEOUT_SECONDS,
        http_client: Any = None,
    ) -> None:
        self.url = url
        self.headers = dict(headers or {})
        self.timeout_seconds = max(1, int(timeout_seconds))
        self._owns_client = http_client is None
        self._client = http_client or httpx.Client(timeout=self.timeout_seconds)
        self.session_id: Optional[str] = None
        self._next_id = 0

    def connect(self) -> None:
        self._request("initialize", _init_params())
        self._post({"jsonrpc": "2.0", "method": "notifications/initialized"}, expect_response=False)

    def list_tools(self) -> List[Dict[str, Any]]:
        return list((self._request("tools/list", {}) or {}).get("tools") or [])

    def call_tool(self, tool_name: str, arguments: Dict[str, Any]) -> str:
        return _extract_tool_text(self._request("tools/call", {"name": tool_name, "arguments": dict(arguments or {})}))

    def _post(self, payload: Dict[str, Any], *, expect_response: bool) -> Any:
        req_headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream", **self.headers}
        if self.session_id:
            req_headers["Mcp-Session-Id"] = self.session_id
        resp = self._client.post(self.url, json=payload, headers=req_headers, timeout=self.timeout_seconds)
        if getattr(resp, "status_code", 200) >= 400:
            raise RuntimeError(f"MCP HTTP 错误 ({resp.status_code}): {getattr(resp, 'text', '')[:200]}")
        sid = resp.headers.get("Mcp-Session-Id") or resp.headers.get("mcp-session-id")
        if sid:
            self.session_id = sid
        text = (getattr(resp, "text", "") or "").strip()
        if not expect_response or not text:
            return None
        ctype = (resp.headers.get("Content-Type") or resp.headers.get("content-type") or "").lower()
        return _parse_sse_payload(text, payload.get("id")) if "text/event-stream" in ctype else json.loads(text)

    def _request(self, method: str, params: Dict[str, Any]) -> Any:
        self._next_id += 1
        return _unwrap_rpc_result(
            self._post({"jsonrpc": "2.0", "id": self._next_id, "method": method, "params": params}, expect_response=True)
        )

    def close(self) -> None:
        if self._owns_client and hasattr(self._client, "close"):
            self._client.close()
