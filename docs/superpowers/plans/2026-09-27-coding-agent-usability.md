# M6 编程智能体可用性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 tiancode 能连续多轮写代码：跨轮回放工具历史、补齐 list/search、对话区可读。

**Architecture:** 不新增分层。模型上下文回放留在 `core/agent`（账本仍是事实源，截断只发生在 derive 视图）；`fs.list` 扩现有 fstool；`search` 新适配器走同一 `ToolPort`；UI 只投影账本与事件桥，markdown 在壳层消毒后渲染。

**Tech Stack:** Go 1.22、现有 Wails/Vue3/Pinia/Vitest；前端新增 `marked` + `dompurify`（不加高亮库）。

**Spec:** `docs/superpowers/specs/2026-09-27-coding-agent-usability-design.md`

## Global Constraints

- TDD：每条契约先写失败测试再写实现；测试名与 CONTRACTS ID 一致。
- 分层：`internal/core` 禁止 import `internal/platform` / `internal/app` / `app`（守卫 R1）；错误禁止 `_ =` 丢弃（R2）。
- 账本 Kind 字符串不改；只给 `tool_call` / `tool_result` 的 JSON 加 `id` 字段。
- 截断预算：模型视图 4096 字节；UI IPC 64KiB；list 500 条；search 默认 50 命中（上限 200）、输出 64KiB。
- 忽略目录名（Windows 大小写不敏感）：`.git`、`node_modules`、`vendor`、`dist`、`bin`。
- 单文件 >400 行必须拆分；导出标识符必须有 godoc；提交 Conventional Commits；工作区 Go 文件 LF。
- 本轮不做：文件树/编辑器/git 面板/git 写/.gitignore 解析/语法高亮库/会话自动标题。
- `VERSION` 本里程碑结束升为 `0.2.0`；完成后必须跑 `scripts/release.ps1`。

## File map

| 文件 | 职责 |
| --- | --- |
| `internal/core/session/ledger.go` | 新增 `NextSeq()`，供空 ID 写成 `call-{seq}` |
| `internal/core/agent/derive.go` | **新建**：`deriveMessages`、配对、模型侧截断 |
| `internal/core/agent/agent.go` | 删除旧 derive；写账本带 `id`；ToolEvent 带全文 |
| `internal/core/agent/agent_test.go` | C-AGT-1~4 |
| `internal/core/llm/port.go` | `ToolEvent.Content` |
| `internal/platform/fstool/fstool.go` | `list` action |
| `internal/platform/fstool/fstool_test.go` | C-FS-5~7 |
| `internal/platform/searchtool/searchtool.go` | **新建**：工作区内容搜索 |
| `internal/platform/searchtool/searchtool_test.go` | **新建**：C-SEARCH-1~6 |
| `internal/app/workspace.go` | `newRegistry` 注册 search |
| `internal/app/chat_service.go` | `ChatMessage` 扩展；`Replay` 投影 tool/thinking |
| `internal/app/chat_service_test.go` | C-APP-3 |
| `internal/app/sessions_test.go` | C-SES-10 期望含工具 |
| `app/app.go` | `chat:tool` 增加 `content` |
| `frontend/src/wails.ts` | `ChatMessageDTO` 新字段 |
| `frontend/src/markdown.ts` | **新建**：marked + DOMPurify |
| `frontend/src/markdown.test.ts` | **新建**：消毒 |
| `frontend/src/stores/chat.ts` | thinking / tool content / Replay 映射 |
| `frontend/src/stores/chat.test.ts` | 对应 vitest |
| `frontend/src/App.vue` | markdown、thinking 折叠、工具卡展开 |
| `frontend/src/style.css` | `.prose-md` 用现有令牌 |
| `docs/*`、`VERSION`、`README.md` | 契约/里程碑/ADR-0007/版本 |

---

### Task 1: 跨轮工具历史（C-AGT-1~4）

**Files:**
- Modify: `internal/core/session/ledger.go`（`Append` 之后加 `NextSeq`）
- Create: `internal/core/agent/derive.go`
- Modify: `internal/core/agent/agent.go`（删掉现有 `deriveMessages`；`turn` 里写 `id`）
- Test: `internal/core/agent/agent_test.go`

**Interfaces:**
- Consumes: `session.Ledger.Replay` / `Append`；`llm.Message` / `llm.ToolCall`
- Produces: `func deriveMessages(ledger *session.Ledger) ([]llm.Message, error)`；`func (l *Ledger) NextSeq() int64`；账本 `tool_call`/`tool_result` JSON 含 `id`

- [ ] **Step 1: Write the failing test C-AGT-1**

Append to `internal/core/agent/agent_test.go`:

```go
// C-AGT-1：第二轮 Run 发给模型的消息必须含上一轮 assistant(tool_calls)+role=tool。
func TestAgent_DerivesToolHistoryAcrossTurns(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "file-x"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"read"}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "fixed"}, {EndReason: llm.EndDone}},
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)

	ch, err := loop.Run(context.Background(), ledger, "fix foo")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	ch, err = loop.Run(context.Background(), ledger, "also bar")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	if fr.requestCount() != 3 {
		t.Fatalf("requests = %d, want 3", fr.requestCount())
	}
	msgs := fr.reqs[2].Messages
	if len(msgs) != 5 {
		t.Fatalf("turn-2 messages = %d, want 5 (user, assistant+tools, tool, assistant, user); got %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "fix foo" {
		t.Fatalf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("msgs[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "c1" || msgs[2].Content != "file-x" {
		t.Fatalf("msgs[2] = %+v", msgs[2])
	}
	if msgs[3].Role != "assistant" || msgs[3].Content != "fixed" {
		t.Fatalf("msgs[3] = %+v", msgs[3])
	}
	if msgs[4].Role != "user" || msgs[4].Content != "also bar" {
		t.Fatalf("msgs[4] = %+v", msgs[4])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/agent/ -count=1 -run TestAgent_DerivesToolHistoryAcrossTurns -v`

Expected: FAIL — `turn-2 messages = 3, want 5`（当前 derive 只有 user/assistant 锚点）。

- [ ] **Step 3: Add NextSeq and persist IDs; move deriveMessages**

In `internal/core/session/ledger.go` after `Append`:

