"""MCP HTTP auth helpers: env expansion, bearer, client_credentials, 401 discovery."""

from __future__ import annotations

import os
import re
import time
from dataclasses import dataclass
from typing import Any, Dict, Optional
from urllib.parse import urljoin, urlparse

import httpx

__all__ = [
    "McpAuthError",
    "expand_env_vars",
    "expand_mapping",
    "parse_www_authenticate",
    "build_auth_headers",
    "OAuthTokenProvider",
]

_ENV_PATTERN = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)")


class McpAuthError(RuntimeError):
    """Raised when MCP HTTP authentication cannot proceed."""


def expand_env_vars(raw: str, environ: Optional[Dict[str, str]] = None) -> str:
    """Expand `${VAR}` / `$VAR` placeholders using environ (default os.environ)."""
    env = environ if environ is not None else os.environ

    def _repl(match: re.Match[str]) -> str:
        key = match.group(1) or match.group(2)
        return env.get(key, match.group(0))

    return _ENV_PATTERN.sub(_repl, raw)


def expand_mapping(raw: Optional[Dict[str, str]], environ: Optional[Dict[str, str]] = None) -> Dict[str, str]:
    return {str(k): expand_env_vars(str(v), environ) for k, v in (raw or {}).items()}


def parse_www_authenticate(header_value: str) -> Dict[str, str]:
    """Parse a WWW-Authenticate header into scheme + parameters (best-effort)."""
    text = (header_value or "").strip()
    if not text:
        return {}
    parts = text.split(None, 1)
    result: Dict[str, str] = {"scheme": parts[0]}
    if len(parts) == 1:
        return result
    for chunk in re.findall(r'([^=\s,]+)=("(?:\\.|[^"])*"|[^,\s]+)', parts[1]):
        key, value = chunk
        if value.startswith('"') and value.endswith('"'):
            value = value[1:-1].replace('\\"', '"')
        result[key.lower()] = value
    return result


def build_auth_headers(
    auth_cfg: Optional[Dict[str, Any]],
    *,
    resource_url: str = "",
    http_client: Any = None,
    environ: Optional[Dict[str, str]] = None,
) -> Dict[str, str]:
    """Resolve configured auth into HTTP headers (Authorization Bearer …)."""
    if not auth_cfg:
        return {}
    kind = str(auth_cfg.get("type") or auth_cfg.get("kind") or "bearer").strip().lower()
    if kind in ("bearer", "token"):
        token = expand_env_vars(str(auth_cfg.get("token") or auth_cfg.get("accessToken") or ""), environ).strip()
        if not token:
            raise McpAuthError("MCP auth.type=bearer 缺少 token")
        return {"Authorization": f"Bearer {token}"}
    if kind in ("client_credentials", "oauth_client_credentials", "oauth"):
        provider = OAuthTokenProvider.from_config(
            auth_cfg, resource_url=resource_url, http_client=http_client, environ=environ
        )
        return {"Authorization": f"Bearer {provider.get_access_token()}"}
    raise McpAuthError(f"不支持的 MCP auth.type: {kind}（当前支持 bearer / client_credentials）")


@dataclass
class _TokenState:
    access_token: str = ""
    refresh_token: str = ""
    expires_at: float = 0.0


