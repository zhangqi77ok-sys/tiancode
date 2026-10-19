package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-10-19：.py 写后做语法诊断（ast.parse，不执行、不写 __pycache__）。
func TestDiagnose_PythonSyntax(t *testing.T) {
	if pythonExe() == "" {
		t.Skip("no python")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "bad.py"), []byte("def f(:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "ok.py"), []byte("def f():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := Diagnose(context.Background(), root, "pkg/bad.py", AutoTimeout)
	if !bad.Attempted || len(bad.Diagnostics) != 1 || bad.Diagnostics[0].File != "pkg/bad.py" || bad.Diagnostics[0].Line != 1 || !strings.Contains(bad.Diagnostics[0].Message, "SyntaxError") {
		t.Fatalf("语法错误应被诊断出来：%+v", bad)
	}
	ok := Diagnose(context.Background(), root, "pkg/ok.py", AutoTimeout)
	if !ok.Attempted || len(ok.Diagnostics) != 0 || ok.Inconclusive {
		t.Fatalf("正确的文件应通过：%+v", ok)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "__pycache__")); !os.IsNotExist(err) {
		t.Fatal("语法检查不应在工作区写 __pycache__")
	}
}