```go
// NextSeq 返回下一次 Append 将使用的序号，不推进水位。
// 单写入方在写入 tool_call 前用它生成 call-{seq}；账本不支持并发双写。
func (l *Ledger) NextSeq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq + 1
}
```

Create `internal/core/agent/derive.go`（包注释已在 `agent.go`，本文件不必再写 package 长注释）:

```go
package agent

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

const toolResultModelLimit = 4096
const toolEventIPCLimit = 64 * 1024

func deriveMessages(ledger *session.Ledger) ([]llm.Message, error) {
	var msgs []llm.Message
	var calls []llm.ToolCall
	results := []*llm.Message{}

	complete := func() bool {
		if len(calls) == 0 {
			return false
		}
		for _, r := range results {
			if r == nil {
				return false
			}
		}
		return len(results) == len(calls)
	}
	flush := func() {
		if !complete() {
			calls, results = nil, nil
			return
		}
		msgs = append(msgs, llm.Message{Role: "assistant", ToolCalls: append([]llm.ToolCall(nil), calls...)})
		for _, r := range results {
			msgs = append(msgs, *r)
		}
		calls, results = nil, nil
	}

	err := ledger.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventUserMessage:
			flush()
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			msgs = append(msgs, llm.Message{Role: "user", Content: p.Text})
		case session.EventToolCall:
			if complete() {
				flush()
			}
			var p struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			id := p.ID
			if id == "" {
				id = fmt.Sprintf("call-%d", ev.Seq())
			}
			calls = append(calls, llm.ToolCall{ID: id, Name: p.Name, Arguments: p.Arguments})
			results = append(results, nil)
		case session.EventToolResult:
			var p struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Content string `json:"content"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			idx := pairToolResult(calls, results, p.ID, p.Name)
			if idx < 0 {
				return nil
			}
			results[idx] = &llm.Message{
				Role:       "tool",
				ToolCallID: calls[idx].ID,
				Content:    truncateToolResult(p.Content),
			}
		case session.EventAssistantMsg:
			flush()
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			msgs = append(msgs, llm.Message{Role: "assistant", Content: p.Text})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	flush()
	return msgs, nil
}

func pairToolResult(calls []llm.ToolCall, results []*llm.Message, id, name string) int {
	if id != "" {
		for i, c := range calls {
			if c.ID == id && results[i] == nil {
				return i
			}
		}
	}
	if name != "" {
		for i, c := range calls {
			if results[i] == nil && c.Name == name {
				return i
			}
		}
	}
	for i, r := range results {
		if r == nil {
			return i
		}
	}
	return -1
}

func truncateToolResult(content string) string {
	if len(content) <= toolResultModelLimit {
		return content
	}
	return truncateToBytes(content, toolResultModelLimit) + fmt.Sprintf("\n\n[truncated, original %d bytes]", len(content))
}

func truncateToBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
```

Delete the old `deriveMessages` function from `agent.go` (the one that only switches on `EventUserMessage` / `EventAssistantMsg`).

In `agent.go` `turn` tool loop, replace the two `Append` payloads:

```go
id := call.ID
if id == "" {
	id = fmt.Sprintf("call-%d", ledger.NextSeq())
	call.ID = id
}
if _, err := ledger.Append(session.EventToolCall, map[string]string{
	"id": id, "name": call.Name, "arguments": call.Arguments,
}); err != nil {
	emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist tool call: %w", err)})
	return
}
result := l.execTool(ctx, call)
if _, err := ledger.Append(session.EventToolResult, map[string]any{
	"id": id, "name": call.Name, "content": result.Content, "is_error": result.IsError,
}); err != nil {
	emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist tool result: %w", err)})
	return
}
```

Keep the in-memory `msgs = append(..., ToolCallID: call.ID)` using the possibly-filled `call.ID`.

- [ ] **Step 4: Run C-AGT-1 and existing agent tests**

Run: `go test ./internal/core/agent/ -count=1`

Expected: PASS（含既有 `TestAgent_ToolRoundtrip` / `TestAgent_HistoryFromLedger`）。

- [ ] **Step 5: Commit C-AGT-1**

```bash
git add internal/core/session/ledger.go internal/core/agent/derive.go internal/core/agent/agent.go internal/core/agent/agent_test.go
git commit -m "feat(agent): 跨轮回放工具历史（C-AGT-1）"
```

- [ ] **Step 6: Write failing tests C-AGT-2, C-AGT-3, C-AGT-4**

```go
// C-AGT-2：发给模型的单条 tool 结果超过 4096 字节必须截断；账本保留全文。
func TestAgent_TruncatesToolResultForModel(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("x", 5000)
	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: big}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "q1")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)
	ch, err = loop.Run(context.Background(), ledger, "q2")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	got := fr.reqs[2].Messages[2].Content
	if len(got) >= 5000 || !strings.Contains(got, "truncated") || !strings.Contains(got, "5000") {
		t.Fatalf("model tool content = %d bytes, %q", len(got), got[:min(80, len(got))])
	}
	if !strings.HasPrefix(got, strings.Repeat("x", 4096)) {
		t.Fatal("truncated view must keep the first 4096 bytes")
	}

	l2, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	full := ""
	if err := l2.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventToolResult {
			return nil
		}
		var p struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		full = p.Content
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if full != big {
		t.Fatalf("ledger content len = %d, want 5000", len(full))
	}
}

// C-AGT-3：没有 result 的 tool_call 不得进入模型消息。
func TestAgent_OmitsUnpairedToolCall(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolCall, map[string]string{
		"id": "c-orphan", "name": "fs", "arguments": "{}",
	}); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	ch, err := loop.Run(context.Background(), ledger, "new")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	for i, m := range fr.reqs[0].Messages {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			t.Fatalf("unpaired tool leaked at msgs[%d] = %+v", i, m)
		}
	}
}

