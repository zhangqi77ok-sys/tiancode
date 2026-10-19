package exttools

import (
	"encoding/json"
	"strings"
	"testing"

	"tiancode/internal/platform/mcpclient"
)

// 2026-10-19：tool=list 的输出带参数 schema（有界），模型不必盲猜参数名。
func TestFormatToolList_IncludesBoundedSchema(t *testing.T) {
	huge := `{"type":"object","description":"` + strings.Repeat("x", 5000) + `"}`
	out := formatToolList([]mcpclient.Tool{
		{Name: "echo", Description: "回显", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)},
		{Name: "big", Description: "大", InputSchema: json.RawMessage(huge)},
		{Name: "noargs", Description: "无参"},
	})
	if !strings.Contains(out, "- echo：回显") || !strings.Contains(out, `参数：{"type":"object","properties":{"text"`) {
		t.Fatalf("应列出名字、说明与 schema：%s", out)
	}
	if len(out) > 4000 {
		t.Fatalf("单个工具的 schema 应有界：len=%d", len(out))
	}
	if strings.Count(out, "参数：") != 2 {
		t.Fatalf("没有 schema 的工具不应输出参数行：%s", out)
	}
	if formatToolList(nil) != "该服务器没有公布工具" {
		t.Fatal("空清单说明不符")
	}
}
