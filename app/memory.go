// 记忆管理绑定（0.0.21）：模型能记的，用户必须看得见、删得掉——透明度红线。
package app

import "tiancode/internal/app"

// MemoryLines 返回两级记忆（全局 + 本项目）原文行，供记忆面板展示。
// 纯对话（无工作区）时项目侧为空，不是错误。
func (b *Bind) MemoryLines(sessionID string) (app.MemoryView, error) {
	return b.chat.MemoryLines(sessionID)
}

// MemoryDelete 删除某作用域的第 line 条（1 基）：全局或本项目。
// 非法 scope / 越界显式报错，壳层只转发不吞错。
func (b *Bind) MemoryDelete(sessionID, scope string, line int) error {
	return b.chat.MemoryDelete(sessionID, scope, line)
}

// MemoryClear 清空某作用域的全部记忆（危险动作；前端确认框放行后调用）。
func (b *Bind) MemoryClear(sessionID, scope string) error {
	return b.chat.MemoryClear(sessionID, scope)
}
