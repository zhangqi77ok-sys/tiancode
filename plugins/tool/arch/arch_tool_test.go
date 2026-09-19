package arch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchTool_Execute(t *testing.T) {
	tempDir := t.TempDir()

	// 1. go.mod
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testarchplugin\n\ngo 1.21\n"), 0644)

	// 2. pkg/demo/demo.go
	demoDir := filepath.Join(tempDir, "pkg", "demo")
	_ = os.MkdirAll(demoDir, 0755)
	demoCode := `package demo

type Runner interface {
	Run() error
}

type MyRunner struct{}

func (m *MyRunner) Run() error {
	return nil
}
`
	_ = os.WriteFile(filepath.Join(demoDir, "demo.go"), []byte(demoCode), 0644)

	// 3. cmd/main.go
	cmdDir := filepath.Join(tempDir, "cmd")
	_ = os.MkdirAll(cmdDir, 0755)
	cmdCode := `package main

import "testarchplugin/pkg/demo"

func executeRunner() {
	var r demo.Runner = &demo.MyRunner{}
	_ = r.Run()
}

func main() {
	executeRunner()
}
`
	_ = os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(cmdCode), 0644)

	tool := NewTool(tempDir)

	// Test action: inspect
	inspectArgs, _ := json.Marshal(map[string]string{
		"action": "inspect",
	})
	resInspect, err := tool.Execute(context.Background(), inspectArgs)
	if err != nil {
		t.Fatalf("tool.Execute inspect failed: %v", err)
	}
	if resInspect.IsError {
		t.Fatalf("expected inspect to succeed, got error: %s", resInspect.Content)
	}
	if !strings.Contains(resInspect.Content, "total_packages") {
		t.Errorf("expected inspect output to contain total_packages: %s", resInspect.Content)
	}

	// Test action: blast_radius
	blastArgs, _ := json.Marshal(map[string]string{
		"action": "blast_radius",
		"target": "pkg/demo.Runner",
	})
	resBlast, err := tool.Execute(context.Background(), blastArgs)
	if err != nil {
		t.Fatalf("tool.Execute blast_radius failed: %v", err)
	}
	if resBlast.IsError {
		t.Fatalf("expected blast_radius to succeed, got error: %s", resBlast.Content)
	}
	if !strings.Contains(resBlast.Content, "call_sites") {
		t.Errorf("expected blast_radius output to contain call_sites: %s", resBlast.Content)
	}

	// Test action: discover_modules
	modArgs, _ := json.Marshal(map[string]string{
		"action": "discover_modules",
	})
	resMod, err := tool.Execute(context.Background(), modArgs)
	if err != nil {
		t.Fatalf("tool.Execute discover_modules failed: %v", err)
	}
	if resMod.IsError {
		t.Fatalf("expected discover_modules to succeed, got error: %s", resMod.Content)
	}
	if !strings.Contains(resMod.Content, "testarchplugin") {
		t.Errorf("expected discover_modules output to contain module name: %s", resMod.Content)
	}

	// Test unknown action
	unknownArgs, _ := json.Marshal(map[string]string{
		"action": "invalid_action",
	})
	resUnknown, err := tool.Execute(context.Background(), unknownArgs)
	if err != nil {
		t.Fatalf("tool.Execute with unknown action returned err: %v", err)
	}
	if !resUnknown.IsError {
		t.Errorf("expected unknown action to return IsError=true")
	}
}
