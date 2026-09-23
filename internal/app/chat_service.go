// Package app 是用例编排层：组装内核完成用户可见用例，并把错误上抛到 UI。
//
// 做什么：ChatService 编排"对话流式"用例（发送消息 → 驱动 agent → 流式回传 → 落账本），
// SessionService 编排会话恢复用例（打开账本 → 重放 → 投影）。
// 被谁依赖：壳层（M2 的 Wails 绑定 app/，未来的 daemon 网关）。
// 依赖谁：internal/core/*；禁止直接执行文件/进程/网络 IO（见 docs/STANDARDS.md 分层表）。
//
// 契约红线：任何持久化/流式错误都必须上抛到 UI（禁止 `_ =` 丢弃）——
// 旧实现 app_chat.go:282/503 静默吞错导致丢消息用户无感，此为 arch_check R2 守卫红线。
package app

// ChatService 编排对话用例。M2 实现时注入 agent.Loop、llm.ProviderPort 与
// session 账本（构造期注入）；当前占位以固定包边界与依赖方向。
type ChatService struct{}

// NewChatService 构造对话用例服务。
func NewChatService() *ChatService { return &ChatService{} }
