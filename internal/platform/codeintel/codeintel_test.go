// codeintel 用例：vet 输出解析、真实 go 工具链端到端、大纲（parser 与启发式）。
package codeintel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVetLine(t *testing.T) {
	cases := []struct {
		line string
		want Diagnostic
		ok   bool
	}{
		{"internal/foo/bar.go:12:5: undefined: X", Diagnostic{File: "internal/foo/bar.go", Line: 12, Col: 5, Message: "undefined: X"}, true},
		{"main.go:9: undefined: y", Diagnostic{File: "main.go", Line: 9, Message: "undefined: y"}, true},                                        // 无 col
		{`vet.exe: .\broken.go:3:24: undefined: missing`, Diagnostic{File: "broken.go", Line: 3, Col: 24, Message: "undefined: missing"}, true}, // Windows 实测形态：exe 前缀 + .\ 相对
		{`C:\proj\pkg\a.go:3:1: syntax error`, Diagnostic{File: "pkg/a.go", Line: 3, Col: 1, Message: "syntax error"}, true},                    // 盘符重组 + Rel 到 root 内相对路径
		{"# tiancode/internal/foo", Diagnostic{}, false},
		{"not a diagnostic line", Diagnostic{}, false},
		{"a.go:0:1: zero line", Diagnostic{}, false},
	}
	for _, c := range cases {
		got, ok := parseVetLine(c.line, `C:\proj`)
		if ok != c.ok {
			t.Fatalf("parseVetLine(%q) ok=%v, want %v", c.line, ok, c.ok)
		}
		if ok && got != c.want {
			t.Fatalf("parseVetLine(%q) = %+v, want %+v", c.line, got, c.want)
		}
	}
}

func TestParseVetOutput_MergesContinuation(t *testing.T) {
	out := "# tiancode/internal/foo\n" +
		"internal/foo/a.go:10:2: undefined: Foo\n" +
		"\tdetails: maybe imported but not used\n"
	ds := parseVetOutput(out, ".")
	if len(ds) != 1 {
		t.Fatalf("应归并为 1 条，实际 %d：%+v", len(ds), ds)
	}
	if !strings.Contains(ds[0].Message, "details") {
		t.Fatalf("续行应并入消息：%q", ds[0].Message)
	}
}

// 真实工具链端到端：坏包必须报出 undefined，干净包必须零诊断。
// 两个包分开（bad/ 子包 vs 根包）——vet 按包诊断，同包会互相看见对方的错误。
func TestDiagnose_BrokenAndClean(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad", "broken.go"), []byte("package bad\n\nfunc Broken() { return missing }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.go"), []byte("package fixture\n\nfunc OK() int { return 42 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Diagnose(context.Background(), root, filepath.Join("bad", "broken.go"), ManualCap)
	if !r.Attempted || len(r.Diagnostics) == 0 {
		t.Fatalf("坏包必须报诊断：%+v", r)
	}
	if !strings.Contains(r.Diagnostics[0].Message, "undefined") {
		t.Fatalf("诊断应含 undefined：%+v", r.Diagnostics[0])
	}
	if r.Diagnostics[0].Line != 3 {
		t.Fatalf("行号应指向 return 行 3，实际 %+v", r.Diagnostics[0])
	}
	if r.Diagnostics[0].File != "bad/broken.go" {
		t.Fatalf("路径应归一为工作区相对正斜杠：%+v", r.Diagnostics[0])
	}
	clean := Diagnose(context.Background(), root, "ok.go", ManualCap)
	if !clean.Attempted || len(clean.Diagnostics) != 0 || clean.Note != "" {
		t.Fatalf("干净包必须零诊断零注记：%+v", clean)
	}
}

func TestDiagnose_GracefulSkips(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct {
		rel, reason string
	}{
		{"a.ts", "仅支持 Go"},
		{filepath.Join("testdata", "x.go"), "testdata"},
		{filepath.Join("vendor", "x.go"), "vendor"},
	} {
		r := Diagnose(context.Background(), root, c.rel, ManualCap)
		if r.Attempted || !strings.Contains(r.Note, c.reason) {
			t.Fatalf("Diagnose(%q) 应跳过并说明 %q：%+v", c.rel, c.reason, r)
		}
	}
	// 无 go.mod
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Diagnose(context.Background(), root, "x.go", ManualCap)
	if r.Attempted || !strings.Contains(r.Note, "go.mod") {
		t.Fatalf("非 Go module 应优雅跳过：%+v", r)
	}
}

// 机制性失败 ≠ 通过：嵌套 module 下 vet 异常退出且无可解析诊断，必须 Inconclusive
// （旧实现把它当"编译诊断通过"，把"没跑成"说成"没错误"——审查抓到）。
func TestDiagnose_InconclusiveOnNestedModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module outer\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "go.mod"), []byte("module inner\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "ok.go"), []byte("package inner\n\nfunc OK() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Diagnose(context.Background(), root, filepath.Join("nested", "ok.go"), ManualCap)
	if !r.Attempted || !r.Inconclusive || len(r.Diagnostics) != 0 {
		t.Fatalf("嵌套 module 应 Inconclusive 而不是误报通过：%+v", r)
	}
	if got := Format(r); !strings.Contains(got, "不可判定") {
		t.Fatalf("Format 应明说不可判定：%q", got)
	}
}

