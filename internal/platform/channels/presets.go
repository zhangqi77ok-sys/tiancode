package channels

import "tiancode/internal/core/llm"

// Preset 是内置渠道模板（纯数据、只读、不落库）。
// 借 dsh-java channels/presets 的设计：加供应商 = 加一条数据，不改代码。
// 为什么全部是 ProtocolOpenAI：各家的"OpenAI 兼容端点"已被广泛支持，
// 一个适配器即可覆盖；差异只在 BaseURL 与模型名。
type Preset struct {
	Key            string       // 模板键（稳定标识）
	Name           string       // 展示名
	Protocol       llm.Protocol // 协议（当前恒为 openai）
	BaseURL        string       // 默认网关地址（custom 为空由用户填）
	SuggestedModel string       // 建议模型（用户可改）
}

// Presets 返回内置模板列表。顺序即 UI 展示顺序（主流优先，自定义兜底）。
func Presets() []Preset {
	return []Preset{
		{Key: "openai", Name: "OpenAI", Protocol: llm.ProtocolOpenAI,
			BaseURL: "https://api.openai.com/v1", SuggestedModel: "gpt-4o-mini"},
		{Key: "deepseek", Name: "DeepSeek", Protocol: llm.ProtocolOpenAI,
			BaseURL: "https://api.deepseek.com/v1", SuggestedModel: "deepseek-chat"},
		{Key: "qwen", Name: "通义千问", Protocol: llm.ProtocolOpenAI,
			BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", SuggestedModel: "qwen-plus"},
		{Key: "zhipu", Name: "智谱 GLM", Protocol: llm.ProtocolOpenAI,
			BaseURL: "https://open.bigmodel.cn/api/paas/v4", SuggestedModel: "glm-4-plus"},
		{Key: "doubao", Name: "豆包（火山方舟）", Protocol: llm.ProtocolOpenAI,
			BaseURL: "https://ark.cn-beijing.volces.com/api/v3", SuggestedModel: "doubao-pro-32k"},
		{Key: "kimi", Name: "Kimi（Moonshot）", Protocol: llm.ProtocolOpenAI,
			BaseURL: "https://api.moonshot.cn/v1", SuggestedModel: "moonshot-v1-8k"},
		{Key: "ollama", Name: "Ollama（本地）", Protocol: llm.ProtocolOpenAI,
			BaseURL: "http://localhost:11434/v1", SuggestedModel: "qwen2.5-coder"},
		{Key: "custom", Name: "自定义网关", Protocol: llm.ProtocolOpenAI,
			BaseURL: "", SuggestedModel: ""},
	}
}
