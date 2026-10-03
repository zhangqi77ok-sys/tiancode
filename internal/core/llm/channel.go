package llm

import (
	"context"
	"fmt"
	"strings"
)

// Protocol 是渠道协议，决定使用哪个供应商适配器。
// 为什么渠道模型放在 core/llm：渠道是"模型调用"领域的一部分——运行时按渠道
// 构造适配器、按协议分派，agent 全程只认 ChatRuntime（ADR-0005 变化点收敛于此）。
// 参照：dsh-java 的 harness_model_setting（渠道/协议/地址/模型/密钥五要素）。
type Protocol string

// ProtocolOpenAI 表示 OpenAI 兼容协议（/chat/completions + SSE）。
// 主流网关（OpenAI/DeepSeek/通义/智谱/豆包/Kimi/Ollama）都提供 OpenAI 兼容端点。
const ProtocolOpenAI Protocol = "openai"

// ProtocolAnthropic 表示 Anthropic Messages 协议（/v1/messages + SSE）。
const ProtocolAnthropic Protocol = "anthropic"

// ProtocolCodex 表示 OpenAI Codex（ChatGPT 订阅）协议：Responses API + OAuth 账号凭证。
const ProtocolCodex Protocol = "codex"

// Valid 报告协议是否已实现。未实现的协议必须显式拒绝，绝不静默降级。
func (p Protocol) Valid() bool {
	return p == ProtocolOpenAI || p == ProtocolAnthropic || p == ProtocolCodex
}

// Channel 是一个模型渠道（对外 DTO 形态；池模型见 platform/channels.Channel）。
type Channel struct {
	ID       string   `json:"id"`       // 稳定标识（生成后不变，供激活引用）
	Name     string   `json:"name"`     // 用户可见名称
	Protocol Protocol `json:"protocol"` // 协议（决定适配器）
	BaseURL  string   `json:"baseUrl"`  // 网关根地址（含 /v1）
	APIKey   string   `json:"apiKey"`   // 凭证（仅存本机；多凭证换行分隔）
	Model    string   `json:"model"`    // 主模型（= Models[0]，兼容旧 DTO）
	// 多协议渠道层字段（0.2.17）：可选，缺省走池默认
	Models   []string `json:"models,omitempty"`   // 该渠道声明的全部模型
	Priority int      `json:"priority,omitempty"` // 越大越优先（新建默认 100）
	Weight   int      `json:"weight,omitempty"`   // 同优先级内加权随机（0 = 默认 100）
	Status   string   `json:"status,omitempty"`   // 空 = enabled
	// 高级字段（0.2.19，池同名透传）：
	AutoBan        bool              `json:"autoBan,omitempty"`        // 上游故障自动禁用（渠道级/凭证级）
	ModelMapping   map[string]string `json:"modelMapping,omitempty"`   // 下游模型名 → 上游真实模型名
	ParamOverride  map[string]any    `json:"paramOverride,omitempty"`  // 请求体覆写（网关合并）
	HeaderOverride map[string]string `json:"headerOverride,omitempty"` // 请求头覆写（网关合并）
	// Auth 是渠道级鉴权配置（0.2.20）：nil = 协议默认（openai → Bearer；anthropic → x-api-key）。
	Auth *AuthConfig `json:"auth,omitempty"`
	// ContextLimit 是渠道声明的上下文上限（token；0 = 未配置，不裁剪）。
	// 只用于本地派生历史时的分级折叠，绝不发给上游；估算口径见
	// internal/core/agent/derive.go 的 estimateTextTokens（ASCII 4 字符≈1 token、
	// 1 个非 ASCII 字符≈1 token，保守取上界）。
	ContextLimit int `json:"contextLimit,omitempty"`

	// PriceIn/PriceOut 是每百万 token 的输入/输出单价（0.0.24 成本估算用；
	// 0 = 未配置，统计面板不显示金额）。只是展示层的估算依据，绝不参与选路。
	PriceIn  float64 `json:"priceIn,omitempty"`
	PriceOut float64 `json:"priceOut,omitempty"`
}

