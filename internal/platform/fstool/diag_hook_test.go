// 编译诊断钩子（C-FS-8/9）与 symbols 大纲（C-FS-10）的工具层集成用例。
// 真跑 go 工具链（fixture 模块），验证"写入→当次回执内联诊断"整条链。
package fstool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newModuleTool 构造一个 Go module 形态的工作区工具（go.mod 落在工具根内；
// 用 tool.Root() 保证与 New 的 EvalSymlinks 归一是同一路径）。
func newModuleTool(t *testing.T) *Tool {
	t.Helper()
	tool := newTool(t)
	if err := os.WriteFile(filepath.Join(tool.Root(), "go.mod"), []byte("module fixture\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestWrite_AutoDiagnoseInline(t *testing.T) {
	tool := newModuleTool(t)
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action":  "write",
		"path":    "broken.go",
		"content": "package fixture\n\nfunc Broken() { return missing }\n",
	}))
	if err != nil {
		t.Fatalf("机制错误：%v", err)
	}
	if res.IsError {
		t.Fatalf("写入本身必须成功（诊断不改写成功语义）：%s", res.Content)
	}
	if !strings.Contains(res.Content, "[编译诊断]") || !strings.Contains(res.Content, "undefined: missing") {
		t.Fatalf("坏代码应内联诊断：%q", res.Content)
	}
	if !strings.Contains(res.Content, "broken.go:3") {
		t.Fatalf("诊断应带可点 path:line：%q", res.Content)
	}
}

func TestWrite_AutoDiagnoseSilentOnClean(t *testing.T) {
	tool := newModuleTool(t)
	res, _ := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action":  "write",
		"path":    "ok.go",
		"content": "package fixture\n\nfunc OK() int { return 42 }\n",
	}))
	if res.IsError || strings.Contains(res.Content, "编译诊断") {
		t.Fatalf("干净写入应静默（不制造噪音）：%+v", res)
	}
}

func TestWrite_AutoDiagnoseSilentNonModule(t *testing.T) {
	tool := newTool(t) // 无 go.mod
	res, _ := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action":  "write",
		"path":    "a.go",
		"content": "package x\n",
	}))
	if res.IsError || strings.Contains(res.Content, "编译诊断") {
		t.Fatalf("非 module 工作区自动诊断应静默：%+v", res)
	}
}

func TestWrite_AutoDiagnoseNonGoSilent(t *testing.T) {
	tool := newModuleTool(t)
	res, _ := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action":  "write",
		"path":    "a.ts",
		"content": "export const x = 1;\n",
	}))
	if res.IsError || strings.Contains(res.Content, "编译诊断") {
		t.Fatalf("非 Go 文件不触发自动诊断：%+v", res)
	}
}

func TestDiagnoseAction_Manual(t *testing.T) {
	tool := newModuleTool(t)
	// 坏代码放 bad/ 子包：vet 按包诊断，与根包的 ok.go 互不可见（否则"诊断 ok.go"
	// 也会看到 broken.go 的错——那是正确行为，fixture 不能同包）
	if _, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action":  "write",
		"path":    "bad/broken.go",
		"content": "package bad\n\nfunc Broken() { return missing }\n",
	})); err != nil {
		t.Fatal(err)
	}
	res, _ := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "diagnose", "path": "bad/broken.go"}))
	if res.IsError || !strings.Contains(res.Content, "undefined") {
		t.Fatalf("手动诊断应报错并说明：%+v", res)
	}
	if _, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action":  "write",
		"path":    "ok.go",
		"content": "package fixture\n\nfunc OK() int { return 42 }\n",
	})); err != nil {
		t.Fatal(err)
	}
	res, _ = tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "diagnose", "path": "ok.go"}))
	if res.IsError || !strings.Contains(res.Content, "编译诊断通过") {
		t.Fatalf("干净包应明说通过：%+v", res)
	}
	res, _ = tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "diagnose", "path": "a.ts"}))
	if res.IsError || !strings.Contains(res.Content, "仅支持 Go") {
		t.Fatalf("非 Go 应明说局限：%+v", res)
	}
}

func TestSymbolsAction(t *testing.T) {
	tool := newModuleTool(t)
	if _, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action":  "write",
		"path":    "svc.go",
		"content": "package fixture\n\ntype Svc struct{}\n\nfunc (s *Svc) Run(n int) error { return nil }\n",
	})); err != nil {
		t.Fatal(err)
	}
	res, _ := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "symbols", "path": "svc.go"}))
	if res.IsError {
		t.Fatalf("大纲不应失败：%s", res.Content)
	}
	for _, want := range []string{"L3 type Svc", "L5 method func (s *Svc) Run(n int) error"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("大纲缺 %q：%q", want, res.Content)
		}
	}
	res, _ = tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "symbols", "path": "x.ini"}))
	if !res.IsError {
		t.Fatalf("无大纲引擎的扩展名应显式报错：%+v", res)
	}
}
