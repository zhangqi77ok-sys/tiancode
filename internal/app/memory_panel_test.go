// 记忆管理用例测试（0.0.21）：Lines 视图 / Delete 越界拒绝 / Clear 回到"从未记过"。
package app

import (
	"strings"
	"testing"
)

// seedMemory 经真实装配的 memory.Store 落几条两级记忆。
func seedMemory(t *testing.T, s *ChatService, sessionID, ws string) {
	t.Helper()
	seedSession(t, s, sessionID, ws)
	// 直接走包 API（与模型侧 memory 工具同一写路径）
	if err := s.memory.Append("global", "", "回复用中文"); err != nil {
		t.Fatal(err)
	}
	if err := s.memory.Append("workspace", ws, "提交说明用 conventional commits"); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryViewAndDelete(t *testing.T) {
	s, ws := newMiniService(t)
	seedMemory(t, s, "s-mem", ws)

	view, err := s.MemoryLines("s-mem")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Global) != 1 || !strings.Contains(view.Global[0], "中文") {
		t.Fatalf("全局记忆视图不对：%v", view.Global)
	}
	if len(view.Project) != 1 || !strings.Contains(view.Project[0], "conventional") {
		t.Fatalf("项目记忆视图不对：%v", view.Project)
	}

	// 删除项目侧第 1 条后归空；越界显式报错
	if err := s.MemoryDelete("s-mem", "workspace", 1); err != nil {
		t.Fatal(err)
	}
	view, err = s.MemoryLines("s-mem")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Project) != 0 {
		t.Fatalf("删除后项目侧应为空：%v", view.Project)
	}
	if err := s.MemoryDelete("s-mem", "workspace", 1); err == nil {
		t.Fatal("越界删除必须报错")
	}
	// 非法 scope 显式报错
	if err := s.MemoryDelete("s-mem", "bogus", 1); err == nil {
		t.Fatal("非法 scope 必须报错")
	}
}

func TestMemoryClear(t *testing.T) {
	s, ws := newMiniService(t)
	seedMemory(t, s, "s-mem2", ws)

	if err := s.MemoryClear("s-mem2", "global"); err != nil {
		t.Fatal(err)
	}
	view, err := s.MemoryLines("s-mem2")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Global) != 0 {
		t.Fatalf("清空后全局侧应为空：%v", view.Global)
	}
	if len(view.Project) != 1 {
		t.Fatalf("只清了 global，项目侧不得受影响：%v", view.Project)
	}
	// 再清空一次幂等（文件已不存在）
	if err := s.MemoryClear("s-mem2", "global"); err != nil {
		t.Fatalf("重复清空必须幂等：%v", err)
	}
}
