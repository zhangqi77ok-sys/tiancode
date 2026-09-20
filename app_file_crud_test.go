package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppFileCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tiancode_file_crud_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	app := NewApp()
	if err := app.SetWorkspace(tempDir); err != nil {
		t.Fatalf("SetWorkspace failed: %v", err)
	}

	// 1. 测试创建新文件
	relFile := "src/components/Button.vue"
	initialContent := "<template><button>Click</button></template>"
	if err := app.CreateFile(relFile, initialContent); err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}

	// 验证文件在磁盘上存在且内容一致
	readContent, err := app.ReadFile(relFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if readContent != initialContent {
		t.Errorf("content mismatch: got %q, want %q", readContent, initialContent)
	}

	// 2. 测试新建子目录
	relDir := "src/assets/images"
	if err := app.CreateDirectory(relDir); err != nil {
		t.Fatalf("CreateDirectory failed: %v", err)
	}
	absDir := filepath.Join(tempDir, filepath.FromSlash(relDir))
	fi, err := os.Stat(absDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("directory was not created: %v", err)
	}

	// 3. 测试重命名文件
	renamedFile := "src/components/MyButton.vue"
	if err := app.RenamePath(relFile, renamedFile); err != nil {
		t.Fatalf("RenamePath failed: %v", err)
	}
	// 原路径应不存在
	if _, err := app.ReadFile(relFile); err == nil {
		t.Errorf("old file still exists after rename")
	}
	// 新路径应存在
	if rc, err := app.ReadFile(renamedFile); err != nil || rc != initialContent {
		t.Errorf("renamed file read error or content mismatch: %v, %q", err, rc)
	}

	// 4. 测试删除文件
	if err := app.DeletePath(renamedFile); err != nil {
		t.Fatalf("DeletePath failed: %v", err)
	}
	if _, err := app.ReadFile(renamedFile); err == nil {
		t.Errorf("file still exists after delete")
	}

	// 5. 路径越界沙箱安全防护 (Fail-Closed)
	escapedPath := "../outside_secret.txt"
	if err := app.CreateFile(escapedPath, "evil"); err == nil {
		t.Errorf("expected security error on path traversal, got nil")
	} else if !strings.Contains(err.Error(), "escapes") && !strings.Contains(err.Error(), "SECURITY") {
		t.Errorf("unexpected error message: %v", err)
	}

	if err := app.DeletePath(escapedPath); err == nil {
		t.Errorf("expected security error on delete traversal, got nil")
	}
}
