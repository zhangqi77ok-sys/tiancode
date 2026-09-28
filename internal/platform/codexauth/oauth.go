// Package codexauth 实现 OpenAI（ChatGPT/Codex 订阅）账号的 OAuth 授权与凭证刷新。
//
// 做什么：PKCE 授权码流（授权 URL → 本地回环收码/手动粘贴 → 换 token → 刷新），
// 凭证以单行 JSON 形态落入渠道 Credential 字段（池按行拆分多凭证，JSON 序列化不带
// 换行，天然是一条）。上游请求的身份伪装（originator/UA/version 配套）在
// adaptors/codex —— 本包只管"拿凭证"。
//
// 端点与客户端常量对齐公开的 Codex CLI 客户端（PKCE 公开 client_id）：
//   - 授权/令牌端点 auth.openai.com；回调固定 http://localhost:1455/auth/callback
//     （端口与 client 注册一致，不可更换）；
//   - 凭据面（换码/刷新）只带 UA + originator 伪装头，**不带** version（照参考实现：
//     推理面才需要 version，凭据面带反而暴露）。
package codexauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tiancode/internal/platform/netproxy"
)

// 授权常量（对齐 Codex CLI 公开客户端；改动需同步说明文档）。
const (
	ClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	AuthorizeURL = "https://auth.openai.com/oauth/authorize"
	TokenURL     = "https://auth.openai.com/oauth/token"
	// RedirectURI 与客户端注册一致：端口不可改（换端口会被授权服务器拒绝）
	RedirectURI  = "http://localhost:1455/auth/callback"
	CallbackPort = 1455
	CallbackPath = "/auth/callback"
	// DesktopAuthBase 是桌面授权入口包装（从 ChatGPT 域名进入的引导页）
	DesktopAuthBase = "https://chatgpt.com/codex/desktop-auth"

	Scopes        = "openid profile email offline_access"
	RefreshScopes = "openid profile email"

	// 凭据面伪装头：UA 与 originator 必须配套（错配会被上游拒绝）
	clientUA         = "codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	clientOriginator = "codex-tui"

	// SessionTTL 是授权会话有效期（与此同时也限制回环监听时长）
	SessionTTL = 30 * time.Minute
)

// PKCE 是一次授权流程的一次性参数。
type PKCE struct {
	State     string
	Verifier  string
	Challenge string
}

// NewPKCE 生成 PKCE 参数。
// 编码口径照参考实现：state/verifier 用 **hex**（OpenAI 不接受 base64url 的 verifier），
// challenge 用 S256 摘要的 base64url（无 padding）。
func NewPKCE() (PKCE, error) {
	state, err := randHex(32)
	if err != nil {
		return PKCE{}, err
	}
	verifier, err := randHex(64)
	if err != nil {
		return PKCE{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCE{State: state, Verifier: verifier, Challenge: challenge}, nil
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("随机数生成失败：%w", err)
	}
	return hex.EncodeToString(b), nil
}

// BuildAuthorizeURL 构造授权 URL（参数与参考实现逐项对齐）。
func BuildAuthorizeURL(pkce PKCE) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", RedirectURI)
	q.Set("scope", Scopes)
	q.Set("state", pkce.State)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	// Codex 客户端专用参数（影响 id_token 内容与简化授权流程）
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	return AuthorizeURL + "?" + q.Encode()
}

// DesktopAuthURL 把授权 URL 包装成 chatgpt.com 的桌面授权入口（用户从 ChatGPT 域名进入）。
func DesktopAuthURL(authURL string) string {
	return DesktopAuthBase + "?authorize_url=" + url.QueryEscape(authURL)
}

// TokenResponse 是 token 端点响应（换码与刷新同构）。
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// Client 是 OAuth 端点客户端（HTTP 客户端与令牌端点均可注入以便测试）。
type Client struct {
	HTTP *http.Client
	// TokenEndpoint 覆盖令牌端点（测试注入 httptest）；空 = 生产端点。
	TokenEndpoint string
	// ProxyFunc 动态返回上游代理（每次请求读取：代理是运行时可变配置）。
	// 授权端点与推理端点必须共用同一出口——否则会出现"浏览器能登录、
	// 换码却按直连 IP 被地区拒绝"（0.2.21 实机错误）。
	ProxyFunc func() string
}

func (c *Client) proxy() string {
	if c.ProxyFunc != nil {
		return c.ProxyFunc()
	}
	return ""
}

// NewClient 构造客户端（nil 用默认客户端，超时 30s）。
func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{HTTP: hc}
}

func (c *Client) tokenURL() string {
	if c.TokenEndpoint != "" {
		return c.TokenEndpoint
	}
	return TokenURL
}

// ExchangeCode 用授权码换 token。
func (c *Client) ExchangeCode(ctx context.Context, code, verifier string) (TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", RedirectURI)
	form.Set("code_verifier", verifier)
	return c.postForm(ctx, form)
}

