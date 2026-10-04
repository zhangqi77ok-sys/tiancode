# 任务清单闭环 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让任务清单从"只给 UI 看的提示文本"变成模型每轮看得见、跨轮不丢、完成状态可核验的系统状态。

**Architecture:** 三档递进，各自独立可发布。档位 1 只动 `derive.go`（把已落账本的 `EventTodo` 投影成模型消息，读数顺带携带 `LatestTodo` 供后续档位复用）；档位 2 动步数分段判据（额度内由模型自评续跑，默认关闭）；档位 3 动 `TodoItem` schema 与工具执行路径（用本轮真实写入记录核账，不信模型自报）。

**Tech Stack:** Go 1.22 / Wails v2 / 六边形分层 / 事件账本 JSONL。**前端零改动**。

**设计文档:** `docs/superpowers/specs/2026-10-04-task-plan-closure-design.md`（已批准）

## Global Constraints

- **Go 不在系统 PATH**，本机位于 `E:\pro\tools\go\bin`。每条命令前先设：`$env:PATH = "E:\pro\tools\go\bin;$env:PATH"`
- **vendor 已入库，禁止新增任何外部依赖**
- **五道门禁全绿才能提交**：`gofmt -l main.go app internal cmd`（须空输出）、`go vet ./...`、`scripts/arch_check.ps1`、`go test ./... -count=1 -timeout 60s`、`golangci-lint run`
- **单任务不超过 3 个文件**（用户纪律）
- **只追加不改写**：账本只追加；任何"读失败"一律显式上抛，禁止静默降级为"没有清单/额度为 0"
- **提交前需用户确认**（本计划不自动执行 git commit）
- 发布前核对三处：装机版本（注册表 `HKCU\...\Uninstall` 的 `DisplayVersion`）、`dist/` 已有产物号、`VERSION`，确认版本号 = 下一个未占用序号

---

# 档位 1：把清单注入模型上下文

## Task 1：派生时注入最新任务清单

**Files:**
- Modify: `internal/core/agent/derive.go`
- Create: `internal/core/agent/todoinject_test.go`

**Interfaces:**
- Consumes: `session.EventTodo`（payload `{"items": []llm.TodoItem}`，`session/todo.go:6`）
- Produces: `func todoNote(items []llm.TodoItem) string`；`DeriveInfo.LatestTodo []llm.TodoItem`（新增字段，Task 4/5 复用，零额外 IO）

- [ ] **Step 1: 写失败测试**

新建 `internal/core/agent/todoinject_test.go`：

```go
package agent

import (
	"strings"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

// appendEvent 是本文件的落账助手。
func appendEvent(t *testing.T, l *session.Ledger, kind session.EventKind, data map[string]any) int64 {
	t.Helper()
	seq, err := l.Append(kind, data)
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// C-AGT-5：账本有 EventTodo 时，派生消息末尾含**最新**一条清单全文，且只有一条。
func TestDerive_InjectsLatestTodoAtEnd(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "干活"})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "旧计划甲", Status: "pending"},
	}})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "新计划乙", Status: "in_progress"},
	}})

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "新计划乙") {
		t.Fatalf("末尾必须是最新清单：%q", last.Content)
	}
	if strings.Contains(last.Content, "旧计划甲") {
		t.Fatalf("只取最新快照，不得带出旧版本：%q", last.Content)
	}
	if n := strings.Count(last.Content, "任务清单·当前"); n != 1 {
		t.Fatalf("清单只注入一次，实际 %d 次", n)
	}
	// 读数必须顺带携带最新条目（档位 2/5 的结构化消费源，避免再扫账本）
	if len(info.LatestTodo) != 1 || info.LatestTodo[0].Text != "新计划乙" {
		t.Fatalf("DeriveInfo 必须携带最新条目：%+v", info.LatestTodo)
	}
}

// 全部 done 的清单**仍然注入**（写明"可收尾"）——模型需要知道计划已清空才会收尾。
func TestDerive_AllDoneTodoStillInjected(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "干活"})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "甲", Status: "done"},
		{Text: "乙", Status: "done"},
	}})

	msgs, _, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "全部条目已标记完成") {
		t.Fatalf("全 done 清单必须注入且写明可收尾：%q", last.Content)
	}
}

// C-AGT-6：极小预算下清单**不被折叠**（五级折叠只作用于 tool/image/body 三个
// 索引表；此测试锁住"当前恰好成立"，防止将来被静默折掉）。
func TestDerive_TodoSurvivesBudgetFolding(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	for _, txt := range []string{"第一轮", "第二轮", "第三轮"} {
		appendEvent(t, l, session.EventUserMessage, map[string]any{"text": txt})
		appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "回复 " + txt})
	}
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "必须全文保留的这一项", Status: "in_progress"},
	}})

	msgs, _, err := deriveMessagesWith(l, DeriveOptions{BudgetTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if !strings.Contains(m.Content, "必须全文保留的这一项") {
			continue
		}
		found = true
		if strings.Contains(m.Content, "已折叠") || strings.Contains(m.Content, "已省略") {
			t.Fatalf("清单被折叠了：%q", m.Content)
		}
	}
	if !found {
		t.Fatalf("极小预算下清单被整段丢弃：%+v", msgs)
	}
}

// C-AGT-7：没有 EventTodo 的账本，派生结果与旧版逐条一致（零噪声）。
func TestDerive_NoTodoNoInjection(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "hi"})

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Content != "hi" {
		t.Fatalf("无清单时不得凭空多出消息：%+v", msgs)
	}
	if info.LatestTodo != nil {
		t.Fatalf("无清单时读数必须为 nil：%+v", info.LatestTodo)
	}
}

// C-AGT-8：清单消息绝不能携带 tool_calls。
// 场景：assistant(text) → todo → tool_call → tool_result。若注入时误设
// lastAssistant，末尾 flush 会把待配对的 tool_calls 并进清单消息。
func TestDerive_TodoMessageNeverCarriesToolCalls(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "改一下"})
	appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "我先看看"})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "改 A 这个文件", Status: "in_progress"},
	}})
	appendEvent(t, l, session.EventToolCall, map[string]any{
		"id": "t1", "name": "fs", "arguments": `{"action":"read","path":"a.go"}`,
	})
	appendEvent(t, l, session.EventToolResult, map[string]any{
		"id": "t1", "name": "fs", "content": "file body", "is_error": false, "title": "a.go",
	})
	appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "读完了"})

	msgs, _, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawTodo := false
	for _, m := range msgs {
		if !strings.Contains(m.Content, "改 A 这个文件") {
			continue
		}
		sawTodo = true
		if len(m.ToolCalls) > 0 {
			t.Fatalf("清单消息吸走了 tool_calls：%+v", m)
		}
	}
	if !sawTodo {
		t.Fatalf("清单消息缺失：%+v", msgs)
	}
}

// C-AGT-9：落在 fork 丢弃区间内的清单不得出现。from_seq 用 Append 返回的真实 seq。
func TestDerive_TodoInsideForkDropIsDiscarded(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	firstSeq := appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "第一轮"})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "被回退掉的旧计划", Status: "pending"},
	}})
	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "第二轮"})
	appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "干完了"})
	appendEvent(t, l, session.EventFork, map[string]any{"from_seq": firstSeq})
	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "重来"})

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "被回退掉的旧计划") {
			t.Fatalf("fork 丢弃区间内的清单复活了：%+v", msgs)
		}
	}
	if info.LatestTodo != nil {
		t.Fatalf("丢弃区间内取到的清单不得进读数：%+v", info.LatestTodo)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
go test ./internal/core/agent/ -run "TestDerive_" -count=1 -timeout 60s
```

Expected: `InjectsLatestTodoAtEnd` / `AllDoneTodoStillInjected` / `TodoSurvivesBudgetFolding` / `TodoMessageNeverCarriesToolCalls` FAIL（注入不存在）；`NoTodoNoInjection` / `TodoInsideForkDropIsDiscarded` PASS；`LatestTodo` 相关断言编译失败（`info.LatestTodo undefined`）。

