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
// 为什么第一版只有它：主流网关（OpenAI/DeepSeek/通义/智谱/豆包/Kimi/Ollama）
// 都提供 OpenAI 兼容端点，一个适配器覆盖绝大多数渠道（YAGNI）。
const ProtocolOpenAI Protocol = "openai"

// Valid 报告协议是否已实现。未实现的协议必须显式拒绝，绝不静默降级。
func (p Protocol) Valid() bool { return p == ProtocolOpenAI }

// Channel 是一个模型渠道。
type Channel struct {
	ID       string   `json:"id"`       // 稳定标识（生成后不变，供激活引用）
	Name     string   `json:"name"`     // 用户可见名称
	Protocol Protocol `json:"protocol"` // 协议（决定适配器）
	BaseURL  string   `json:"baseUrl"`  // 网关根地址（含 /v1）
	APIKey   string   `json:"apiKey"`   // 密钥（仅存本机）
	Model    string   `json:"model"`    // 该渠道默认模型
}

// ChannelView 是渠道的外发视图（UI/事件用）：密钥脱敏，只告知是否已配置。
type ChannelView struct {
	Channel
	HasKey bool `json:"hasKey"`
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
	if strings.TrimSpace(c.BaseURL) == "" {
		missing = append(missing, "baseUrl")
	}
	if strings.TrimSpace(c.Model) == "" {
		missing = append(missing, "model")
	}
	if len(missing) > 0 {
		return fmt.Errorf("渠道缺少字段：%s", strings.Join(missing, "、"))
	}
	if !c.Protocol.Valid() {
		return fmt.Errorf("不支持的 protocol %q（当前仅支持 %s）", c.Protocol, ProtocolOpenAI)
	}
	return nil
}
