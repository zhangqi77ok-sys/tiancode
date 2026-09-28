package codexauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Manager 管理进行中的授权会话与本地回调监听。
//
// 桌面增强（相对参考实现的手动粘贴）：本机直接监听 1455（redirect_uri 的固定端口），
// 浏览器授权后自动收码换 token —— UI 无需用户复制粘贴；监听失败（端口占用等）时
// 降级为"手动粘贴回调地址"，两条通道共用同一会话与换码逻辑。
type Manager struct {
	client *Client

	mu       sync.Mutex
	sessions map[string]*session
	listener net.Listener
	server   *http.Server
}

type session struct {
	id        string
	pkce      PKCE
	createdAt time.Time

	mu     sync.Mutex
	cred   *Credential
	errMsg string
	taken  bool // 凭证已被取走（一次性）
}

// StartInfo 是一次授权启动的结果（给 UI 展示与打开浏览器）。
type StartInfo struct {
	SessionID string `json:"sessionId"`
	// AuthURL 是 auth.openai.com 原始授权链接
	AuthURL string `json:"authUrl"`
	// DesktopURL 是 chatgpt.com 包装链接（默认打开它：用户从 ChatGPT 域名进入）
	DesktopURL string `json:"desktopUrl"`
	// Listening 报告本地 1455 是否在监听（true = 浏览器授权完成后自动生效，
	// false = 需要用户手动粘贴回调地址）
	Listening bool `json:"listening"`
}

// Status 是会话状态查询结果（凭证本身不出本包：UI 只看到摘要）。
type Status struct {
	// State：pending（等待授权） | done（已拿到凭证，待绑定渠道） | error
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
	Display string `json:"display,omitempty"`
}

// NewManager 构造管理器。
func NewManager(client *Client) *Manager {
	if client == nil {
		client = NewClient(nil)
	}
	return &Manager{client: client, sessions: map[string]*session{}}
}

// Start 开始一次授权：生成 PKCE 与会话，并尽力启动回环监听。
func (m *Manager) Start() (StartInfo, error) {
	pkce, err := NewPKCE()
	if err != nil {
		return StartInfo{}, err
	}
	id, err := randHex(16)
	if err != nil {
		return StartInfo{}, err
	}
	s := &session{id: id, pkce: pkce, createdAt: time.Now()}

	m.mu.Lock()
	m.sweepLocked()
	m.sessions[id] = s
	listening := m.ensureListenerLocked()
	m.mu.Unlock()

	authURL := BuildAuthorizeURL(pkce)
	return StartInfo{
		SessionID:  id,
		AuthURL:    authURL,
		DesktopURL: DesktopAuthURL(authURL),
		Listening:  listening,
	}, nil
}

// Poll 查询会话状态（done 表示凭证已就绪，调用 Bind 取走后会话销毁）。
func (m *Manager) Poll(id string) (Status, error) {
	s := m.get(id)
	if s == nil {
		return Status{}, fmt.Errorf("授权会话不存在或已过期（请重新发起授权）")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.errMsg != "":
		return Status{State: "error", Error: s.errMsg}, nil
	case s.cred != nil:
		return Status{State: "done", Display: s.cred.Display()}, nil
	default:
		return Status{State: "pending"}, nil
	}
}

// Complete 提交手动粘贴的回调地址（或裸 code）——回环不可用时的兜底通道。
// codeOrURL 支持三种形态：完整回调 URL、以 ?code= 开头的 query、裸 code。
func (m *Manager) Complete(ctx context.Context, id, codeOrURL string) (Status, error) {
	s := m.get(id)
	if s == nil {
		return Status{}, fmt.Errorf("授权会话不存在或已过期（请重新发起授权）")
	}
	code, state := parseCallbackInput(codeOrURL)
	if code == "" {
		return Status{}, fmt.Errorf("未能从输入中识别授权码（支持完整回调地址或裸 code）")
	}
	if state != "" && state != s.pkce.State {
		return Status{}, fmt.Errorf("state 不匹配：粘贴的回调不属于本次授权（请确认复制的是最新一次授权的结果）")
	}
	if err := m.exchange(ctx, s, code); err != nil {
		return Status{}, err
	}
	return m.Poll(id)
}