// AuthConfig 是渠道级鉴权配置：独立于协议——适配器决定"怎么发请求"，
// 鉴权决定"拿什么证明身份"。为什么需要它：大量中转/自建网关不用标准鉴权头
// （api-key 头、无 Bearer 前缀的 Authorization、URL 参数 key、完全无鉴权），
// 此前三种情形都无法接入（header_override 不支持凭证插值，写死密钥会破坏多 Key 轮询）。
// 设计参照 new-api AdvancedCustom 的 Auth{type,name,value} 三字段，并提升为所有渠道通用。
type AuthConfig struct {
	// Type：default（协议默认）| bearer | header | query | none
	Type string `json:"type"`
	// Name 是请求头名或 URL 参数名（type=header/query 时必填）
	Name string `json:"name,omitempty"`
	// Value 是值模板，支持 {api_key} 占位符（如 "Bearer {api_key}"、"Token {api_key}"）；
	// 空 = 仅凭证本身（"api-key: <key>" 形态）
	Value string `json:"value,omitempty"`
}

// 鉴权方式（AuthConfig.Type 的取值）。
const (
	AuthDefault = "default" // 跟随协议默认
	AuthBearer  = "bearer"  // Authorization: Bearer <key>
	AuthHeader  = "header"  // 自定义请求头 Name: Value
	AuthQuery   = "query"   // URL 查询参数 Name=Value
	AuthNone    = "none"    // 不发任何鉴权（如本地网关前置代理已鉴权）
)

// Validate 校验鉴权配置合法性：保存时早失败，而不是首次请求才报错。
func (a *AuthConfig) Validate() error {
	if a == nil {
		return nil
	}
	switch a.Type {
	case "", AuthDefault, AuthBearer, AuthNone:
		return nil
	case AuthHeader:
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("鉴权方式 header 需要指定请求头名称")
		}
		return nil
	case AuthQuery:
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("鉴权方式 query 需要指定 URL 参数名称")
		}
		return nil
	default:
		return fmt.Errorf("不支持的鉴权方式 %q（可用：default/bearer/header/query/none）", a.Type)
	}
}

// ChannelView 是渠道的外发视图（UI/事件用）：密钥脱敏，只告知是否已配置。
type ChannelView struct {
	Channel
	HasKey bool `json:"hasKey"`
	// 凭证摘要（0.2.19）：列表卡片一眼看到"3 条 · 1 禁用"（逐条管理走凭证视图）。
	CredentialCount    int `json:"credentialCount"`
	CredentialDisabled int `json:"credentialDisabled"`
}

// Sanitized 返回脱敏视图（C-CH-6：密钥绝不出现在日志/事件中）。
func (c Channel) Sanitized() ChannelView {
	view := ChannelView{Channel: c, HasKey: c.APIKey != ""}
	view.APIKey = ""
	return view
}

// ModelDiscoverer 是可选能力：拉取上游可用模型列表（供 UI 的"同步模型"）。
// 为什么单独成接口：不是所有协议都能列模型，调用方按需探测能力（能力接口优于胖端口）。
type ModelDiscoverer interface {
	// DiscoverModels 返回上游模型 id 列表（已排序）。
	DiscoverModels(ctx context.Context) ([]string, error)
}

// ValidateChannel 校验渠道必填字段与协议合法性（C-CH-2）。
// 返回的错误信息包含字段名，供 UI 直接定位。
func ValidateChannel(c Channel) error {
	var missing []string
	if strings.TrimSpace(c.Name) == "" {
		missing = append(missing, "name")
	}
	// baseUrl 仅 Codex 协议可空（上游入口固定，由适配器默认提供）；其余协议要求显式地址，
	// 避免"忘填地址"静默打到官方端点——那是拿用户凭证打错地方。
	if strings.TrimSpace(c.BaseURL) == "" && c.Protocol != ProtocolCodex {
		missing = append(missing, "baseUrl")
	}
	if strings.TrimSpace(c.Model) == "" && len(c.Models) == 0 {
		missing = append(missing, "model")
	}
	if len(missing) > 0 {
		return fmt.Errorf("渠道缺少字段：%s", strings.Join(missing, "、"))
	}
	if !c.Protocol.Valid() {
		return fmt.Errorf("不支持的 protocol %q（当前仅支持 %s）", c.Protocol, ProtocolOpenAI)
	}
	if c.ContextLimit < 0 {
		return fmt.Errorf("渠道 contextLimit 不能为负数（%d）", c.ContextLimit)
	}
	return c.Auth.Validate()
}
