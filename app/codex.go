// Codex（ChatGPT 订阅）账号授权与凭证绑定的 IPC 绑定。
// 凭证纪律：授权换取的 token 绝不经过前端——绑定动作在编排层一次完成，
// 前端只拿脱敏摘要（CodexBindResult.Display）。
package app

import (
	"tiancode/internal/app"
	"tiancode/internal/platform/codexauth"
)

// StartCodexOAuth 发起 ChatGPT 账号授权，返回授权链接与会话 ID。
// Listening=true 时本机 1455 回环已就绪：浏览器授权完成后自动生效（前端轮询即可）。
func (b *Bind) StartCodexOAuth() (codexauth.StartInfo, error) {
	return b.chat.StartCodexOAuth()
}

// PollCodexOAuth 查询授权状态（pending/done/error）。
func (b *Bind) PollCodexOAuth(sessionID string) (codexauth.Status, error) {
	return b.chat.PollCodexOAuth(sessionID)
}

// BindCodexOAuth 完成授权绑定：channelID 空 = 新建 Codex 渠道；
// codeOrURL 非空 = 手动粘贴回调兜底（回环不可用时的通道）。
func (b *Bind) BindCodexOAuth(sessionID string, channelID string, name string, codeOrURL string) (app.CodexBindResult, error) {
	return b.chat.BindCodexOAuth(b.appCtx(), sessionID, channelID, name, codeOrURL)
}

// ImportCodexCredential 从粘贴内容（auth.json / access_token / refresh_token）导入并绑定渠道。
func (b *Bind) ImportCodexCredential(channelID string, name string, raw string) (app.CodexBindResult, error) {
	return b.chat.ImportCodexCredential(b.appCtx(), channelID, name, raw)
}

// CodexCredentialOf 返回渠道的 ChatGPT 账号绑定摘要（渠道管理回显"已绑定 xx"）。
func (b *Bind) CodexCredentialOf(channelID string) (app.CodexCredentialInfo, error) {
	return b.chat.CodexCredentialOf(channelID), nil
}
