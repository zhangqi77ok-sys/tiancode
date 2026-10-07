package app

import (
	"context"
	"strings"
	"testing"

	"tiancode/internal/core/agent"
)

// webfetch 是共享工具（无工作区路径依赖）：纯对话（无会话工具集）也必须注册，
// 系统说明必须写明能力边界与参数——模型此前没有读网页能力，查文档/查报错是硬伤。
func TestChatService_WebFetchSharedToolAndPreface(t *testing.T) {
	s := newChannelService(t, Config{})

	// assembleRegistry(nil) 模拟纯对话：webfetch 仍要在场
	reg, err := s.assembleRegistry(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("webfetch"); !ok {
		t.Fatal("webfetch 必须作为共享工具注册（纯对话也可用）")
	}

	// 系统说明：工具名、动作、参数、能力边界都要讲清
	ag := agent.NewLoop(nil, "m", nil)
	if err := s.applyExtensionPreface(context.Background(), ag, "", false); err != nil {
		t.Fatal(err)
	}
	p := ag.Preface()
	for _, want := range []string{"webfetch", "fetch", "url", "max_bytes", "GET", "browser"} {
		if !strings.Contains(p, want) {
			t.Fatalf("系统说明缺 %q：%q", want, p)
		}
	}
}
