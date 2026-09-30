package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"tiancode/internal/core/agent"
	"tiancode/internal/platform/tones"
)

// prefaceOf 走真实装配路径取一次"本轮系统提示"。
func prefaceOf(t *testing.T, s *ChatService, root string) string {
	t.Helper()
	ag := agent.NewLoop(nil, "m", nil)
	if err := s.applyExtensionPreface(context.Background(), ag, root); err != nil {
		t.Fatal(err)
	}
	return ag.Preface()
}

// 第 8 批：语气段必须进系统提示（缺文件 = 固定 plain），且排在环境事实之后。
func TestChatService_PrefaceCarriesTone(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	p := prefaceOf(t, s, t.TempDir())
	if !strings.Contains(p, "## 语气（固定）") || !strings.Contains(p, "用最后一条用户消息的语言和详略回答。") {
		t.Fatalf("系统提示缺语气段：%s", p)
	}
	if !strings.Contains(p, tones.FactConstraint) {
		t.Fatalf("语气段必须带事实约束：%s", p)
	}
	if strings.Index(p, "运行环境") > strings.Index(p, "## 语气") {
		t.Fatalf("拼接顺序应为 清单 → 环境事实 → 语气：%s", p)
	}
}

// 同一设置连续两轮拼接逐字相同：applyDynamicPreface 的"文本未变不替换"与
// 上游 prompt cache 都建立在这个性质上。
func TestChatService_PrefaceStableAcrossTurns(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	root := t.TempDir()
	if first, second := prefaceOf(t, s, root), prefaceOf(t, s, root); first != second {
		t.Fatalf("同一设置两轮必须逐字相同：\n%s\n---\n%s", first, second)
	}
}

// 设置改动下一轮生效；停用的条目从名单里消失；停用默认语气被拒；坏文件显式报错。
func TestChatService_ToneSettingsTakeEffectNextTurn(t *testing.T) {
	path := t.TempDir() + string(os.PathSeparator) + "tones.json"
	s := newChannelService(t, Config{TonesPath: path})
	defer s.Close()
	root := t.TempDir()

	if err := s.SaveTones(tones.File{Mode: tones.ModeAuto, Default: "plain", Disabled: []string{"skeptical"}}); err != nil {
		t.Fatal(err)
	}
	p := prefaceOf(t, s, root)
	if !strings.Contains(p, "## 语气（自动选择）") || !strings.Contains(p, "默认语气：plain") {
		t.Fatalf("自动模式没生效：%s", p)
	}
	if strings.Contains(p, "skeptical") {
		t.Fatalf("已停用的语气不该出现在名单里：%s", p)
	}
	if !strings.Contains(p, "先写应补的测试和失败条件。") {
		t.Fatalf("未停用的语气必须在名单里（test-first 的做法）：%s", p)
	}
	if strings.Contains(p, "## 语气（固定）") {
		t.Fatalf("自动模式不该再带固定段：%s", p)
	}

	// 停用默认语气：保存失败（拒绝写盘）
	if err := s.SaveTones(tones.File{Mode: tones.ModeFixed, Default: "plain", Disabled: []string{"plain"}}); err == nil {
		t.Fatal("停用默认语气必须拒绝保存")
	}

	// 坏文件：本轮显式失败，不静默当成"没有语气"
	if err := os.WriteFile(path, []byte("{不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	ag := agent.NewLoop(nil, "m", nil)
	if err := s.applyExtensionPreface(context.Background(), ag, root); err == nil {
		t.Fatal("语气文件坏掉必须让本轮报错")
	}
}
