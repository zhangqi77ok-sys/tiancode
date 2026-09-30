package agent

import (
	"fmt"

	"tiancode/internal/core/llm"
)

// todoTrack 跟踪本轮的 todo 快照是否过期（阶段 4-1）。
//
// 为什么需要：todo 是模型自己维护的**全量快照**，实机观察是"只交一次、之后干活不再回写"
// （用户截图：清单停在 0/4 已完成，模型其实一直在干活）——界面显示的是假进度。
// 这里不改 todo 工具的对外契约（仍是全量快照），只在快照过期时**在下一步的请求里**
// 让它重交一次；提醒只存在于本轮内存消息：不落账本、不进系统提示（系统提示必须逐字稳定，
// 否则 prompt cache 每轮失效）。
type todoTrack struct {
	items    []llm.TodoItem // 上次快照；nil = 本轮还没交过清单
	dirty    bool           // 交过快照之后又干过活（快照可能已过期）
	reminded bool           // 这张快照已经提醒过一次：同一张快照不反复催
}

// observe 记下一次新快照（模型调用 todo 且校验通过）：进度已回写，提醒状态清零。
func (t *todoTrack) observe(items []llm.TodoItem) {
	t.items = items
	t.dirty = false
	t.reminded = false
}

// markWork 标记"交过快照之后又干过活"；todo 自身的回写不算活（不算进展）。
func (t *todoTrack) markWork() {
	if t.items != nil {
		t.dirty = true
	}
}

// consumeRefresh 取"该提醒模型重交清单"的通知：只在快照过期（之后干过活）且这张快照
// 还没催过时给一次。返回空串 = 不需要提醒。
func (t *todoTrack) consumeRefresh() string {
	if t.items == nil || !t.dirty || t.reminded {
		return ""
	}
	t.dirty = false
	t.reminded = true
	return fmt.Sprintf("（本轮提醒，不是用户原话）任务清单还是上一次提交的快照（%d 项），但你已经继续执行过了："+
		"进度有变化就用 todo 工具重新提交**整张清单**（全量快照，不要只发变化项），让界面上的进度与实际一致；"+
		"确实没有变化就不用重交。", len(t.items))
}
