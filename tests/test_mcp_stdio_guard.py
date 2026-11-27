"""MCP stdio drops sensitive Python env names and accepts string JSON-RPC ids."""

from __future__ import annotations

import io
import queue
from types import SimpleNamespace

from tiancode.core.mcp_transports import StdioMcpTransport, _build_scrubbed_env, _pending_waiter


def test_stdio_env_drops_python_secret_names(monkeypatch) -> None:
    monkeypatch.setenv("PYTHON_TOKEN", "ISOLATED-CANARY")
    monkeypatch.setenv("PYTHONPATH", "/tmp/py")
    monkeypatch.setenv("PATH", "/usr/bin")

    env = _build_scrubbed_env()

    assert "PYTHON_TOKEN" not in env
    assert "ISOLATED-CANARY" not in env.values()
    assert env.get("PYTHONPATH") == "/tmp/py"
    assert env.get("PATH") == "/usr/bin"


def test_pending_waiter_accepts_a_stringified_id() -> None:
    waiter: queue.Queue[object] = queue.Queue(maxsize=1)
    pending = {2: waiter}

    assert _pending_waiter(pending, "2") is waiter
    assert _pending_waiter(pending, 2) is waiter
    assert _pending_waiter(pending, "nope") is None
    assert _pending_waiter(pending, True) is None


def test_stdio_reader_delivers_a_string_jsonrpc_id() -> None:
    transport = StdioMcpTransport("echo")
    waiter: queue.Queue[object] = queue.Queue(maxsize=1)
    transport._pending[2] = waiter
    transport._proc = SimpleNamespace(
        stdout=io.StringIO('{"jsonrpc":"2.0","id":"2","result":{"ok":true}}\n')
    )

    transport._reader_loop()

    message = waiter.get_nowait()
    assert isinstance(message, dict)
    assert message["result"]["ok"] is True
