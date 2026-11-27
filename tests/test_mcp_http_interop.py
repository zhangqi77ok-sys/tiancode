"""HTTP MCP interop: protocol negotiation, auth, pagination, legacy SSE fallback."""

from __future__ import annotations

import json
from typing import Any, Dict, List, Optional

import pytest

from tiancode.core.mcp_auth import expand_env_vars, format_401_hint, parse_www_authenticate
from tiancode.core.mcp_transports import (
    HttpMcpTransport,
    _MCP_PROTOCOL_VERSION,
    _negotiate_protocol_version,
)


class _FakeResponse:
    def __init__(
        self,
        status_code: int = 200,
        payload: Any = None,
        headers: Optional[Dict[str, str]] = None,
        text: Optional[str] = None,
        content_type: str = "application/json",
    ) -> None:
        self.status_code = status_code
        self._payload = payload
        self.headers = {"Content-Type": content_type, **(headers or {})}
        if text is not None:
            self.text = text
        elif payload is None:
            self.text = ""
        else:
            self.text = json.dumps(payload)

    def json(self) -> Any:
        return self._payload


class _FakeClient:
    def __init__(self) -> None:
        self.calls: List[Dict[str, Any]] = []
        self._queue: List[_FakeResponse] = []
        self.closed = False

    def enqueue(self, *responses: _FakeResponse) -> None:
        self._queue.extend(responses)

    def post(self, url: str, json: Any = None, headers: Optional[Dict[str, str]] = None, timeout: Any = None, data: Any = None):
        self.calls.append({"method": "POST", "url": url, "json": json, "headers": dict(headers or {}), "data": data})
        if not self._queue:
            raise AssertionError(f"unexpected POST {url}: {json}")
        return self._queue.pop(0)

    def get(self, url: str, headers: Optional[Dict[str, str]] = None, timeout: Any = None):
        self.calls.append({"method": "GET", "url": url, "headers": dict(headers or {})})
        if not self._queue:
            raise AssertionError(f"unexpected GET {url}")
        return self._queue.pop(0)

    def delete(self, url: str, headers: Optional[Dict[str, str]] = None, timeout: Any = None):
        self.calls.append({"method": "DELETE", "url": url, "headers": dict(headers or {})})
        return _FakeResponse(status_code=200, payload=None, text="")

    def close(self) -> None:
        self.closed = True


def _init_ok(version: str = "2025-06-18", session: str = "sess-1") -> _FakeResponse:
    return _FakeResponse(
        payload={"jsonrpc": "2.0", "id": 1, "result": {"protocolVersion": version, "capabilities": {}, "serverInfo": {"name": "demo"}}},
        headers={"Mcp-Session-Id": session},
    )


def test_expand_env_vars(monkeypatch) -> None:
    monkeypatch.setenv("MCP_TOKEN", "secret-1")
    assert expand_env_vars("Bearer ${MCP_TOKEN}") == "Bearer secret-1"
    assert expand_env_vars("x-$MCP_TOKEN-y") == "x-secret-1-y"


def test_negotiate_protocol_version_accepts_known() -> None:
    assert _negotiate_protocol_version({"protocolVersion": "2024-11-05"}) == "2024-11-05"
    with pytest.raises(RuntimeError, match="不兼容"):
        _negotiate_protocol_version({"protocolVersion": "1999-01-01"})


def test_parse_www_authenticate_and_401_hint() -> None:
    parsed = parse_www_authenticate(
        'Bearer realm="mcp", resource_metadata="https://ex.com/.well-known/oauth-protected-resource", scope="mcp"'
    )
    assert parsed["scheme"] == "Bearer"
    assert "oauth-protected-resource" in parsed["resource_metadata"]
    hint = format_401_hint(401, 'Bearer resource_metadata="https://ex.com/.well-known/oauth-protected-resource"', "https://ex.com/mcp")
    assert "鉴权" in hint
    assert "oauth-protected-resource" in hint


def test_http_connect_sends_protocol_version_after_init() -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(),
        _FakeResponse(status_code=202, text=""),  # initialized notification
        _FakeResponse(payload={"jsonrpc": "2.0", "id": 2, "result": {"tools": [{"name": "ping", "inputSchema": {"type": "object"}}]}}),
    )
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport.connect()
    assert transport.protocol_version == "2025-06-18"
    tools = transport.list_tools()
    assert tools[0]["name"] == "ping"

    # initialize has no MCP-Protocol-Version; subsequent tools/list must include negotiated version
    init_headers = client.calls[0]["headers"]
    assert "MCP-Protocol-Version" not in init_headers
    list_headers = client.calls[2]["headers"]
    assert list_headers.get("MCP-Protocol-Version") == "2025-06-18"
    assert list_headers.get("Mcp-Session-Id") == "sess-1"
    assert _MCP_PROTOCOL_VERSION == "2025-06-18"


