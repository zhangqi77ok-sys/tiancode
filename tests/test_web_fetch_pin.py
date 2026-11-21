"""web_fetch must connect to the address it validated, not a later lookup.

No real network and no credentials. DNS answers and TCP connects are local fakes.
"""

from __future__ import annotations

import socket
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

from tiancode.tools.web_ops import fetch_url_content


class _LoopbackHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        self.server.hits.append(self.path)  # type: ignore[attr-defined]
        body = b"CANARY-FROM-LOOPBACK"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt: str, *args: object) -> None:
        return


class _RecordingClient:
    def __init__(self) -> None:
        self.urls: list[str] = []
        self.hosts: list[str] = []
        self.snis: list[str] = []

    def __enter__(self) -> "_RecordingClient":
        return self

    def __exit__(self, *exc: object) -> bool:
        return False

    def get(self, url: str, headers: dict | None = None, extensions: dict | None = None, **_: object) -> object:
        self.urls.append(url)
        self.hosts.append((headers or {}).get("Host", ""))
        if extensions and extensions.get("sni_hostname"):
            self.snis.append(str(extensions["sni_hostname"]))

        class _Response:
            status_code = 200
            headers: dict[str, str] = {}
            text = "PUBLIC-BODY"

        return _Response()


def _patch_dns(monkeypatch, answers: dict[str, list[str]]) -> None:
    counts = {host: 0 for host in answers}

    def fake(host: str, port: int, *args: object, **kwargs: object) -> list:
        if host not in answers:
            raise AssertionError(f"unexpected lookup {host}")
        seq = answers[host]
        ip = seq[min(counts[host], len(seq) - 1)]
        counts[host] += 1
        return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, port))]

    monkeypatch.setattr(socket, "getaddrinfo", fake)


def test_fetch_uses_validated_ip_and_original_host(monkeypatch) -> None:
    _patch_dns(monkeypatch, {"docs.example": ["1.2.3.4", "127.0.0.1"]})
    client = _RecordingClient()

    text = fetch_url_content("https://docs.example/guide", http_client=client, timeout_seconds=2)

    assert text == "PUBLIC-BODY"
    assert client.urls == ["https://1.2.3.4/guide"]
    assert client.hosts == ["docs.example"]
    assert client.snis == ["docs.example"]


def test_rebind_does_not_read_loopback_server(monkeypatch) -> None:
    hits: list[str] = []

    class _Server(HTTPServer):
        def __init__(self) -> None:
            super().__init__(("127.0.0.1", 0), _LoopbackHandler)
            self.hits = hits

    server = _Server()
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    destinations: list[tuple] = []

    def refuse(address: tuple, *args: object, **kwargs: object) -> socket.socket:
        destinations.append(address)
        raise OSError("pinned connect stopped in test")

    monkeypatch.setattr(socket, "create_connection", refuse)
    _patch_dns(monkeypatch, {"rebind.test": ["1.2.3.4", "127.0.0.1"]})

    try:
        text = fetch_url_content(f"http://rebind.test:{port}/secret", timeout_seconds=2)
    finally:
        server.shutdown()
        thread.join(timeout=2)

    assert "CANARY-FROM-LOOPBACK" not in text
    assert hits == []
    assert destinations
    assert all(item[0] == "1.2.3.4" for item in destinations)


def test_redirect_to_loopback_is_rejected(monkeypatch) -> None:
    _patch_dns(monkeypatch, {"docs.example": ["1.2.3.4"]})

    class _RedirectClient:
        def __enter__(self) -> "_RedirectClient":
            return self

        def __exit__(self, *exc: object) -> bool:
            return False

        def get(self, url: str, **_: object) -> object:
            if "127.0.0.1" in url:
                class _Leaked:
                    status_code = 200
                    headers: dict[str, str] = {}
                    text = "CANARY-FROM-LOOPBACK"

                return _Leaked()

            class _Redirect:
                status_code = 302
                headers = {"location": "http://127.0.0.1/secret"}
                text = ""

            return _Redirect()

    text = fetch_url_content("http://docs.example/start", http_client=_RedirectClient(), timeout_seconds=2)

    assert "CANARY-FROM-LOOPBACK" not in text
    assert "安全拦截" in text


def test_ambient_proxy_is_not_the_connection(monkeypatch) -> None:
    hits: list[str] = []

    class _Server(HTTPServer):
        def __init__(self) -> None:
            super().__init__(("127.0.0.1", 0), _LoopbackHandler)
            self.hits = hits

    server = _Server()
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    monkeypatch.setenv("HTTP_PROXY", f"http://127.0.0.1:{port}")
    monkeypatch.setenv("HTTPS_PROXY", f"http://127.0.0.1:{port}")
    monkeypatch.setenv("ALL_PROXY", f"http://127.0.0.1:{port}")
    destinations: list[tuple] = []

    def refuse(address: tuple, *args: object, **kwargs: object) -> socket.socket:
        destinations.append(address)
        raise OSError("proxy connect stopped in test")

    monkeypatch.setattr(socket, "create_connection", refuse)
    _patch_dns(monkeypatch, {"docs.example": ["1.2.3.4"]})

    try:
        text = fetch_url_content("http://docs.example/guide", timeout_seconds=2)
    finally:
        server.shutdown()
        thread.join(timeout=2)

    assert "CANARY-FROM-LOOPBACK" not in text
    assert hits == []
    assert destinations
    assert all(item[0] == "1.2.3.4" for item in destinations)
