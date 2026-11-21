"""SSRF-guarded read-only HTTP fetch and HTML text extraction tool."""

from __future__ import annotations

import ipaddress
import re
import socket
from dataclasses import dataclass
from html.parser import HTMLParser
from typing import Any, List
from urllib.parse import urljoin, urlparse, urlunparse

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


@dataclass(frozen=True)
class _PublicEndpoint:
    """One public URL and the address resolved for that check."""

    url: str
    host: str
    ip: str


def _resolve_public_endpoint(url: str) -> _PublicEndpoint:
    """Validate a URL and return the public address from that same lookup.

    A later lookup is not used. That second answer is how a name can flip
    from a public address to a loopback address after the check has passed.
    """
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

    pinned_ip = ""
    for _, _, _, _, sockaddr in infos:
        address = str(sockaddr[0]).split("%", 1)[0]
        if _is_disallowed_ip(address):
            raise ValueError(f"安全拦截：目标域名 {host} 解析到内网/私有/回环地址 {address}")
        if not pinned_ip:
            pinned_ip = address
    if not pinned_ip:
        raise ValueError(f"域名解析失败 ({host}): 没有可用地址")
    return _PublicEndpoint(url=cleaned, host=host, ip=pinned_ip)


def validate_public_http_url(url: str) -> str:
    """Validate scheme and ensure hostname does not resolve to private/loopback/metadata IPs."""
    return _resolve_public_endpoint(url).url


def _host_header(url: str) -> str:
    """Host header for the original name, including a non-default port."""
    parsed = urlparse(url)
    host = parsed.hostname or ""
    try:
        host = host.encode("idna").decode("ascii")
    except UnicodeError:
        pass
    if ":" in host:
        host = f"[{host}]"
    default_port = 443 if parsed.scheme.lower() == "https" else 80
    if parsed.port and parsed.port != default_port:
        return f"{host}:{parsed.port}"
    return host


def _request_url_for_ip(url: str, ip: str) -> str:
    """Rewrite only the connection host. Path and query stay on the original URL."""
    parsed = urlparse(url)
    ip_text = ip.split("%", 1)[0]
    host = f"[{ip_text}]" if ":" in ip_text else ip_text
    netloc = host if parsed.port is None else f"{host}:{parsed.port}"
    if parsed.username is not None:
        auth = parsed.username
        if parsed.password is not None:
            auth = f"{auth}:{parsed.password}"
        netloc = f"{auth}@{netloc}"
    return urlunparse((parsed.scheme, netloc, parsed.path or "/", parsed.params, parsed.query, ""))


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
        client_cm = (
            http_client
            if http_client is not None
            else httpx.Client(timeout=timeout_seconds, trust_env=False)
        )
        with client_cm as client:
            for _ in range(4):
                endpoint = _resolve_public_endpoint(cur_url)
                extensions = None
                if urlparse(endpoint.url).scheme.lower() == "https":
                    sni_host = endpoint.host
                    try:
                        sni_host = sni_host.encode("idna").decode("ascii")
                    except UnicodeError:
                        pass
                    extensions = {"sni_hostname": sni_host}
                resp = client.get(
                    _request_url_for_ip(endpoint.url, endpoint.ip),
                    headers={
                        "User-Agent": "TianCode/0.1 (+read-only)",
                        "Host": _host_header(endpoint.url),
                    },
                    follow_redirects=False,
                    timeout=timeout_seconds,
                    extensions=extensions,
                )
                status = getattr(resp, "status_code", 200)
                loc = resp.headers.get("location") or resp.headers.get("Location")
                if status in (301, 302, 303, 307, 308) and loc:
                    cur_url = urljoin(endpoint.url, loc)
                    continue
                if status >= 400:
                    return f"❌ HTTP 请求失败 ({status}): {endpoint.url}"
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
