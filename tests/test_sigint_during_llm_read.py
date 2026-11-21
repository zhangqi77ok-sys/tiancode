"""Real SIGINT during a hung local model stream must exit 130.

The server is 127.0.0.1 only. The API key is the literal sk-test. Nothing
here contacts a network model or reads user configuration.
"""

from __future__ import annotations

import os
import pty
import select
import signal
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from tiancode.core.llm_adapter import ReasonerChatOpenAI
from tiancode.core.llm_interrupt import abort_blocking_llm_reads


class _HangHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self) -> None:
        self._hang()

    def do_POST(self) -> None:
        self._hang()

    def _hang(self) -> None:
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        self.wfile.write(b"data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
        self.wfile.flush()
        self.server.hung.set()  # type: ignore[attr-defined]
        self.server.hung_count = getattr(self.server, "hung_count", 0) + 1  # type: ignore[attr-defined]
        time.sleep(30)

    def log_message(self, format: str, *args) -> None:
        return


class _FastHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self) -> None:
        body = b"ok"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args) -> None:
        return


def _serve(handler: type[BaseHTTPRequestHandler]) -> ThreadingHTTPServer:
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    if handler is _HangHandler:
        server.hung = threading.Event()  # type: ignore[attr-defined]
        server.hung_count = 0  # type: ignore[attr-defined]
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def test_abort_unblocks_socket_and_client_still_requests() -> None:
    hung = _serve(_HangHandler)
    fast = _serve(_FastHandler)
    llm = ReasonerChatOpenAI(
        model="deepseek-chat",
        api_key="sk-test",
        base_url=f"http://127.0.0.1:{hung.server_address[1]}/v1",
        timeout=30,
        max_retries=0,
    )
    http = llm.root_client._client

    def _read() -> None:
        try:
            with http.stream("GET", f"http://127.0.0.1:{hung.server_address[1]}/stall") as response:
                for _chunk in response.iter_bytes():
                    pass
        except BaseException:
            return

    worker = threading.Thread(target=_read)
    worker.start()
    assert hung.hung.wait(5)  # type: ignore[attr-defined]
    abort_blocking_llm_reads(llm)
    worker.join(2)
    assert not worker.is_alive()

    follow = http.get(f"http://127.0.0.1:{fast.server_address[1]}/health")
    assert follow.status_code == 200
    assert follow.text == "ok"
    hung.shutdown()
    fast.shutdown()
    # A reset socket may surface as an exception or as a short read. Either
    # way the worker must already have finished, and the client must still
    # be able to open a later request.


def test_oneshot_sigint_during_hung_sse_exits_130(tmp_path: Path) -> None:
    server = _serve(_HangHandler)
    work = tmp_path / "work"
    rules = tmp_path / "rules"
    work.mkdir()
    rules.mkdir()
    (tmp_path / "config.json").write_text("{}", encoding="utf-8")
    (tmp_path / "mcp.json").write_text('{"mcpServers":{}}', encoding="utf-8")
    env = os.environ.copy()
    env.update({
        "TIANCODE_SESSIONS_PATH": str(tmp_path / "sessions"),
        "TIANCODE_CONFIG_PATH": str(tmp_path / "config.json"),
        "TIANCODE_MEMORY_PATH": str(tmp_path / "memory.json"),
        "TIANCODE_MCP_CONFIG": str(tmp_path / "mcp.json"),
        "TIANCODE_RULES_PATH": str(rules),
        "TIANSHELL_RUNTIME_PATH": str(tmp_path / "no-tianshell-runtime.json"),
        "PYTHONUNBUFFERED": "1",
    })
    port = server.server_address[1]
    proc = subprocess.Popen(
        [
            sys.executable,
            "-m",
            "tiancode",
            "--base-url",
            f"http://127.0.0.1:{port}/v1",
            "--key",
            "sk-test",
            "--dir",
            str(work),
            "--no-heal",
            "--yes",
            "say hi",
        ],
        cwd=Path(__file__).resolve().parents[1],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    try:
        assert server.hung.wait(10)  # type: ignore[attr-defined]
        assert proc.poll() is None
        proc.send_signal(signal.SIGINT)
        try:
            code = proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=2)
            output = proc.stdout.read() if proc.stdout is not None else ""
            pytest.fail(f"SIGINT during hung SSE did not exit within 3s\n{output[-800:]}")
        output = proc.stdout.read() if proc.stdout is not None else ""
        assert code == 130, output[-800:]
        assert "已取消当前生成" in output
        assert not (work / "cancel_out.txt").exists()
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait(timeout=2)
        server.shutdown()


def _read_pty(fd: int, seconds: float) -> bytes:
    data = b""
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        ready, _, _ = select.select([fd], [], [], 0.2)
        if fd not in ready:
            continue
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        data += chunk
    return data


def test_repl_two_sigints_during_hung_sse_return_to_prompt(tmp_path: Path) -> None:
    server = _serve(_HangHandler)
    work = tmp_path / "work"
    rules = tmp_path / "rules"
    work.mkdir()
    rules.mkdir()
    (tmp_path / "config.json").write_text("{}", encoding="utf-8")
    (tmp_path / "mcp.json").write_text('{"mcpServers":{}}', encoding="utf-8")
    env = os.environ.copy()
    env.update({
        "TIANCODE_SESSIONS_PATH": str(tmp_path / "sessions"),
        "TIANCODE_CONFIG_PATH": str(tmp_path / "config.json"),
        "TIANCODE_MEMORY_PATH": str(tmp_path / "memory.json"),
        "TIANCODE_MCP_CONFIG": str(tmp_path / "mcp.json"),
        "TIANCODE_RULES_PATH": str(rules),
        "TIANSHELL_RUNTIME_PATH": str(tmp_path / "no-tianshell-runtime.json"),
        "PYTHONUNBUFFERED": "1",
        "TERM": "xterm-256color",
    })
    port = server.server_address[1]
    master, slave = pty.openpty()
    proc = subprocess.Popen(
        [
            sys.executable,
            "-m",
            "tiancode",
            "--base-url",
            f"http://127.0.0.1:{port}/v1",
            "--key",
            "sk-test",
            "--dir",
            str(work),
            "--no-heal",
            "--yes",
        ],
        cwd=Path(__file__).resolve().parents[1],
        env=env,
        stdin=slave,
        stdout=slave,
        stderr=slave,
    )
    os.close(slave)
    captured = b""
    try:
        captured += _read_pty(master, 1.5)
        for attempt in (1, 2):
            os.write(master, f"say hi {attempt}\n".encode())
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline and server.hung_count < attempt:  # type: ignore[attr-defined]
                time.sleep(0.05)
            assert server.hung_count >= attempt  # type: ignore[attr-defined]
            assert proc.poll() is None
            proc.send_signal(signal.SIGINT)
            captured += _read_pty(master, 2)
            assert captured.decode("utf-8", "replace").count("已取消当前生成") >= attempt
            assert proc.poll() is None
        os.write(master, b"\x04")
        try:
            code = proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=2)
            pytest.fail("REPL did not leave the prompt on EOF")
        assert code == 0
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait(timeout=2)
        os.close(master)
        server.shutdown()
