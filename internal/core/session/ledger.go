// Package session 定义会话事件账本与崩溃恢复契约。
//
// 做什么：以追加式 JSONL 事件账本记录会话全过程；
// 崩溃恢复 = 顺序重放到最后一个完整事件，半行（写入中断）直接截断。
// 被谁依赖：internal/app（用例编排）。
// 依赖谁：仅 stdlib。
//
// 设计取舍见 docs/adr/0002-event-log-session.md：
// 旧实现每条消息对 JSON 全量读-改-写（store.go），Windows rename 冲突时
// 静默丢消息（app_chat.go `_ =` 吞错）；事件账本把最坏情况从"丢任意消息"
// 降级为"丢最后一个未写完的事件"，且恢复语义可测。
package session

// EventKind 是事件类型枚举。M1 实现时按需补齐，但一经发布不得改名
// （账本文件按 Kind 字符串持久化，改名=旧账本不可重放）。
type EventKind string

const (
	EventSessionStart    EventKind = "session_start"
	EventUserMessage     EventKind = "user_message"
	EventAssistantDelta  EventKind = "assistant_delta"   // 流式文本增量
	EventAssistantMsg    EventKind = "assistant_message" // 一条完整助手消息（恢复锚点）
	EventToolCall        EventKind = "tool_call"
	EventToolResult      EventKind = "tool_result"
	EventTurnEnd         EventKind = "turn_end"
	EventError           EventKind = "error"
)

// SessionEvent 是账本中的最小事件单元。
// 实现契约：Seq 在单个账本内单调递增；写入方必须持久化成功后才推进内存状态
// （write-ahead：账本即事实源，内存只是缓存）。
type SessionEvent interface {
	// Seq 返回事件在账本中的序号（从 1 开始）。
	Seq() int64
	// Kind 返回事件类型。
	Kind() EventKind
	// Encode 输出该事件的单行 JSON（JSONL 账本的一行，不含换行符）。
	Encode() ([]byte, error)
}
