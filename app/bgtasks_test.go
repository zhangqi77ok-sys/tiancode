package app

import (
	"testing"
)

// 右栏「任务」tab 绑定契约：BgTasksSnapshot 按 sessionID 取该会话 shell 后台任务快照。
// 会话没有 shell 工具集（纯对话 / 还没发过消息）= 空表且不报错——面板轮询不该被
// "没有任务"打断。任务内容本身的投影正确性在 internal/app（经真实 shelltool 全链路）。
func TestBind_BgTasksSnapshot(t *testing.T) {
	b, _ := newBindForTest(t, t.TempDir())
	if got := b.BgTasksSnapshot("s-none"); got == nil || len(got) != 0 {
		t.Fatalf("无工具集的会话应返回空表（非 nil）：got %v", got)
	}
	if got := b.BgTasksSnapshot(""); got == nil || len(got) != 0 {
		t.Fatalf("草稿会话同样返回空表：got %v", got)
	}
}
