package app

import "tiancode/internal/platform/catalog"

// GetExtensions 返回本机 MCP 与 Skill 清单。
func (b *Bind) GetExtensions() (catalog.File, error) {
	return b.chat.Extensions()
}

// SaveExtensions 保存 MCP 与 Skill 清单。启用项会在下一轮对话注入模型。
func (b *Bind) SaveExtensions(f catalog.File) error {
	return b.chat.SaveExtensions(f)
}