- [ ] **Step 3: 实现注入**

`internal/core/agent/derive.go` 四处改动：

**改动 1** — `DeriveInfo` 结构体末尾（`Dropped bool` 之后）加字段：

```go
	// LatestTodo 是派生时账本里的最新任务清单（nil = 从未提交过清单）。
	// 随派生读数顺带返回：内核做结构化判定（自主续跑的自评/防自欺）时直接用，
	// 不必再扫一遍账本。落在 fork 丢弃区间内的清单不进这里。
	LatestTodo []llm.TodoItem
```

**改动 2** — `deriveMessagesWith` 内、`var lastAssistant = -1` 之后加变量：

```go
	// latestTodo 是扫描中见到的最新任务清单（全量快照语义，后者覆盖前者）。
	// 为什么不原位投影：EventTodo 是"当前状态"而非"历史事件"，原位投影会在
	// 最终版计划前面堆一串历史版本；只在扫描结束后追加到末尾——位置落在最后
	// 一条 user 消息（当前轮）之后，模型一定看得到。
	var latestTodo []llm.TodoItem
```

**改动 3** — `projectEvent` 的 switch 中、`case session.EventUserMessage:` 之前插入：

```go
		case session.EventTodo:
			// 任务清单（档位 1）：只留最新一条，扫描结束后投影为一条消息。
			// **不设 lastAssistant**——flush() 会把待配对的 tool_calls 并入
			// lastAssistant 指向的消息，误设会让清单吸走下一次工具调用的参数
			//（与 EventUserEdit 同一纪律）。
			var p struct {
				Items []llm.TodoItem `json:"items"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			latestTodo = p.Items
```

**改动 4** — 两处：
a) fork 重启的重置行（`msgs, calls, toolRefs, imageRefs, bodyRefs = nil, nil, nil, nil, nil`）末尾追加 `latestTodo = nil`；
b) `flush()` 之后、`totalTurns := turn + 1` 之前插入投影；`info := DeriveInfo{...}` 之后加一行赋值：

```go
	// 任务清单投影到末尾（档位 1）。全量 done 也照投——模型需要知道"计划已清空"
	// 才会收尾；nil（从未提交）不投——没登记过计划的项目不该凭空多一段说明。
	if len(latestTodo) > 0 {
		msgs = append(msgs, llm.Message{Role: "assistant", Content: todoNote(latestTodo)})
	}
```

```go
	info := DeriveInfo{BudgetTokens: opt.BudgetTokens, LatestTodo: latestTodo}
