package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcore "tiancode/internal/app"
)

// newChatServiceForBind 构造绑定测试用的 ChatService 并设定工作区（root 为空 = 纯对话）。
func newChatServiceForBind(t *testing.T, root string) *appcore.ChatService {
	t.Helper()
	s, err := appcore.NewChatService(appcore.Config{
		DataDir:      t.TempDir(),
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
	return s
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

	b := &Bind{chat: newChatServiceForBind(t, root)}

	// 空查询：列出文件（跳过目录）
	hits, err := b.SearchWorkspaceFiles("")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(hits, "\n")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "frontend/src/Composer.vue") {
		t.Fatalf("应命中工作区文件：%v", hits)
	}
	if strings.Contains(joined, "node_modules") || strings.Contains(joined, ".git") || strings.Contains(joined, "dist/") {
		t.Fatalf("依赖/构建目录必须跳过：%v", hits)
	}

	// 前缀过滤
	hits, err = b.SearchWorkspaceFiles("composer")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.HasSuffix(hits[0], "Composer.vue") {
		t.Fatalf("过滤命中不符：%v", hits)
	}

	// 无工作区（SetWorkspace("") 清除）：显式报错
	b2 := &Bind{chat: newChatServiceForBind(t, "")}
	if err := b2.chat.SetWorkspace(""); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.SearchWorkspaceFiles(""); err == nil {
		t.Fatal("无工作区必须报错")
	}
}
