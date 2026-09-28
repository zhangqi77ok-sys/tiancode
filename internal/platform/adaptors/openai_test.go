package adaptors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// OpenAI 适配器五段流水线走通：URL/鉴权头/请求体映射/SSE 解析。
func TestOpenAI_Pipeline(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"from-openai"}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	srv := httptest.NewServer(http200(sse))
	defer srv.Close()

	a := OpenAI{IdleTimeout: 2 * time.Second}
	rc := RouteContext{BaseURL: srv.URL, Credential: "sk-1", Model: "up-model"}
	hdr := mapHeader()
	if err := a.SetupHeaders(rc, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Authorization") != "Bearer sk-1" {
		t.Fatalf("Authorization = %q", hdr.Get("Authorization"))
	}
	if got := a.GetRequestURL(rc); !strings.HasSuffix(got, "/chat/completions") {
		t.Fatalf("url = %q", got)
	}
	body, err := a.ConvertRequest(rc, llmChatRequest())
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req["model"] != "up-model" {
		t.Fatalf("model = %v（rc.Model 必须是 mapping 后的上游模型名）", req["model"])
	}
	resp, err := a.DoRequest(context.Background(), rc, hdr, body)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := a.ConvertResponse(context.Background(), rc, resp)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	terminals := 0
	for c := range ch {
		if c.EndReason != llmEndNone {
			terminals++
			continue
		}
		text.WriteString(c.Delta)
	}
	if text.String() != "from-openai" || terminals != 1 {
		t.Fatalf("text=%q terminals=%d", text.String(), terminals)
	}
	if resp.Request.Header.Get("Authorization") != "Bearer sk-1" {
		t.Fatalf("出站请求未携带 Bearer")
	}
	_ = http.StatusOK
}
