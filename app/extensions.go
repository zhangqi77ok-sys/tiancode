package app

import (
	"context"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"tiancode/internal/platform/catalog"
)

// ProbeMcpServer 列出一台已启用 MCP 服务器公布的工具名（第 8 批：/ 菜单里当场选工具）。
// 只读探测：不写账本、不产生工具卡；远程服务器返回现成的「不支持自动列工具」错误原文。
func (b *Bind) ProbeMcpServer(name string) ([]string, error) {
	return b.chat.ProbeMcpServer(context.Background(), name)
}

// GetExtensions 返回本机 MCP 与 Skill 清单。
func (b *Bind) GetExtensions() (catalog.File, error) {
	return b.chat.Extensions()
}

// SaveExtensions 保存 MCP 与 Skill 清单。启用项会在下一轮对话注入模型；
// 保存成功即广播 extensions:changed——管理面板开着时实时刷新（AI 通过
// ext_manage 添加的扩展同样经这里落盘，用户不必重启/重开面板）。
func (b *Bind) SaveExtensions(f catalog.File) error {
	if err := b.chat.SaveExtensions(f); err != nil {
		return err
	}
	wruntime.EventsEmit(b.appCtx(), "extensions:changed")
	return nil
}