func TestFormat_CleanAndSorted(t *testing.T) {
	if got := Format(Result{Attempted: true}); got != "编译诊断通过（go vet，含 _test.go）" {
		t.Fatalf("干净话术不符：%q", got)
	}
	res := Result{Attempted: true, Diagnostics: []Diagnostic{
		{File: "b.go", Line: 2, Message: "m2"},
		{File: "a.go", Line: 9, Message: "m9"},
		{File: "a.go", Line: 1, Message: "m1"},
	}}
	got := Format(res)
	if !strings.Contains(got, "[编译诊断] 3 处：") {
		t.Fatalf("缺计数头：%q", got)
	}
	if strings.Index(got, "a.go:1") > strings.Index(got, "a.go:9") {
		t.Fatalf("同文件应按行号排序：%q", got)
	}
	if strings.Index(got, "a.go:1") > strings.Index(got, "b.go:2") {
		t.Fatalf("应按文件排序：%q", got)
	}
}

func TestSymbols_GoOutline(t *testing.T) {
	src := []byte(`package demo

import "fmt"

const Limit = 3

type Tool struct{ root string }

func (t *Tool) Run(n int) error { return nil }

func helper() {}
`)
	ss, note := Symbols("a.go", src)
	if note != "" {
		t.Fatalf("Go 大纲不应有注记：%q", note)
	}
	want := []struct{ kind, text string }{
		{"package", "demo"},
		{"const", "Limit"},
		{"type", "Tool struct"},
		{"method", "func (t *Tool) Run(n int) error"},
		{"func", "func helper()"},
	}
	if len(ss) != len(want) {
		t.Fatalf("条目数不符（import 不进大纲）：%+v", ss)
	}
	for i, w := range want {
		if ss[i].Kind != w.kind || !strings.Contains(ss[i].Text, w.text) {
			t.Fatalf("第 %d 条 = %s %q, want %s~%q", i, ss[i].Kind, ss[i].Text, w.kind, w.text)
		}
	}
	// 行号必须可跳 read：helper 在第 11 行
	if ss[len(ss)-1].Line != 11 {
		t.Fatalf("func helper 行号应为 11：%+v", ss[len(ss)-1])
	}
}

func TestSymbols_GoSyntaxErrorStillOutlines(t *testing.T) {
	src := []byte("package demo\n\nfunc Good() {}\nfunc Bad( {}}\n")
	ss, _ := Symbols("bad.go", src)
	// Good 的签名必须可用；坏函数取不到签名容许为 "?"（错误恢复的部分 AST），
	// 但条目本身要保留（模型至少知道这里有个函数）。
	foundGood, foundBad := false, false
	for _, s := range ss {
		if s.Text == "func Good()" {
			foundGood = true
		}
		if s.Kind == "func" && strings.HasPrefix(s.Text, "func Bad") {
			foundBad = true
		}
	}
	if !foundGood {
		t.Fatalf("语法错误也应给出完好函数的大纲：%+v", ss)
	}
	if !foundBad {
		t.Fatalf("坏函数条目应保留：%+v", ss)
	}
}

func TestSymbols_RegexFallbackAndUnknown(t *testing.T) {
	ts := []byte("export function main() {}\nexport class Widget {}\nconst x = 1;\n")
	ss, note := Symbols("app.ts", ts)
	if note == "" || len(ss) != 2 {
		t.Fatalf("ts 启发式应报 function+class 并带注记：%+v %q", ss, note)
	}
	md := []byte("# Title\n\ntext\n\n## Section\n")
	ss, _ = Symbols("r.md", md)
	if len(ss) != 2 || ss[0].Kind != "heading" {
		t.Fatalf("md 标题大纲不符：%+v", ss)
	}
	if ss, note := Symbols("x.ini", []byte("key=val\n")); ss != nil || note == "" {
		t.Fatalf("未知扩展名应明说不支持：%+v %q", ss, note)
	}
}

func TestSymbols_Bounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := 0; i < symbolLimit+50; i++ {
		fmt.Fprintf(&b, "func F%d() {}\n\n", i)
	}
	ss, _ := Symbols("big.go", []byte(b.String()))
	if len(ss) > symbolLimit {
		t.Fatalf("大纲应有界（≤%d），实际 %d", symbolLimit, len(ss))
	}
}
