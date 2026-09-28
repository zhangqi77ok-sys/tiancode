package codexauth

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CredentialType 是凭证识别标记（渠道 Credential 字段里的 JSON type）。
const CredentialType = "codex_oauth"

// Credential 是 Codex OAuth 凭证：以**单行 JSON** 存渠道 Credential 字段。
// 为什么复用 Credential（而非新增结构化字段）：池的 Credential 是不透明字符串、
// 按行拆分多凭证——单行 JSON 天然是一条凭证，多账号轮询/禁用机制零改动可复用。
type Credential struct {
	Type             string `json:"type"` // 恒 "codex_oauth"
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	IDToken          string `json:"id_token,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"` // Unix 秒
	Email            string `json:"email,omitempty"`
	ChatGPTAccountID string `json:"chatgpt_account_id,omitempty"`
	ChatGPTUserID    string `json:"chatgpt_user_id,omitempty"`
	PlanType         string `json:"plan_type,omitempty"`
	OrganizationID   string `json:"organization_id,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
}

// JSON 序列化为单行 JSON（池按行拆分凭证，绝不能有换行）。
func (c Credential) JSON() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("凭证序列化失败：%w", err)
	}
	return string(b), nil
}

// Parse 识别并解析凭证：raw 是 JSON 且带 codex_oauth 标记（或含 access_token）才算。
// 普通 API Key 返回 false（调用方按 Key 形态处理）。
func Parse(raw string) (Credential, bool) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "{") {
		return Credential{}, false
	}
	var c Credential
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return Credential{}, false
	}
	if c.Type != CredentialType && c.AccessToken == "" {
		return Credential{}, false
	}
	return c, true
}

// refreshWindow 是提前刷新窗口：距过期不足该时长即刷新（避免请求途中过期）。
const refreshWindow = 5 * time.Minute

// NeedsRefresh 报告凭证是否需要在请求前刷新。
func (c Credential) NeedsRefresh(now time.Time) bool {
	if c.ExpiresAt == 0 {
		return false // 无过期信息：交给运行时 401 处理（不主动刷）
	}
	if c.RefreshToken == "" {
		return false // 无 RT 可刷（如 PAT 导入的裸 AT）
	}
	return now.Add(refreshWindow).Unix() >= c.ExpiresAt
}

// Display 是脱敏展示文案（凭证视图/渠道卡片用，绝不外发 token）。
func (c Credential) Display() string {
	parts := []string{}
	if c.Email != "" {
		parts = append(parts, c.Email)
	}
	if c.PlanType != "" {
		parts = append(parts, strings.ToUpper(c.PlanType[:1])+c.PlanType[1:])
	}
	if len(parts) == 0 {
		return "ChatGPT 账号"
	}
	return strings.Join(parts, " · ")
}
