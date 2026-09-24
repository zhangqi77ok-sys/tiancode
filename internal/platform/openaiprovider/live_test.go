package openaiprovider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 真网关冒烟测试（默认跳过，仅当设置 TIANCODE_LIVE_* 环境变量时运行）。
//
// 为什么保留在仓库：单元测试用 httptest 模拟上游，**发现不了真实网关的报文差异**
// （例如中转站返回的 reasoning_content、非标准 finish_reason、分块粒度）。
// 接入新渠道时手动跑一次，确认"我们的 provider 真的能跟它说话"：
//
//	$env:TIANCODE_LIVE_BASEURL='https://ss2a.top/v1'
//	$env:TIANCODE_LIVE_APIKEY='sk-...'
//	$env:TIANCODE_LIVE_MODEL='grok-4.7'
//	go test ./internal/platform/openaiprovider/ -run Live -v
//
// 密钥只来自环境变量，绝不写入代码或配置仓库（STANDARDS §1）。
func TestLiveGateway_StreamChat(t *testing.T) {
	base := os.Getenv("TIANCODE_LIVE_BASEURL")
	key := os.Getenv("TIANCODE_LIVE_APIKEY")
	model := os.Getenv("TIANCODE_LIVE_MODEL")
	if base == "" || key == "" || model == "" {
		t.Skip("未设置 TIANCODE_LIVE_BASEURL / TIANCODE_LIVE_APIKEY / TIANCODE_LIVE_MODEL，跳过真网关冒烟")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ch, err := New(Options{BaseURL: base, APIKey: key}).StreamChat(ctx, llm.ChatRequest{
		Model:    model,
		Messages: []llm.Message{{Role: "user", Content: "只回复两个字：收到"}},
	})
	if err != nil {
		t.Fatalf("建立流失败（前置错误应在此暴露）: %v", err)
	}

	var text strings.Builder
	var end llm.EndReason
	for c := range ch {
		text.WriteString(c.Delta)
		if c.EndReason != llm.EndNone {
			end = c.EndReason
			if c.Err != nil {
				t.Fatalf("终态错误: %v", c.Err)
			}
		}
	}
	// 契约要求：正常结束必须是 EndDone（而非通道静默关闭）
	if end != llm.EndDone {
		t.Fatalf("endReason = %d, want EndDone(%d)", end, llm.EndDone)
	}
	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("上游已结束但没有任何文本增量")
	}
	t.Logf("真网关流式验证通过：model=%s 输出=%q", model, strings.TrimSpace(text.String()))
}
