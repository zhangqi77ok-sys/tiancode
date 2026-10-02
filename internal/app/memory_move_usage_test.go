// 记忆注入 / 会话迁移 / 用量沉淀的用例测试（0.0.19）。
package app

import (
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
	"tiancode/internal/platform/memory"
)

// ---------- 记忆注入 ----------

// 空记忆不占上下文；写入后进系统提示；两级分别标注；项目记忆不串味；下一轮可见。
func TestChatService_MemorySectionInPreface(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	root := t.TempDir()

	if p := prefaceOf(t, s, root); strings.Contains(p, "长期记忆") {
		t.Fatalf("没有任何记忆时整段不该出现：%s", p)
	}
	if err := s.memory.Append(memory.ScopeGlobal, root, "用户偏好 pnpm，不用 npm"); err != nil {
		t.Fatal(err)
	}
	if err := s.memory.Append(memory.ScopeWorkspace, root, "本项目提交用 conventional commits"); err != nil {
		t.Fatal(err)
	}
	p := prefaceOf(t, s, root)
	if !strings.Contains(p, "## 长期记忆") || !strings.Contains(p, "用户偏好 pnpm") || !strings.Contains(p, "本项目提交用 conventional commits") {
		t.Fatalf("系统提示缺记忆段：%s", p)
	}
	if !strings.Contains(p, "### 全局") || !strings.Contains(p, "### 本项目") {
		t.Fatalf("两级记忆必须分别标注：%s", p)
	}
	// 其他项目的记忆不串味
	other := prefaceOf(t, s, t.TempDir())
	if strings.Contains(other, "本项目提交用 conventional commits") {
		t.Fatalf("项目记忆泄漏到了别的项目：%s", other)
	}
	// 追加后下一轮立即可见（快照按轮取，注入点每轮重新读）
	if err := s.memory.Append(memory.ScopeGlobal, root, "第二条"); err != nil {
		t.Fatal(err)
	}
	if p2 := prefaceOf(t, s, root); !strings.Contains(p2, "第二条") {
		t.Fatalf("下一轮注入必须包含新记忆：%s", p2)
	}
}

// ---------- 会话迁移 ----------

func sameDir(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "\\", "/"), strings.ReplaceAll(b, "\\", "/"))
}

// 迁移后：归属（Meta/侧栏分组）与工具根（SessionWorkspace）同规则切换；
// 移出空间 = 纯对话归属；目标目录不存在当场拒绝；运行中拒绝迁移。
func TestChatService_MoveSessionRehomesRootAndGrouping(t *testing.T) {
	oldRoot, newRoot := t.TempDir(), t.TempDir()
	s := newChannelService(t, Config{})
	defer s.Close()
	// 手工落一个带归属的会话（归属规则与 Send 落的快照同一种事件）
	l, err := s.ledgerFor("s-move")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": oldRoot}); err != nil {
		t.Fatal(err)
	}
	if got := s.SessionWorkspace("s-move"); !sameDir(got, oldRoot) {
		t.Fatalf("前置：归属应为旧目录，got %q", got)
	}
	if err := s.MoveSession("s-move", newRoot); err != nil {
		t.Fatal(err)
	}
	if got := s.SessionWorkspace("s-move"); !sameDir(got, newRoot) {
		t.Fatalf("迁移后工具根应为新目录：got %q want %q", got, newRoot)
	}
	sums, err := s.SessionSummaries()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sm := range sums {
		if sm.ID == "s-move" {
			found = true
			if !sameDir(sm.Workspace, newRoot) {
				t.Fatalf("侧栏分组应跟随迁移：got %q want %q", sm.Workspace, newRoot)
			}
		}
	}
	if !found {
		t.Fatal("迁移后的会话必须在列表里")
	}

	// 移出空间 = 纯对话归属（本地工具下一轮下线）
	if err := s.MoveSession("s-move", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.SessionWorkspace("s-move"); got != "" {
		t.Fatalf("移出空间后归属应为空：got %q", got)
	}
	// 目标目录不存在：当场拒绝，绝不迁进黑洞
	if err := s.MoveSession("s-move", filepath.Join(newRoot, "不存在")); err == nil {
		t.Fatal("迁移到不存在的目录必须拒绝")
	}
}

// 迁移事件落在账本上：move 覆盖首个快照；move 到空串后，后续快照不再改归属。
func TestMeta_WorkspaceMoveOverridesFirstSnapshot(t *testing.T) {
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s-meta")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": "D:/proj/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspaceMove, map[string]string{"path": "D:/proj/b"}); err != nil {
		t.Fatal(err)
	}
	m, err := session.ReadMeta(dir, "s-meta")
	if err != nil {
		t.Fatal(err)
	}
	if m.Workspace != "D:/proj/b" {
		t.Fatalf("move 必须覆盖首个快照：got %q", m.Workspace)
	}
	if _, err := l.Append(session.EventWorkspaceMove, map[string]string{"path": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": "D:/proj/c"}); err != nil {
		t.Fatal(err)
	}
	if m, _ = session.ReadMeta(dir, "s-meta"); m.Workspace != "" {
		t.Fatalf("move 到空串后归属应为空：got %q", m.Workspace)
	}
}

// ---------- 用量沉淀 ----------

// RecordUsage 落账本，SessionSummaries 聚合出来（一轮多行累加）。
func TestChatService_UsageAggregatedInSummaries(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	if _, err := s.ledgerFor("s-usage"); err != nil {
		t.Fatal(err)
	}
	s.RecordUsage("s-usage", 100, 50, 150)
	s.RecordUsage("s-usage", 200, 80, 280)
	sums, err := s.SessionSummaries()
	if err != nil {
		t.Fatal(err)
	}
	var got *SessionSummary
	for i := range sums {
		if sums[i].ID == "s-usage" {
			got = &sums[i]
		}
	}
	if got == nil {
		t.Fatal("会话必须在列表里")
	}
	if got.PromptTokens != 300 || got.CompletionTokens != 130 || got.TotalTokens != 430 {
		t.Fatalf("用量聚合错误：%+v", got)
	}
}
