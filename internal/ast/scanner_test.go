package ast

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanWorkspaceAST_Safe(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ast_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Write a valid Go file
	validCode := `package sample

type Service interface {
	Do() error
}

type MyService struct {
	Name string
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "sample.go"), []byte(validCode), 0644)

	// Write a malformed Go file
	malformed := `package !!! syntax error !!!`
	_ = os.WriteFile(filepath.Join(tempDir, "bad.go"), []byte(malformed), 0644)

	nodes, err := ScanWorkspaceAST(tempDir)
	if err != nil {
		t.Fatalf("ScanWorkspaceAST returned unexpected error: %v", err)
	}

	foundSample := false
	for _, n := range nodes {
		if n.Name == "sample.go" {
			foundSample = true
			break
		}
	}
	if !foundSample {
		t.Errorf("expected sample.go node to be found in AST")
	}
}

func TestScanWorkspaceAST_WindowsDriveNormalization(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ast_drive_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	validCode := `package hello
type Greeter struct {}`
	_ = os.WriteFile(filepath.Join(tempDir, "hello.go"), []byte(validCode), 0644)

	vol := filepath.VolumeName(tempDir)
	var testDir = tempDir
	if vol != "" {
		// 切换盘符大小写
		lowerVol := strings.ToLower(vol)
		testDir = lowerVol + tempDir[len(vol):]
	}

	nodes, err := ScanWorkspaceAST(testDir)
	if err != nil {
		t.Fatalf("ScanWorkspaceAST with drive case variation failed: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("expected at least 1 node, got 0")
	}
}

func TestScanWorkspaceAST_NonExistentDir(t *testing.T) {
	// 空路径
	if _, err := ScanWorkspaceAST(""); err == nil {
		t.Errorf("expected error for empty rootDir, got nil")
	}

	// 明确不存在的路径
	nonExistent := filepath.Join(os.TempDir(), "definitely_not_exist_tcode_dir_99999")
	if _, err := ScanWorkspaceAST(nonExistent); err == nil {
		t.Errorf("expected error for non-existent rootDir [%s], got nil", nonExistent)
	}
}

func TestAnalyzeWorkspaceArchitecture_FullDAGAndContracts(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "arch_analyzer_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 写入 go.mod
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testarch\n\ngo 1.21\n"), 0644)

	// 写入 pkg/plugin/v1/tool.go
	pkgDir := filepath.Join(tempDir, "pkg", "plugin", "v1")
	_ = os.MkdirAll(pkgDir, 0755)
	pkgCode := `package v1

type ToolPlugin interface {
	ID() string
	Execute() error
}
`
	_ = os.WriteFile(filepath.Join(pkgDir, "tool.go"), []byte(pkgCode), 0644)

	// 写入 internal/core/loop/engine.go
	coreDir := filepath.Join(tempDir, "internal", "core", "loop")
	_ = os.MkdirAll(coreDir, 0755)
	coreCode := `package loop

import (
	_ "testarch/pkg/plugin/v1"
)

type Engine struct {
	MaxTurns int
}

func (e *Engine) Run() error {
	return nil
}
`
	_ = os.WriteFile(filepath.Join(coreDir, "engine.go"), []byte(coreCode), 0644)

	// 写入 plugins/tool/fs/fs.go (合规插件，实现 ToolPlugin 契约)
	fsDir := filepath.Join(tempDir, "plugins", "tool", "fs")
	_ = os.MkdirAll(fsDir, 0755)
	fsCode := `package fs

import (
	_ "testarch/pkg/plugin/v1"
)

type FSTool struct{}

func (f *FSTool) ID() string {
	return "tool.fs"
}

func (f *FSTool) Execute() error {
	return nil
}
`
	_ = os.WriteFile(filepath.Join(fsDir, "fs.go"), []byte(fsCode), 0644)

	// 写入 plugins/tool/bad/bad.go (违规插件，非法反向引用 internal/core/loop)
	badDir := filepath.Join(tempDir, "plugins", "tool", "bad")
	_ = os.MkdirAll(badDir, 0755)
	badCode := `package bad

import (
	_ "testarch/internal/core/loop"
)

type BadTool struct{}
`
	_ = os.WriteFile(filepath.Join(badDir, "bad.go"), []byte(badCode), 0644)

	// 执行分析
	report, err := AnalyzeWorkspaceArchitecture(tempDir)
	if err != nil {
		t.Fatalf("AnalyzeWorkspaceArchitecture failed: %v", err)
	}

	if report.TotalPackages < 4 {
		t.Errorf("expected at least 4 packages, got %d", report.TotalPackages)
	}

	// 验证层级判定
	foundCore := false
	foundSpec := false
	foundTool := false
	for _, p := range report.Packages {
		if p.ID == "internal/core/loop" && p.Layer == "core" {
			foundCore = true
		}
		if p.ID == "pkg/plugin/v1" && p.Layer == "spec" {
			foundSpec = true
		}
		if p.ID == "plugins/tool/fs" && p.Layer == "tool" {
			foundTool = true
		}
	}
	if !foundCore || !foundSpec || !foundTool {
		t.Errorf("expected layers classification to be accurate: core=%v, spec=%v, tool=%v", foundCore, foundSpec, foundTool)
	}

	// 验证契约多态匹配 (FSTool 实现 ToolPlugin)
	foundContract := false
	for _, c := range report.Contracts {
		if c.InterfaceName == "ToolPlugin" {
			for _, impl := range c.Implementations {
				if impl.StructName == "FSTool" && impl.Status == "compliant" {
					foundContract = true
					break
				}
			}
		}
	}
	if !foundContract {
		t.Errorf("expected FSTool to be detected as implementing ToolPlugin contract")
	}

	// 验证违规检测 (bad -> internal/core/loop)
	foundViolation := false
	for _, e := range report.Edges {
		if e.From == "plugins/tool/bad" && e.To == "internal/core/loop" && e.IsViolation {
			foundViolation = true
			if !strings.Contains(e.ViolationReason, "铁律 7") {
				t.Errorf("expected violation reason to mention 铁律 7, got %s", e.ViolationReason)
			}
			break
		}
	}
	if !foundViolation {
		t.Errorf("expected illegal reverse dependency plugins/tool/bad -> internal/core/loop to be caught as violation")
	}

	// 测试影响面分析 (Blast Radius)
	blast, err := AnalyzeBlastRadius(tempDir, "pkg/plugin/v1.ToolPlugin")
	if err != nil {
		t.Fatalf("AnalyzeBlastRadius failed: %v", err)
	}
	if blast.RiskLevel == "" {
		t.Errorf("expected risk level to be computed")
	}
	if len(blast.DirectCallers) == 0 {
		t.Errorf("expected direct callers to be found for ToolPlugin package")
	}
}

func TestDiscoverGoModules_MonorepoAndSubApps(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mod_discovery_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. 根模块 go.mod
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module myroot\n\ngo 1.22\n"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main\nfunc main(){}"), 0644)

	// 2. 子模块 sub/go.mod
	subDir := filepath.Join(tempDir, "subpkg")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "go.mod"), []byte("module subpkg\n\ngo 1.22\n"), 0644)
	_ = os.WriteFile(filepath.Join(subDir, "sub.go"), []byte("package subpkg\n"), 0644)

	// 3. cmd/installer 子应用
	cmdDir := filepath.Join(tempDir, "cmd", "installer")
	_ = os.MkdirAll(cmdDir, 0755)
	_ = os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main(){}"), 0644)

	modules, err := DiscoverGoModules(tempDir)
	if err != nil {
		t.Fatalf("DiscoverGoModules failed: %v", err)
	}

	if len(modules) < 3 {
		t.Fatalf("expected at least 3 modules, got %d: %+v", len(modules), modules)
	}

	foundRoot := false
	foundSub := false
	foundCmd := false
	for _, m := range modules {
		if m.IsRoot {
			foundRoot = true
		}
		if m.Type == "sub_module" && m.RelPath == "subpkg" {
			foundSub = true
		}
		if m.Type == "cmd_app" && m.RelPath == "cmd/installer" {
			foundCmd = true
		}
	}

	if !foundRoot {
		t.Errorf("expected root module to be discovered")
	}
	if !foundSub {
		t.Errorf("expected sub_module 'subpkg' to be discovered")
	}
	if !foundCmd {
		t.Errorf("expected cmd_app 'cmd/installer' to be discovered")
	}
}

func TestAnalyzeBlastRadius_CallSites(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "blast_callsites_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// go.mod
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testcalls\n\ngo 1.21\n"), 0644)

	// pkg/calc/calc.go
	calcDir := filepath.Join(tempDir, "pkg", "calc")
	_ = os.MkdirAll(calcDir, 0755)
	calcCode := `package calc

func Add(a, b int) int {
	return a + b
}
`
	_ = os.WriteFile(filepath.Join(calcDir, "calc.go"), []byte(calcCode), 0644)

	// cmd/app/main.go
	cmdDir := filepath.Join(tempDir, "cmd", "app")
	_ = os.MkdirAll(cmdDir, 0755)
	mainCode := `package main

import "testcalls/pkg/calc"

func doCompute() int {
	res := calc.Add(10, 20)
	return res
}

func main() {
	_ = doCompute()
}
`
	_ = os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(mainCode), 0644)

	// cmd/app/main_test.go
	testCode := `package main

import (
	"testing"
	"testcalls/pkg/calc"
)

func TestAdd(t *testing.T) {
	if calc.Add(1, 2) != 3 {
		t.Fail()
	}
}
`
	_ = os.WriteFile(filepath.Join(cmdDir, "main_test.go"), []byte(testCode), 0644)

	// Analyze blast radius for pkg/calc.Add
	blast, err := AnalyzeBlastRadius(tempDir, "pkg/calc.Add")
	if err != nil {
		t.Fatalf("AnalyzeBlastRadius failed: %v", err)
	}

	if len(blast.CallSites) < 2 {
		t.Fatalf("expected at least 2 call sites, got %d: %+v", len(blast.CallSites), blast.CallSites)
	}

	foundMainCall := false
	foundTestCall := false
	for _, cs := range blast.CallSites {
		if strings.Contains(cs.File, "main.go") && cs.Function == "doCompute" {
			foundMainCall = true
			if !strings.Contains(cs.Snippet, "calc.Add(10, 20)") {
				t.Errorf("expected snippet to contain 'calc.Add(10, 20)', got: %s", cs.Snippet)
			}
			if cs.Line <= 0 {
				t.Errorf("expected valid line number, got %d", cs.Line)
			}
		}
		if strings.Contains(cs.File, "main_test.go") && cs.Function == "TestAdd" {
			foundTestCall = true
		}
	}

	if !foundMainCall {
		t.Errorf("expected main.go doCompute call site to be captured")
	}
	if !foundTestCall {
		t.Errorf("expected main_test.go TestAdd call site to be captured")
	}

	// Verify affected tests list includes cmd/app
	foundAffectedTest := false
	for _, at := range blast.AffectedTests {
		if strings.Contains(at, "cmd/app") {
			foundAffectedTest = true
			break
		}
	}
	if !foundAffectedTest {
		t.Errorf("expected cmd/app to be in AffectedTests, got %+v", blast.AffectedTests)
	}
}


