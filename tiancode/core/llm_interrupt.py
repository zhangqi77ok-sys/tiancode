"""Unblock a model HTTP read so Ctrl+C can leave the graph thread pool.

LangGraph runs the chat request on a worker thread. The main thread then
waits for that worker while shutting the run down. Closing the HTTP client
does not wake a socket already blocked in recv, so one SIGINT starts the
shutdown and the process still sits on the response. Shutting the socket
down lets the worker finish and the existing KeyboardInterrupt handler run.
"""

from __future__ import annotations

import socket
from typing import Any, Iterator


def abort_blocking_llm_reads(llm: Any) -> None:
    """Wake sockets the chat client is currently reading. Other requests stay usable."""
    client = _httpx_client(llm)
    if client is None:
        return
    for sock in _open_sockets(client):
        try:
            sock.shutdown(socket.SHUT_RDWR)
        except OSError:
            continue


def _httpx_client(llm: Any) -> Any:
    root = getattr(llm, "root_client", None)
    wrapped = getattr(root, "_client", None)
    if getattr(wrapped, "_transport", None) is not None:
        return wrapped
    completions = getattr(llm, "client", None)
    openai_client = getattr(completions, "_client", None)
    wrapped = getattr(openai_client, "_client", None)
    if getattr(wrapped, "_transport", None) is not None:
        return wrapped
    return None


def _open_sockets(client: Any) -> Iterator[socket.socket]:
    transport = getattr(client, "_transport", None)
    pool = getattr(transport, "_pool", None)
    if pool is None:
        return
    connections = getattr(pool, "connections", None)
    if callable(connections):
        try:
            connections = connections()
        except Exception:
            return
    if connections is None:
        connections = getattr(pool, "_connections", None)
    if not connections:
        return
    seen: set[int] = set()
    for connection in list(connections):
        yield from _sockets_under(connection, seen)


def _sockets_under(obj: Any, seen: set[int]) -> Iterator[socket.socket]:
    stack = [obj]
    while stack:
        current = stack.pop()
        if current is None or id(current) in seen:
            continue
        seen.add(id(current))
        getter = getattr(current, "get_extra_info", None)
        if getter is not None:
            try:
                sock = getter("socket")
            except Exception:
                sock = None
            if isinstance(sock, socket.socket):
                yield sock
        for name in ("_connection", "_network_stream", "_http1", "_http2"):
            child = getattr(current, name, None)
            if child is not None:
                stack.append(child)
