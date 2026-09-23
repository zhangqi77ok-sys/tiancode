package channels

import "testing"

// presets 是只读内置模板（借 dsh-java channels/presets 的设计）：
// key 唯一、字段完整、协议合法——模板数据错误会在 UI 直接暴露给用户，必须结构自洽。
func TestPresets_StructurallyValid(t *testing.T) {
	ps := Presets()
	if len(ps) < 5 {
		t.Fatalf("presets = %d, want >= 5（主流网关模板）", len(ps))
	}
	seen := make(map[string]bool, len(ps))
	hasCustom := false
	for _, p := range ps {
		if p.Key == "" || p.Name == "" {
			t.Fatalf("preset incomplete: %+v", p)
		}
		if !p.Protocol.Valid() {
			t.Fatalf("preset %s has invalid protocol %q", p.Key, p.Protocol)
		}
		if seen[p.Key] {
			t.Fatalf("duplicate preset key %q", p.Key)
		}
		seen[p.Key] = true
		if p.Key == "custom" {
			hasCustom = true
			continue // 自定义模板的 BaseURL 由用户填写，允许为空
		}
		if p.BaseURL == "" {
			t.Fatalf("preset %s missing baseUrl", p.Key)
		}
		if p.SuggestedModel == "" {
			t.Fatalf("preset %s missing suggestedModel", p.Key)
		}
	}
	if !hasCustom {
		t.Fatal("must provide a 'custom' preset for arbitrary gateways")
	}
}