// C-AGT-4：旧账本缺 id 时合成 call-{seq} 且配对合法。
func TestAgent_SyntheticIDsForLegacyLedger(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "old"}); err != nil {
		t.Fatal(err)
	}
	callEv, err := ledger.Append(session.EventToolCall, map[string]string{
		"name": "fs", "arguments": "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolResult, map[string]any{
		"name": "fs", "content": "ok", "is_error": false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": "done"}); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	ch, err := loop.Run(context.Background(), ledger, "next")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	msgs := fr.reqs[0].Messages
	wantID := fmt.Sprintf("call-%d", callEv.Seq())
	if len(msgs) < 4 {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != wantID {
		t.Fatalf("msgs[1] = %+v, want id %s", msgs[1], wantID)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != wantID || msgs[2].Content != "ok" {
		t.Fatalf("msgs[2] = %+v", msgs[2])
	}
}
```

If `min` is unavailable on Go 1.22, use a local helper or `if len(got) < 80 { ... }` — do **not** add a dependency. Go 1.22 无 builtins `min` for int in older point releases? Go 1.21+ has min. Go 1.22.5 有。可用。

- [ ] **Step 7: Run the three tests — C-AGT-2 may fail until truncate is used; C-AGT-3/4 should already pass if derive.go matches spec**

Run: `go test ./internal/core/agent/ -count=1 -run "TestAgent_TruncatesToolResultForModel|TestAgent_OmitsUnpairedToolCall|TestAgent_SyntheticIDsForLegacyLedger" -v`

Expected: C-AGT-2 FAIL if truncate not yet applied on derive path（若 Step 3 已实现 truncate 则三者全绿）。缺实现就补 `truncateToolResult` 调用。C-AGT-3 FAIL 若 flush 在缺 result 时仍 emit。

- [ ] **Step 8: Run full agent package**

Run: `go test ./internal/core/agent/ -count=1`

Expected: PASS

- [ ] **Step 9: Commit C-AGT-2~4**

```bash
git add internal/core/agent/agent_test.go internal/core/agent/derive.go
git commit -m "test(agent): 锁定工具结果截断与旧账本合成 ID（C-AGT-2~4）"
```

---

### Task 2: fs.list（C-FS-5~7）

**Files:**
- Modify: `internal/platform/fstool/fstool.go`（`Description`/`Schema`/`Execute` switch + `list` 方法）
- Test: `internal/platform/fstool/fstool_test.go`

**Interfaces:**
- Consumes: 现有 `Tool.resolve`、`bizErr`/`bizErrf`
- Produces: `action=list`；缺省 `path="."`；最多 500 条；非递归

- [ ] **Step 1: Write failing tests**

Append to `fstool_test.go`:

```go
// C-FS-5：list 越界或非目录 → IsError 且工作区零修改。
func TestFSList_RejectsEscapeAndNonDir(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "a.txt", "x")
	parent := filepath.Dir(tool.Root())

	for _, p := range []string{"../evil", filepath.Join(parent, "x")} {
		res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list", "path": p}))
		if err != nil {
			t.Fatalf("path %q: mechanism error %v", p, err)
		}
		if !res.IsError {
			t.Fatalf("path %q: escape must be IsError", p)
		}
	}
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list", "path": "a.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("list on file must be IsError")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "evil" {
			t.Fatal("list escape created parent entry")
		}
	}
}

// C-FS-6：list 最多 500 条，超出截断并标注总数。
func TestFSList_OutputBounded(t *testing.T) {
	tool := newTool(t)
	for i := 0; i < 510; i++ {
		mustWrite(t, tool, filepath.Join("f", fmt.Sprintf("%04d.txt", i)), "x")
	}
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list", "path": "f"}))
	if err != nil || res.IsError {
		t.Fatalf("list: %v %s", err, res.Content)
	}
	lines := strings.Split(strings.TrimRight(res.Content, "\n"), "\n")
	if len(lines) != 501 { // 500 entries + truncated line
		t.Fatalf("lines = %d, want 501", len(lines))
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "truncated") || !strings.Contains(last, "510") {
		t.Fatalf("truncation line = %q", last)
	}
}

// C-FS-7：list 只列下一层。
func TestFSList_NonRecursive(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "sub/nested.txt", "x")
	mustWrite(t, tool, "top.txt", "y")
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list"}))
	if err != nil || res.IsError {
		t.Fatalf("list: %v %s", err, res.Content)
	}
	if strings.Contains(res.Content, "nested.txt") {
		t.Fatalf("recursive leak: %s", res.Content)
	}
	if !strings.Contains(res.Content, "top.txt") || !strings.Contains(res.Content, "sub/") {
		t.Fatalf("missing top entries: %s", res.Content)
	}
}
```

Add `"fmt"` to the test file imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/platform/fstool/ -count=1 -run "TestFSList_" -v`

Expected: FAIL — `unknown action "list"`（现有 default 分支）。

- [ ] **Step 3: Implement list**

Update `Description` to mention list。Update `Schema` action enum to `["read","write","replace","list"]`。

In `Execute` switch add `case "list": return t.list(args.Path)` and change default message to `want read/write/replace/list`。

Add:

```go
const listLimit = 500

func (t *Tool) list(path string) (tools.ToolResult, error) {
	if path == "" {
		path = "."
	}
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return bizErrf("list failed: %v", err), nil
	}
	if !info.IsDir() {
		return bizErrf("not a directory: %s", path), nil
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return bizErrf("list failed: %v", err), nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	total := len(entries)
	if total == 0 {
		return tools.ToolResult{Content: "empty directory"}, nil
	}
	var b strings.Builder
	n := total
	if n > listLimit {
		n = listLimit
	}
	for i := 0; i < n; i++ {
		e := entries[i]
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "dir  %s/\n", name)
			continue
		}
		size := "-"
		if fi, err := e.Info(); err == nil {
			size = fmt.Sprintf("%d", fi.Size())
		}
		fmt.Fprintf(&b, "file %s  %s\n", name, size)
	}
	if total > listLimit {
		fmt.Fprintf(&b, "(truncated, showing %d of %d entries)\n", listLimit, total)
	}
	return tools.ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
}
```

Add imports: `"fmt"` and `"sort"`（`strings` 已有）。

- [ ] **Step 4: Run fstool tests**

Run: `go test ./internal/platform/fstool/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/platform/fstool/fstool.go internal/platform/fstool/fstool_test.go
git commit -m "feat(fs): 非递归有界 list（C-FS-5~7）"
```

---

### Task 3: search 工具 + 装配（C-SEARCH-1~6）

**Files:**
- Create: `internal/platform/searchtool/searchtool.go`
- Create: `internal/platform/searchtool/searchtool_test.go`
- Modify: `internal/app/workspace.go`（`newRegistry` 注册 search）

