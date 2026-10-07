package subagenttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// fakeRuntime 按脚本回放每次 Chat 调用（与内核测试的假运行时同形态）。
type fakeRuntime struct {
	mu   sync.Mutex
	reqs []llm.ChatRequest
	file [][]llm.StreamChunk
}

func (f *fakeRuntime) Chat(_ context.Context, req llm.ChatRequest, _ llm.RuntimePolicy) (<-chan llm.StreamChunk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	n := len(f.reqs)
	if n > len(f.file) {
		return nil, fmt.Errorf("unexpected Chat call #%d", n)
	}
	seg := f.file[n-1]
	ch := make(chan llm.StreamChunk, len(seg))
	for _, c := range seg {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// scriptTool 是带记录的桩工具。
type scriptTool struct {
	name      string
	result    tools.ToolResult
	err       error
	execCalls []json.RawMessage
}

func (s *scriptTool) Name() string        { return s.name }
func (s *scriptTool) Description() string { return "scripted " + s.name }
func (s *scriptTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"}}}`)
}
func (s *scriptTool) Execute(_ context.Context, a json.RawMessage) (tools.ToolResult, error) {
	s.execCalls = append(s.execCalls, append(json.RawMessage(nil), a...))
	if s.err != nil {
		return tools.ToolResult{}, s.err
	}
	return s.result, nil
}

func toolCall(idx int, name, args string) llm.StreamChunk {
	return llm.StreamChunk{ToolCalls: []llm.ToolCallChunk{{Index: idx, ID: fmt.Sprintf("c%d", idx), Name: name, ArgumentsDelta: args}}}
}

func run(t *testing.T, tl *Tool, args string) tools.ToolResult {
	t.Helper()
	res, err := tl.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Execute 机制错误：%v", err)
	}
	return res
}

// C-SUB-1/3：子代理在自己的账本里跑完整轮（工具调用 → 报告），报告原样交还，
// 主对话不接触中间过程。
func TestSubagent_RunsIsolatedAndReturnsReport(t *testing.T) {
	fs := &scriptTool{name: "fs", result: tools.ToolResult{Content: "package main\nfunc main() {}", Title: "main.go"}}
	rt := &fakeRuntime{file: [][]llm.StreamChunk{
		{toolCall(0, "fs", `{"action":"read","path":"main.go"}`), llm.StreamChunk{EndReason: llm.EndDone}},
		{{Delta: "main.go 定义了 main 函数，无其他内容。"}, {EndReason: llm.EndDone}},
	}}
	tl := New(rt, "test-model", fs)
	res := run(t, tl, `{"prompt":"读 main.go 并报告内容"}`)

	if res.IsError {
		t.Fatalf("不应失败：%s", res.Content)
	}
	if !strings.Contains(res.Content, "main 函数") {
		t.Fatalf("报告应交还主对话：%q", res.Content)
	}
	if res.Title != "子代理调研" || res.Op != "subagent" {
		t.Fatalf("语义标签缺失：%q/%q", res.Title, res.Op)
	}
	// 子代理确实读到了文件（第一段请求里有 fs 调用与子代理前言）
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.reqs) != 2 {
		t.Fatalf("模型调用数 = %d, want 2", len(rt.reqs))
	}
	if !strings.Contains(rt.reqs[0].Messages[0].Content, "只读调研子代理") {
		t.Fatalf("子代理应带只读前言：%q", rt.reqs[0].Messages[0].Content)
	}
}