// Bind 取走凭证（一次性）并执行绑定回调（建/更新渠道由 app 层完成）。
// 返回凭证与摘要；会话随即销毁。
func (m *Manager) Bind(id string) (Credential, error) {
	s := m.get(id)
	if s == nil {
		return Credential{}, fmt.Errorf("授权会话不存在或已过期（请重新发起授权）")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cred == nil {
		return Credential{}, fmt.Errorf("授权尚未完成（浏览器授权进行中或已失败）")
	}
	if s.taken {
		return Credential{}, fmt.Errorf("本次授权凭证已被使用（请重新发起授权）")
	}
	s.taken = true
	cred := *s.cred

	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return cred, nil
}

// Close 关闭回环监听（应用退出时调用）；关闭失败向上传播（不静默）。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	if m.server != nil {
		err = m.server.Close()
		m.server = nil
	}
	m.listener = nil
	return err
}

// ---- 内部 ----

func (m *Manager) get(id string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	return m.sessions[id]
}

// sweepLocked 清理过期会话（TTL 30 分钟；调用方持锁）。
func (m *Manager) sweepLocked() {
	now := time.Now()
	for id, s := range m.sessions {
		if now.Sub(s.createdAt) > SessionTTL {
			delete(m.sessions, id)
		}
	}
}

// ensureListenerLocked 尽力启动 1455 回环监听；失败返回 false（降级手动粘贴）。
func (m *Manager) ensureListenerLocked() bool {
	if m.listener != nil {
		return true
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", CallbackPort))
	if err != nil {
		return false // 端口占用（如本机 codex CLI 在跑）：不阻断，走手动粘贴
	}
	m.listener = ln
	mux := http.NewServeMux()
	mux.HandleFunc(CallbackPath, m.handleCallback)
	m.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = m.server.Serve(ln) }()
	return true
}

func (m *Manager) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" {
		writeCallbackPage(w, false, "回调缺少 code 参数")
		return
	}
	s := m.sessionByState(state)
	if s == nil {
		writeCallbackPage(w, false, "授权会话不存在或已过期，请回到 tiancode 重新发起授权")
		return
	}
	if err := m.exchange(r.Context(), s, code); err != nil {
		writeCallbackPage(w, false, "换取凭证失败："+err.Error())
		return
	}
	writeCallbackPage(w, true, "")
}

func (m *Manager) sessionByState(state string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	for _, s := range m.sessions {
		if s.pkce.State == state {
			return s
		}
	}
	return nil
}

// exchange 换码并写入会话（幂等：已成功的会话不重复换，避免 code 二次使用报错）。
func (m *Manager) exchange(ctx context.Context, s *session, code string) error {
	s.mu.Lock()
	if s.cred != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	resp, err := m.client.ExchangeCode(ctx, code, s.pkce.Verifier)
	if err != nil {
		s.mu.Lock()
		s.errMsg = err.Error()
		s.mu.Unlock()
		return err
	}
	cred := TokenToCredential(resp, ClientID)
	s.mu.Lock()
	s.cred = &cred
	s.errMsg = ""
	s.mu.Unlock()
	return nil
}

// parseCallbackInput 从粘贴内容提取 code 与 state。
// 支持：完整回调 URL、query 串（?code=…&state=…）、裸 code。
func parseCallbackInput(raw string) (code, state string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	if i := strings.Index(s, "code="); i >= 0 {
		rest := s[i+len("code="):]
		code = cutParam(rest)
		if j := strings.Index(rest, "state="); j >= 0 {
			state = cutParam(rest[j+len("state="):])
		}
		return decodeParam(code), decodeParam(state)
	}
	return s, "" // 裸 code
}

func cutParam(s string) string {
	if i := strings.IndexAny(s, "&#"); i >= 0 {
		return s[:i]
	}
	return s
}

func decodeParam(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if v, err := url.QueryUnescape(s); err == nil {
		return v
	}
	return s
}

// writeCallbackPage 输出浏览器侧结果页（简洁、无外部资源）。
func writeCallbackPage(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>授权完成</title>` +
			`<div style="font-family:system-ui;max-width:480px;margin:80px auto;text-align:center">` +
			`<h2>✅ 授权完成</h2><p>已成功绑定 ChatGPT 账号，请回到 tiancode 继续。</p></div>`))
		return
	}
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>授权失败</title>` +
		`<div style="font-family:system-ui;max-width:480px;margin:80px auto;text-align:center">` +
		`<h2>⚠️ 授权失败</h2><p>` + htmlEscape(msg) + `</p></div>`))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
