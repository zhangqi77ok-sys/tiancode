package app

import (
	"context"
	"testing"

	"tiancode/internal/core/llm"
)

// 同会话并发 Send 必须被后端拒绝（0.2.27）：一份账本上交错写会让 Replay 顺序错乱。
// 不同会话不受影响（多会话并行）；流结束后同会话可再次发送。
func TestChatService_SendRejectsConcurrentSameSession(t *testing.T) {
	srv, _ := newTestUpstream(t, 0)
	s := newChannelService(t, Config{})
	if _, err := s.AddChannel(llm.Channel{
		Name: "m", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	// 第一次 Send：不读流，让它在后台跑
	stream1, err := s.Send(context.Background(), "sess-1", "hi")
	if err != nil {
		t.Fatal(err)
	}
	// 同会话第二次 Send：必须拒绝（可见错误）
	if _, err := s.Send(context.Background(), "sess-1", "again"); err == nil {
		t.Fatal("同会话并发 Send 必须被拒绝")
	}
	// 不同会话：允许并行
	stream2, err := s.Send(context.Background(), "sess-2", "hi")
	if err != nil {
		t.Fatalf("不同会话应允许并行：%v", err)
	}

	drain := func(ch <-chan llm.StreamChunk) {
		for range ch {
		}
	}
	drain(stream1)
	drain(stream2)

	// 流结束后占位释放：同会话可再次发送
	stream3, err := s.Send(context.Background(), "sess-1", "again")
	if err != nil {
		t.Fatalf("回合结束后应可再次发送：%v", err)
	}
	drain(stream3)
}

// 运行中的会话不可删除（后端守卫，与前端提示同源）。
func TestChatService_DeleteRunningSessionRejected(t *testing.T) {
	srv, _ := newTestUpstream(t, 0)
	s := newChannelService(t, Config{})
	if _, err := s.AddChannel(llm.Channel{
		Name: "m", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := s.Send(context.Background(), "sess-del", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("sess-del"); err == nil {
		t.Fatal("运行中的会话必须拒绝删除")
	}
	for range stream {
	}
	if err := s.DeleteSession("sess-del"); err != nil {
		t.Fatalf("回合结束后应可删除：%v", err)
	}
}