```

**改动 5** — 文件末尾新增 `todoNote`（import 补 `fmt`，已有 `strings`）：

```go
// todoNote 把任务清单渲染成注入模型的一段说明。
// 逐字输出模型自己写的条目，不改写不摘要——路径与行号必须精确，
// 摘要模型会写错（与本文件拒绝摘要模型同一纪律，见文件头注释）。
func todoNote(items []llm.TodoItem) string {
	label := map[string]string{"pending": "待办", "in_progress": "进行中", "done": "已完成"}
	done := 0
	for _, it := range items {
		if it.Status == "done" {
			done++
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[任务清单·当前] 你在本会话登记的计划（最新一次提交，共 %d 项，已完成 %d 项）：\n", len(items), done)
	for i, it := range items {
		name := label[it.Status]
		if name == "" {
			name = it.Status // 未知状态照原样显示，不静默吞掉
		}
		fmt.Fprintf(&sb, "%d. [%s] %s\n", i+1, name, it.Text)
	}
	if done == len(items) {
		sb.WriteString("全部条目已标记完成——请对照实际产出确认无遗漏，无遗漏即可收尾。")
	} else {
		sb.WriteString("开工前先对照本清单。状态变化时用 todo 工具重新提交**整张清单**（全量快照）。")
	}
	return sb.String()
}
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
go test ./internal/core/agent/ -count=1 -timeout 60s
```

Expected: 全包 ok（含 6 条新测试）。

- [ ] **Step 5: 过五道门禁**（命令见 Global Constraints）

- [ ] **Step 6: 提交（经用户确认后）**

```powershell
git add internal/core/agent/derive.go internal/core/agent/todoinject_test.go
git commit -m "feat(agent): 任务清单注入模型上下文（C-AGT-5~9）

EventTodo 此前只落账本供 Replay，derive 从不投影——模型每轮看不到计划，
跨轮必然失忆、清单不可能被遵守。补 projectEvent 的 EventTodo 分支：
只取最新快照、追加到派生消息末尾（不设 lastAssistant，不吸走 tool_calls）、
不进折叠三索引表（天然免疫，测试锁住）；全 done 也注入（模型才知道可收尾）。
DeriveInfo 顺带携带 LatestTodo 读数，供自主续跑的结构化判定复用。"
```

## Task 2：登记档位 1 契约

**Files:**
- Modify: `docs/CONTRACTS.md`

- [ ] **Step 1: `## C-AGT` 表格末尾追加**

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-AGT-5 | 账本存在 `EventTodo` 时，派生消息末尾含**最新一条**清单全文，且只注入一次；`DeriveInfo.LatestTodo` 同步携带该快照 | `TestDerive_InjectsLatestTodoAtEnd` |
| C-AGT-6 | 清单消息**不被任何一级折叠裁剪**（含 `BudgetTokens=1`），不得折成"已折叠/已省略"或整段丢弃 | `TestDerive_TodoSurvivesBudgetFolding` |
| C-AGT-7 | 无 `EventTodo` 的账本，派生结果与旧版逐条一致（零噪声，`LatestTodo` 为 nil） | `TestDerive_NoTodoNoInjection` |
| C-AGT-8 | 清单消息不得携带 `tool_calls`（注入不设 `lastAssistant`，否则 flush 把待配对调用并入清单） | `TestDerive_TodoMessageNeverCarriesToolCalls` |
| C-AGT-9 | fork 丢弃区间内的清单不得出现（消息与读数都不复活）；**全部 `done` 的清单仍然注入**并写明"可收尾" | `TestDerive_TodoInsideForkDropIsDiscarded` / `TestDerive_AllDoneTodoStillInjected` |

- [ ] **Step 2: `## 契约变更记录` 表格末尾追加**

| 2026-10-04 | **新增 C-AGT-5 ~ C-AGT-9** | 档位 1：任务清单注入模型上下文。根因是 `deriveMessagesWith` 从不投影 `EventTodo`——清单落了账本却只喂给 UI，模型每轮开局看不到计划，表现为"跨轮失忆 + 清单不遵守 + 假完成"。四条实现约束各有锁定测试：只取最新、折叠免疫、不设 `lastAssistant`、fork 区间不复活 | `docs/superpowers/specs/2026-10-04-task-plan-closure-design.md` 档位 1 |

- [ ] **Step 3: 提交（经用户确认后）**

```powershell
git add docs/CONTRACTS.md
git commit -m "docs: 登记 C-AGT-5~9（任务清单注入模型上下文）"
```

---

# 档位 2：自主续跑（额度内由模型自评，默认关闭）

> **必须在档位 1 实测后开始**：自评信任清单，清单看不见时自评无从谈起。

## Task 3：autorun 配置包

**Files:**
- Create: `internal/platform/autorun/autorun.go`
- Create: `internal/platform/autorun/autorun_test.go`

**Interfaces:**
- Produces: `autorun.New(path string) *Store`（空路径 = `%APPDATA%\tiancode\autorun.json`）；`(*Store).Resolve() (int, error)`；`autorun.MaxSegments = 3`；`autorun.File{Segments int}`

- [ ] **Step 1: 写失败测试**

新建 `internal/platform/autorun/autorun_test.go`：

```go
package autorun

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 缺文件 = 0（关闭，不是错误）；正常值透传；硬封顶；负数归零；
// 坏文件必须报错——静默当 0 会让用户以为功能开着（与"静默把语气当空"同一类事故）。
func TestStore_Resolve(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "autorun.json")

	s := New(p)
	if got, err := s.Resolve(); err != nil || got != 0 {
		t.Fatalf("缺文件必须回落到关闭：%d %v", got, err)
	}

	writeFixture(t, p, `{"segments":2}`)
	if got, err := s.Resolve(); err != nil || got != 2 {
		t.Fatalf("正常值未生效：%d %v", got, err)
	}

	writeFixture(t, p, `{"segments":99}`)
	if got, err := s.Resolve(); err != nil || got != MaxSegments {
		t.Fatalf("必须硬封顶在 %d：%d %v", MaxSegments, got, err)
	}

	writeFixture(t, p, `{"segments":-3}`)
	if got, err := s.Resolve(); err != nil || got != 0 {
		t.Fatalf("负数必须归零：%d %v", got, err)
	}

	writeFixture(t, p, `坏 JSON`)
	if _, err := s.Resolve(); err == nil {
		t.Fatal("坏文件必须报错，不得静默当 0")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
go test ./internal/platform/autorun/ -count=1 -timeout 60s
```

Expected: 编译失败（包不存在 / 符号未定义）。

- [ ] **Step 3: 实现**

新建 `internal/platform/autorun/autorun.go`：

```go
// Package autorun 保存"自主续跑"的开关设置（autorun.json，与 tones.json 同目录）。
//
// 为什么单独一个文件：自主续跑是**执行策略**开关（要不要让模型自己决定"是否继续"），
// 既不是渠道连接、也不是回答语气、更不是喂料，与现有任何存储的语义都对不上。
// 它只存一个数字——允许自主续几段。缺文件 = 0（关闭）：这是保守默认，
// "决策权交给模型"的功能必须由用户显式开启（ADR-0009）。
package autorun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"tiancode/internal/platform/configfile"
)

// MaxSegments 是硬上限：配置被手改成天文数字时也不放大失控面。
// 它是"决策权交给模型"之后的唯一刹车——额度必须有上界。
const MaxSegments = 3

// File 是 autorun.json 的内容（字段与文件一一对应）。
type File struct {
	Segments int `json:"segments"` // 0 = 关闭（默认）；>0 = 允许自主续跑的最大段数
}

// Defaults 是缺文件时的设置：{"segments":0}，即关闭。
func Defaults() File { return File{Segments: 0} }

// Store 读写自主续跑设置。
type Store struct {
	path string
	mu   sync.Mutex
}

// New 使用给定路径；空路径则用 %APPDATA%\tiancode\autorun.json。
func New(path string) *Store {
	if path == "" {
		path = filepath.Join(configfile.Dir(), "autorun.json")
	}
	return &Store{path: path}
}

// Resolve 读出可直接注入内核的额度（每轮调用方读一次快照）。
// 缺文件回落 0；负数归零；超上限夹到 MaxSegments。
// 解析失败**返回错误**而不是静默当 0——坏文件必须可见（见包注释）。
func (s *Store) Resolve() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取自主续跑设置失败：%w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return 0, fmt.Errorf("自主续跑设置不是合法 JSON（%s）：%w", s.path, err)
	}
	switch {
	case f.Segments <= 0:
		return 0, nil
	case f.Segments > MaxSegments:
		return MaxSegments, nil
	default:
		return f.Segments, nil
	}
}
```

- [ ] **Step 4: 跑测试确认通过 + 过门禁**

- [ ] **Step 5: 提交（经用户确认后）**

```powershell
git add internal/platform/autorun/
git commit -m "feat(autorun): 自主续跑额度设置包

只读存储（无 Save——当前没有 UI 写入口，用户手改 autorun.json，YAGNI）。
缺文件=0（关闭）是保守默认；坏文件显式报错，绝不静默当 0；硬封顶 3 段。"
```

## Task 4：Loop 额度字段与 Send 接线

**Files:**
- Modify: `internal/core/agent/agent.go`（`Loop` 字段 + `latestTodo` 字段 + Run 里两处赋值 + setter）
- Modify: `internal/app/chat_service.go`（`Config.AutoRunPath` + `ChatService.autoRun` + `NewChatService` 装配 + `Send` 注入）
- Create: `internal/core/agent/autocontinue_test.go`

**Interfaces:**
- Consumes: `autorun.Store.Resolve()`（Task 3）；`DeriveInfo.LatestTodo`（Task 1）
- Produces: `(*Loop).SetAutoContinueSegments(n int)`；`(*Loop).autoQuotaLeft() int`；`Loop.autoBudget`、`Loop.autoSegments`、`Loop.latestTodo []llm.TodoItem`

- [ ] **Step 1: 写失败测试**

新建 `internal/core/agent/autocontinue_test.go`：

```go
package agent

import "testing"

// C-AGT-12（配置侧）：默认额度为 0；setter 生效；负数归零。
func TestLoop_AutoContinueBudgetDefaultsToZero(t *testing.T) {
	loop := NewLoop(nil, "m", nil)
	if loop.autoBudget != 0 {
		t.Fatalf("未开启时额度必须为 0：%d", loop.autoBudget)
	}
	loop.SetAutoContinueSegments(3)
	if loop.autoBudget != 3 {
		t.Fatalf("额度未生效：%d", loop.autoBudget)
	}
	loop.SetAutoContinueSegments(-5)
	if loop.autoBudget != 0 {
		t.Fatalf("负额度必须归零：%d", loop.autoBudget)
	}
}

// 额度耗尽判定：额度是唯一刹车，触顶后 autoQuotaLeft 必须为 0。
func TestLoop_AutoQuotaExhausts(t *testing.T) {
	loop := NewLoop(nil, "m", nil)
	loop.SetAutoContinueSegments(2)
	if got := loop.autoQuotaLeft(); got != 2 {
		t.Fatalf("初始额度 %d，want 2", got)
	}
	loop.autoSegments = 2
	if got := loop.autoQuotaLeft(); got != 0 {
		t.Fatalf("用尽后应为 0，实际 %d", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败** → Expected: `undefined: loop.autoBudget` / `undefined: loop.autoSegments`（编译失败）

- [ ] **Step 3: 实现 agent.go**

**3a** — `Loop` 结构体（`todo todoTrack` 字段之后）加：

```go
	// autoBudget 是本轮允许的**自主续跑段数上限**（0 = 关闭，默认）。
	// 由 Send 每轮从 autorun 设置读出注入（照 SetContextBudget 的模式）：
	// 内核不读配置、不引入新事实源。它是"是否继续"的决策权从用户移交给模型
	// 之后的**唯一刹车**，必须是硬上限（ADR-0009）。
	autoBudget int
	// autoSegments 是本轮已经自主续跑的段数（Loop 每轮重建，天然每轮归零）。
	autoSegments int
	// latestTodo 是本轮派生时账本里的最新任务清单（nil = 从未提交过）。
	// 每轮 Run（含续跑前的重新派生）从 derive 读数里取，不额外扫账本。
	latestTodo []llm.TodoItem
```

**3b** — `SetContextBudget` 之后加：

```go
// SetAutoContinueSegments 设置本轮允许自主续跑的段数上限。
// 0（默认）= 关闭，步数用尽时一律问用户，行为与旧版逐条等价；
// n > 0 = 允许模型在自评通过且未触顶时自主续跑至多 n 段（ADR-0009）。
func (l *Loop) SetAutoContinueSegments(n int) {
	if l == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	l.autoBudget = n
}

// autoQuotaLeft 返回本轮还剩几段自主续跑额度。
func (l *Loop) autoQuotaLeft() int {
	if l.autoBudget <= l.autoSegments {
		return 0
	}
	return l.autoBudget - l.autoSegments
}
```

**3c** — `Run` 内首次派生（`msgs, ctxInfo, err := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})`，约 agent.go:326）的 err 检查之后加：

```go
	l.latestTodo = ctxInfo.LatestTodo
