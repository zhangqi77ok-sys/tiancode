package oemtext

import "testing"

func TestDecode(t *testing.T) {
	if got := Decode([]byte{0xD6, 0xD0, 0xCE, 0xC4}); got != "中文" {
		t.Fatalf("GBK = %q", got)
	}
	if got := Decode([]byte("héllo 世界")); got != "héllo 世界" {
		t.Fatalf("UTF-8 = %q", got)
	}
	if got := Decode(nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
}