def test_http_bearer_auth_header() -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(payload={"jsonrpc": "2.0", "id": 2, "result": {"tools": []}}),
    )
    transport = HttpMcpTransport(
        "https://mcp.example.com/mcp",
        http_client=client,
        auth={"type": "bearer", "token": "tok-abc"},
    )
    transport.connect()
    transport.list_tools()
    assert client.calls[0]["headers"].get("Authorization") == "Bearer tok-abc"


def test_http_client_credentials_oauth() -> None:
    client = _FakeClient()
    client.enqueue(
        _FakeResponse(payload={"access_token": "at-1", "expires_in": 3600, "token_type": "bearer"}),
        _init_ok(session="s2"),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(payload={"jsonrpc": "2.0", "id": 2, "result": {"tools": []}}),
    )
    transport = HttpMcpTransport(
        "https://mcp.example.com/mcp",
        http_client=client,
        auth={
            "type": "client_credentials",
            "tokenUrl": "https://auth.example.com/oauth/token",
            "clientId": "cid",
            "clientSecret": "sec",
            "scopes": ["mcp"],
            "resource": "https://mcp.example.com/mcp",
        },
    )
    transport.connect()
    transport.list_tools()
    token_call = client.calls[0]
    assert token_call["url"] == "https://auth.example.com/oauth/token"
    assert token_call["data"]["grant_type"] == "client_credentials"
    assert token_call["data"]["resource"] == "https://mcp.example.com/mcp"
    assert client.calls[1]["headers"]["Authorization"] == "Bearer at-1"


def test_http_tools_list_pagination() -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(
            payload={
                "jsonrpc": "2.0",
                "id": 2,
                "result": {"tools": [{"name": "a"}], "nextCursor": "p2"},
            }
        ),
        _FakeResponse(
            payload={
                "jsonrpc": "2.0",
                "id": 3,
                "result": {"tools": [{"name": "b"}]},
            }
        ),
    )
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport.connect()
    tools = transport.list_tools()
    assert [t["name"] for t in tools] == ["a", "b"]
    assert client.calls[3]["json"]["params"] == {"cursor": "p2"}


def test_http_legacy_sse_fallback_on_405() -> None:
    client = _FakeClient()
    client.enqueue(
        _FakeResponse(status_code=405, text="method not allowed"),
        _FakeResponse(status_code=200, text="event: endpoint\ndata: /messages\n\n", content_type="text/event-stream"),
        _FakeResponse(
            payload={"jsonrpc": "2.0", "id": 1, "result": {"protocolVersion": "2024-11-05", "capabilities": {}, "serverInfo": {"name": "legacy"}}},
            headers={"Mcp-Session-Id": "legacy-sess"},
        ),
        _FakeResponse(status_code=202, text=""),
    )
    transport = HttpMcpTransport("https://legacy.example.com/sse", http_client=client)
    transport.connect()
    assert transport.protocol_version == "2024-11-05"
    assert transport._post_endpoint == "https://legacy.example.com/messages"
    assert client.calls[0]["method"] == "POST"
    assert client.calls[1]["method"] == "GET"
    assert client.calls[2]["url"] == "https://legacy.example.com/messages"


def test_http_404_reinitializes_the_session_once() -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(session="sess-old"),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(status_code=404, text="session missing"),
        _init_ok(session="sess-new"),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(payload={"jsonrpc": "2.0", "id": 3, "result": {"content": [{"type": "text", "text": "pong"}]}}),
    )
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport.connect()
    assert transport.call_tool("ping", {}) == "pong"
    assert transport.session_id == "sess-new"
    methods = [call["json"].get("method") for call in client.calls if call["method"] == "POST" and call.get("json")]
    assert methods.count("initialize") == 2
    retry = [call for call in client.calls if call.get("json", {}).get("method") == "tools/call"][-1]
    assert retry["headers"].get("Mcp-Session-Id") == "sess-new"


def test_http_503_retries_tools_list_with_retry_after() -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(status_code=503, text="busy", headers={"Retry-After": "0.2"}),
        _FakeResponse(payload={"jsonrpc": "2.0", "id": 2, "result": {"tools": [{"name": "ping"}]}}),
    )
    waits: list[float] = []
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport._sleeper = waits.append
    transport.connect()
    assert transport.list_tools()[0]["name"] == "ping"
    assert waits == [0.2]


def test_initialize_500_is_retried() -> None:
    client = _FakeClient()
    client.enqueue(
        _FakeResponse(status_code=500, text="boot"),
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
    )
    waits: list[float] = []
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport._sleeper = waits.append
    transport.connect()
    assert transport.protocol_version == "2025-06-18"
    assert waits and waits[0] > 0
    methods = [call["json"].get("method") for call in client.calls if call["method"] == "POST" and call.get("json")]
    assert methods.count("initialize") == 2


