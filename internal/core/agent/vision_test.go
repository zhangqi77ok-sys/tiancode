// 视觉反馈（0.0.37）用例：工具结果带 ModelImage → 回合内合成 user 消息把图
// 送进后续请求；不落账本（重放派生不含它，之后回合模型可再截）。
package agent

import (
	"context"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

func TestLoop_ModelImageReachesNextRequest(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	shot := &scriptTool{name: "browser", result: tools.ToolResult{
		Content:    "已截图 s1/shot-0001.png",
		ModelImage: "data:image/png;base64,AAAA",
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(shot); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "browser", ArgumentsDelta: `{"action":"screenshot","for_model":true}`}}}, {EndReason: llm.EndDone}},
		{{Delta: "页面显示登录表单"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "test-model", registry)
	ch, err := loop.Run(context.Background(), ledger, "看看页面长什么样")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	// 第二次请求里必须出现合成 user 消息（图片 Parts + Content 同源——附件轮形态）
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.reqs) < 2 {
		t.Fatalf("应有 2 次模型调用，实际 %d", len(fr.reqs))
	}
	found := false
	for _, m := range fr.reqs[1].Messages {
		if m.Role != "user" || len(m.Parts) == 0 {
			continue
		}
		if m.Content == "" {
			t.Fatal("合成消息必须 Parts+Content 同源（Anthropic 零块会报错的形态）")
		}
		for _, p := range m.Parts {
			if p.Type == "image_url" && p.ImageURL == "data:image/png;base64,AAAA" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("第二次请求未携带截图图像")
	}
	// 第一次请求（截图前）不得包含图像
	for _, m := range fr.reqs[0].Messages {
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				t.Fatal("首次请求不应有合成图像")
			}
		}
	}

	// 账本重放派生不得出现合成图像消息（不落账本，之后回合模型可再截）
	msgs2, _, err := deriveMessagesWith(ledger, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs2 {
		for _, p := range m.Parts {
			if p.Type == "image_url" && strings.Contains(p.ImageURL, "AAAA") {
				t.Fatal("合成图像消息不得落账本")
			}
		}
	}
}

// IsError 的结果不送图：失败的工具调用不该把残缺画面喂给模型。
func TestLoop_ModelImageSkippedOnError(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	shot := &scriptTool{name: "browser", result: tools.ToolResult{
		Content: "已截图", IsError: true,
		ModelImage: "data:image/png;base64,BBBB",
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(shot); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "browser", ArgumentsDelta: `{"action":"screenshot","for_model":true}`}}}, {EndReason: llm.EndDone}},
		{{Delta: "好的"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "test-model", registry)
	ch, err := loop.Run(context.Background(), ledger, "看看页面")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for _, m := range fr.reqs[1].Messages {
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				t.Fatal("IsError 的结果不得送图")
			}
		}
	}
}
