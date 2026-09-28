package adaptors

import (
	"net/http"
	"testing"

	"tiancode/internal/core/llm"
)

// 渠道级鉴权：各形态构造头/URL 的语义（0.2.20，对齐 new-api Auth 三字段并通用化）。
func TestApplyAuth_Modes(t *testing.T) {
	cases := []struct {
		name     string
		auth     *llm.AuthConfig
		cred     string
		wantAuth string // Authorization 头期望值（"" 表示不得存在）
		wantHdr  [2]string
		wantErr  bool
	}{
		{"缺省走协议默认 Bearer", nil, "k1", "Bearer k1", [2]string{}, false},
		{"default 显式回落协议默认", &llm.AuthConfig{Type: llm.AuthDefault}, "k1", "Bearer k1", [2]string{}, false},
		{"bearer 显式", &llm.AuthConfig{Type: llm.AuthBearer}, "k1", "Bearer k1", [2]string{}, false},
		{"none 不设任何鉴权头", &llm.AuthConfig{Type: llm.AuthNone}, "k1", "", [2]string{}, false},
		{"header 仅凭证（api-key 形态）", &llm.AuthConfig{Type: llm.AuthHeader, Name: "api-key", Value: "{api_key}"}, "k1", "", [2]string{"api-key", "k1"}, false},
		{"header 带前缀模板", &llm.AuthConfig{Type: llm.AuthHeader, Name: "X-Token", Value: "Token {api_key}"}, "k1", "", [2]string{"X-Token", "Token k1"}, false},
		{"header 无值 = 仅凭证", &llm.AuthConfig{Type: llm.AuthHeader, Name: "X-Key"}, "k1", "", [2]string{"X-Key", "k1"}, false},
		{"空凭证不设鉴权", &llm.AuthConfig{Type: llm.AuthBearer}, "", "", [2]string{}, false},
		{"header 缺名称报错", &llm.AuthConfig{Type: llm.AuthHeader}, "k1", "", [2]string{}, true},
		{"query 缺名称报错", &llm.AuthConfig{Type: llm.AuthQuery}, "k1", "", [2]string{}, true},
		{"未知类型报错", &llm.AuthConfig{Type: "magic"}, "k1", "", [2]string{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := RouteContext{Credential: tc.cred, Auth: tc.auth}
			hdr := http.Header{}
			err := ApplyAuth(rc, hdr, AuthDefaultBearer)
			if tc.wantErr {
				if err == nil {
					t.Fatal("应报错（显式拒绝而非静默）")
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyAuth: %v", err)
			}
			if got := hdr.Get("Authorization"); got != tc.wantAuth {
				t.Fatalf("Authorization = %q, want %q", got, tc.wantAuth)
			}
			if tc.wantHdr[0] != "" {
				if got := hdr.Get(tc.wantHdr[0]); got != tc.wantHdr[1] {
					t.Fatalf("%s = %q, want %q", tc.wantHdr[0], got, tc.wantHdr[1])
				}
			}
		})
	}
}

// query 型鉴权：URL 拼接（含已有 query 时用 &）。
func TestWithAuthQuery(t *testing.T) {
	rc := RouteContext{Credential: "k1", Auth: &llm.AuthConfig{Type: llm.AuthQuery, Name: "key", Value: "{api_key}"}}
	if got := WithAuthQuery("https://up/v1/messages", rc); got != "https://up/v1/messages?key=k1" {
		t.Fatalf("拼接 = %q", got)
	}
	if got := WithAuthQuery("https://up/v1/messages?api-version=2024", rc); got != "https://up/v1/messages?api-version=2024&key=k1" {
		t.Fatalf("已有 query 时 = %q", got)
	}
	// 非 query 型 / 无凭证：原样
	if got := WithAuthQuery("https://up/x", RouteContext{Credential: "k1"}); got != "https://up/x" {
		t.Fatalf("非 query 型不得改写：%q", got)
	}
	if got := WithAuthQuery("https://up/x", RouteContext{Auth: rc.Auth}); got != "https://up/x" {
		t.Fatalf("无凭证不得追加：%q", got)
	}
}

// 头覆写支持 {api_key} 插值（多 Key 轮询下始终是最新选出的凭证）。
func TestApplyHeaderOverrideRendersAPIKey(t *testing.T) {
	hdr := http.Header{}
	ApplyHeaderOverride(hdr, map[string]string{
		"api-key":       "{api_key}",
		"Authorization": "{api_key}", // 无 Bearer 前缀形态
		"X-Static":      "fixed",
		"X-Tpl":         "Token {api_key}",
	}, "k9")
	if hdr.Get("api-key") != "k9" || hdr.Get("Authorization") != "k9" {
		t.Fatalf("插值失败：api-key=%q Authorization=%q", hdr.Get("api-key"), hdr.Get("Authorization"))
	}
	if hdr.Get("X-Static") != "fixed" || hdr.Get("X-Tpl") != "Token k9" {
		t.Fatalf("静态值/模板渲染异常：%q %q", hdr.Get("X-Static"), hdr.Get("X-Tpl"))
	}
}

// 模板渲染词法：只认 {api_key}，空模板 = 仅凭证。
func TestRenderAuthValue(t *testing.T) {
	if got := RenderAuthValue("", "k"); got != "k" {
		t.Fatalf("空模板 = %q, want 仅凭证", got)
	}
	if got := RenderAuthValue("Bearer {api_key}", "k"); got != "Bearer k" {
		t.Fatalf("模板 = %q", got)
	}
	if got := RenderAuthValue("plain", "k"); got != "plain" {
		t.Fatalf("无占位符应原样 = %q", got)
	}
}
