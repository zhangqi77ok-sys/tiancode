package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 0.0.11：消息文本里的 @相对路径 必须真正读成附件交给模型——此前 @ 只在
// 输入框弹菜单、插入裸路径，模型不理会就是"没效果"（用户实测反馈）。
func TestResolveAtReferences(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("main.go", "package main")
	mustWrite("docs/note.md", "# note")
	mustWrite("pic.png", "\x89PNG\r\n\x1a\nfake")
	svc := &ChatService{}
	// materialize 落盘用相对路径 att/<sid>：chdir 到临时目录，不污染源码树
	tmpCwd := t.TempDir()
	oldWd, _ := os.Getwd()
	if err := os.Chdir(tmpCwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	t.Run("命中转附件", func(t *testing.T) {
		atts, err := svc.resolveAtReferences("s1", root, "先看 @main.go 和 @docs/note.md 再说")
		if err != nil {
			t.Fatal(err)
		}
		if len(atts) != 2 {
			t.Fatalf("want 2 attachments, got %d: %+v", len(atts), atts)
		}
		if atts[0].Name != "main.go" || atts[0].Inline != "full" {
			t.Fatalf("文本文件应内联：%+v", atts[0])
		}
		if atts[1].Name != "docs_note.md" { // 子路径分隔符拍平
			t.Fatalf("%+v", atts[1])
		}
	})

	t.Run("图片按 kind=image", func(t *testing.T) {
		atts, err := svc.resolveAtReferences("s1", root, "看这张 @pic.png")
		if err != nil {
			t.Fatal(err)
		}
		if len(atts) != 1 || atts[0].Kind != "image" {
			t.Fatalf("%+v", atts)
		}
	})

	t.Run("不存在与越界静默保留原文", func(t *testing.T) {
		atts, err := svc.resolveAtReferences("s1", root, "谈谈 @nope.go 和 @../../etc/passwd 还有 a@b.com")
		if err != nil {
			t.Fatal(err)
		}
		if len(atts) != 0 {
			t.Fatalf("不应产生附件：%+v", atts)
		}
	})

	t.Run("目录与绝对路径跳过", func(t *testing.T) {
		atts, err := svc.resolveAtReferences("s1", root, "@docs 和 @C:\\Windows\\system32")
		if err != nil {
			t.Fatal(err)
		}
		if len(atts) != 0 {
			t.Fatalf("%+v", atts)
		}
	})

	t.Run("空工作区与无@直接返回", func(t *testing.T) {
		if atts, _ := svc.resolveAtReferences("s1", "", "@main.go"); len(atts) != 0 {
			t.Fatalf("空工作区：%+v", atts)
		}
		if atts, _ := svc.resolveAtReferences("s1", root, "没有引用"); len(atts) != 0 {
			t.Fatalf("无@：%+v", atts)
		}
	})

	t.Run("附件落在会话目录（重放可还原）", func(t *testing.T) {
		atts, err := svc.resolveAtReferences("sess-x", root, "@main.go")
		if err != nil || len(atts) != 1 {
			t.Fatalf("%v %+v", err, atts)
		}
		// materialize 的落盘命名：file-<序号>-<安全名>，目录 att/<sid>/
		if atts[0].Path != filepath.ToSlash(filepath.Join("att", "sess-x", "file-1-"+atts[0].Name)) {
			t.Fatalf("应落会话附件目录：%+v", atts[0])
		}
	})
}
