package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 前端诊断（0.0.42）：分派与跳过。
func TestWebDiag_SkipsWithoutProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("const x: number = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Diagnose(context.Background(), dir, "a.ts", time.Second)
	if r.Attempted {
		t.Fatalf("无工程应跳过：%+v", r)
	}
	if !strings.Contains(r.Note, "跳过类型诊断") {
		t.Fatalf("跳过应说明原因：%q", r.Note)
	}
	// 非前端扩展名仍走 go vet 侧（Note 是 Go 的跳过话术）
	r = Diagnose(context.Background(), dir, "a.py", time.Second)
	if strings.Contains(r.Note, "类型诊断") {
		t.Fatalf("非前端文件不应进类型诊断：%q", r.Note)
	}
}

// makeTSProject 造一个最小前端工程，检查器是输出固定内容的 .cmd 桩（Windows）。
func makeTSProject(t *testing.T, stub string) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "node_modules", ".bin"), 0o700))
	must(os.MkdirAll(filepath.Join(dir, "src"), 0o700))
	must(os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"fixture"}`), 0o600))
	must(os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0o600))
	must(os.WriteFile(filepath.Join(dir, "src", "a.ts"), []byte("const x: number = 'oops';\n"), 0o600))
	must(os.WriteFile(filepath.Join(dir, "node_modules", ".bin", "tsc.cmd"), []byte(stub), 0o700))
	return dir
}

// 工程级检查跑起来：本文件的诊断内联、其它文件只计数。
func TestWebDiag_ProjectRunFiltersToFile(t *testing.T) {
	dir := makeTSProject(t, "@echo off\r\necho src/a.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.\r\necho src/other.ts(9,1): error TS9999: other file error\r\nexit /b 1\r\n")
	r := Diagnose(context.Background(), dir, "src/a.ts", 5*time.Second)
	if !r.Attempted || r.Inconclusive {
		t.Fatalf("应尝试且可判定：%+v", r)
	}
	if r.Via != "tsc" {
		t.Fatalf("Via 应为 tsc：%+v", r)
	}
	if len(r.Diagnostics) != 1 {
		t.Fatalf("本文件应恰 1 条诊断：%+v", r.Diagnostics)
	}
	d := r.Diagnostics[0]
	if d.File != "src/a.ts" || d.Line != 1 || d.Col != 7 || !strings.Contains(d.Message, "TS2322") {
		t.Fatalf("诊断字段不符：%+v", d)
	}
	if !strings.Contains(r.Note, "其他文件 1 处") {
		t.Fatalf("其它文件应只计数：%q", r.Note)
	}
}

// 本文件干净、其它文件有错：明说，不冒充"本文件有问题"也不冒充"全通过"。
func TestWebDiag_CleanFileOthersDirty(t *testing.T) {
	dir := makeTSProject(t, "@echo off\r\necho src/other.ts(9,1): error TS9999: other\r\nexit /b 1\r\n")
	r := Diagnose(context.Background(), dir, "src/a.ts", 5*time.Second)
	if !r.Attempted || len(r.Diagnostics) != 0 {
		t.Fatalf("本文件应无诊断：%+v", r)
	}
	if !strings.Contains(r.Note, "本文件无类型诊断") || !strings.Contains(r.Note, "1 处") {
		t.Fatalf("应明说其它文件有错：%q", r.Note)
	}
}

// 全干净：Attempted 且零诊断零备注（自动钩子静默）。
func TestWebDiag_AllClean(t *testing.T) {
	dir := makeTSProject(t, "@echo off\r\nexit /b 0\r\n")
	r := Diagnose(context.Background(), dir, "src/a.ts", 5*time.Second)
	if !r.Attempted || len(r.Diagnostics) != 0 || r.Note != "" || r.Inconclusive {
		t.Fatalf("全干净应静默通过：%+v", r)
	}
	if !strings.Contains(Format(r), "tsc") {
		t.Fatalf("干净话术应带检查器名：%q", Format(r))
	}
}

// 退出异常且无输出：Inconclusive（绝不把"没跑成"说成"没错误"）。
func TestWebDiag_InconclusiveOnSilentFailure(t *testing.T) {
	dir := makeTSProject(t, "@echo off\r\nexit /b 2\r\n")
	r := Diagnose(context.Background(), dir, "src/a.ts", 5*time.Second)
	if !r.Attempted || !r.Inconclusive {
		t.Fatalf("退出异常且无诊断应为 Inconclusive：%+v", r)
	}
}

// 工程定位：文件在 src 下也能找到上层的 package.json+node_modules。
func TestWebDiag_LocatesProjectFromSubdir(t *testing.T) {
	dir := makeTSProject(t, "@echo off\r\nexit /b 0\r\n")
	// makeTSProject 已建 src；再把文件放更深的 src/nested
	if err := os.MkdirAll(filepath.Join(dir, "src", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "nested", "b.ts"), []byte("export {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Diagnose(context.Background(), dir, "src/nested/b.ts", 5*time.Second)
	if !r.Attempted {
		t.Fatalf("子目录文件应能定位到工程：%+v", r)
	}
}

// parseWebLine 单元：Windows 绝对路径与 ./ 前缀。
func TestParseWebLine(t *testing.T) {
	d, ok := parseWebLine(`C:\proj\src\a.ts(3,5): error TS2345: boom`, `C:\proj`)
	if !ok || d.File != "src/a.ts" || d.Line != 3 || d.Col != 5 || d.Message != "error TS2345: boom" {
		t.Fatalf("绝对路径解析不符：%+v ok=%v", d, ok)
	}
	d, ok = parseWebLine(`./src/b.ts(2,1): error TS1: x`, `C:\proj`)
	if !ok || d.File != "src/b.ts" {
		t.Fatalf("./ 前缀应剥离：%+v ok=%v", d, ok)
	}
	if _, ok := parseWebLine("随机日志行 error TS1: x", `C:\proj`); ok {
		t.Fatal("无定位结构的行不应解析")
	}
	if _, ok := parseWebLine("src/c.ts(1): error TS1: x", `C:\proj`); ok {
		t.Fatal("缺 col 的形态不解析（宁可漏报不误报）")
	}
}
