package app

import (
	"context"
	"strings"
	"testing"
)

// 重命名会话：标题落账本，随后出现在会话摘要里（并持久化——重开服务仍在）。
func TestChatService_RenameSessionPersists(t *testing.T) {
	cfg := Config{WorkDir: t.TempDir()}
	dataDir := t.TempDir()
	cfg.DataDir = dataDir

	s := newChannelService(t, cfg)
	ctx := context.Background()

	// 先产生一个会话（Send 会建立账本；无渠道不影响本例关注点）
	if _, err := s.Send(ctx, "s1", "hi"); err != nil {
		// 无渠道时 Send 会报错，但这不影响账本目录：改为直接重命名
		t.Logf("send without channel: %v", err)
	}
	if err := s.RenameSession("s1", "  渠道排查  "); err != nil {
		t.Fatalf("rename: %v", err)
	}

	sums, err := s.SessionSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].ID != "s1" || sums[0].Title != "渠道排查" {
		t.Fatalf("summaries = %+v, want s1/渠道排查（标题应去除首尾空白）", sums)
	}

	// 重新装配（模拟重启）：标题必须仍在（账本事实，非内存态）
	s2, err := NewChatService(Config{
		DataDir: dataDir, WorkDir: cfg.WorkDir,
		ChannelsPath: s.cfg.ChannelsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	sums2, err := s2.SessionSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums2) != 1 || sums2[0].Title != "渠道排查" {
		t.Fatalf("after restart: %+v", sums2)
	}
}

// 空标题与超长标题必须拒绝（空标题会让侧栏出现无法辨认的条目）。
func TestChatService_RenameSessionValidation(t *testing.T) {
	s := newChannelService(t, Config{})
	if err := s.RenameSession("s1", "   "); err == nil {
		t.Fatal("blank title must be rejected")
	}
	long := strings.Repeat("字", 61)
	if err := s.RenameSession("s1", long); err == nil {
		t.Fatal("too long title must be rejected")
	}
	if err := s.RenameSession("../evil", "正常"); err == nil {
		t.Fatal("bad session id must be rejected")
	}
}
