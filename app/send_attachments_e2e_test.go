package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/app"
)

// 0.0.26 端到端回归：**带附件发送**必须把流推到最后一段（前端事件桥）。
//
// 实机故障"上传文件或图片后发出去没有回复"：SendWithAttachments 丢掉了服务返回的
// 流通道 → 没人消费 → 内核卡在第二块（账本只留一条增量、界面永远"正在思考"、
// 90 秒后被看门狗当"上游黑洞"收掉）。请求侧一直是好的。
// 这个用例用伪造上游 + 真实 ChatService + 真实 Bind，把那条路径整条走一遍——
// 换回"丢通道"的写法，它就会挂（drainTurn 不再被调用 → 收不到任何事件）。
func TestSendWithAttachmentsDeliversStreamToBridge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for _, part := range []string{"这是", "附件", "的说明"} {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", part)
			if f != nil {
				f.Flush()
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f != nil {
			f.Flush()
		}
	}))
	defer srv.Close()

	// 不用 t.TempDir：ChatService 的账本句柄在进程内保持打开，Windows 上会拒绝删除
	//（用可忽略失败的清理，不让"删不掉临时目录"变成测试失败）。
	dir, err := os.MkdirTemp("", "tc-send-att-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	channels := map[string]any{
		"version": 2,
		"channels": []map[string]any{{
			"id": "ch-test", "type": "openai", "name": "fake",
			"baseUrl": srv.URL + "/v1", "credential": "test-key",
			"models": []string{"m1"}, "groups": []string{"default"},
			"status": "enabled", "priority": 100, "weight": 0, "autoBan": false,
		}},
		"activeId": "ch-test",
	}
	raw, _ := json.Marshal(channels)
	channelsPath := filepath.Join(dir, "channels.json")
	if err := os.WriteFile(channelsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	svc, err := app.NewChatService(app.Config{ //nolint:govet // err 已在上面声明
		DataDir:        filepath.Join(dir, "sessions"),
		ChannelsPath:   channelsPath,
		ExtensionsPath: filepath.Join(dir, "extensions.json"),
		TonesPath:      filepath.Join(dir, "tones.json"),
	})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}

	b := New(svc)
	type ev struct {
		name string
		data any
	}
	var events []ev
	b.emit = func(name string, payload any) {
		events = append(events, ev{name, payload})
	}

	att := base64.StdEncoding.EncodeToString([]byte("附件正文：ERROR 第 3 行"))
	attsJSON, _ := json.Marshal([]map[string]string{{
		"kind": "file", "name": "a.txt", "mediaType": "text/plain", "dataB64": att,
	}})
	if err := b.SendWithAttachments("s-att-1", "这是什么", string(attsJSON), ""); err != nil {
		t.Fatalf("带附件发送失败：%v", err)
	}

	// 1) 流必须推到事件桥（丢通道的旧写法这里是空的）
	var text strings.Builder
	terminal := 0
	for _, e := range events {
		if e.name == "chat:chunk" {
			if m, ok := e.data.(map[string]string); ok {
				text.WriteString(m["delta"])
			}
		}
		if e.name == "chat:terminal" {
			terminal++
		}
	}
	if got := text.String(); got != "这是附件的说明" {
		t.Fatalf("流式正文没有完整推到事件桥：%q（事件 %d 个）", got, len(events))
	}
	if terminal != 1 {
		t.Fatalf("终态事件必须恰好一个，实际 %d", terminal)
	}

	// 2) 用户的附件（含内联内容）必须真的落了账本
	led, err := os.ReadFile(filepath.Join(dir, "sessions", "s-att-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(led), "a.txt") {
		t.Fatalf("账本里没有附件引用：%s", string(led))
	}
	if !strings.Contains(string(led), `"inline":"full"`) {
		t.Fatalf("文本附件应内联：%s", string(led))
	}
}
