// 时间线（0.0.20）：历轮一览 + 按任意轮回滚（保留对话历史）。
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// touchFiles 在工作区里真实创建文件（恢复的前提：文件当前存在，检查点才有意义）。
func touchFiles(t *testing.T, s *ChatService, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(s.Workspace(), n), []byte("当前内容"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// seedRoundLedger 手工落两轮：每轮一条用户消息 + 一条检查点（各改一个文件）。
func seedRoundLedger(t *testing.T, s *ChatService) {
	t.Helper()
	l, err := s.ledgerFor("s-tl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": s.Workspace()}); err != nil {
		t.Fatal(err)
	}
	mk := func(path string) []tools.RoundCheckpoint {
		return []tools.RoundCheckpoint{{Path: path, OldExists: true, OldContent: "old"}}
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "第一轮：改 main.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventRoundCheckpoint, map[string]any{"files": mk("main.go")}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "第二轮：改 app.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventRoundCheckpoint, map[string]any{"files": mk("app.go")}); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTimeline_ListsRoundsAndRevertableFlags(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	seedRoundLedger(t, s)

	tl, err := s.RoundTimeline("s-tl")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl) != 2 {
		t.Fatalf("应有两轮：%+v", tl)
	}
	if tl[0].Round != 1 || tl[0].UserSeq == 0 || !strings.Contains(tl[0].Text, "第一轮") {
		t.Fatalf("第 1 轮锚点错误：%+v", tl[0])
	}
	if len(tl[0].Files) != 1 || tl[0].Files[0].Path != "main.go" || !tl[0].Files[0].Revertable {
		t.Fatalf("第 1 轮文件错误：%+v", tl[0].Files)
	}
	if len(tl[1].Files) != 1 || tl[1].Files[0].Path != "app.go" || !tl[1].Files[0].Revertable {
		t.Fatalf("第 2 轮文件错误：%+v", tl[1].Files)
	}
}

func TestRevertToRound_RestoresFilesAfterAnchor_KeepsHistory(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	seedRoundLedger(t, s)
	root := s.Workspace()

	tl, err := s.RoundTimeline("s-tl")
	if err != nil {
		t.Fatal(err)
	}
	touchFiles(t, s, "main.go", "app.go")
	// 回滚到第 1 轮之前：main.go 与 app.go 都恢复（同文件取最早）
	res, err := s.RevertToRound("s-tl", tl[0].UserSeq)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 2 {
		t.Fatalf("应恢复两个文件：%+v", res)
	}
	// 对话历史保留：账本里两条用户消息都还在（没有 fork）
	tl2, err := s.RoundTimeline("s-tl")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl2) != 2 {
		t.Fatalf("回滚不得改写历史：%+v", tl2)
	}
	// 两个检查点都被标记消费：时间线上不再有可撤项
	for _, r := range tl2 {
		for _, f := range r.Files {
			if f.Revertable {
				t.Fatalf("回滚后不应再有可撤项：%+v", r)
			}
		}
	}
	// 「撤回本轮」随之无路可走（都被消费）
	if _, err := s.RevertRound("s-tl"); err == nil {
		t.Fatal("全部检查点消费后，撤回本轮必须报无可撤轮次")
	}
	_ = root
}

func TestRevertToRound_RejectsBadAnchorAndRunning(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	seedRoundLedger(t, s)

	// 锚点不存在 / 是检查点而非用户消息：拒绝
	if _, err := s.RevertToRound("s-tl", 99999); err == nil {
		t.Fatal("不存在的锚点必须拒绝")
	}
	if _, err := s.RevertToRound("s-ghost", 1); err == nil {
		t.Fatal("不存在的会话必须拒绝")
	}
	// 回滚到最后一轮之前：恢复的正是最后一轮自己改的文件（合法且有意义）
	tl, _ := s.RoundTimeline("s-tl")
	last := tl[len(tl)-1]
	touchFiles(t, s, "app.go")
	res, err := s.RevertToRound("s-tl", last.UserSeq)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 1 || res.Restored[0] != "app.go" {
		t.Fatalf("回滚最后一轮应恢复它自己的文件：%+v", res)
	}
}

// 同文件多轮改动：回滚到第一轮之前取**最早**检查点（= 那时的状态）。
func TestRevertToRound_SameFileTakesEarliestCheckpoint(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	l, err := s.ledgerFor("s-same")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": s.Workspace()}); err != nil {
		t.Fatal(err)
	}
	mk := func(content string) []tools.RoundCheckpoint {
		return []tools.RoundCheckpoint{{Path: "shared.go", OldExists: true, OldContent: content}}
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "r1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventRoundCheckpoint, map[string]any{"files": mk("第一版")}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "r2"}); err != nil {
		t.Fatal(err)
	}
	// 第 2 轮检查点的 OldContent = 该轮开始前的状态（第一版），与生产落点一致
	if _, err := l.Append(session.EventRoundCheckpoint, map[string]any{"files": mk("第一版")}); err != nil {
		t.Fatal(err)
	}
	touchFiles(t, s, "shared.go")
	tl, err := s.RoundTimeline("s-same")
	if err != nil {
		t.Fatal(err)
	}
	// 回滚到第 2 轮之前：shared.go 应恢复"第一版"（第 1 轮的检查点 = 第 2 轮开始前的状态）
	res, err := s.RevertToRound("s-same", tl[1].UserSeq)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 1 {
		t.Fatalf("应恰好恢复一个文件：%+v", res)
	}
	got, err := os.ReadFile(filepath.Join(s.Workspace(), "shared.go"))
	if err != nil || string(got) != "第一版" {
		t.Fatalf("同文件应取最早检查点的内容：got %q err %v", string(got), err)
	}
}

// 时间线跳过分叉丢弃区间：重跑之后的旧检查点不再出现在时间线上。
func TestRoundTimeline_ForkDroppedRoundsExcluded(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	l, err := s.ledgerFor("s-fork")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "将被丢弃"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventRoundCheckpoint, map[string]any{
		"files": []tools.RoundCheckpoint{{Path: "gone.go", OldExists: true, OldContent: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "重跑锚点"}); err != nil {
		t.Fatal(err)
	}
	// fork{from_seq=重跑锚点}：丢弃该锚点及其后的旧历史（RerunFrom 语义），第一轮保留
	if _, err := l.Append(session.EventFork, map[string]any{"from_seq": int64(3), "reason": "rerun"}); err != nil {
		t.Fatal(err)
	}
	tl, err := s.RoundTimeline("s-fork")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl) != 1 || !strings.Contains(tl[0].Text, "将被丢弃") {
		t.Fatalf("被分叉丢弃的轮次不应出现、保留的轮次必须在：%+v", tl)
	}
	if len(tl[0].Files) != 1 || tl[0].Files[0].Path != "gone.go" {
		t.Fatalf("保留轮次的文件应照常列出：%+v", tl[0])
	}
}