@pytest.mark.parametrize("status", [408, 500, 502, 504])
def test_tools_call_timeout_statuses_are_not_retried(status: int) -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(status_code=status, text="late"),
    )

    def _fail_if_sleep(_seconds: float) -> None:
        raise AssertionError(f"tools/call {status} must not retry")

    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport._sleeper = _fail_if_sleep
    transport.connect()
    with pytest.raises(RuntimeError, match=str(status)):
        transport.call_tool("ping", {})
    calls = [call["json"].get("method") for call in client.calls if call["method"] == "POST" and call.get("json")]
    assert calls.count("tools/call") == 1


def test_initialize_502_is_retried() -> None:
    client = _FakeClient()
    client.enqueue(
        _FakeResponse(status_code=502, text="gateway", headers={"Retry-After": "0.2"}),
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
    )
    waits: list[float] = []
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport._sleeper = waits.append
    transport.connect()
    assert transport.protocol_version == "2025-06-18"
    assert waits == [0.2]
    methods = [call["json"].get("method") for call in client.calls if call["method"] == "POST" and call.get("json")]
    assert methods.count("initialize") == 2


def test_tools_call_503_is_retried() -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(status_code=503, text="busy", headers={"Retry-After": "0.2"}),
        _FakeResponse(payload={"jsonrpc": "2.0", "id": 3, "result": {"content": [{"type": "text", "text": "pong"}]}}),
    )
    waits: list[float] = []
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport._sleeper = waits.append
    transport.connect()
    assert transport.call_tool("ping", {}) == "pong"
    assert waits == [0.2]
    calls = [call["json"].get("method") for call in client.calls if call["method"] == "POST" and call.get("json")]
    assert calls.count("tools/call") == 2


def test_initialize_408_is_retried() -> None:
    client = _FakeClient()
    client.enqueue(
        _FakeResponse(status_code=408, text="slow"),
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
    )
    waits: list[float] = []
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport._sleeper = waits.append
    transport.connect()
    assert transport.protocol_version == "2025-06-18"
    assert waits and waits[0] > 0
    methods = [call["json"].get("method") for call in client.calls if call["method"] == "POST" and call.get("json")]
    assert methods.count("initialize") == 2


def test_tools_call_500_is_not_retried() -> None:
    client = _FakeClient()
    client.enqueue(
        _init_ok(),
        _FakeResponse(status_code=202, text=""),
        _FakeResponse(status_code=500, text="boom"),
    )
    def _fail_if_sleep(_seconds: float) -> None:
        raise AssertionError("tools/call 500 must not retry")

    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport._sleeper = _fail_if_sleep
    transport.connect()
    with pytest.raises(RuntimeError, match="500"):
        transport.call_tool("ping", {})
    calls = [call["json"].get("method") for call in client.calls if call["method"] == "POST" and call.get("json")]
    assert calls.count("tools/call") == 1


def test_legacy_sse_stops_at_the_endpoint_event() -> None:
    class _Lines:
        def __init__(self) -> None:
            self.status_code = 200
            self.headers = {"Content-Type": "text/event-stream"}
            self.text = "SHOULD-NOT-READ-WHOLE-BODY"
            self.consumed = 0

        def iter_lines(self):
            for line in ("event: endpoint", "data: /messages", "", "STOP"):
                self.consumed += 1
                if line == "STOP":
                    raise AssertionError("read past the endpoint event")
                yield line

        def __enter__(self):
            return self

        def __exit__(self, *_args: object) -> bool:
            return False

    client = _FakeClient()
    lines = _Lines()

    def stream(method: str, url: str, headers: dict | None = None, timeout: object = None) -> _Lines:
        client.calls.append({"method": method, "url": url, "headers": dict(headers or {})})
        return lines

    client.stream = stream  # type: ignore[attr-defined]
    client.enqueue(
        _FakeResponse(status_code=405, text="method not allowed"),
        _FakeResponse(
            payload={"jsonrpc": "2.0", "id": 1, "result": {"protocolVersion": "2024-11-05", "capabilities": {}, "serverInfo": {"name": "legacy"}}},
            headers={"Mcp-Session-Id": "legacy-sess"},
        ),
        _FakeResponse(status_code=202, text=""),
    )
    transport = HttpMcpTransport("https://legacy.example.com/sse", http_client=client)
    transport.connect()
    assert transport.protocol_version == "2024-11-05"
    assert transport._post_endpoint == "https://legacy.example.com/messages"
    assert lines.consumed == 2
    assert client.calls[1]["method"] == "GET"


def test_http_close_sends_session_delete() -> None:
    client = _FakeClient()
    client.enqueue(_init_ok(), _FakeResponse(status_code=202, text=""))
    transport = HttpMcpTransport("https://mcp.example.com/mcp", http_client=client)
    transport.connect()
    transport.close()
    assert any(c["method"] == "DELETE" for c in client.calls)