```

**3d** — 续跑前的重新派生（agent.go:766 与 568 附近的两处 `refreshed, next, derr := deriveMessagesWith(...)`）各自 err 检查之后同样加 `l.latestTodo = next.LatestTodo`。

- [ ] **Step 4: 实现 chat_service.go**

**4a** — `Config` 结构体（`MemoryPath` 之后）加：

```go
	// AutoRunPath 是自主续跑额度设置；缺省 %APPDATA%\tiancode\autorun.json（档位 2）。
	// 可注入是为测试隔离（同进程多实例不互相污染）。
	AutoRunPath string
```

**4b** — `ChatService` 结构体加字段（`s.memory` 附近）：

```go
	autoRun *autorun.Store // 自主续跑额度（档位 2，ADR-0009）
```

**4c** — `NewChatService` 内 `s.memory = memory.NewStore(cfg.MemoryPath)` 之后加：

```go
	s.autoRun = autorun.New(cfg.AutoRunPath)
```

**4d** — `Send` 内 `ag.SetTrailingNote(func() string { return s.checkNote(sessionID) })` 之后、"以下三个前置失败路径"注释之前插入：

```go
	// 自主续跑额度（档位 2，ADR-0009）：每轮读一次快照（与语气/记忆同纪律——
	// 改设置不影响进行中的轮次）。读失败显式阻断 Send：静默当 0 会让用户以为
	// 功能开着。此处与 applyExtensionPreface 同属"看门狗启动前的失败路径"：
	// release + cancelRun 就地收尾，不碰 watchStopped。
	segments, aerr := s.autoRun.Resolve()
	if aerr != nil {
		release()
		cancelRun()
		return nil, aerr
	}
	ag.SetAutoContinueSegments(segments)
```

**4e** — import 补 `"tiancode/internal/platform/autorun"`。

- [ ] **Step 5: 跑测试确认通过**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
go test ./internal/core/agent/ ./internal/app/ -count=1 -timeout 60s
go build ./...
```

- [ ] **Step 6: 过门禁 + 提交（经用户确认后）**

```powershell
git add internal/core/agent/agent.go internal/app/chat_service.go internal/core/agent/autocontinue_test.go
git commit -m "feat(agent): 自主续跑额度注入（默认关闭）

Loop 增加 autoBudget/autoSegments/latestTodo 与 SetAutoContinueSegments；
Send 每轮从 autorun 设置读出注入（读失败显式阻断，与语气/记忆同纪律）。
默认 0 = 关闭：步数用尽时行为与旧版逐条等价，零风险。"
```

## Task 5：自评裁决 selfAssess

**Files:**
- Create: `internal/core/agent/selfassess.go`
- Modify: `internal/core/agent/autocontinue_test.go`（追加）

**Interfaces:**
- Consumes: `Loop.latestTodo`（Task 4）、`llm.ChatRuntime.Chat`、`todoNote`（Task 1）
- Produces: `type autoVerdict string`；`verdictDone/verdictContinue/verdictBlocked`；`func parseAutoVerdict(text string) autoVerdict`；`func (l *Loop) selfAssess(ctx context.Context, msgs []llm.Message, items []llm.TodoItem, segment int) (autoVerdict, string, error)`

- [ ] **Step 1: 追加失败测试**

追加到 `internal/core/agent/autocontinue_test.go`（import 补 `"context"`、`"strings"`、`"tiancode/internal/core/llm"`）：

```go
// C-AGT-14：解析只接受三种合法值；JSON 坏、字段缺失、取值非法一律降级 blocked。
// fail-closed——解析不了就问用户，绝不猜一个"继续"出去。
func TestParseAutoVerdict(t *testing.T) {
	cases := []struct {
		in   string
		want autoVerdict
	}{
		{`{"verdict":"done","reason":"都改完了"}`, verdictDone},
		{`前面有闲聊 {"verdict":"continue","reason":"还有三处"} 后面有话`, verdictContinue},
		{`{"verdict":"blocked","reason":"不知道从哪下手"}`, verdictBlocked},
		{`{"verdict":"whatever"}`, verdictBlocked},
		{`{"reason":"缺 verdict 字段"}`, verdictBlocked},
		{`完全不是 JSON`, verdictBlocked},
		{``, verdictBlocked},
	}
	for _, c := range cases {
		if got := parseAutoVerdict(c.in); got != c.want {
			t.Fatalf("parseAutoVerdict(%q) = %q，want %q", c.in, got, c.want)
		}
	}
}

// C-AGT-10：自评调用不得携带工具定义——否则模型会"边自评边继续干活"，
// 自评失去判断意义且步数失控。
func TestLoop_SelfAssessCarriesNoTools(t *testing.T) {
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: `{"verdict":"continue","reason":"还有三处要改"}`}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	loop.SetAutoContinueSegments(2)

	verdict, reason, err := loop.selfAssess(context.Background(),
		[]llm.Message{{Role: "user", Content: "干活"}},
		[]llm.TodoItem{{Text: "甲", Status: "pending"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != verdictContinue || !strings.Contains(reason, "三处") {
		t.Fatalf("裁决解析错：%q / %q", verdict, reason)
	}
	if len(fr.reqs) != 1 {
		t.Fatalf("应只发一次请求，实际 %d", len(fr.reqs))
	}
	if len(fr.reqs[0].Tools) != 0 {
		t.Fatalf("自评调用不得携带工具：%d 个", len(fr.reqs[0].Tools))
	}
}

// C-AGT-13：清单全部 done 时不得采纳 continue（防自欺，结构化判定而非文本匹配）。
func TestLoop_SelfAssessRefusesContinueWhenAllDone(t *testing.T) {
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: `{"verdict":"continue","reason":"我还能干"}`}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	loop.SetAutoContinueSegments(5)

	verdict, _, err := loop.selfAssess(context.Background(),
		[]llm.Message{{Role: "user", Content: "继续"}},
		[]llm.TodoItem{{Text: "甲", Status: "done"}, {Text: "乙", Status: "done"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != verdictDone {
		t.Fatalf("清单全 done 时不得采纳 continue，实际 %q", verdict)
	}
}

// 从未提交过清单（items 为空）时自评不得判 done——没有清单就谈不上"清单完成"，
// 必须降级 blocked 去问用户（否则没有清单的轮次会被无声放行）。
func TestLoop_SelfAssessWithoutTodoIsBlocked(t *testing.T) {
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: `{"verdict":"done","reason":"我觉得完成了"}`}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	loop.SetAutoContinueSegments(2)

	verdict, _, err := loop.selfAssess(context.Background(),
		[]llm.Message{{Role: "user", Content: "干活"}}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != verdictBlocked {
		t.Fatalf("无清单时自评必须降级 blocked，实际 %q", verdict)
	}
}
```

- [ ] **Step 2: 跑测试确认失败** → Expected: `undefined: parseAutoVerdict` / `undefined: selfAssess` / `undefined: verdictDone` 等（编译失败）

- [ ] **Step 3: 实现**

新建 `internal/core/agent/selfassess.go`：

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
)

// autoVerdict 是模型对"还要不要继续"的自主裁决（档位 2，ADR-0009）。
type autoVerdict string

const (
	// verdictDone 清单已清空，直接收尾（不再问用户）。
	verdictDone autoVerdict = "done"
	// verdictContinue 仍有明确未完成项且知道下一步，额度内可自主续跑。
	verdictContinue autoVerdict = "continue"
	// verdictBlocked 无明确下一步 / 反复失败 / 额度耗尽 / 无清单 / 解析失败——
	// 一律问用户。解析失败也归这里：解析不了就问用户，绝不猜一个 continue（fail-closed）。
	verdictBlocked autoVerdict = "blocked"
)