**Interfaces:**
- Consumes: `core/tools.ToolPort`；工作区绝对路径
- Produces: `searchtool.New(root string) *Tool` 与 `searchtool.NewWithTimeout(root string, d time.Duration) *Tool`；`Name() == "search"`；`newRegistry` 四件套 fs/shell/git/search

- [ ] **Step 1: Write failing tests**

Create `internal/platform/searchtool/searchtool_test.go`:

```go
package searchtool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newWS(t *testing.T) *Tool {
	t.Helper()
	return New(t.TempDir())
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSearch_PathEscapeRejected(t *testing.T) {
	tool := newWS(t)
	parent := filepath.Dir(tool.root)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "x", "path": "../evil"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("escape must be IsError")
	}
	res, err = tool.Execute(context.Background(), args(t, map[string]any{"pattern": "x", "path": parent}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("absolute escape must be IsError")
	}
}

func TestSearch_OutputBounded(t *testing.T) {
	tool := newWS(t)
	for i := 0; i < 80; i++ {
		write(t, tool.root, filepath.Join("src", fmt.Sprintf("%03d.go", i)), "needle here")
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 5}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	body := strings.TrimSuffix(res.Content, "\n")
	lines := strings.Split(body, "\n")
	matchLines := 0
	for _, ln := range lines {
		if strings.Contains(ln, "needle") && !strings.Contains(ln, "truncated") {
			matchLines++
		}
	}
	if matchLines != 5 {
		t.Fatalf("match lines = %d, want 5; %s", matchLines, res.Content)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Fatal("must annotate max_matches truncation")
	}
}

func TestSearch_InvalidPattern(t *testing.T) {
	tool := newWS(t)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "["}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "invalid pattern") {
		t.Fatalf("%+v", res)
	}
}

func TestSearch_SkipsIgnoredAndBinary(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "lib/a.go", "HIT-lib")
	write(t, tool.root, "vendor/a.go", "HIT-vendor")
	write(t, tool.root, "bin/a.go", "HIT-bin")
	if err := os.WriteFile(filepath.Join(tool.root, "lib", "blob.bin"), []byte("HIT-\x00binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "lib/a.go") && !strings.Contains(res.Content, `lib\a.go`) {
		t.Fatalf("missing lib hit: %s", res.Content)
	}
	if strings.Contains(res.Content, "vendor") || strings.Contains(res.Content, "HIT-bin") || strings.Contains(res.Content, "blob.bin") {
		t.Fatalf("ignored/binary leaked: %s", res.Content)
	}

	res, err = tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-", "path": "vendor"}))
	if err != nil || res.IsError {
		t.Fatalf("explicit vendor: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "HIT-vendor") {
		t.Fatalf("explicit vendor root must be searched: %s", res.Content)
	}
}

func TestSearch_NoMatchIsSuccess(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "a.txt", "hello")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "zzz-nope"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "no matches") {
		t.Fatalf("%+v", res)
	}
}

func TestSearch_TimeoutPartial(t *testing.T) {
	tool := NewWithTimeout(t.TempDir(), time.Millisecond)
	for i := 0; i < 3000; i++ {
		write(t, tool.root, filepath.Join("d", fmt.Sprintf("%04d.txt", i)), "needle line")
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 200}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("want TimedOut, got %+v", res)
	}
	if res.Content == "" {
		t.Fatal("Content must be non-empty")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/platform/searchtool/ -count=1`

Expected: FAIL — 包不存在或 `undefined: New`。

- [ ] **Step 3: Implement searchtool**

Create `internal/platform/searchtool/searchtool.go`:

```go
// Package searchtool 实现工作区受控内容搜索。
//
// 做什么：按正则扫描工作区文本文件，返回 path:line:text；跳过内置忽略目录与二进制。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、stdlib。
package searchtool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"tiancode/internal/core/tools"
)

const (
	defaultTimeout    = 15 * time.Second
	maxFileBytes      = 1 << 20
	maxOutputBytes    = 64 * 1024
	defaultMaxMatches = 50
	capMaxMatches     = 200
	headProbe         = 8 * 1024
)

// Tool 是工作区内容搜索工具。
type Tool struct {
	root    string
	timeout time.Duration
}

// New 使用默认 15s 超时。
func New(root string) *Tool { return NewWithTimeout(root, defaultTimeout) }

// NewWithTimeout 供测试注入短超时。
func NewWithTimeout(root string, d time.Duration) *Tool {
	if d <= 0 {
		d = defaultTimeout
	}
	return &Tool{root: root, timeout: d}
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "search" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "在工作区内搜索文件内容（正则）。返回 path:line:text。默认跳过 .git/node_modules/vendor/dist/bin。不要用 shell 做全库 rg。"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Go 正则"},
    "path": {"type": "string", "description": "相对工作区的起点，默认 ."},
    "glob": {"type": "string", "description": "只匹配文件名，如 *.go"},
    "max_matches": {"type": "integer", "description": "命中上限，默认 50，最大 200"}
  },
  "required": ["pattern"]
}`)
}

func skipDirName(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "vendor", "dist", "bin":
		return true
	default:
		return false
	}
}

func (t *Tool) resolve(path string) (string, error) {
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path not allowed: %s", path)
	}
	clean := filepath.Clean(filepath.Join(t.root, path))
	if clean != t.root && !strings.HasPrefix(clean, t.root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return clean, nil
}