class OAuthTokenProvider:
    """Machine-oriented OAuth helper: client_credentials and optional refresh_token."""

    def __init__(
        self,
        token_url: str,
        client_id: str,
        client_secret: str = "",
        scopes: Optional[list[str]] = None,
        resource: str = "",
        refresh_token: str = "",
        http_client: Any = None,
    ) -> None:
        self.token_url = token_url
        self.client_id = client_id
        self.client_secret = client_secret
        self.scopes = list(scopes or [])
        self.resource = resource
        self._owns_client = http_client is None
        self._client = http_client or httpx.Client(timeout=30.0)
        self._state = _TokenState(refresh_token=refresh_token)

    @classmethod
    def from_config(
        cls,
        auth_cfg: Dict[str, Any],
        *,
        resource_url: str = "",
        http_client: Any = None,
        environ: Optional[Dict[str, str]] = None,
    ) -> "OAuthTokenProvider":
        token_url = expand_env_vars(str(auth_cfg.get("tokenUrl") or auth_cfg.get("token_url") or ""), environ).strip()
        client_id = expand_env_vars(str(auth_cfg.get("clientId") or auth_cfg.get("client_id") or ""), environ).strip()
        client_secret = expand_env_vars(
            str(auth_cfg.get("clientSecret") or auth_cfg.get("client_secret") or ""), environ
        ).strip()
        refresh = expand_env_vars(
            str(auth_cfg.get("refreshToken") or auth_cfg.get("refresh_token") or ""), environ
        ).strip()
        resource = expand_env_vars(
            str(auth_cfg.get("resource") or resource_url or ""), environ
        ).strip()
        scopes_raw = auth_cfg.get("scopes") or auth_cfg.get("scope") or []
        if isinstance(scopes_raw, str):
            scopes = [s for s in scopes_raw.replace(",", " ").split() if s]
        else:
            scopes = [str(s) for s in scopes_raw]
        if not token_url or not client_id:
            raise McpAuthError("MCP client_credentials 需要 tokenUrl 与 clientId")
        return cls(
            token_url=token_url,
            client_id=client_id,
            client_secret=client_secret,
            scopes=scopes,
            resource=resource,
            refresh_token=refresh,
            http_client=http_client,
        )

    def get_access_token(self, *, force_refresh: bool = False) -> str:
        now = time.time()
        if not force_refresh and self._state.access_token and self._state.expires_at - 30 > now:
            return self._state.access_token
        if self._state.refresh_token and (force_refresh or self._state.access_token):
            try:
                return self._fetch_token(grant_type="refresh_token", refresh_token=self._state.refresh_token)
            except McpAuthError:
                if not force_refresh and not self.client_secret:
                    raise
        return self._fetch_token(grant_type="client_credentials")

    def _fetch_token(self, *, grant_type: str, refresh_token: str = "") -> str:
        data: Dict[str, str] = {
            "grant_type": grant_type,
            "client_id": self.client_id,
        }
        if self.client_secret:
            data["client_secret"] = self.client_secret
        if self.scopes:
            data["scope"] = " ".join(self.scopes)
        if self.resource:
            data["resource"] = self.resource
        if grant_type == "refresh_token":
            data["refresh_token"] = refresh_token
        resp = self._client.post(
            self.token_url,
            data=data,
            headers={"Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded"},
        )
        if getattr(resp, "status_code", 200) >= 400:
            raise McpAuthError(f"OAuth token 请求失败 ({resp.status_code}): {getattr(resp, 'text', '')[:200]}")
        try:
            payload = resp.json()
        except Exception as exc:
            raise McpAuthError(f"OAuth token 响应不是 JSON: {exc}") from exc
        token = str(payload.get("access_token") or "").strip()
        if not token:
            raise McpAuthError("OAuth token 响应缺少 access_token")
        expires_in = payload.get("expires_in")
        try:
            ttl = float(expires_in) if expires_in is not None else 3600.0
        except (TypeError, ValueError):
            ttl = 3600.0
        self._state.access_token = token
        self._state.expires_at = time.time() + max(60.0, ttl)
        new_refresh = str(payload.get("refresh_token") or "").strip()
        if new_refresh:
            self._state.refresh_token = new_refresh
        return token

    def close(self) -> None:
        if self._owns_client and hasattr(self._client, "close"):
            self._client.close()


def format_401_hint(status_code: int, www_authenticate: str, server_url: str) -> str:
    """Build an actionable error when a remote MCP rejects unauthenticated requests."""
    parsed = parse_www_authenticate(www_authenticate)
    meta = parsed.get("resource_metadata") or parsed.get("resource")
    if not meta and server_url:
        # RFC9728 well-known fallback hint
        parsed_url = urlparse(server_url)
        origin = f"{parsed_url.scheme}://{parsed_url.netloc}"
        meta = urljoin(origin + "/", ".well-known/oauth-protected-resource")
    hint = (
        f"MCP HTTP 需要鉴权 ({status_code})。可在 mcp.json 配置 auth.bearer / "
        "auth.client_credentials，或 headers.Authorization。"
    )
    if meta:
        hint += f" 资源元数据: {meta}"
    if parsed.get("scope"):
        hint += f" scope={parsed['scope']}"
    return hint
