package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"

	appcore "tiancode/internal/app"
)

// newBindForTest 构造绑定测试用的 Bind（返回 DataDir 供账本夹具用）。
func newBindForTest(t *testing.T, root string) (*Bind, string) {
	t.Helper()
	dataDir := t.TempDir()
	s, err := appcore.NewChatService(appcore.Config{
		DataDir:      dataDir,
		WorkDir:      t.TempDir(),
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if root != "" {
		if err := s.SetWorkspace(root); err != nil {
			t.Fatal(err)
		}
	}
	return &Bind{chat: s}, dataDir
}

// 0.0.09：SearchWorkspaceFiles——@ 引用的文件列表：只读遍历、跳过依赖/构建
// 目录、命中上限；无工作区显式报错（不静默返回空）。
func TestBind_SearchWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main")
	write("frontend/src/Composer.vue", "<template/>")
	write("node_modules/left-pad/index.js", "x") // 必须跳过
	write(".git/objects/ab", "x")                // 必须跳过
	write("dist/bundle.js", "x")                 // 必须跳过

	b, dataDir := newBindForTest(t, root)

	// 空查询：列出文件（跳过目录）。0.0.10：按会话归属查询——用 session API
	// 给该会话写一份带归属的账本（DataDir 结构与真实装配一致）
	l, err := session.OpenLedger(dataDir, "s-at")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": root}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	hits, err := b.SearchWorkspaceFiles("s-at", "")
	if err != nil {
		t.Fatalf("搜索失败（root 解析问题）：%v", err)
	}
	joined := strings.Join(hits, "\n")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "frontend/src/Composer.vue") {
		t.Fatalf("应命中工作区文件：%v", hits)
	}
	if strings.Contains(joined, "node_modules") || strings.Contains(joined, ".git") || strings.Contains(joined, "dist/") {
		t.Fatalf("依赖/构建目录必须跳过：%v", hits)
	}

	// 前缀过滤
	hits, err = b.SearchWorkspaceFiles("s-at", "composer")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.HasSuffix(hits[0], "Composer.vue") {
		t.Fatalf("过滤命中不符：%v", hits)
	}

	// 无工作区（纯对话）：显式报错
	b2, _ := newBindForTest(t, "")
	if _, err := b2.SearchWorkspaceFiles("s-nobody", ""); err == nil {
		t.Fatal("无工作区必须报错")
	}
}