// Execute 实现工具端口。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "cancelled", IsError: true, TimedOut: true}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var a struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		MaxMatches int    `json:"max_matches"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("invalid arguments: %v", err), IsError: true}, nil
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return tools.ToolResult{Content: "pattern is required", IsError: true}, nil
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("invalid pattern: %v", err), IsError: true}, nil
	}
	if a.Glob != "" {
		if _, err := filepath.Match(a.Glob, "x"); err != nil {
			return tools.ToolResult{Content: fmt.Sprintf("invalid glob: %v", err), IsError: true}, nil
		}
	}
	max := a.MaxMatches
	if max < 1 {
		max = defaultMaxMatches
	}
	if max > capMaxMatches {
		max = capMaxMatches
	}
	start, err := t.resolve(a.Path)
	if err != nil {
		return tools.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	var b strings.Builder
	matches := 0
	truncatedMatches := false
	walkErr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != start && skipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if a.Glob != "" {
			ok, _ := filepath.Match(a.Glob, d.Name())
			if !ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileBytes {
			return nil
		}
		hits, err := searchFile(p, re)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(t.root, p)
		if err != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		for _, h := range hits {
			line := fmt.Sprintf("%s:%d:%s\n", rel, h.line, h.text)
			if b.Len()+len(line) > maxOutputBytes {
				b.WriteString("(truncated, output limit 64KiB)\n")
				return errStop
			}
			b.WriteString(line)
			matches++
			if matches >= max {
				truncatedMatches = true
				return errStop
			}
		}
		return nil
	})

	content := strings.TrimRight(b.String(), "\n")
	timedOut := ctx.Err() != nil
	if walkErr != nil && walkErr != errStop && walkErr != context.DeadlineExceeded && walkErr != context.Canceled {
		return tools.ToolResult{Content: fmt.Sprintf("search failed: %v", walkErr), IsError: true}, nil
	}
	if timedOut {
		if content == "" {
			return tools.ToolResult{Content: "TIMEOUT", IsError: true, TimedOut: true}, nil
		}
		return tools.ToolResult{Content: content + "\nTIMEOUT", TimedOut: true}, nil
	}
	if truncatedMatches {
		if content != "" {
			content += "\n"
		}
		content += fmt.Sprintf("(truncated, max_matches %d)", max)
	}
	if matches == 0 && !truncatedMatches {
		return tools.ToolResult{Content: "no matches"}, nil
	}
	return tools.ToolResult{Content: content}, nil
}

var errStop = fmt.Errorf("search stop")

type hit struct {
	line int
	text string
}

func searchFile(path string, re *regexp.Regexp) ([]hit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, headProbe)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var hits []hit
	lineNo := 0
	for sc.Scan() {
		lineNo++
		text := strings.TrimRight(sc.Text(), "\r")
		if !utf8.ValidString(text) {
			continue
		}
		if re.MatchString(text) {
			hits = append(hits, hit{line: lineNo, text: text})
		}
	}
	return hits, sc.Err()
}
```

`Tool.root` 在测试里用到，保持小写字段 + 同包测试可访问（测试文件 `package searchtool`）。

- [ ] **Step 4: Run searchtool tests**

Run: `go test ./internal/platform/searchtool/ -count=1`

Expected: PASS。若 `TestSearch_TimeoutPartial` 在极快机器上未超时：把文件数加到 8000 或把超时改成 `time.Microsecond`，**不要放宽断言成「超时可选」**。

- [ ] **Step 5: Register in newRegistry**

`internal/app/workspace.go`:

```go
import "tiancode/internal/platform/searchtool"
```

Inside the `for _, reg :=` slice add:

```go
func() error { return registry.Register(searchtool.New(workDir)) },
```

- [ ] **Step 6: Run app package tests (workspace/channel still green)**

Run: `go test ./internal/app/ -count=1`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/platform/searchtool internal/app/workspace.go
git commit -m "feat(search): 工作区有界内容搜索（C-SEARCH-1~6）"
```

---

### Task 4: Replay 投影 + 事件桥全文（C-APP-3 / C-SES-10）

**Files:**
- Modify: `internal/core/llm/port.go`（`ToolEvent.Content`）
- Modify: `internal/core/agent/agent.go`（转发 ToolEvent 时填 Content，64KiB 截断）
- Modify: `internal/app/chat_service.go`（`ChatMessage` + `Replay`）
- Modify: `internal/app/chat_service_test.go`（C-APP-3；更新 `TestChatService_ReplayProjectsAnchors` 仍成立）
- Modify: `internal/app/sessions_test.go`（导出含工具 — 见下，若本任务用手工账本测 Replay，导出测试在同任务用 ledger 投影即可）
- Modify: `app/app.go`（`chat:tool` 加 `content`）
- Modify: `frontend/src/wails.ts`（DTO）

**Interfaces:**
- Consumes: Task 1 账本字段 `id`/`thinking` delta；`toolEventIPCLimit` / `truncateToBytes`（agent 包内）
- Produces:

```go
type ChatMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ToolName string `json:"toolName,omitempty"`
	Status   string `json:"status,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}
