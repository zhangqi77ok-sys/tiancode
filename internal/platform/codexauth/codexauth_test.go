package codexauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// PKCE：state/verifier 为 hex（OpenAI 不接受 base64url verifier），challenge 为 S256 base64url。
func TestNewPKCE_EncodingShape(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.State) != 64 || !isHex(p.State) {
		t.Fatalf("state 应为 64 位 hex：%q", p.State)
	}
	if len(p.Verifier) != 128 || !isHex(p.Verifier) {
		t.Fatalf("verifier 应为 128 位 hex：%q", p.Verifier)
	}
	if strings.ContainsAny(p.Challenge, "+/=") {
		t.Fatalf("challenge 应为 base64url 无 padding：%q", p.Challenge)
	}
	if p.State == p.Verifier {
		t.Fatal("state 与 verifier 不得相同")
	}
}

// 授权 URL 参数与参考实现逐项一致（少一个参数授权页行为就会变）。
func TestBuildAuthorizeURL_Params(t *testing.T) {
	p := PKCE{State: "st", Verifier: "vf", Challenge: "ch"}
	u, err := url.Parse(BuildAuthorizeURL(p))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := map[string]string{
		"response_type":              "code",
		"client_id":                  ClientID,
		"redirect_uri":               RedirectURI,
		"scope":                      Scopes,
		"state":                      "st",
		"code_challenge":             "ch",
		"code_challenge_method":      "S256",
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if u.Host != "auth.openai.com" {
		t.Fatalf("授权域名 = %q", u.Host)
	}
	// 桌面包装链接：authorize_url 必须整串 urlencode（否则 query 会串味）
	d := DesktopAuthURL(BuildAuthorizeURL(p))
	if !strings.HasPrefix(d, DesktopAuthBase+"?authorize_url=") {
		t.Fatalf("包装链接形态异常：%q", d)
	}
	if strings.Contains(d, "&code_challenge=") {
		t.Fatal("authorize_url 未被编码（内层参数泄漏到外层 query）")
	}
}

// id_token 解析：身份字段嵌套在 https://api.openai.com/auth 命名空间下。
func TestParseIDToken(t *testing.T) {
	payload := map[string]any{
		"email": "a@b.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acc-1",
			"chatgpt_user_id":    "usr-1",
			"chatgpt_plan_type":  "plus",
		},
		"organizations": []map[string]any{
			{"id": "org-x"},
			{"id": "org-default", "is_default": true},
		},
	}
	tok := fakeJWT(t, payload)
	c, err := ParseIDToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Email != "a@b.com" || c.ChatGPTAccountID != "acc-1" || c.ChatGPTUserID != "usr-1" || c.PlanType != "plus" {
		t.Fatalf("claims = %+v", c)
	}
	if c.OrganizationID != "org-default" {
		t.Fatalf("organization_id = %q（应取默认组织）", c.OrganizationID)
	}
	if _, ok := JWTExpiry(tok); !ok {
		t.Fatal("JWTExpiry 应可解码")
	}
	if _, err := ParseIDToken("not-a-jwt"); err == nil {
		t.Fatal("非 JWT 必须报错")
	}
}

// 凭证 JSON：单行（池按行拆分凭证）、识别标记、刷新判据。
func TestCredential_JSONAndRefresh(t *testing.T) {
	now := time.Now()
	cred := Credential{
		Type: CredentialType, AccessToken: "at", RefreshToken: "rt",
		ExpiresAt: now.Add(time.Minute).Unix(), Email: "a@b.com", PlanType: "plus",
	}
	js, err := cred.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(js, "\n") {
		t.Fatal("凭证 JSON 必须单行（池按行拆分多凭证）")
	}
	back, ok := Parse(js)
	if !ok || back.AccessToken != "at" || back.RefreshToken != "rt" {
		t.Fatalf("round-trip = %+v ok=%v", back, ok)
	}
	if !back.NeedsRefresh(now) {
		t.Fatal("1 分钟后过期应触发提前刷新（窗口 5 分钟）")
	}
	if back.Display() != "a@b.com · Plus" {
		t.Fatalf("display = %q", back.Display())
	}
	// 普通 API Key 不是 OAuth 凭证
	if _, ok := Parse("sk-abc"); ok {
		t.Fatal("普通 Key 不得识别为 OAuth 凭证")
	}
	// 无 RT 不主动刷（PAT 导入的裸 AT 场景）
	noRT := Credential{Type: CredentialType, AccessToken: "at", ExpiresAt: now.Add(time.Second).Unix()}
	if noRT.NeedsRefresh(now) {
		t.Fatal("无 refresh_token 不应触发刷新")
	}
}

// 回调粘贴解析：完整 URL / query 串 / 裸 code 三种形态。
func TestParseCallbackInput(t *testing.T) {
	code, state := parseCallbackInput("http://localhost:1455/auth/callback?code=abc&state=st1")
	if code != "abc" || state != "st1" {
		t.Fatalf("URL 形态 = %q/%q", code, state)
	}
	code, state = parseCallbackInput("?code=abc&state=st1")
	if code != "abc" || state != "st1" {
		t.Fatalf("query 形态 = %q/%q", code, state)
	}
	code, state = parseCallbackInput("abc")
	if code != "abc" || state != "" {
		t.Fatalf("裸 code = %q/%q", code, state)
	}
	if code, _ := parseCallbackInput("  "); code != "" {
		t.Fatal("空白输入应返回空")
	}
}

