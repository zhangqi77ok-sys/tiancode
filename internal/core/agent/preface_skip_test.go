package agent

import (
	"testing"

	"tiancode/internal/core/llm"
)

// 第 5 批：preface 未变时不替换 system（前缀逐字节不变 → 上游 prompt cache 命中）；
// 清单变化时必须替换（扩展回合内增删立即生效的既有契约不变）。
func TestApplyDynamicPreface_SkipsUnchanged(t *testing.T) {
	loop := NewLoop(nil, "m", nil)
	loop.SetPrefaceFn(func() string { return "清单 v1" })
	msgs := []llm.Message{{Role: "system", Content: "清单 v1"}, {Role: "user", Content: "hi"}}

	got := loop.applyDynamicPreface(msgs)
	if loop.prefaceSkips != 1 {
		t.Fatalf("相同 preface 应跳过替换：skips=%d", loop.prefaceSkips)
	}
	if got[0].Content != "清单 v1" {
		t.Fatalf("跳过后 system 必须保持不变：%q", got[0].Content)
	}

	// 清单变化：必须替换并重置跳过语义（下一次相同才再跳过）
	loop.SetPrefaceFn(func() string { return "清单 v2" })
	got = loop.applyDynamicPreface(msgs)
	if got[0].Content != "清单 v2" {
		t.Fatalf("清单变化必须替换 system：%q", got[0].Content)
	}
	if loop.prefaceSkips != 1 {
		t.Fatalf("变化不应计入跳过：skips=%d", loop.prefaceSkips)
	}

	// 再次相同：跳过
	loop.applyDynamicPreface(msgs)
	if loop.prefaceSkips != 2 {
		t.Fatalf("第二次相同应再跳过：skips=%d", loop.prefaceSkips)
	}
}