// RefreshAccess 用 refresh_token 换新 access_token。
// scope 去掉 offline_access（对齐参考实现：刷新时用 RefreshScopes）。
func (c *Client) RefreshAccess(ctx context.Context, refreshToken, clientID string) (TokenResponse, error) {
	if strings.TrimSpace(clientID) == "" {
		clientID = ClientID
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	form.Set("scope", RefreshScopes)
	return c.postForm(ctx, form)
}

func (c *Client) postForm(ctx context.Context, form url.Values) (TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", clientUA)
	req.Header.Set("originator", clientOriginator)
	// 走全局代理（若有）：授权端点与推理端点必须同一出口
	client, err := netproxy.Client(c.HTTP, c.proxy())
	if err != nil {
		return TokenResponse{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("连接授权服务器失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("读取授权响应失败：%w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		// 地区限制是可读化程度最低、最需要指引的错误（用户看不出"出口 IP 被拒"）：
		// 明确指向代理配置，避免反复重试授权。
		if strings.Contains(detail, "unsupported_country_region_territory") {
			return TokenResponse{}, fmt.Errorf(
				"OpenAI 拒绝了当前网络出口（地区不受支持）：请在「渠道管理」面板顶部配置网络代理（如 http://127.0.0.1:7897）后重试授权")
		}
		return TokenResponse{}, fmt.Errorf("授权服务器返回 HTTP %d：%s", resp.StatusCode, detail)
	}
	var out TokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return TokenResponse{}, fmt.Errorf("解析授权响应失败：%w", err)
	}
	if out.AccessToken == "" {
		return TokenResponse{}, fmt.Errorf("授权响应缺少 access_token")
	}
	return out, nil
}

// Claims 是从 id_token 提取的身份信息。
type Claims struct {
	Email            string
	ChatGPTAccountID string
	ChatGPTUserID    string
	PlanType         string
	OrganizationID   string
	ExpiresAt        int64 // JWT exp（Unix 秒；0 = 无）
}

// ParseIDToken 解码 id_token 的 payload（**不验签**：token 直接来自授权服务器；
// 对齐参考实现的本地自用口径，不做 JWKS 校验）。
func ParseIDToken(idToken string) (Claims, error) {
	payload, err := jwtPayload(idToken)
	if err != nil {
		return Claims{}, err
	}
	var raw struct {
		Email string `json:"email"`
		Exp   int64  `json:"exp"`
		Auth  struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
			ChatGPTUserID    string `json:"chatgpt_user_id"`
			PlanType         string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
		Organizations []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"is_default"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, fmt.Errorf("id_token 解析失败：%w", err)
	}
	c := Claims{
		Email:            raw.Email,
		ChatGPTAccountID: raw.Auth.ChatGPTAccountID,
		ChatGPTUserID:    raw.Auth.ChatGPTUserID,
		PlanType:         raw.Auth.PlanType,
		ExpiresAt:        raw.Exp,
	}
	for _, o := range raw.Organizations {
		if o.IsDefault || c.OrganizationID == "" {
			c.OrganizationID = o.ID
			if o.IsDefault {
				break
			}
		}
	}
	return c, nil
}

// JWTExpiry 解码任意 JWT 的 exp（access_token 也是 JWT，可用于判断过期）。
func JWTExpiry(token string) (int64, bool) {
	payload, err := jwtPayload(token)
	if err != nil {
		return 0, false
	}
	var raw struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil || raw.Exp == 0 {
		return 0, false
	}
	return raw.Exp, true
}

func jwtPayload(token string) ([]byte, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("不是 JWT 形态（缺少分段）")
	}
	seg := parts[1]
	if pad := len(seg) % 4; pad != 0 {
		seg += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		if raw, err = base64.RawURLEncoding.DecodeString(parts[1]); err != nil {
			return nil, fmt.Errorf("JWT payload 解码失败：%w", err)
		}
	}
	return raw, nil
}

// TokenToCredential 把 token 响应组装成渠道凭证（id_token 提供 email/account/plan）。
func TokenToCredential(t TokenResponse, clientID string) Credential {
	cred := Credential{
		Type:         CredentialType,
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		IDToken:      t.IDToken,
		ExpiresAt:    time.Now().Add(time.Duration(t.ExpiresIn) * time.Second).Unix(),
		ClientID:     clientID,
	}
	if cred.ClientID == "" {
		cred.ClientID = ClientID
	}
	if claims, err := ParseIDToken(t.IDToken); err == nil {
		cred.Email = claims.Email
		cred.ChatGPTAccountID = claims.ChatGPTAccountID
		cred.ChatGPTUserID = claims.ChatGPTUserID
		cred.PlanType = claims.PlanType
		cred.OrganizationID = claims.OrganizationID
	}
	return cred
}
