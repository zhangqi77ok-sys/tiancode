package channels

import "tiancode/internal/core/llm"

// 渠道状态：enabled 可用；manually_disabled 用户手动停用；auto_disabled 上游故障自动禁用。
const (
	StatusEnabled          = "enabled"
	StatusManuallyDisabled = "manually_disabled"
	StatusAutoDisabled     = "auto_disabled"
)

// DefaultGroup 是缺省调用方分组（桌面应用当前只有一组调用方）。
const DefaultGroup = "default"

// DefaultWeight：weight<=0 视为默认权重（spec 允许实现取 100）。
const DefaultWeight = 100

// Channel 是一条上游渠道（多协议网关的数据模型）。
type Channel struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // 协议类型（决定适配器与默认 base_url），稳定字符串
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl,omitempty"` // 空 = 该 type 的默认地址
	// Credential 不透明：单 API Key / 换行分隔多 Key / OAuth JSON / AK|SK 复合。
	// 选路与持久化层不解析，只有适配器解释。
	Credential string `json:"credential,omitempty"`
	// CredentialState 仅多凭证时维护：每条凭证禁用标记 + 轮询下标。
	CredentialState *CredentialState `json:"credentialState,omitempty"`
	Models          []string         `json:"models"` // 对下游声明的模型名
	Groups          []string         `json:"groups"` // 允许使用的调用方分组（可多个）
	Status          string           `json:"status"`
	Priority        int              `json:"priority"` // 越大越优先
	Weight          int              `json:"weight"`   // 同优先级内加权随机；0 视为默认权重
	// ModelMapping：下游模型名 → 上游真实模型名；无映射原样传递。
	ModelMapping map[string]string `json:"modelMapping,omitempty"`
	// ParamOverride/HeaderOverride：转发前合并进请求（网关职责）。
	ParamOverride  map[string]any    `json:"paramOverride,omitempty"`
	HeaderOverride map[string]string `json:"headerOverride,omitempty"`
	// Extra：协议专用且不参与检索（azure api-version、区域、账号 ID 等）。
	Extra   map[string]string `json:"extra,omitempty"`
	AutoBan bool              `json:"autoBan"`
	// Auth 是渠道级鉴权配置（0.2.20）：nil = 协议默认；选路原样透传到适配器，
	// 池层不解释（与 Credential 同一纪律——鉴权形态的解释权在适配器）。
	Auth *llm.AuthConfig `json:"auth,omitempty"`
}

// CredentialState 记录多凭证的启用状态与轮询下标（与按行拆分的凭证一一对应）。
type CredentialState struct {
	Disabled []bool `json:"disabled"`
	Next     int    `json:"next"`
}

// CredentialEnabled 报告凭证下标 i 是否启用（nil 状态 = 全部启用）。
func (c *Channel) CredentialEnabled(i int) bool {
	if c.CredentialState == nil || i >= len(c.CredentialState.Disabled) {
		return true
	}
	return !c.CredentialState.Disabled[i]
}

// DisplayName 返回渠道展示名（无名回退 ID）：面向用户的错误文本与启动日志共用一处，
// 避免"UI 叫名字、错误里叫 ID"两套称呼（用户对不上号就没法排查）。
func (c *Channel) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}

// StatusLabel 返回渠道状态的中文标签（面向用户的错误文本使用；未知状态原样返回）。
func StatusLabel(status string) string {
	switch status {
	case StatusEnabled:
		return "启用"
	case StatusManuallyDisabled:
		return "手动停用"
	case StatusAutoDisabled:
		return "自动禁用"
	default:
		return status
	}
}
