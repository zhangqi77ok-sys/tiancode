"""SSRF-guarded read-only HTTP fetch and HTML text extraction tool."""

from __future__ import annotations

import ipaddress
import re
import socket
from html.parser import HTMLParser
from typing import Any, List
from urllib.parse import urljoin, urlparse

import httpx
from langchain_core.tools import BaseTool, tool

_BLOCKED_HOSTS = frozenset({"localhost", "localhost.localdomain", "metadata.google.internal"})
_SKIP_TAGS = frozenset({"script", "style", "noscript", "svg"})
_BLOCK_TAGS = frozenset({"p", "div", "section", "article", "li", "tr", "h1", "h2", "h3", "h4", "br", "title"})


class _HtmlTextExtractor(HTMLParser):
    """Strip scripts/styles and convert HTML markup into readable plain text."""

    def __init__(self) -> None:
        super().__init__()
        self._skip_depth = 0
        self._chunks: List[str] = []

    def handle_starttag(self, tag: str, attrs: Any) -> None:
        low = tag.lower()
        if low in _SKIP_TAGS:
            self._skip_depth += 1
        elif low in _BLOCK_TAGS and self._skip_depth == 0:
            self._chunks.append("\n")

    def handle_endtag(self, tag: str) -> None:
        low = tag.lower()
        if low in _SKIP_TAGS and self._skip_depth > 0:
            self._skip_depth -= 1
        elif low in _BLOCK_TAGS and self._skip_depth == 0:
            self._chunks.append("\n")

    def handle_data(self, data: str) -> None:
        if self._skip_depth == 0 and data:
            self._chunks.append(data)

    def get_text(self) -> str:
        raw = "".join(self._chunks)
        lines = [re.sub(r"[ \t]+", " ", ln).strip() for ln in raw.splitlines()]
        return "\n".join(ln for ln in lines if ln)


def _is_disallowed_ip(raw_ip: str) -> bool:
    ip = ipaddress.ip_address(raw_ip.split("%", 1)[0])
    return bool(
        ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_multicast or ip.is_reserved or ip.is_unspecified
    )


def validate_public_http_url(url: str) -> str:
    """Validate scheme and ensure hostname does not resolve to private/loopback/metadata IPs."""
    cleaned = (url or "").strip()
    parsed = urlparse(cleaned)
    if parsed.scheme.lower() not in ("http", "https") or not parsed.hostname:
        raise ValueError(f"安全拦截：仅支持公网 http/https URL: {url}")

    host = parsed.hostname.lower().rstrip(".")
    if host in _BLOCKED_HOSTS or host.endswith((".local", ".internal")):
        raise ValueError(f"安全拦截：禁止访问本地或保留域名: {host}")

    try:
        if _is_disallowed_ip(host):
            raise ValueError(f"安全拦截：禁止访问私有/回环/保留 IP 地址: {host}")
    except ValueError as exc:
        if "安全拦截" in str(exc):
            raise

    port = parsed.port or (443 if parsed.scheme.lower() == "https" else 80)
    try:
        infos = socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
    except OSError as exc:
        raise ValueError(f"域名解析失败 ({host}): {exc}") from exc

    for _, _, _, _, sockaddr in infos:
        if _is_disallowed_ip(str(sockaddr[0])):
            raise ValueError(f"安全拦截：目标域名 {host} 解析到内网/私有/回环地址 {sockaddr[0]}")
    return cleaned


def _extract_body_text(resp: Any) -> str:
    ctype = (resp.headers.get("content-type") or resp.headers.get("Content-Type") or "").lower()
    text = getattr(resp, "text", "") or ""
    if "html" not in ctype:
        return text.strip()
    parser = _HtmlTextExtractor()
    parser.feed(text)
    parser.close()
    return parser.get_text()


def fetch_url_content(
    url: str,
    max_chars: int = 12_000,
    timeout_seconds: int = 15,
    http_client: Any = None,
) -> str:
    """Fetch public web documentation safely with redirect-hop SSRF re-validation."""
    budget = max(200, min(30_000, int(max_chars)))
    cur_url = url
    try:
        client_cm = http_client if http_client is not None else httpx.Client(timeout=timeout_seconds)
        with client_cm as client:
            for _ in range(4):
                safe_url = validate_public_http_url(cur_url)
                resp = client.get(
                    safe_url,
                    headers={"User-Agent": "TianCode/0.1 (+read-only)"},
                    follow_redirects=False,
                    timeout=timeout_seconds,
                )
                status = getattr(resp, "status_code", 200)
                loc = resp.headers.get("location") or resp.headers.get("Location")
                if status in (301, 302, 303, 307, 308) and loc:
                    cur_url = urljoin(safe_url, loc)
                    continue
                if status >= 400:
                    return f"❌ HTTP 请求失败 ({status}): {safe_url}"
                body = _extract_body_text(resp)
                if len(body) > budget:
                    return f"{body[:budget]}\n\n... [网页正文已按 {budget} 字符上限截断] ..."
                return body or "(网页正文为空)"
        return f"❌ 重定向次数过多，已终止抓取: {url}"
    except ValueError as exc:
        return f"❌ {exc}"
    except Exception as exc:
        return f"❌ 网页抓取失败 ({url}): {exc}"


def create_web_tools() -> List[BaseTool]:
    """Factory creating SSRF-protected read-only web fetch tools."""

    @tool
    def web_fetch(url: str, max_chars: int = 12000) -> str:
        """只读抓取公网 http/https 文档网页并提取纯文本内容（自动拦截内网/回环地址）。"""
        return fetch_url_content(url=url, max_chars=max_chars)

    return [web_fetch]
