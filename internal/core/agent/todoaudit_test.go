package agent

import (
	"strings"
	"testing"

	"tiancode/internal/core/llm"
)

// C-AGT-15：旧形态（无 files）必须仍能解析——向后兼容降级。
func TestTodoItems_FilesOptional(t *testing.T) {
	items, err := todoItems(`{"items":[{"text":"甲","status":"done"}]}`)
	if err != nil {
		t.Fatalf("旧形态必须仍能解析：%v", err)
	}
	if len(items[0].Files) != 0 {
		t.Fatalf("files 应为空：%+v", items[0])
	}
}

// files 是加法：新形态可解析；空白路径与归一后重复的路径显式拒绝（模型写错要让它知道）。
func TestTodoItems_ParsesFiles(t *testing.T) {
	items, err := todoItems(`{"items":[{"text":"甲","status":"in_progress","files":["a.go","b/c.go"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(items[0].Files) != 2 || items[0].Files[0] != "a.go" {
		t.Fatalf("files 解析错：%+v", items[0])
	}
	if _, err := todoItems(`{"items":[{"text":"甲","status":"done","files":["  "]}]}`); err == nil {
		t.Fatal("空白路径必须拒，不得静默丢弃")
	}
	if _, err := todoItems(`{"items":[{"text":"甲","status":"done","files":["a.go","A.GO"]}]}`); err == nil {
		t.Fatal("归一后重复的路径必须拒——重复声明会让核账口径混乱")
	}
}

// C-AGT-17：路径归一——Windows 语义（大小写不敏感、斜杠等价、去冗余）。
func TestNormalizePathKey(t *testing.T) {
	cases := [][2]string{
		{`internal\app\x.go`, `INTERNAL/APP/X.GO`},
		{`./a/b.go`, `a\b.go`},
		{`a//b.go`, `a/b.go`},
	}
	for _, c := range cases {
		if normalizePathKey(c[0]) != normalizePathKey(c[1]) {
			t.Fatalf("归一不等价：%q vs %q", c[0], c[1])
		}
	}
	if normalizePathKey(`a/b.go`) == normalizePathKey(`a/c.go`) {
		t.Fatal("不同路径不得归一成同一个键")
	}
}

// C-AGT-18：只认 fs 的 write/replace；其它 action、其它工具、解析不出的一律不算。
// 白名单口径与 isReadOnlyCall 同一纪律（解析不出 = 不认定）。
func TestWriteTargetOf(t *testing.T) {
	cases := []struct {
		call llm.ToolCall
		want string
		ok   bool
	}{
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"write","path":"a.go","content":"x"}`}, `a.go`, true},
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"replace","path":"a.go","target":"t","replacement":"r"}`}, `a.go`, true},
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"read","path":"a.go"}`}, ``, false},
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"write"}`}, ``, false},
		{llm.ToolCall{Name: "shell", Arguments: `{"command":"echo x > a.go"}`}, ``, false},
		{llm.ToolCall{Name: "fs", Arguments: `坏 JSON`}, ``, false},
	}
	for _, c := range cases {
		got, ok := writeTargetOf(c.call)
		if ok != c.ok || (ok && got != normalizePathKey(c.want)) {
			t.Fatalf("writeTargetOf(%+v) = (%q,%v)，want (%q,%v)", c.call, got, ok, c.want, c.ok)
		}
	}
}

// C-AGT-16：标 done 且声明 files，但本轮未观察到对其中任一文件的写入 → 计入待纠偏。
func TestUnaudited_DetectsUnwrittenFiles(t *testing.T) {
	l := NewLoop(nil, "m", nil)
	l.written["b.go"] = true

	items := []llm.TodoItem{
		{Text: "已动过", Status: "done", Files: []string{"b.go"}},          // 全写过 → 不报
		{Text: "没动过", Status: "done", Files: []string{"a.go"}},          // 一个没写 → 报
		{Text: "写了一半", Status: "done", Files: []string{"b.go", "c.go"}}, // 部分没写 → 报
		{Text: "没声明", Status: "done"},                                   // 不声明 → 不报
		{Text: "还没做", Status: "in_progress", Files: []string{"z.go"}},   // 非 done → 不报
	}
	bad := l.unaudited(items)
	if len(bad) != 2 {
		t.Fatalf("应报出 2 项，实际 %d：%v", len(bad), bad)
	}
	joined := strings.Join(bad, "；")
	if !strings.Contains(joined, "没动过") || !strings.Contains(joined, "写了一半") {
		t.Fatalf("报出的条目不对：%v", bad)
	}
	if strings.Contains(joined, "已动过") || strings.Contains(joined, "没声明") || strings.Contains(joined, "还没做") {
		t.Fatalf("不该报的报了：%v", bad)
	}
}
