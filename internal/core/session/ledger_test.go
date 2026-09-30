package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestLedger 创建临时目录下的测试账本。
func newTestLedger(t *testing.T) (*Ledger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := OpenLedger(dir, "s1")
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	return l, dir
}

func ledgerPath(dir string) string { return filepath.Join(dir, "s1.jsonl") }

// C-SES-1：追加成功 = 事件行已落盘（含换行），账本即事实源。
func TestLedger_AppendBeforeAdvance(t *testing.T) {
	l, dir := newTestLedger(t)
	defer l.Close()

	ev, err := l.Append(EventUserMessage, map[string]string{"text": "hello"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if ev.Seq() != 1 {
		t.Fatalf("first seq = %d, want 1", ev.Seq())
	}

	raw, err := os.ReadFile(ledgerPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("event line must end with newline, got %q", raw)
	}
	if !strings.Contains(string(raw), `"kind":"user_message"`) {
		t.Fatalf("line missing kind field: %s", raw)
	}
}

// C-SES-2：重放按 Seq 严格有序，结果与写入顺序一致。
func TestLedger_ReplayOrder(t *testing.T) {
	l, dir := newTestLedger(t)
	defer l.Close() // Windows: 未关闭的句柄会阻止 TempDir 清理
	kinds := []EventKind{EventUserMessage, EventAssistantDelta, EventTurnEnd}
	for i, k := range kinds {
		if _, err := l.Append(k, map[string]int{"i": i}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	l2, err := OpenLedger(dir, "s1")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()

	var got []EventKind
	var seqs []int64
	err = l2.Replay(func(ev Event) error {
		got = append(got, ev.Kind())
		seqs = append(seqs, ev.Seq())
		return nil
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("replayed %d events, want 3", len(got))
	}
	for i := range kinds {
		if got[i] != kinds[i] {
			t.Fatalf("event %d kind = %s, want %s", i, got[i], kinds[i])
		}
		if seqs[i] != int64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, seqs[i], i+1)
		}
	}
}

// C-SES-3：尾部半行（写入中断）→ 重放只含完整事件，后续追加序号不冲突。
func TestLedger_ReplayTornTail(t *testing.T) {
	l, dir := newTestLedger(t)
	defer l.Close() // Windows: 未关闭的句柄会阻止 TempDir 清理
	for i := 0; i < 2; i++ {
		if _, err := l.Append(EventUserMessage, map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	// 模拟崩溃时的半行写入：无换行符的不完整 JSON
	f, err := os.OpenFile(ledgerPath(dir), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":3,"kind":"user_messa`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	l2, err := OpenLedger(dir, "s1")
	if err != nil {
		t.Fatalf("reopen with torn tail: %v", err)
	}
	defer l2.Close()

	n := 0
	err = l2.Replay(func(ev Event) error { n++; return nil })
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 2 {
		t.Fatalf("replayed %d events after torn tail, want 2", n)
	}
	// 截断后追加，序号必须接续为 3（不允许与半行声明冲突或跳号）
	ev, err := l2.Append(EventUserMessage, map[string]int{"after": 1})
	if err != nil {
		t.Fatalf("append after repair: %v", err)
	}
	if ev.Seq() != 3 {
		t.Fatalf("seq after repair = %d, want 3", ev.Seq())
	}
}

// C-SES-4：追加失败必须返回错误，禁止吞掉。
func TestLedger_AppendErrorPropagates(t *testing.T) {
	l, _ := newTestLedger(t)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventUserMessage, nil); err == nil {
		t.Fatal("append after close must return error")
	}
}

// 第 1 批：增量合批——窗口内连续 delta 不逐条 fsync；关键事件立即刷（含此前攒批）。
// 断言用"远小于 N"而非精确 0：机器慢时窗口提前到期的少量刷盘是合法行为，
// 但不能退化成"每条都刷"（那正是本改动的修复对象）。
func TestLedger_DeltaBatching(t *testing.T) {
	l, dir := newTestLedger(t)
	defer l.Close()

	for i := 0; i < 200; i++ {
		if _, err := l.Append(EventAssistantDelta, map[string]string{"text": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if l.syncCount >= 20 {
		t.Fatalf("窗口内 200 条增量触发了 %d 次 fsync（合批失效）", l.syncCount)
	}

	// 关键事件（终态）必须立即落盘，并把此前攒批一并刷掉
	if _, err := l.Append(EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ledgerPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), `"kind":"assistant_delta"`); n != 200 {
		t.Fatalf("终态落盘后应有 200 条增量行，实际 %d", n)
	}
	if !strings.Contains(string(raw), `"kind":"turn_end"`) {
		t.Fatal("终态事件必须已落盘")
	}
}

// 第 1 批：用户消息/工具调用与结果/todo/助手锚点/终态/错误一律立即 fsync
// （崩溃最多丢最后一小段增量，绝不丢已完成的事实）。
func TestLedger_CriticalKindsFlushImmediately(t *testing.T) {
	l, dir := newTestLedger(t)
	defer l.Close()

	kinds := []EventKind{EventUserMessage, EventToolCall, EventToolResult, EventTodo, EventAssistantMsg, EventTurnEnd, EventError}
	for i, k := range kinds {
		before := l.syncCount
		if _, err := l.Append(k, map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
		if l.syncCount != before+1 {
			t.Fatalf("%s 未立即 fsync（syncCount %d → %d）", k, before, l.syncCount)
		}
		raw, err := os.ReadFile(ledgerPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), string(k)) {
			t.Fatalf("%s 未落盘", k)
		}
	}
}

// 第 1 批：Close 先刷攒批（进程正常退出不丢已确认的增量）。
func TestLedger_CloseFlushesPendingDeltas(t *testing.T) {
	l, dir := newTestLedger(t)
	for i := 0; i < 3; i++ {
		if _, err := l.Append(EventAssistantDelta, map[string]string{"text": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ledgerPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), `"kind":"assistant_delta"`); n != 3 {
		t.Fatalf("Close 应刷掉 3 条增量，实际 %d", n)
	}
}

// C-SES-6：轮内崩溃 → 重放恢复到最后一条完整事件（assistant_message 锚点）。
func TestLedger_CrashReplayRecovery(t *testing.T) {
	l, dir := newTestLedger(t)
	defer l.Close() // "崩溃"语义由 fsync 保证；关闭仅为释放 Windows 文件句柄
	if _, err := l.Append(EventUserMessage, map[string]string{"text": "q"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventAssistantMsg, map[string]string{"text": "a"}); err != nil {
		t.Fatal(err)
	}
	// 模拟崩溃：不调用 Close，直接以新实例重新打开读取磁盘状态
	l2, err := OpenLedger(dir, "s1")
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer l2.Close()

	var last Event
	err = l2.Replay(func(ev Event) error { last = ev; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if last == nil || last.Kind() != EventAssistantMsg {
		t.Fatalf("last recovered event = %v, want assistant_message", last)
	}
	if last.Seq() != 2 {
		t.Fatalf("last seq = %d, want 2", last.Seq())
	}
}