// autoAssessPrompt 是自评的追加提问（只存在于本次请求，不落账本、不进系统提示——
// 与 pinnedCallNote / SetTrailingNote 同一纪律：它是"内部判断"，不是会话事实）。
const autoAssessPrompt = "（本轮内部判断，不是用户原话）你已连续执行 %d 步。" +
	"对照上面的任务清单判断接下来该怎么办，用且仅用一行 JSON 回答，" +
	"不要调用任何工具、不要继续干活：" +
	`{"verdict":"done|continue|blocked","reason":"一句话理由"}。` +
	"done=清单已全部完成；continue=仍有明确未完成项且你知道下一步做什么；" +
	"blocked=没有明确下一步或反复失败。"

// parseAutoVerdict 从模型回复里取裁决：取首个 '{' 到末个 '}' 之间的 JSON 段。
// 解析失败或取值非法一律 verdictBlocked。
func parseAutoVerdict(text string) autoVerdict {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return verdictBlocked
	}
	var p struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &p); err != nil {
		return verdictBlocked
	}
	switch autoVerdict(strings.TrimSpace(p.Verdict)) {
	case verdictDone, verdictContinue, verdictBlocked:
		return autoVerdict(strings.TrimSpace(p.Verdict))
	default:
		return verdictBlocked
	}
}

// selfAssess 让模型对着任务清单自评该不该继续。
//
// 纪律：
//  1. **不带工具**（Tools: nil）——否则模型会"边自评边继续干活"，判断失去意义。
//  2. 判定用**结构化数据**（调用方传入的 items），不做文本匹配：
//     全 done → 强制 done（模型说 continue 也忽略，防自欺）；
//     无清单 → 强制 blocked（没有清单就谈不上"清单完成"，必须问用户）。
//  3. 返回的 reason 是模型的原文（截到 200 字），供落账与 UI 展示。
func (l *Loop) selfAssess(ctx context.Context, msgs []llm.Message, items []llm.TodoItem, segment int) (autoVerdict, string, error) {
	if err := ctx.Err(); err != nil {
		return verdictBlocked, "", err
	}
	ask := fmt.Sprintf(autoAssessPrompt, segment*MaxStepsPerTurn)
	req := make([]llm.Message, 0, len(msgs)+1)
	req = append(req, msgs...)
	req = append(req, llm.Message{Role: "user", Content: ask})

	ch, err := l.runtime.Chat(ctx, llm.ChatRequest{
		Model:    l.model,
		Messages: req,
		Tools:    nil, // 纪律 1：不带工具
	}, llm.DefaultRuntimePolicy())
	if err != nil {
		return verdictBlocked, "", fmt.Errorf("self assess: %w", err)
	}
	var sb strings.Builder
	for c := range ch {
		if c.Delta != "" {
			sb.WriteString(c.Delta)
		}
	}
	// 纪律 2：结构化判定优先于模型自述
	if len(items) == 0 {
		return verdictBlocked, "本轮没有登记任务清单，无法自评", nil
	}
	allDone := true
	for _, it := range items {
		if it.Status != "done" {
			allDone = false
			break
		}
	}
	if allDone {
		return verdictDone, "任务清单已全部完成", nil
	}
	raw := sb.String()
	verdict := parseAutoVerdict(raw)
	reason := strings.TrimSpace(raw)
	if len(reason) > 200 {
		reason = reason[:200] + "…"
	}
	return verdict, reason, nil
}
```

> 注意：`fakeRuntime.reqs` 已记录每次请求的 `Tools` 字段（既有 `TestLoop_StepLimitDeclineEndsNormally` 在 `steplimit_test.go:138` 断言过 `fr.reqs[N].Tools`），上述断言可直接使用，无需改产线代码或测试基建。

- [ ] **Step 4: 跑测试确认通过 + 过门禁 + 提交（经用户确认后）**

```powershell
git add internal/core/agent/selfassess.go internal/core/agent/autocontinue_test.go
git commit -m "feat(agent): 自评裁决 done/continue/blocked

无工具调用 + 结构化防自欺（全 done 强制 done、无清单强制 blocked）+
fail-closed 解析（坏 JSON 一律 blocked 去问用户）。"
```

## Task 6：接线到步数分段

**Files:**
- Modify: `internal/core/agent/agent.go`（`turn` 内步数用尽分支，agent.go:760-787）
- Modify: `internal/core/agent/autocontinue_test.go`（追加）

**Interfaces:**
- Consumes: `selfAssess`（Task 5）、`autoQuotaLeft`（Task 4）、`continueAfterLimit`（ask.go:87）

- [ ] **Step 1: 追加端到端失败测试**

追加到 `autocontinue_test.go`（import 补 `"time"`、`"tiancode/internal/core/session"`、`"tiancode/internal/core/tools"`）：

```go
// C-AGT-12（行为侧）：额度 0 时步数用尽仍走 continueAfterLimit（旧行为逐条等价）。
// 已有 TestLoop_StepLimitAskThenContinue 覆盖该路径（steplimit_test.go），
// 这里补"额度 > 0 且模型答 done → 不问用户直接收尾"的对照。
func TestLoop_AutoAssessDoneEndsWithoutAsking(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	// 前段：模型每步调一次 noop；到步数上限后自评答 done → 直接收尾总结
	script := stepScript(MaxStepsPerTurn)
	script = append(script, []llm.StreamChunk{
		{Delta: `{"verdict":"done","reason":"全部完成"}`}, {EndReason: llm.EndDone},
	})
	// 收尾总结（无工具总结步）
	script = append(script, []llm.StreamChunk{{Delta: "收尾"}, {EndReason: llm.EndDone}})

	fr := &fakeRuntime{script: script}
	registry := tools.NewRegistry()
	if err := registry.Register(&noopTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)
	loop.SetAutoContinueSegments(2)
	asker := &fakeAsker{}
	loop.SetAsker(asker)

	ch, err := loop.Run(context.Background(), ledger, "干一个大活")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 10*time.Second)
	if asker.calls != 0 {
		t.Fatalf("自评 done 时不得打扰用户：asker.calls=%d", asker.calls)
	}
}

// C-AGT-11：触顶后必须回落到问用户（额度是唯一刹车）。
// 额度 1 段：第一段自评 continue（自主续），第二段用尽时额度归零 → 问用户。
func TestLoop_AutoQuotaThenAskUser(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	// 时序：段 1 的 25 步（索引 0-24）→ 自评（索引 25，答 continue）→ 段 2 的 25 步
	//（索引 26-50）→ 额度已尽走询问（不占脚本）→ 用户同意 → 段 3 第 1 步即"收尾"
	//（索引 51，无工具 → 最终回答）。脚本必须与该调用序严格对齐，错位会让自评弹到 noop 响应。
	script := stepScript(MaxStepsPerTurn)
	script = append(script, []llm.StreamChunk{
		{Delta: `{"verdict":"continue","reason":"还有活"}`}, {EndReason: llm.EndDone},
	})
	script = append(script, stepScript(MaxStepsPerTurn)...)
	script = append(script, []llm.StreamChunk{{Delta: "收尾"}, {EndReason: llm.EndDone}})

	fr := &fakeRuntime{script: script}
	registry := tools.NewRegistry()
	if err := registry.Register(&noopTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)
	loop.SetAutoContinueSegments(1)
	asker := &fakeAsker{replies: []string{"继续执行"}}
	loop.SetAsker(asker)

	ch, err := loop.Run(context.Background(), ledger, "干一个更大的活")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 30*time.Second)
	if asker.calls != 1 {
		t.Fatalf("额度触顶后必须问用户：asker.calls=%d", asker.calls)
	}
}
```

> 注意：`noopTool` 与 `stepScript` 已存在于 `steplimit_test.go`（同包测试文件共享，不得重复定义）。

- [ ] **Step 2: 跑测试确认失败**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
go test ./internal/core/agent/ -run "TestLoop_Auto" -count=1 -timeout 60s
```

