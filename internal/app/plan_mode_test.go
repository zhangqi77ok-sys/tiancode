// 方案模式（0.0.35）用例：只读是**结构保证**——fs 写动作业务拒绝、shell 等写路径
// 工具结构性不在场（工具清单里根本没有），系统说明带方案段；只读动作照常可用。
package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/platform/fstool"
)

// planFS 单元：写动作拒绝（白名单外一律拒），读动作委托。
func TestPlanFS_Gating(t *testing.T) {
	dir := t.TempDir()
	inner := fstool.New(dir)
	p := planFS{inner: inner}

	res, err := p.Execute(context.Background(), mustJSON(t, map[string]any{
		"action": "write", "path": "evil.txt", "content": "x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "方案模式") {
		t.Fatalf("写动作必须业务拒绝：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("文件不得被创建")
	}

	res, err = p.Execute(context.Background(), mustJSON(t, map[string]any{"action": "read", "path": "a.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError && !strings.Contains(res.Content, "nope") {
		// read 委托到真 fs：文件不存在会得到 fs 自己的业务错误（不是方案模式拒绝）
		if strings.Contains(res.Content, "方案模式") {
			t.Fatalf("只读动作不应被方案模式拦截：%+v", res)
		}
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
} // 端到端：方案模式发送 → 模型调 fs.write 被拒（文件不存在）→ 工具清单里没有
// shell → 系统说明带方案段 → 模型随后正常收尾。
func TestSendPlan_ReadOnlyEnforced(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(string(body), "\"fs\"") == false || len(bodies) == 1 {
			// 第 1 次调用：模型试图直接写文件
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"p1\",\"function\":{\"name\":\"fs\",\"arguments\":\"{\\\"action\\\":\\\"write\\\",\\\"path\\\":\\\"evil.txt\\\",\\\"content\\\":\\\"x\\\"}\"}}]}}]}\n\n"))
		} else {
			// 后续：正常收尾
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"方案如下……\"}}]}\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	s := newChannelService(t, Config{})
	defer s.Close()
	if _, err := s.AddChannel(llm.Channel{
		Name: "plan", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}
	l, err := s.ledgerFor("s-plan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": root}); err != nil {
		t.Fatal(err)
	}

	ch, err := s.SendPlan(context.Background(), "s-plan", "把登录功能加上")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	// 1) 文件不得存在
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("方案模式下写文件必须被拦（文件不得存在）")
	}
	// 2) 账本里的 fs 工具结果必须是业务拒绝
	sawRefusal := false
	if err := l.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventToolResult && strings.Contains(string(ev.Data()), "方案模式") {
			sawRefusal = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !sawRefusal {
		t.Fatal("fs.write 应收到方案模式的业务拒绝")
	}
	// 3) 请求侧：工具清单没有 shell/browser/memory，系统说明带方案段
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("上游请求未捕获")
	}
	first := bodies[0]
	if strings.Contains(first, "\"shell\"") {
		t.Fatal("方案模式工具清单不得包含 shell")
	}
	if strings.Contains(first, "\"browser\"") {
		t.Fatal("方案模式工具清单不得包含 browser")
	}
	if !strings.Contains(first, "方案模式") {
		t.Fatal("系统说明必须带方案模式段")
	}
	if !strings.Contains(first, "\"fs\"") {
		t.Fatal("fs（只读包装）必须在场")
	}
}
