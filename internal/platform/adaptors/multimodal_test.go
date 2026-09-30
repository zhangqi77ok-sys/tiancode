package adaptors

import (
	"encoding/json"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
)

// 0.0.25 附件轮三协议一致性。
// 事实来源（实机反馈"上传文件或图片后发出去没有回复"）：
//   - derive 在有附件时把正文放进 Parts（文字 + 图片 data URL），Content 也写同一份文字；
//   - OpenAI 兼容读 Parts ✓；Anthropic 只读 Content ✗（那条用户消息一个块都不生成、
//     随后被 flush 丢掉）；Codex 只写 m.Content ✗（空串 input_text）。
// 这个夹具就是 derive 实际产出的形态（Content 与 Parts 同源的文字 + 内联图片）。
const attImageB64 = "UE5HREFUQQ==" // "PNGDATA" 的 base64

const (
	attTextHead = "看这张图和日志\n[图片 shot.png：图像见多模态部分]"
	attTextTail = "\n\n[附件文件 att/app.log 内容如下]\nERROR 第 3 行"
	attText     = attTextHead + attTextTail
)

func attachmentUserMessage() llm.Message {
	return llm.Message{
		Role:    "user",
		Content: attText,
		Parts: []llm.ContentPart{
			{Type: "text", Text: attTextHead},
			{Type: "image_url", Name: "shot.png", ImageURL: "data:image/png;base64," + attImageB64},
			{Type: "text", Text: attTextTail},
		},
	}
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("请求体不是 JSON：%v", err)
	}
	return got
}

// Anthropic：文字 → text block（含内联文件正文），图片 data URL → base64 image block；
// 文字块里不得出现图片 base64，整个请求体里不得出现 http(s) 图片链接。
func TestAnthropic_AttachmentMessageCarriesTextAndInlineImage(t *testing.T) {
	body, err := Anthropic{}.ConvertRequest(RouteContext{Model: "claude-3-5-sonnet"}, llm.ChatRequest{
		Model:    "claude-3-5-sonnet",
		Messages: []llm.Message{attachmentUserMessage()},
	})
	if err != nil {
		t.Fatalf("转换不得报错（附件轮必须能发出去）：%v", err)
	}
	got := decodeBody(t, body)
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %d：%v", len(msgs), got["messages"])
	}
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Fatalf("messages[0].role = %v", m0["role"])
	}
	blocks, _ := m0["content"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("应转出 3 个块（text/image/text）：%v", m0["content"])
	}
	// 文字块：用户原文 + 内联文件正文
	b0 := blocks[0].(map[string]any)
	if b0["type"] != "text" || !strings.Contains(b0["text"].(string), "看这张图和日志") {
		t.Fatalf("blocks[0] = %v", b0)
	}
	b2 := blocks[2].(map[string]any)
	if b2["type"] != "text" || !strings.Contains(b2["text"].(string), "ERROR 第 3 行") {
		t.Fatalf("blocks[2] = %v（内联文件正文必须作为文字块发出去）", b2)
	}
	for _, b := range []map[string]any{b0, b2} {
		if strings.Contains(b["text"].(string), attImageB64) {
			t.Fatalf("图片 base64 不得混进文字块：%v", b["text"])
		}
	}
	// 图片块：base64 内联（不是 http(s) URL）
	b1 := blocks[1].(map[string]any)
	if b1["type"] != "image" {
		t.Fatalf("blocks[1] = %v", b1)
	}
	src, _ := b1["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != attImageB64 {
		t.Fatalf("图片块必须是 base64 内联：%v", src)
	}
	if s := string(body); strings.Contains(s, "http://") || strings.Contains(s, "https://") {
		t.Fatalf("请求体里不得出现 http(s) 图片链接（本地工具不转公网 URL）：%s", s)
	}
}

// Anthropic：Content 与 Parts 都为空 → 明确报错，不静默省略这条用户消息
//（此前静默省略正表现为"消息发出去了，上游什么都没收到"）。
func TestAnthropic_EmptyUserMessageFailsLoud(t *testing.T) {
	_, err := Anthropic{}.ConvertRequest(RouteContext{Model: "claude"}, llm.ChatRequest{
		Messages: []llm.Message{{Role: "user"}},
	})
	if err == nil {
		t.Fatal("没有任何内容的用户消息必须报错，不得省略")
	}
	if !strings.Contains(err.Error(), "多模态片段") {
		t.Fatalf("错误要说清是哪条消息、缺什么：%v", err)
	}
}

// Codex：文本取自 Parts（附件轮的正文在那里），input_text 非空，含"未发送图像"与文件名；
// 请求体里不得出现猜出来的图片字段（该协议这条链路没有图片字段）。
func TestCodex_AttachmentTextOnlyNoInventedImageField(t *testing.T) {
	body, err := Codex{}.ConvertRequest(RouteContext{Model: "gpt-5-codex"}, llm.ChatRequest{
		Model:    "gpt-5-codex",
		Messages: []llm.Message{attachmentUserMessage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBody(t, body)
	input, _ := got["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %v", got["input"])
	}
	item := input[0].(map[string]any)
	content, _ := item["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", item["content"])
	}
	c0 := content[0].(map[string]any)
	if c0["type"] != "input_text" {
		t.Fatalf("content[0] = %v（不得发明别的片段类型）", c0)
	}
	text, _ := c0["text"].(string)
	if strings.TrimSpace(text) == "" {
		t.Fatal("input_text 不得为空串（此前附件轮就是空串）")
	}
	for _, want := range []string{"看这张图和日志", "ERROR 第 3 行", "本协议未发送图像", "shot.png"} {
		if !strings.Contains(text, want) {
			t.Fatalf("input_text 缺 %q：%q", want, text)
		}
	}
	if strings.Contains(text, attImageB64) {
		t.Fatalf("图片 base64 不得进文字：%q", text)
	}
	raw := string(body)
	for _, banned := range []string{"input_image", "image_url", "image/png"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("Codex 请求体不得出现 %q：%s", banned, raw)
		}
	}
}

// OpenAI 兼容：行为与 0.0.10 一致——Parts 进 content 数组（文字 + 图片 data URL），
// 这条只是把适配器入口也锁一遍（实现在 openaiprovider.MarshalChatRequest）。
func TestOpenAI_AttachmentPartsUnchanged(t *testing.T) {
	body, err := OpenAI{}.ConvertRequest(RouteContext{Model: "gpt-4o"}, llm.ChatRequest{
		Model:    "gpt-4o",
		Messages: []llm.Message{attachmentUserMessage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBody(t, body)
	m0 := got["messages"].([]any)[0].(map[string]any)
	parts, ok := m0["content"].([]any)
	if !ok {
		t.Fatalf("Parts 非空时 content 必须是数组：%T", m0["content"])
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %d", len(parts))
	}
	if p0 := parts[0].(map[string]any); !strings.Contains(p0["text"].(string), "看这张图和日志") {
		t.Fatalf("parts[0] = %v", p0)
	}
	img := parts[1].(map[string]any)
	url := img["image_url"].(map[string]any)["url"].(string)
	if url != "data:image/png;base64,"+attImageB64 {
		t.Fatalf("image url = %v（必须是内联 data URL）", url)
	}
	if strings.HasPrefix(url, "http") {
		t.Fatalf("不得是 http(s) URL：%v", url)
	}
	if p2 := parts[2].(map[string]any); !strings.Contains(p2["text"].(string), "ERROR 第 3 行") {
		t.Fatalf("parts[2] = %v", p2)
	}
}