Expected: `AutoAssessDoneEndsWithoutAsking` FAIL（当前实现无脑问用户，`asker.calls=1`）。

- [ ] **Step 3: 接线**

把 `agent.go:760-787` 的 `if step >= MaxStepsPerTurn { ... }` 整块替换为：

```go
		if step >= MaxStepsPerTurn {
			// 自主续跑（档位 2，ADR-0009）：额度 > 0 时先让模型对照清单自评。
			//   continue 且未触顶 → 自主续一段（不打扰用户，落账留痕）；
			//   done → 清单已清空，跳出走收尾总结；
			//   blocked / 自评失败 / 额度=0 或已尽 → 原询问路径（旧行为）。
			// 判据用结构化 latestTodo（selfAssess 内部强制：全 done 采纳 done、
			// 无清单降级 blocked），不信任模型自述。
			autoCont := false
			allDone := false
			if l.autoQuotaLeft() > 0 {
				v, reason, aerr := l.selfAssess(ctx, msgs, l.latestTodo, segment)
				if aerr == nil {
					allDone = v == verdictDone
					autoCont = v == verdictContinue
					if autoCont {
						// 落账留痕（复用 EventAssistantMsg：前端零改动即可见，
						// Replay 自动投影，derive 会把它并入模型上下文）
						if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{
							"text": fmt.Sprintf("（系统）已连续执行 %d 步；对照任务清单判断仍有明确下一步，自主续跑（第 %d 段，额度上限 %d 段）：%s",
								segment*MaxStepsPerTurn, l.autoSegments+1, l.autoBudget, reason),
						}); err != nil {
							emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist auto continue note: %w", err)})
							return
						}
						l.autoSegments++
					}
				}
				// aerr != nil：自评失败不猜——落到下面问用户（错误不静默吞掉，
				// 已由 selfAssess 返回；此处保持与"无问答通道"相同的保守路径）
			}
			if allDone {
				break // 清单已清空 → 跳出分段循环走"无工具总结"收尾（break 属于本 for）
			}
			if autoCont {
				segment++
				// 续跑前重新折叠（与既有"用户同意续跑"路径同一套动作：
				// 本段已把大量工具输出写进上下文，开局派生已过时）
				refreshed, next, derr := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})
				if derr != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("derive history: %w", derr)})
					return
				}
				l.latestTodo = next.LatestTodo
				msgs = l.attachPreface(refreshed)
				if ev := l.contextEvent(next); ev != nil {
					if forward(llm.StreamChunk{Context: ev}) {
						return
					}
				}
				step = 0 // 重新计一段（for 自增后回到 1）
			} else if l.continueAfterLimit(ctx, segment) {
				segment++
				refreshed, next, derr := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})
				if derr != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("derive history: %w", derr)})
					return
				}
				l.latestTodo = next.LatestTodo
				msgs = l.attachPreface(refreshed)
				if ev := l.contextEvent(next); ev != nil {
					if forward(llm.StreamChunk{Context: ev}) {
						return
					}
				}
				msgs = append(msgs, llm.Message{Role: "user", Content: fmt.Sprintf(
					"用户同意继续：已跑满 %d 步（第 %d 段开始），现在继续执行，仍可使用全部工具。",
					MaxStepsPerTurn, segment)})
				step = 0
			} else if ctx.Err() != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
				return
			} else {
				break
			}
		}
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
go test ./internal/core/agent/ -count=1 -timeout 60s
```

- [ ] **Step 5: 过门禁 + 提交（经用户确认后）**

```powershell
git add internal/core/agent/agent.go internal/core/agent/autocontinue_test.go
git commit -m "feat(agent): 步数分段接自主自评（C-AGT-11~13）

额度内自评 continue 则自主续段并落账留痕（复用 EventAssistantMsg，前端零改动）；
done 直接收尾不打扰；额度触顶/自评失败/blocked 回落原询问路径。
额度=0 时分支与旧版逐条等价。"
```

## Task 7：ADR-0009 与档位 2 契约登记

**Files:**
- Create: `docs/adr/0009-auto-continue-autonomy.md`
- Modify: `docs/CONTRACTS.md`

- [ ] **Step 1: 写 ADR-0009**

内容必须覆盖（照 `docs/adr/0007-approval-gate.md` 的体例）：

- **背景**：四项症状之"要人工续跑"（每 25 步打断一次，大工程要人工点 N 次）；诊断证据（`ask.go:87` `continueAfterLimit` 无条件问用户）。
- **决策**：额度内把"是否继续"的决策权交给模型——自评三态 done/continue/blocked。
- **约束**（五条，全部有锁定测试）：额度硬封顶 3 段（autorun.MaxSegments）；默认 0 关闭；自评调用不带工具（C-AGT-10）；自评失败/解析失败/无清单一律降级问用户（C-AGT-14）；清单全 done 强制采纳 done（C-AGT-13）。
- **后果**：AI 可能误判续跑，额度是唯一刹车；档位 3 未落地前不建议长期开着额度（自评信任清单，清单可能假完成）。
- **替代方案与否决理由**：直接调大 `MaxStepsPerTurn` 被否——失控循环的 token 风险不设防；引入规划子 Agent 被否——跨子系统，需独立 spec。

- [ ] **Step 2: 追加契约 C-AGT-10 ~ C-AGT-14**

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-AGT-10 | 自评调用不得携带工具定义（否则模型边自评边干活，步数失控） | `TestLoop_SelfAssessCarriesNoTools` |
| C-AGT-11 | 自主续跑累计段数不得超过配置额度，触顶后必问用户 | `TestLoop_AutoQuotaExhausts` / `TestLoop_AutoQuotaThenAskUser` |
| C-AGT-12 | 额度为 0（默认未开启）时步数用尽一律问用户，行为与旧版逐条等价 | `TestLoop_AutoContinueBudgetDefaultsToZero` + 既有 `TestLoop_StepLimitAskThenContinue` |
| C-AGT-13 | 清单全部 `done` 时不得续跑（结构化判定强制采纳 done，模型答 continue 也忽略）；**无清单（items 空）时自评强制降级 blocked** | `TestLoop_SelfAssessRefusesContinueWhenAllDone` / `TestLoop_SelfAssessWithoutTodoIsBlocked` |
| C-AGT-14 | 自评响应解析失败、取值非法、缺字段一律降级 `blocked`（问用户），绝不猜 `continue` | `TestParseAutoVerdict` |

- [ ] **Step 3: 追加契约变更记录**

| 2026-10-04 | **新增 C-AGT-10 ~ C-AGT-14** | 档位 2：自主续跑。步数用尽时先让模型对照清单自评，额度内自主续段，不再每 25 步打扰用户。额度硬封顶 3 段、默认 0 关闭、自评不带工具、无清单强制 blocked、解析失败即问用户——五条都是"决策权交给模型"的刹车 | ADR-0009 |

- [ ] **Step 4: 提交（经用户确认后）**

```powershell
git add docs/adr/0009-auto-continue-autonomy.md docs/CONTRACTS.md
git commit -m "docs: ADR-0009 自主续跑 + C-AGT-10~14"
```

---

# 档位 3：客观核账（不信模型自报）

## Task 8：TodoItem 扩展 files 字段与路径归一

**Files:**
- Modify: `internal/core/llm/port.go`（`TodoItem` 加 `Files`）
- Modify: `internal/core/agent/todo.go`（Schema 加字段 + `todoItems` 校验 + `normalizePathKey`）
- Create: `internal/core/agent/todoaudit_test.go`

**Interfaces:**
- Produces: `llm.TodoItem.Files []string`（JSON `files`，可空）；`func normalizePathKey(p string) string`

- [ ] **Step 1: 写失败测试**

新建 `internal/core/agent/todoaudit_test.go`：

