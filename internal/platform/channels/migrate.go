package channels

import (
	"strings"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/configfile"
)

// MigrateFromConfig 由旧 config.json 生成首个渠道（C-CH-1：首次运行免二次配置）。
// 源配置缺少关键字段或是模板占位符时不迁移（返回 false），由调用方走"首次运行引导"。
func MigrateFromConfig(src configfile.File) (Channel, bool) {
	if src.BaseURL == "" || src.Model == "" {
		return Channel{}, false
	}
	if looksLikePlaceholder(src.BaseURL, src.Model, src.APIKey) {
		return Channel{}, false
	}
	// 密钥可以缺失（部分本地网关如 Ollama 不需要），其余字段必须有
	return Channel{
		ID:         NewID(),
		Name:       "默认渠道（由配置文件迁移）",
		Type:       string(llm.ProtocolOpenAI),
		BaseURL:    src.BaseURL,
		Credential: src.APIKey,
		Models:     []string{src.Model},
		Groups:     []string{DefaultGroup},
		Status:     StatusEnabled,
		Priority:   100,
	}, true
}

// placeholderHints 是配置模板里的占位符特征（模板值必须原样保留在文档里，
// 但绝不能变成"真渠道"——实机事故：首启迁移出 your-gateway.example 假渠道，
// 用户看到"有渠道"却连不通，比没有渠道更难排查）。
var placeholderHints = []string{"your-", "placeholder", ".example", "change-me", "sk-xxxx"}

// looksLikePlaceholder 判断配置项是否为模板占位符（大小写不敏感）。
func looksLikePlaceholder(values ...string) bool {
	for _, v := range values {
		lower := strings.ToLower(strings.TrimSpace(v))
		for _, hint := range placeholderHints {
			if strings.Contains(lower, hint) {
				return true
			}
		}
	}
	return false
}