// C-SUB-2：写路径结构性排除——子代理的 fs 写被闸门拒绝，模型收到可读原因后
// 仍能继续收尾。
func TestSubagent_WriteActionsRejected(t *testing.T) {
	fs := &scriptTool{name: "fs", result: tools.ToolResult{Content: "已写入"}}
	rt := &fakeRuntime{file: [][]llm.StreamChunk{
		{toolCall(0, "fs", `{"action":"write","path":"x.go","content":"evil"}`), llm.StreamChunk{EndReason: llm.EndDone}},
		{{Delta: "写被拒绝，调研无法继续。"}, {EndReason: llm.EndDone}},
	}}
	tl := New(rt, "test-model", fs)
	run(t, tl, `{"prompt":"试图写文件"}`)

	if fs.execCalls != nil {
		t.Fatal("fs.write 不应透传到真工具")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.reqs) != 2 {
		t.Fatalf("模型调用数 = %d, want 2", len(rt.reqs))
	}
	var sawRejection bool
	for _, req := range rt.reqs {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "子代理只读") {
				sawRejection = true
			}
		}
	}
	if !sawRejection {
		t.Fatalf("拒绝原因应回给模型：%+v", rt.reqs[0].Messages)
	}
	// memory 的写同样拒绝
	mem := &scriptTool{name: "memory", result: tools.ToolResult{Content: "ok"}}
	gate := readOnlyTool{inner: mem}
	if res2, _ := gate.Execute(context.Background(), json.RawMessage(`{"action":"append"}`)); !res2.IsError {
		t.Fatal("memory append 应被拒绝")
	}
}

// C-SUB-5：子代理工具集里没有 task（不递归），只含包装后的只读工具。
func TestSubagent_RegistryHasNoTaskAndWrapsReadonly(t *testing.T) {
	fs := &scriptTool{name: "fs", result: tools.ToolResult{}}
	search := &scriptTool{name: "search", result: tools.ToolResult{}}
	tl := New(nil, "test-model", fs, search, nil)
	reg, err := tl.buildRegistry()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range reg.Definitions() {
		names[d.Name] = true
	}
	if names["task"] || names["shell"] || names["browser"] {
		t.Fatalf("子代理不应拥有写/递归工具：%v", names)
	}
	if !names["fs"] || !names["search"] {
		t.Fatalf("只读工具应在场：%v", names)
	}
	// fs 被包装（同名的闸门实例注册时替换了原始实例——从注册表取回的应拦截写）
	wrapped, ok := reg.Get("fs")
	if !ok {
		t.Fatal("fs 应注册")
	}
	if res, _ := wrapped.Execute(context.Background(), json.RawMessage(`{"action":"replace"}`)); !res.IsError {
		t.Fatal("包装后的 fs 应拒绝 replace")
	}
}

// C-SUB-4：主轮取消 → 子代理以取消终态收束，结果为可读的业务失败。
func TestSubagent_CancelPropagates(t *testing.T) {
	rt := &fakeRuntime{file: [][]llm.StreamChunk{}} // 任何调用都会越界报错，走不到
	_ = rt
	// 直接用 manual channel 控制时序：流挂着，取消 ctx
	hang := make(chan llm.StreamChunk)
	blocking := &blockingRuntime{ch: hang}
	tl := New(blocking, "test-model")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan tools.ToolResult, 1)
	go func() {
		res, _ := tl.Execute(ctx, json.RawMessage(`{"prompt":"挂着"}`))
		done <- res
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if !res.IsError {
			t.Fatalf("取消应产生业务失败：%s", res.Content)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后未收束")
	}
}

type blockingRuntime struct{ ch chan llm.StreamChunk }

func (b *blockingRuntime) Chat(ctx context.Context, _ llm.ChatRequest, _ llm.RuntimePolicy) (<-chan llm.StreamChunk, error) {
	go func() {
		<-ctx.Done()
		b.ch <- llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()}
		close(b.ch)
	}()
	return b.ch, nil
}

// 参数校验与机制故障。
func TestSubagent_ArgumentValidation(t *testing.T) {
	tl := New(nil, "test-model")
	if res := run(t, tl, `{}`); !res.IsError || !strings.Contains(res.Content, "prompt") {
		t.Fatalf("缺 prompt 应拒绝：%+v", res)
	}
	if res := run(t, tl, `not-json`); !res.IsError || !strings.Contains(res.Content, "JSON") {
		t.Fatalf("非法 JSON 应为业务失败：%+v", res)
	}
	// 模型调用直接失败 → 业务失败
	failing := &failRuntime{}
	tl2 := New(failing, "test-model")
	if res := run(t, tl2, `{"prompt":"x"}`); !res.IsError || !strings.Contains(res.Content, "执行失败") {
		t.Fatalf("启动失败应可读：%+v", res)
	}
}

type failRuntime struct{}

func (failRuntime) Chat(context.Context, llm.ChatRequest, llm.RuntimePolicy) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("no channel")
}
