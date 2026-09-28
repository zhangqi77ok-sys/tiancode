package adaptors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/codexauth"
)

// Chat → Responses 转换契约：system 提升为 instructions、item 数组形态、
// store=false/stream=true、tools 转换、上游不接受的字段不出现。
func TestCodexConvertRequest(t *testing.T) {
	req := llm.ChatRequest{
		Model: "gpt-5-codex",
		Messages: []llm.Message{
			{Role: "system", Content: "你是助手"},
			{Role: "user", Content: "你好"},
			{Role: "assistant", Content: "调用工具", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "fs", Arguments: `{"a":1}`}}},
			{Role: "tool", ToolCallID: "call_1", Content: "结果"},
		},
		Tools: []llm.ToolDef{{Name: "fs", Description: "读文件", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	body, err := Codex{}.ConvertRequest(RouteContext{Model: "gpt-5-codex"}, req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if got["instructions"] != "你是助手" {
		t.Fatalf("instructions = %v（system 应提升）", got["instructions"])
	}
	if got["stream"] != true || got["store"] != false {
		t.Fatalf("stream/store = %v/%v（Codex 强制 true/false）", got["stream"], got["store"])
	}
	for _, banned := range []string{"max_output_tokens", "max_completion_tokens", "temperature", "top_p"} {
		if _, ok := got[banned]; ok {
			t.Fatalf("字段 %s 不得出现（Codex 上游不接受）", banned)
		}
	}

	input, _ := got["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input 长度 = %d：%v", len(input), input)
	}
	item0, _ := input[0].(map[string]any)
	if item0["role"] != "user" || item0["type"] != "message" {
		t.Fatalf("input[0] = %v", item0)
	}
	item1, _ := input[1].(map[string]any)
	if item1["role"] != "assistant" {
		t.Fatalf("input[1] = %v", item1)
	}
	item2, _ := input[2].(map[string]any)
	if item2["type"] != "function_call" || item2["call_id"] != "fc_1" || item2["name"] != "fs" {
		t.Fatalf("input[2] = %v（call_ 应规范化为 fc_）", item2)
	}
	item3, _ := input[3].(map[string]any)
	if item3["type"] != "function_call_output" || item3["call_id"] != "fc_1" || item3["output"] != "结果" {
		t.Fatalf("input[3] = %v", item3)
	}
	// system 项不得出现在 input（Codex 不接受 role:system）
	for _, it := range input {
		if m, ok := it.(map[string]any); ok && m["role"] == "system" {
			t.Fatal("system 消息必须已提升，不得留在 input")
		}
	}

	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", got["tools"])
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["type"] != "function" || tool0["name"] != "fs" || tool0["strict"] != false {
		t.Fatalf("tools[0] = %v", tool0)
	}
}

// 无 system / 无工具时 instructions 仍存在（上游要求该字段）。
func TestCodexConvertRequest_EmptyInstructionsStillPresent(t *testing.T) {
	body, err := Codex{}.ConvertRequest(RouteContext{Model: "m"}, llm.ChatRequest{
		Model: "m", Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["instructions"]; !ok || v != "" {
		t.Fatalf("instructions 必须存在（可空串）：%v ok=%v", v, ok)
	}
}

// 身份头与鉴权：OAuth 凭证 → Bearer + chatgpt-account-id + 三件套配套；
// 普通 Key → Bearer（无 account-id）。
func TestCodexSetupHeaders(t *testing.T) {
	cred := codexauth.Credential{
		Type: codexauth.CredentialType, AccessToken: "at-1", ChatGPTAccountID: "acc-1",
	}
	js, err := cred.JSON()
	if err != nil {
		t.Fatal(err)
	}
	hdr := http.Header{}
	if err := (Codex{}).SetupHeaders(RouteContext{ChannelID: "ch1", Credential: js}, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Authorization") != "Bearer at-1" {
		t.Fatalf("Authorization = %q", hdr.Get("Authorization"))
	}
	if hdr.Get("chatgpt-account-id") != "acc-1" {
		t.Fatalf("chatgpt-account-id = %q", hdr.Get("chatgpt-account-id"))
	}
	if hdr.Get("originator") != "codex-tui" || !strings.HasPrefix(hdr.Get("User-Agent"), "codex-tui/") {
		t.Fatalf("身份三件套错配：originator=%q UA=%q", hdr.Get("originator"), hdr.Get("User-Agent"))
	}
	if hdr.Get("version") == "" || hdr.Get("OpenAI-Beta") != "responses=experimental" {
		t.Fatalf("version/OpenAI-Beta 缺失：%q/%q", hdr.Get("version"), hdr.Get("OpenAI-Beta"))
	}
	if hdr.Get("session_id") == "" || hdr.Get("session_id") != hdr.Get("conversation_id") {
		t.Fatalf("session_id/conversation_id 应一致且非空：%q/%q", hdr.Get("session_id"), hdr.Get("conversation_id"))
	}

	// 同一渠道的会话 ID 必须稳定（会话漂移影响上游缓存）
	hdr2 := http.Header{}
	if err := (Codex{}).SetupHeaders(RouteContext{ChannelID: "ch1", Credential: js}, hdr2); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("session_id") != hdr2.Get("session_id") {
		t.Fatal("同渠道会话 ID 应稳定")
	}

	// 普通 Key：Bearer 且无 account-id
	hdr3 := http.Header{}
	if err := (Codex{}).SetupHeaders(RouteContext{Credential: "sk-key"}, hdr3); err != nil {
		t.Fatal(err)
	}
	if hdr3.Get("Authorization") != "Bearer sk-key" || hdr3.Get("chatgpt-account-id") != "" {
		t.Fatalf("Key 形态头 = %q / %q", hdr3.Get("Authorization"), hdr3.Get("chatgpt-account-id"))
	}
}

// Codex SSE 流：文本/思考/工具分片/终态 + usage；失败与提前关闭的终态语义。
func TestCodexStream(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"status":"in_progress"}}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"想一下"}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"你好"}`,
		"",
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"fc_x","name":"fs"}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"a\""}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":":1}"}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	t.Cleanup(srv.Close)

	var text, thinking strings.Builder
	var args strings.Builder
	var terminal llm.StreamChunk
	idName := map[int][2]string{}
	codex := Codex{HTTPClient: srv.Client()}
	hdr := http.Header{}
	resp, err := codex.DoRequest(context.Background(), RouteContext{BaseURL: srv.URL}, hdr, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := codex.ConvertResponse(context.Background(), RouteContext{}, resp)
	if err != nil {
		t.Fatal(err)
	}
	for c := range ch {
		if c.EndReason != llm.EndNone {
			terminal = c
			continue
		}
		text.WriteString(c.Delta)
		thinking.WriteString(c.Thinking)
		for _, tc := range c.ToolCalls {
			if tc.ID != "" || tc.Name != "" {
				idName[tc.Index] = [2]string{tc.ID, tc.Name}
			}
			args.WriteString(tc.ArgumentsDelta)
		}
	}
	if text.String() != "你好" || thinking.String() != "想一下" {
		t.Fatalf("text=%q thinking=%q", text.String(), thinking.String())
	}
	if idName[1] != [2]string{"fc_x", "fs"} || args.String() != `{"a":1}` {
		t.Fatalf("tool = %v args=%q", idName[1], args.String())
	}
	if terminal.EndReason != llm.EndDone || terminal.Usage == nil || terminal.Usage.TotalTokens != 15 {
		t.Fatalf("terminal = %+v", terminal)
	}

	// response.failed → EndError 且带原因
	failSSE := `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"x","message":"账号被限流"}}}`
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(failSSE))
	}))
	t.Cleanup(srv2.Close)
	resp2, err := codex.DoRequest(context.Background(), RouteContext{BaseURL: srv2.URL}, http.Header{}, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := codex.ConvertResponse(context.Background(), RouteContext{}, resp2)
	if err != nil {
		t.Fatal(err)
	}
	var term2 llm.StreamChunk
	for c := range ch2 {
		if c.EndReason != llm.EndNone {
			term2 = c
		}
	}
	if term2.EndReason != llm.EndError || term2.Err == nil || !strings.Contains(term2.Err.Error(), "账号被限流") {
		t.Fatalf("failed 终态 = %+v", term2)
	}

	// 无终态直接断流 → EndError（绝不假装成功）
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"半截\"}\n\n"))
	}))
	t.Cleanup(srv3.Close)
	resp3, _ := codex.DoRequest(context.Background(), RouteContext{BaseURL: srv3.URL}, http.Header{}, []byte("{}"))
	ch3, err := codex.ConvertResponse(context.Background(), RouteContext{}, resp3)
	if err != nil {
		t.Fatal(err)
	}
	var term3 llm.StreamChunk
	for c := range ch3 {
		if c.EndReason != llm.EndNone {
			term3 = c
		}
	}
	if term3.EndReason != llm.EndError {
		t.Fatalf("断流终态 = %+v（应 EndError）", term3)
	}
}

// 非 2xx：ConvertResponse 之前显式报错（错误正文带上游信息）。
func TestCodexHTTPErrorIsPreStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"token expired"}}`))
	}))
	t.Cleanup(srv.Close)
	codex := Codex{HTTPClient: srv.Client()}
	resp, err := codex.DoRequest(context.Background(), RouteContext{BaseURL: srv.URL}, http.Header{}, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codex.ConvertResponse(context.Background(), RouteContext{}, resp); err == nil {
		t.Fatal("非 2xx 必须报错")
	} else if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "token expired") {
		t.Fatalf("错误信息 = %v", err)
	}
}

// call_id 规范化：幂等、前缀正确、超长确定性截断。
func TestNormalizeCallID(t *testing.T) {
	cases := map[string]string{
		"call_abc":  "fc_abc",
		"fc_abc":    "fc_abc",
		"other":     "fc_other",
		"":          "",
		"ctc_keep":  "ctc_keep",
		"tsc_keep2": "tsc_keep2",
	}
	for in, want := range cases {
		if got := normalizeCallID(in); got != want {
			t.Fatalf("normalizeCallID(%q) = %q, want %q", in, got, want)
		}
	}
	long := "call_" + strings.Repeat("x", 200)
	got := normalizeCallID(long)
	if len(got) > 64 || got != normalizeCallID(long) || !strings.HasPrefix(got, "fc_") {
		t.Fatalf("超长 ID 规范化失败：len=%d %q", len(got), got)
	}
}
