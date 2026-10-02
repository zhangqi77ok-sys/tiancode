package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }

func TestAppendReadDelete_Global(t *testing.T) {
	s := newTestStore(t)
	// 空记忆：读 = 空切片，不是错误（"还没有记忆"是常态）
	lines, err := s.Lines(ScopeGlobal, "")
	if err != nil || len(lines) != 0 {
		t.Fatalf("空记忆应读出空切片：%v %v", lines, err)
	}
	if err := s.Append(ScopeGlobal, "", "用户偏好 pnpm"); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ScopeGlobal, "", "回复用中文"); err != nil {
		t.Fatal(err)
	}
	lines, err = s.Lines(ScopeGlobal, "")
	if err != nil || len(lines) != 2 || lines[0] != "用户偏好 pnpm" {
		t.Fatalf("追加后读回应为 2 条：%v %v", lines, err)
	}
	// 删除第 1 条；越界删除显式报错
	if err := s.Delete(ScopeGlobal, "", 1); err != nil {
		t.Fatal(err)
	}
	if lines, _ = s.Lines(ScopeGlobal, ""); len(lines) != 1 || lines[0] != "回复用中文" {
		t.Fatalf("删除后应剩 1 条：%v", lines)
	}
	if err := s.Delete(ScopeGlobal, "", 5); err == nil {
		t.Fatal("越界删除必须报错（绝不静默删错行）")
	}
	if err := s.Append(ScopeGlobal, "", "   "); err == nil {
		t.Fatal("空内容追加必须报错")
	}
}

func TestWorkspace_SameRootSameFile_DifferentRootsIsolated(t *testing.T) {
	s := newTestStore(t)
	// 同一目录的两种写法（大小写/反斜杠/尾斜杠）必须落到同一份记忆
	if err := s.Append(ScopeWorkspace, `D:\Work\Proj`, "提交用 conventional commits"); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Lines(ScopeWorkspace, "d:/work/proj/")
	if err != nil || len(lines) != 1 {
		t.Fatalf("同一目录不同写法应读到同一份：%v %v", lines, err)
	}
	// 另一个项目互不可见
	other, err := s.Lines(ScopeWorkspace, `E:\Other`)
	if err != nil || len(other) != 0 {
		t.Fatalf("别的项目不应看到这份记忆：%v %v", other, err)
	}
}

func TestWorkspace_EmptyRootErrors(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Lines(ScopeWorkspace, ""); err == nil {
		t.Fatal("纯对话（无 root）读 workspace 记忆必须报错")
	}
	if err := s.Append(ScopeWorkspace, "  ", "x"); err == nil {
		t.Fatal("无 root 追加 workspace 记忆必须报错")
	}
	if _, err := s.pathOf("bogus", ""); err == nil {
		t.Fatal("未知 scope 必须报错")
	}
}

func TestAppend_MultiLineSplitsTrimsAndCaps(t *testing.T) {
	s := newTestStore(t)
	if err := s.Append(ScopeGlobal, "", "第一条\n\n第二条\n"); err != nil {
		t.Fatal(err)
	}
	lines, _ := s.Lines(ScopeGlobal, "")
	if len(lines) != 2 || lines[1] != "第二条" {
		t.Fatalf("多行应按行拆条、空行丢弃：%v", lines)
	}
	// 行数硬顶：超限拒绝且不落盘
	s2 := newTestStore(t)
	err := s2.Append(ScopeGlobal, "", strings.Repeat("行\n", maxLines+1))
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超过行数上限必须显式报错：%v", err)
	}
}

func TestTool_AppendReadDelete(t *testing.T) {
	s := newTestStore(t)
	tool := NewTool(s, `D:\Proj`) // workspace scope 的落点
	if tool.Name() != "memory" {
		t.Fatalf("工具名 = %q", tool.Name())
	}
	call := func(args string) (string, bool) {
		t.Helper()
		res, err := tool.Execute(context.Background(), jsonRaw(args))
		if err != nil {
			t.Fatalf("机制错误：%v", err)
		}
		return res.Content, res.IsError
	}
	content, isErr := call(`{"action":"append","scope":"workspace","text":"本项目用 pnpm"}`)
	if isErr || content != "已记住：本项目用 pnpm" {
		t.Fatalf("append 结果异常：%q %v", content, isErr)
	}
	content, isErr = call(`{"action":"read","scope":"workspace"}`)
	if isErr || !strings.Contains(content, "1. 本项目用 pnpm") {
		t.Fatalf("read 应带行号列出：%q %v", content, isErr)
	}
	content, isErr = call(`{"action":"delete","scope":"workspace","line":1}`)
	if isErr {
		t.Fatalf("delete 失败：%q", content)
	}
	content, _ = call(`{"action":"read","scope":"workspace"}`)
	if content != "（该作用域还没有记忆）" {
		t.Fatalf("删空后 read 应给空态文案：%q", content)
	}
}

func TestTool_BadArgsAndScopes(t *testing.T) {
	s := newTestStore(t)
	tool := NewTool(s, "") // 纯对话：workspace scope 不可用
	if _, err := tool.Execute(context.Background(), jsonRaw(`{bad`)); err != nil {
		t.Fatalf("坏 JSON 应走 IsError 而非机制错误（模型可继续推理）：%v", err)
	}
	res, _ := tool.Execute(context.Background(), jsonRaw(`{"action":"append","scope":"workspace","text":"x"}`))
	if !res.IsError || res.Content == "" {
		t.Fatalf("无 root 的 workspace scope 必须业务失败：%+v", res)
	}
	res, _ = tool.Execute(context.Background(), jsonRaw(`{"action":"append","scope":"nope","text":"x"}`))
	if !res.IsError {
		t.Fatal("未知 scope 必须业务失败")
	}
	res, _ = tool.Execute(context.Background(), jsonRaw(`{"action":"append","scope":"global","text":""}`))
	if !res.IsError {
		t.Fatal("append 缺 text 必须业务失败")
	}
	res, _ = tool.Execute(context.Background(), jsonRaw(`{"action":"bogus","scope":"global"}`))
	if !res.IsError {
		t.Fatal("未知 action 必须业务失败")
	}
}
