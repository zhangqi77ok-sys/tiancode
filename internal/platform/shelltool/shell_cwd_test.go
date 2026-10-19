package shelltool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runShell(t *testing.T, tool *Tool, args map[string]any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return res.Content, res.IsError
}

// 2026-10-19：cwd 指定工作区内子目录（不必再写 cd x && ...）；越界与非目录拒绝。
func TestShell_CwdInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "marker.txt"), []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(Options{Root: dir})
	defer tool.Close()
	cmd := "cat marker.txt"
	if runtime.GOOS == "windows" {
		cmd = "type marker.txt"
	}
	if out, isErr := runShell(t, tool, map[string]any{"action": "run", "command": cmd, "cwd": "sub"}); isErr || !strings.Contains(out, "here") {
		t.Fatalf("cwd=sub 应在子目录执行：isErr=%v %q", isErr, out)
	}
	for _, bad := range []string{"..", "../..", "sub/marker.txt", "nope"} {
		if out, isErr := runShell(t, tool, map[string]any{"action": "run", "command": cmd, "cwd": bad}); !isErr {
			t.Fatalf("cwd=%q 应拒绝：%q", bad, out)
		}
	}
	abs := dir
	if out, isErr := runShell(t, tool, map[string]any{"action": "run", "command": cmd, "cwd": abs}); !isErr || !strings.Contains(out, "相对") {
		t.Fatalf("绝对路径 cwd 应拒绝：%q", out)
	}
}

// shell=powershell 在 Windows 上用 PowerShell 执行；未知 shell 拒绝；默认仍是 cmd。
func TestShell_PowerShellOption(t *testing.T) {
	dir := t.TempDir()
	tool := New(Options{Root: dir})
	defer tool.Close()
	if out, isErr := runShell(t, tool, map[string]any{"action": "run", "command": "echo x", "shell": "bash"}); !isErr || !strings.Contains(out, "unknown shell") {
		t.Fatalf("未知 shell 应拒绝：%q", out)
	}
	if runtime.GOOS != "windows" {
		if _, isErr := runShell(t, tool, map[string]any{"action": "run", "command": "echo x", "shell": "powershell"}); !isErr {
			t.Fatal("非 Windows 上 powershell 应拒绝")
		}
		return
	}
	out, isErr := runShell(t, tool, map[string]any{"action": "run", "command": "Write-Output ($PSVersionTable.PSVersion.Major -ge 5)", "shell": "powershell"})
	if isErr || !strings.Contains(out, "True") {
		t.Fatalf("PowerShell 语法应可执行：isErr=%v %q", isErr, out)
	}
	// 默认 cmd：PowerShell cmdlet 在 cmd 里不认识（证明默认行为没变）
	if out, isErr := runShell(t, tool, map[string]any{"action": "run", "command": "Write-Output hi"}); !isErr {
		t.Fatalf("默认 shell 应仍是 cmd：%q", out)
	}
}