```go
package agent

import "testing"

// C-AGT-15：旧形态（无 files）必须仍能解析——向后兼容降级。
func TestTodoItems_FilesOptional(t *testing.T) {
	items, err := todoItems(`{"items":[{"text":"甲","status":"done"}]}`)
	if err != nil {
		t.Fatalf("旧形态必须仍能解析：%v", err)
	}
	if len(items[0].Files) != 0 {
		t.Fatalf("files 应为空：%+v", items[0])
	}
}

// files 是加法：新形态可解析；空白路径与重复路径显式拒绝（模型写错要让它知道）。
func TestTodoItems_ParsesFiles(t *testing.T) {
	items, err := todoItems(`{"items":[{"text":"甲","status":"in_progress","files":["a.go","b/c.go"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(items[0].Files) != 2 || items[0].Files[0] != "a.go" {
		t.Fatalf("files 解析错：%+v", items[0])
	}
	if _, err := todoItems(`{"items":[{"text":"甲","status":"done","files":["  "]}]}`); err == nil {
		t.Fatal("空白路径必须拒，不得静默丢弃")
	}
	if _, err := todoItems(`{"items":[{"text":"甲","status":"done","files":["a.go","A.GO"]}]}`); err == nil {
		t.Fatal("归一后重复的路径必须拒——重复声明会让核账口径混乱")
	}
}

// C-AGT-17：路径归一——Windows 语义（大小写不敏感、斜杠等价、去冗余）。
func TestNormalizePathKey(t *testing.T) {
	cases := [][2]string{
		{`internal\app\x.go`, `INTERNAL/APP/X.GO`},
		{`./a/b.go`, `a\b.go`},
		{`a//b.go`, `a/b.go`},
	}
	for _, c := range cases {
		if normalizePathKey(c[0]) != normalizePathKey(c[1]) {
			t.Fatalf("归一不等价：%q vs %q", c[0], c[1])
		}
	}
	if normalizePathKey(`a/b.go`) == normalizePathKey(`a/c.go`) {
		t.Fatal("不同路径不得归一成同一个键")
	}
}
```

- [ ] **Step 2: 跑测试确认失败** → Expected: `items[0].Files undefined` / `undefined: normalizePathKey`

- [ ] **Step 3: 实现**

**3a** — `internal/core/llm/port.go` 的 `TodoItem` 改为：

```go
// TodoItem 是任务清单的单项。
type TodoItem struct {
	Text   string `json:"text"`
	Status string `json:"status"` // "pending" | "in_progress" | "done"
	// Files 是这一项预期改动的文件（工作区相对路径，可空）。
	// 声明后系统会核对本轮是否真的写入过——声明了却没动会被拒绝（档位 3）。
	// 可空是刻意的：旧账本与"模型没把握"的情形都走"不核对"降级。
	Files []string `json:"files,omitempty"`
}
```

**3b** — `internal/core/agent/todo.go` 的 `Schema()` 中 `items.properties` 增加：

```json
"files": {
  "type": "array",
  "description": "这一项预期改动的文件（工作区相对路径）。声明后系统会核对本轮是否真的写入过——声明了却没动会被拒绝，所以只在确有把握时填；拿不准就留空（留空=不核对）。",
  "items": {"type": "string"}
}
```

**3c** — `todoItems` 校验循环内（`switch it.Status` 之后）追加：

```go
		seen := map[string]bool{}
		for j, f := range it.Files {
			f = strings.TrimSpace(f)
			if f == "" {
				return nil, fmt.Errorf("items[%d].files[%d] must not be blank", i, j)
			}
			key := normalizePathKey(f)
			if seen[key] {
				return nil, fmt.Errorf("items[%d].files has duplicate path %q", i, f)
			}
			seen[key] = true
		}
```

**3d** — 文件末尾新增（import 补 `path/filepath`）：

```go
// normalizePathKey 归一文件路径用于比对：Windows 大小写不敏感、斜杠等价、
// 去掉冗余分隔。核账两侧（模型声明 / 实际写入）都过这个函数，口径才一致。
func normalizePathKey(p string) string {
	s := strings.ReplaceAll(strings.TrimSpace(p), `/`, `\`)
	s = strings.TrimPrefix(s, `.\`)
	return strings.ToLower(filepath.Clean(s))
}
```

- [ ] **Step 4: 跑测试确认通过 + 过门禁 + 提交（经用户确认后）**

```powershell
git add internal/core/llm/port.go internal/core/agent/todo.go internal/core/agent/todoaudit_test.go
git commit -m "feat(todo): 条目可声明预期改动的文件

TodoItem 加 files（可空，加法向后兼容）。空白路径与归一后重复的路径显式拒绝。
路径归一为 Windows 语义，供核账两侧共用。"
```

## Task 9：采集本轮真实写入记录

**Files:**
- Modify: `internal/core/agent/agent.go`（`Loop.written` 字段 + `NewLoop` 初始化 + `finishCall` 采集 + `writeTargetOf`）
- Modify: `internal/core/agent/todoaudit_test.go`（追加）

**Interfaces:**
- Produces: `Loop.written map[string]bool`；`func writeTargetOf(call llm.ToolCall) (string, bool)`

- [ ] **Step 1: 追加失败测试**

追加到 `todoaudit_test.go`（import 补 `"tiancode/internal/core/llm"`）：

```go
// C-AGT-18：只认 fs 的 write/replace；其它 action、其它工具、解析不出的一律不算。
// 白名单口径与 isReadOnlyCall 同一纪律（解析不出 = 不认定）。
func TestWriteTargetOf(t *testing.T) {
	cases := []struct {
		call llm.ToolCall
		want string
		ok   bool
	}{
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"write","path":"a.go","content":"x"}`}, `a.go`, true},
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"replace","path":"a.go","target":"t","replacement":"r"}`}, `a.go`, true},
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"read","path":"a.go"}`}, ``, false},
		{llm.ToolCall{Name: "fs", Arguments: `{"action":"write"}`}, ``, false},
		{llm.ToolCall{Name: "shell", Arguments: `{"command":"echo x > a.go"}`}, ``, false},
		{llm.ToolCall{Name: "fs", Arguments: `坏 JSON`}, ``, false},
	}
	for _, c := range cases {
		got, ok := writeTargetOf(c.call)
		if ok != c.ok || (ok && got != normalizePathKey(c.want)) {
			t.Fatalf("writeTargetOf(%+v) = (%q,%v)，want (%q,%v)", c.call, got, ok, c.want, c.ok)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败** → Expected: `undefined: writeTargetOf`

- [ ] **Step 3: 实现 agent.go**

**3a** — `Loop` 加字段（`latestTodo` 之后）：

```go
	// written 是本轮真实发生过写入的文件集（归一化路径 → true）。
	// 数据源是工具执行结果本身，零新增 IO（档位 3 核账的事实依据）。
	// Loop 每轮重建，天然"只认本轮"。
	written map[string]bool
```

**3b** — `NewLoop` 改为：

```go
func NewLoop(rt llm.ChatRuntime, model string, registry *tools.Registry) *Loop {
	return &Loop{runtime: rt, model: model, registry: registry, written: map[string]bool{}}
}
```

**3c** — `finishCall` 闭包内、`EventToolResult` 落账成功之后（`finishCall` 的 `return nil` 之前）追加：

```go
		// 档位 3：成功写入才记——IsError 的调用没有改成，不算事实。
		if !result.IsError {
			if p, ok := writeTargetOf(call); ok {
				l.written[p] = true
			}
		}
```

**3d** — 文件末尾新增：

```go
// writeTargetOf 判断一次工具调用是否为"写入了某个文件"，返回归一化路径。
// 白名单口径与 isReadOnlyCall 同一纪律：只认 fs 的 write/replace，
// 其余（shell 重定向、MCP 写操作、解析不出的形态）一律不认定——
// 核账宁可漏判也不误判，否则会拿"模型声明得对、系统没认出来"去拒绝模型。
func writeTargetOf(call llm.ToolCall) (string, bool) {
	if call.Name != "fs" {
		return ``, false
	}
	var p struct {
		Action string `json:"action"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &p); err != nil {
		return ``, false
	}
	if p.Action != "write" && p.Action != "replace" {
		return ``, false
	}
	path := strings.TrimSpace(p.Path)
	if path == "" {
		return ``, false
	}
	return normalizePathKey(path), true
}
```

- [ ] **Step 4: 跑测试确认通过 + 过门禁 + 提交（经用户确认后）**

```powershell
git add internal/core/agent/agent.go internal/core/agent/todoaudit_test.go
git commit -m "feat(agent): 采集本轮真实写入文件集