// 导入解析：auth.json（各键名形态）/ 裸 access_token / 裸 refresh_token（刷新换 AT）。
func TestParseImport(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-at", "refresh_token": "new-rt", "expires_in": 3600,
		})
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.Client())
	client.TokenEndpoint = srv.URL
	ctx := context.Background()

	// 1) auth.json 形态（tokens 嵌套 + account 对象）
	authJSON := `{"tokens":{"access_token":"at1","refresh_token":"rt1","id_token":""},"account":{"id":"acc-9"},"email":"x@y.com"}`
	cred, err := ParseImport(ctx, client, authJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "at1" || cred.RefreshToken != "rt1" || cred.ChatGPTAccountID != "acc-9" || cred.Email != "x@y.com" {
		t.Fatalf("auth.json 导入 = %+v", cred)
	}
	js, err := cred.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Parse(js); !ok {
		t.Fatal("导入产物必须可被 Parse 识别")
	}

	// 2) 裸 refresh_token（非 JWT）→ 调刷新端点换取 AT
	cred, err = ParseImport(ctx, client, "raw-refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "new-at" || cred.RefreshToken != "new-rt" {
		t.Fatalf("RT 导入 = %+v", cred)
	}
	if gotForm.Get("grant_type") != "refresh_token" || gotForm.Get("refresh_token") != "raw-refresh-token" {
		t.Fatalf("刷新请求 = %v", gotForm)
	}

	// 3) 只有 refresh_token 的 JSON → 同样走刷新
	cred, err = ParseImport(ctx, client, `{"refresh_token":"rt-only"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "new-at" {
		t.Fatalf("仅 RT JSON 导入 = %+v", cred)
	}

	// 4) 空内容 / 无 token 的 JSON 必须报错
	if _, err := ParseImport(ctx, client, "  "); err == nil {
		t.Fatal("空输入应报错")
	}
	if _, err := ParseImport(ctx, client, `{"foo":1}`); err == nil {
		t.Fatal("无 token 的 JSON 应报错")
	}
}

// 刷新：只在响应带回新 RT 时覆盖（防把可用 RT 被空值覆盖）。
func TestRefreshCredential_KeepsOldRTOnEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at2", "expires_in": 1800})
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.Client())
	client.TokenEndpoint = srv.URL

	old := Credential{Type: CredentialType, AccessToken: "at1", RefreshToken: "keep-me", ClientID: ClientID}
	got, err := RefreshCredential(context.Background(), client, old)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at2" || got.RefreshToken != "keep-me" {
		t.Fatalf("刷新结果 = %+v（RT 应保留）", got)
	}
	if got.ExpiresAt <= time.Now().Unix() {
		t.Fatal("expires_at 未更新")
	}
}

// 会话与回环：Start → 手动 Complete（用 mock 换码）→ Poll done → Bind 一次性取走。
func TestManager_CompleteAndBind(t *testing.T) {
	payload := map[string]any{
		"email":                       "u@e.com",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acc-1", "chatgpt_plan_type": "pro"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "authorization_code" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "at-1",
			"refresh_token": "rt-1",
			"id_token":      fakeJWT(t, payload),
			"expires_in":    3600,
		})
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.Client())
	client.TokenEndpoint = srv.URL
	m := NewManager(client)
	t.Cleanup(func() { m.Close() })

	info, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID == "" || !strings.Contains(info.AuthURL, "client_id=") {
		t.Fatalf("info = %+v", info)
	}
	if st, err := m.Poll(info.SessionID); err != nil || st.State != "pending" {
		t.Fatalf("初始状态 = %+v err=%v", st, err)
	}
	// state 不匹配必须拒绝（防串会话）
	if _, err := m.Complete(context.Background(), info.SessionID, "?code=x&state=wrong"); err == nil {
		t.Fatal("state 不匹配必须拒绝")
	}
	// 正确 state 的手动粘贴（完整回调 URL 形态）
	cb := "http://localhost:1455/auth/callback?code=good-code&state=" + url.QueryEscape(stateOf(t, info.AuthURL))
	if _, err := m.Complete(context.Background(), info.SessionID, cb); err != nil {
		t.Fatal(err)
	}
	st, err := m.Poll(info.SessionID)
	if err != nil || st.State != "done" || !strings.Contains(st.Display, "u@e.com") {
		t.Fatalf("完成后状态 = %+v err=%v", st, err)
	}
	if strings.Contains(st.Display, "at-1") {
		t.Fatal("状态摘要绝不能含 token")
	}
	cred, err := m.Bind(info.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "at-1" || cred.ChatGPTAccountID != "acc-1" {
		t.Fatalf("凭证 = %+v", cred)
	}
	if _, err := m.Bind(info.SessionID); err == nil {
		t.Fatal("凭证一次性：重复 Bind 必须报错")
	}
}

func stateOf(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state")
}

func fakeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString(b)
	return head + "." + body + ".sig"
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