type ToolEvent struct {
	Name    string
	Status  string
	Summary string
	Content string
}
```

- [ ] **Step 1: Write failing C-APP-3**

Replace the comment on `TestChatService_ReplayProjectsAnchors` to still assert delta text is not projected. Add:

```go
// C-APP-3：Replay 投影 tool 卡与 assistant thinking。
func TestChatService_ReplayIncludesTools(t *testing.T) {
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventAssistantDelta, map[string]any{"text": "x", "thinking": "plan-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolCall, map[string]string{"id": "c1", "name": "fs", "arguments": "{}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolResult, map[string]any{"id": "c1", "name": "fs", "content": "file-x", "is_error": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventAssistantDelta, map[string]any{"text": "y", "thinking": "plan-b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventAssistantMsg, map[string]string{"text": "done"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := NewChatService(Config{
		DataDir: dir, WorkDir: ".", ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	msgs, err := s.Replay("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len=%d %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "u1" {
		t.Fatalf("msgs[0]=%+v", msgs[0])
	}
	if msgs[1].Role != "tool" || msgs[1].ToolName != "fs" || msgs[1].Status != "success" || msgs[1].Content != "file-x" {
		t.Fatalf("msgs[1]=%+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "done" || msgs[2].Thinking != "plan-aplan-b" {
		t.Fatalf("msgs[2]=%+v", msgs[2])
	}

	md, err := s.ExportSessionMarkdown("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "file-x") || !strings.Contains(md, "## 工具") {
		t.Fatalf("export must include tool section:\n%s", md)
	}
}
```

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/app/ -count=1 -run TestChatService_ReplayIncludesTools -v`

Expected: FAIL — Replay 仍只有 2 条锚点。

- [ ] **Step 3: Implement Replay + ToolEvent**

`llm/port.go` ToolEvent:

```go
type ToolEvent struct {
	Name    string
	Status  string
	Summary string
	Content string // 全文；壳层 IPC 上限 64KiB，由 agent 截断
}
```

`agent.go` 构造 ToolEvent（`toolEventIPCLimit` 已在 derive.go）:

```go
content := result.Content
if len(content) > toolEventIPCLimit {
	content = truncateToBytes(content, toolEventIPCLimit) + fmt.Sprintf("\n\n[truncated, original %d bytes]", len(result.Content))
}
summary := result.Content
if len(summary) > 200 {
	summary = summary[:200] + "…"
}
if forward(llm.StreamChunk{ToolEvent: &llm.ToolEvent{
	Name: call.Name, Status: status, Summary: summary, Content: content,
}}) {
	return
}
```

`chat_service.go` ChatMessage 与 Replay:

```go
type ChatMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ToolName string `json:"toolName,omitempty"`
	Status   string `json:"status,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

func (s *ChatService) Replay(sessionID string) ([]ChatMessage, error) {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return nil, err
	}
	var out []ChatMessage
	var thinking strings.Builder
	err = ledger.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventUserMessage:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			out = append(out, ChatMessage{Role: "user", Content: p.Text})
		case session.EventAssistantDelta:
			var p struct {
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			thinking.WriteString(p.Thinking)
		case session.EventToolResult:
			var p struct {
				Name    string `json:"name"`
				Content string `json:"content"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			st := "success"
			if p.IsError {
				st = "error"
			}
			out = append(out, ChatMessage{Role: "tool", Content: p.Content, ToolName: p.Name, Status: st})
		case session.EventAssistantMsg:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			out = append(out, ChatMessage{Role: "assistant", Content: p.Text, Thinking: thinking.String()})
			thinking.Reset()
		}
		return nil
	})
	return out, err
}
```

`app/app.go` 事件：

```go
wruntime.EventsEmit(ctx, "chat:tool", map[string]string{
	"sessionID": sessionID,
	"name":      c.ToolEvent.Name,
	"status":    c.ToolEvent.Status,
	"summary":   c.ToolEvent.Summary,
	"content":   c.ToolEvent.Content,
})
```

`wails.ts`:

```ts
export interface ChatMessageDTO {
  role: string
  content: string
  toolName?: string
  status?: string
  thinking?: string
}
```

- [ ] **Step 4: Run app + agent tests**

Run: `go test ./internal/app/ ./internal/core/agent/ ./internal/core/llm/ -count=1`

Expected: PASS。`TestChatService_ReplayProjectsAnchors` 仍为 2 条（无 tool 事件）。

- [ ] **Step 5: Commit**

```bash
git add internal/core/llm/port.go internal/core/agent/agent.go internal/app/chat_service.go internal/app/chat_service_test.go app/app.go frontend/src/wails.ts
git commit -m "feat(app): Replay 投影工具卡与 thinking（C-APP-3）"
```

---

### Task 5: markdown 消毒渲染

**Files:**
- Create: `frontend/src/markdown.ts`
- Create: `frontend/src/markdown.test.ts`
- Modify: `frontend/package.json`（由 npm install 改，不要手改 lock 以外的依赖）

**Interfaces:**
- Consumes: `marked.parse`、`DOMPurify.sanitize`
- Produces: `export function renderMarkdown(src: string): string`（永远返回安全 HTML 字符串；失败则转义纯文本）

- [ ] **Step 1: Install deps**

Run from `frontend/`:

```
npm install marked dompurify
npm install -D @types/dompurify
```

- [ ] **Step 2: Write failing test**

`frontend/src/markdown.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  it('渲染代码块与粗体', () => {
    const html = renderMarkdown('**hi**\n\n```go\nfmt.Println(1)\n```')
    expect(html).toContain('<strong>')
    expect(html).toContain('<pre>')
    expect(html).toContain('fmt.Println')
  })

  it('去掉 script 与 javascript URL', () => {
    const html = renderMarkdown('<script>alert(1)</script>[x](javascript:alert(1))')
    expect(html.toLowerCase()).not.toContain('<script')
    expect(html.toLowerCase()).not.toContain('javascript:')
  })

  it('空输入返回空串', () => {
    expect(renderMarkdown('')).toBe('')
  })
})
```

- [ ] **Step 3: Run test to verify fail**

Run: `npm test -- src/markdown.test.ts`（cwd `frontend`）

Expected: FAIL — 无法解析 `./markdown`。

- [ ] **Step 4: Implement**

`frontend/src/markdown.ts`:

```ts
import { marked } from 'marked'
import DOMPurify from 'dompurify'

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

export function renderMarkdown(src: string): string {
  if (!src) return ''
  try {
    const html = marked.parse(src, { async: false }) as string
    return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
  } catch {
    return `<pre>${escapeHtml(src)}</pre>`
  }
}
```

- [ ] **Step 5: Run frontend tests**

Run: `npm test`（cwd `frontend`）

Expected: PASS（含既有 14 个 + 新用例）

- [ ] **Step 6: Commit**

```bash
git add frontend/package.json frontend/package-lock.json frontend/src/markdown.ts frontend/src/markdown.test.ts
git commit -m "feat(ui): markdown 渲染并消毒"
```

---

### Task 6: chat store 接 thinking 与工具全文

**Files:**
- Modify: `frontend/src/stores/chat.ts`
- Modify: `frontend/src/stores/chat.test.ts`

**Interfaces:**
- Consumes: `ChatMessageDTO.thinking/toolName/status`；`onTool` payload `content`
- Produces: `ChatMsg.thinking?: string`；`onChunk` 累加 thinking；`selectSession` 映射新字段；`onTool` 用 `content || summary`

- [ ] **Step 1: Write failing store tests**

Append to `chat.test.ts`（扩展 hoisted mock 的 Replay 返回值——在现有 `Replay: async () => []` 改为可变 `h.replay`）:

在 `h` 中加 `replay: [] as { role: string; content: string; toolName?: string; status?: string; thinking?: string }[]`，`Replay: async () => h.replay`。`beforeEach` 里 `h.replay = []`。

```ts
  it('onChunk 累加 thinking 与 delta', async () => {
    const store = useChatStore()
    await store.newSession()
    store.messages.push({ role: 'assistant', content: '', streaming: true })
    store.onChunk({ sessionID: store.sessionId, delta: 'Hi', thinking: 'plan' })
    expect(store.messages[0].content).toBe('Hi')
    expect(store.messages[0].thinking).toBe('plan')
  })

  it('onTool 保存全文 content', async () => {
    const store = useChatStore()
    await store.newSession()
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'ab…', content: 'abcdef' })
    expect(store.messages[0]).toMatchObject({ role: 'tool', toolName: 'fs', status: 'success', content: 'abcdef' })
  })

  it('selectSession 映射 Replay 的 tool 与 thinking', async () => {
    h.summaries = [{ id: 's-1', title: '' }]
    h.replay = [
      { role: 'user', content: 'u' },
      { role: 'tool', content: 'file-x', toolName: 'fs', status: 'success' },
      { role: 'assistant', content: 'done', thinking: 'plan' },
    ]
    const store = useChatStore()
    await store.loadSessions()
    await store.selectSession('s-1')
    expect(store.messages).toEqual([
      { role: 'user', content: 'u' },
      { role: 'tool', content: 'file-x', toolName: 'fs', status: 'success' },
      { role: 'assistant', content: 'done', thinking: 'plan' },
    ])
  })
```

Update `onTool` 类型：`content?: string`。

- [ ] **Step 2: Run to verify fail**

Run: `npm test -- src/stores/chat.test.ts`（cwd `frontend`）

Expected: FAIL — thinking 未写入 / Replay 丢掉字段。

- [ ] **Step 3: Implement store**

`ChatMsg` 增加 `thinking?: string`。

`selectSession`:

```ts
messages.value = history.map((m) => ({
  role: m.role as ChatMsg['role'],
  content: m.content,
  toolName: m.toolName,
  status: m.status,
  thinking: m.thinking,
}))
```

`onChunk`:

```ts
if (last?.streaming) {
  last.content += p.delta
  if (p.thinking) last.thinking = (last.thinking || '') + p.thinking
}
```

`onTool`:

```ts
function onTool(p: { sessionID: string; name: string; status: string; summary: string; content?: string }) {
  if (p.sessionID !== sessionId.value) return
  messages.value.push({
    role: 'tool',
    content: p.content || p.summary,
    toolName: p.name,
    status: p.status,
    at: Date.now(),
  })
}
```

`App.vue` 里 `EventsOn('chat:tool', ...)` 的 payload 类型同步加 `content: string`（Task 7 会改模板；本任务若编译因 App.vue 未改而仍通过，可先只改 store 回调签名，App.vue 在 Task 7 一起改）。为避免 `vue-tsc` 在 Task 6 提交后因 App.vue 旧签名失败，**本任务同时把 App.vue 的 EventsOn 回调参数加上 content 字段，但不改模板。**

- [ ] **Step 4: Run frontend tests**

Run: `npm test`（cwd `frontend`）

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/stores/chat.ts frontend/src/stores/chat.test.ts frontend/src/App.vue
git commit -m "feat(ui): 会话 store 保留 thinking 与工具全文"
```

---

### Task 7: 对话区 markdown / thinking 折叠 / 工具展开

**Files:**
- Modify: `frontend/src/App.vue`
- Modify: `frontend/src/style.css`

**Interfaces:**
- Consumes: `renderMarkdown`；`ChatMsg.thinking`；工具 `content`
- Produces: 助手气泡 `v-html` 仅经 `renderMarkdown`；thinking 流式默认展开、终态默认折叠；工具卡点击展开 `<pre>`

- [ ] **Step 1: Add CSS**

Append to `style.css`（只用已有令牌）:

```css
.prose-md pre {
  overflow-x: auto;
  padding: 10px 12px;
  border-radius: 12px;
  background: var(--c-surface-soft);
  border: 1px solid var(--c-border);
  font-size: 12px;
  line-height: 1.55;
}
.prose-md code {
  font-family: ui-monospace, Consolas, monospace;
  font-size: 0.92em;
}
.prose-md a {
  color: var(--c-primary);
}
.prose-md p {
  margin: 0 0 0.6em;
}
.prose-md p:last-child {
  margin-bottom: 0;
}
.tool-full {
  max-height: 240px;
  overflow: auto;
  margin-top: 6px;
  padding: 10px 12px;
  border-radius: 12px;
  background: var(--c-surface-soft);
  border: 1px solid var(--c-border);
  font-size: 12px;
  white-space: pre-wrap;
}
```

- [ ] **Step 2: Update App.vue script**

Add:

```ts
import { renderMarkdown } from './markdown'

const thinkingOpen = ref<Record<number, boolean>>({})
const toolOpen = ref<Record<number, boolean>>({})

function isThinkingOpen(i: number, streaming?: boolean) {
  if (i in thinkingOpen.value) return thinkingOpen.value[i]
  return !!streaming
}
function toggleThinking(i: number, streaming?: boolean) {
  thinkingOpen.value[i] = !isThinkingOpen(i, streaming)
}
function chipText(s: string) {
  return s.length > 200 ? s.slice(0, 200) + '…' : s
}
```

`onMounted` 的 `chat:tool` 回调类型加 `content: string`（若 Task 6 已加则保持）。

- [ ] **Step 3: Update templates**

Replace the tool card block with:

```vue
            <div v-if="m.role === 'tool'" class="flex flex-col items-start gap-1">
              <button
                class="inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-xs"
                :class="
                  m.status === 'error'
                    ? 'border-[var(--c-err)] bg-[var(--c-err-soft)] text-[var(--c-err)]'
                    : 'border-[var(--c-border)] bg-[var(--c-ok-soft)] text-[var(--c-text-dim)]'
                "
                @click="toolOpen[i] = !toolOpen[i]"
              >
                <span class="h-1.5 w-1.5 rounded-full" :class="m.status === 'error' ? 'bg-[var(--c-err)]' : 'bg-[var(--c-ok)]'"></span>
                <span class="font-medium text-[var(--c-text)]">{{ m.toolName }}</span>
                <span class="max-w-[420px] truncate">{{ chipText(m.content) }}</span>
              </button>
              <pre v-if="toolOpen[i]" class="tool-full max-w-[85%]">{{ m.content }}</pre>
            </div>
```

Replace assistant inner content:

```vue
            <div v-else class="flex flex-col items-start gap-1">
              <div class="flex items-center gap-2 text-[11px] text-[var(--c-text-faint)]">
                <span class="font-medium text-[var(--c-text-dim)]">AGENT</span><span>{{ fmtTime(m.at) }}</span>
              </div>
              <button
                v-if="m.thinking"
                class="chip text-[11px]"
                @click="toggleThinking(i, m.streaming)"
              >
                思考
              </button>
              <pre
                v-if="m.thinking && isThinkingOpen(i, m.streaming)"
                class="tool-full max-w-[85%] text-[var(--c-text-dim)]"
              >{{ m.thinking }}</pre>
              <div
                class="prose-md max-w-[85%] rounded-2xl border px-4 py-3 text-sm leading-6"
                :class="
                  m.term === 3 || m.term === 4
                    ? 'border-[var(--c-warn)] bg-[var(--c-warn-soft)]'
                    : m.error
                      ? 'border-[var(--c-err)] bg-[var(--c-err-soft)]'
                      : 'border-[var(--c-border)] bg-[var(--c-surface)]'
                "
              >
                <div v-if="m.error || m.term === 3 || m.term === 4" class="whitespace-pre-wrap">{{ m.content }}</div>
                <div v-else v-html="renderMarkdown(m.content)"></div><span v-if="m.streaming" class="caret"></span>
              </div>
            </div>
```

错误/取消/超时路径保持纯文本（终态标签不是 markdown）。

- [ ] **Step 4: Typecheck and unit tests**

Run:

```
npm test
npm run build
```

cwd `frontend`。Expected: vitest PASS；`vue-tsc --noEmit` 与 vite build 成功。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.vue frontend/src/style.css
git commit -m "feat(ui): 对话区 markdown、thinking 折叠与工具展开"
```

---

### Task 8: 文档、契约表、VERSION

**Files:**
- Modify: `docs/CONTRACTS.md`、`docs/ARCHITECTURE.md`、`docs/MILESTONES.md`、`docs/TESTING.md`、`docs/PENDING.md`、`README.md`、`VERSION`
- Create: `docs/adr/0007-tool-result-model-truncation.md`

**Interfaces:**
- Consumes: 本计划已落地的测试名
- Produces: 文档与代码同一提交；VERSION=`0.2.0`

- [ ] **Step 1: Write ADR-0007**

```markdown
# ADR-0007：工具结果在模型视图截断，账本保留全文

日期：2026-09-27 ｜ 状态：已接受

## 背景

跨轮把 `tool_result` 回放进模型上下文后，单次 shell/git/search 输出可达数十 KB，长会话会线性撑爆窗口。若在写入账本时截断，导出、UI 展开和审计会永远失去原文。

## 决策

1. 账本 `tool_result.content` 存全文。
2. `deriveMessages` 发给模型时按 4096 字节截断（UTF-8 符文边界）并标注 `[truncated, original N bytes]`。
3. UI IPC 另限 64KiB，与模型预算独立。

## 后果

- 旧会话重放自动享受同一预算，不必改写 JSONL。
- 同轮多步 ReAct 仍使用内存中的全文（截断只发生在跨轮 derive）。
- 契约 C-AGT-2 锁定此行为。
```

- [ ] **Step 2: Patch CONTRACTS.md**

After C-APP-2 add C-AGT table (IDs and test names exactly as spec). Add C-FS-5~7 rows. Add C-SEARCH table. Add C-APP-3. Append 契约变更记录 dated 2026-09-27.

- [ ] **Step 3: Patch remaining docs**

`ARCHITECTURE.md` 工具表加 search；对话流加「跨轮 derive 含 tool_calls」。  
`MILESTONES.md` 新增 M6 已完成出口清单（实现完成时再勾；本任务先把章节写下，出口标准与 spec §2 一致）。  
`TESTING.md` 契约清单纯 M6。  
`PENDING.md` 顶部更新：本轮交付 C-AGT/C-FS-5~7/C-SEARCH/C-APP-3；未做仍为人工 GUI 与 shell 时序脆弱点。  
`README.md` 内置工具表加 search，fs 行写上 list。  
`VERSION` 改为：

```
0.2.0
```

- [ ] **Step 4: Commit**

```bash
git add docs README.md VERSION
git commit -m "docs: 登记 M6 契约与 ADR-0007；VERSION 0.2.0"
```

---

### Task 9: 全量门禁与发布

**Files:** 无源码（除非门禁报错回修）

- [ ] **Step 1: Format and static gates**

Run from repo root:

```
gofmt -l main.go app internal cmd
go vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/arch_check.ps1
go test ./... -count=1
```

Expected: gofmt 空输出；vet 0；`[ARCH CHECK] PASS`；全部包 ok。若 searchtool 超时用例在负载下红，隔离重跑 `go test ./internal/platform/searchtool/ -count=1` 后再判定（与 `TESTING.md` shell 时序同一纪律）。

- [ ] **Step 2: Frontend gates**

```
cd frontend
npm test
npm run build
```

Expected: vitest 全绿；vue-tsc + vite 成功。构建后若 `frontend/dist/.gitkeep` 丢失，确认 `keepDistPlaceholder` 插件补回。

- [ ] **Step 3: Release pipeline**

```
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1
```

Expected: `dist/tiancode-setup-v0.2.0.exe` 与 `dist/tiancode-v0.2.0-portable.zip` 生成。不要把 `dist/` 提交进 git。

- [ ] **Step 4: Final commit only if Step 1–2 produced fixes**

若门禁迫使改代码，按 Conventional Commits 追加 `fix:` / `test:` 提交，再重跑 Step 1–3。无修复则无需空提交。

---

## Self-review (spec coverage)

| Spec | Task |
| --- | --- |
| §4 跨轮历史 / ID / 截断 / 半截调用 | Task 1 |
| §5.1 fs.list | Task 2 |
| §5.2–5.3 search + newRegistry | Task 3 |
| §6.1 Replay / 导出同源 | Task 4 |
| §6.2 ToolEvent.Content / 事件桥 | Task 4 |
| §6.3 markdown + DOMPurify | Task 5 |
| §6.3 store thinking/tool | Task 6 |
| §6.3 App.vue 折叠/展开 | Task 7 |
| §7 错误处理 | 各任务测试（越界、坏正则、超时、消毒失败回退） |
| §8 契约登记 | Task 8 |
| §9 VERSION 0.2.0 + release | Task 8–9 |
| 明确不做的工作台/git 写 | 无对应任务（有意） |