Loop 持有 written（归一化路径集），finishCall 落账成功后按 writeTargetOf
采集，零新增 IO。只认 fs 的 write/replace 且必须成功——核账宁可漏判不误判。"
```

## Task 10：核账判定与契约登记

**Files:**
- Modify: `internal/core/agent/todo.go`（`runTodo` 核账 + `unaudited`）
- Modify: `internal/core/agent/todoaudit_test.go`（追加）
- Modify: `docs/CONTRACTS.md`

**Interfaces:**
- Consumes: `Loop.written`（Task 9）、`normalizePathKey`（Task 8）
- Produces: `func (l *Loop) unaudited(items []llm.TodoItem) []string`

- [ ] **Step 1: 追加失败测试**

追加到 `todoaudit_test.go`（import 补 `"strings"`）：

```go
// C-AGT-16：标 done 且声明 files，但本轮未观察到对其中任一文件的写入 → 计入待纠偏。
func TestUnaudited_DetectsUnwrittenFiles(t *testing.T) {
	l := NewLoop(nil, "m", nil)
	l.written["b.go"] = true

	items := []llm.TodoItem{
		{Text: "已动过", Status: "done", Files: []string{"b.go"}},          // 全写过 → 不报
		{Text: "没动过", Status: "done", Files: []string{"a.go"}},          // 一个没写 → 报
		{Text: "写了一半", Status: "done", Files: []string{"b.go", "c.go"}}, // 部分没写 → 报
		{Text: "没声明", Status: "done"},                                    // 不声明 → 不报
		{Text: "还没做", Status: "in_progress", Files: []string{"z.go"}},    // 非 done → 不报
	}
	bad := l.unaudited(items)
	if len(bad) != 2 {
		t.Fatalf("应报出 2 项，实际 %d：%v", len(bad), bad)
	}
	joined := strings.Join(bad, "；")
	if !strings.Contains(joined, "没动过") || !strings.Contains(joined, "写了一半") {
		t.Fatalf("报出的条目不对：%v", bad)
	}
	if strings.Contains(joined, "已动过") || strings.Contains(joined, "没声明") || strings.Contains(joined, "还没做") {
		t.Fatalf("不该报的报了：%v", bad)
	}
}
```

- [ ] **Step 2: 跑测试确认失败** → Expected: `undefined: l.unaudited`（编译失败）

- [ ] **Step 3: 实现**

**3a** — `internal/core/agent/todo.go` 的 `runTodo` 中，`l.todo.observe(items)` 之前插入：

```go
	// 客观核账（档位 3）：声明 done 但本轮没观察到对所声明文件的写入 → 拒绝。
	// 判据只来自系统侧的写入记录（l.written），不信模型自报。模型可以重交一次
	// 绕过——核账的价值是让漏项当场暴露一次，不是杜绝漏项（设计文档"边界"一节）。
	if bad := l.unaudited(items); len(bad) > 0 {
		return tools.ToolResult{
			Content: fmt.Sprintf("todo 核账未通过，以下条目标为 done 但本轮未观察到对所声明文件的写入：%s。"+
				"要么现在真的改掉，要么把状态改回 in_progress/pending；"+
				"若这些文件已由用户在本会话外完成，请在重交清单时说明。",
				strings.Join(bad, "；")),
			IsError: true, Title: title, Op: op,
		}
	}
```

**3b** — 文件末尾新增：

```go
// unaudited 返回"标 done、声明了 files、但本轮没观察到写入"的条目描述。
// 三类降级放行：未声明 files（旧账本/模型没把握）、非 done、声明文件全部写过。
func (l *Loop) unaudited(items []llm.TodoItem) []string {
	var bad []string
	for _, it := range items {
		if it.Status != "done" || len(it.Files) == 0 {
			continue
		}
		for _, f := range it.Files {
			if !l.written[normalizePathKey(f)] {
				bad = append(bad, fmt.Sprintf("%q（声明改 %s）", it.Text, f))
				break
			}
		}
	}
	return bad
}
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
go test ./internal/core/agent/ ./internal/core/llm/ -count=1 -timeout 60s
```

- [ ] **Step 5: 追加契约 C-AGT-15 ~ C-AGT-18 与变更记录**

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-AGT-15 | `TodoItem.Files` 缺失（旧账本）或条目未声明 files → **不核对、不拒绝**（向后兼容降级） | `TestTodoItems_FilesOptional` / `TestUnaudited_DetectsUnwrittenFiles` |
| C-AGT-16 | 条目标 `done` 且声明 files，但本轮未观察到对其中**任一**文件的写入 → 拒绝该次 todo 提交（`IsError`），给出可执行的纠偏指引 | `TestUnaudited_DetectsUnwrittenFiles` |
| C-AGT-17 | 核账路径比对走 Windows 归一（大小写不敏感、斜杠等价、去 `./`）；空白路径与归一后重复的路径在解析期即拒绝 | `TestNormalizePathKey` / `TestTodoItems_ParsesFiles` |
| C-AGT-18 | 核账只认 `fs.write` / `fs.replace` 的**成功**结果；shell 重定向、MCP 写操作、解析不出的调用一律不认定（宁可漏判不误判）；`written` 每轮 Run 重建，只认本轮 | `TestWriteTargetOf` |

变更记录：

| 2026-10-04 | **新增 C-AGT-15 ~ C-AGT-18** | 档位 3：客观核账。否决"每项加 verify 命令、系统执行验证"方案（等于模型自己出考题自己判卷，且引入命令执行副作用），改用 `files` 声明 + 系统侧写入记录取证，零新增 IO。**边界**：只能证明"文件被写过"，不能证明"改对了"；模型可重交绕过——核账是纠偏不是闸门 | `docs/superpowers/specs/2026-10-04-task-plan-closure-design.md` 档位 3 |

- [ ] **Step 6: 过门禁 + 提交（经用户确认后）**

```powershell
git add internal/core/agent/todo.go internal/core/agent/todoaudit_test.go docs/CONTRACTS.md
git commit -m "feat(todo): 客观核账完成状态（C-AGT-15~18）

条目声明 files 且标 done 时，系统拿本轮真实写入记录核对；没动过就拒绝该次
提交并给出纠偏指引。判据来自系统账本而非模型自报，零新增 IO。"
```

---

## 收尾：三档全部完成后

- [ ] **Step 1: 全量门禁（含前端）**

```powershell
$env:PATH = "E:\pro\tools\go\bin;$env:PATH"
gofmt -l main.go app internal cmd
go vet ./...
go test ./... -count=1 -timeout 60s
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/arch_check.ps1
golangci-lint run
cd frontend; npm run build; npm test; cd ..
```

- [ ] **Step 2: 核对发布三处（防同号不同物）**

装机版本（注册表 `HKCU\...\Uninstall` 的 `DisplayVersion`）、`dist/` 已有产物号、`VERSION` 文件。`VERSION` 必须等于"下一个未占用序号"才能打包；同时同步 README 中的产物文件名。

- [ ] **Step 3: 更新 `docs/MILESTONES.md`**

登记实测证据：门禁结果 + 产物大小 + 装机走查结论。未走完的项如实写"未验证"。

- [ ] **Step 4: 执行打包（经用户确认后）**

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1
```

本地发布，不打 tag、不推送。
