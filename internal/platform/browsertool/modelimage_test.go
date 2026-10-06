// 视觉反馈（0.0.37）单元用例：截图 → 模型 data URL 的转换与尺寸上限。
package browsertool

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelImageDataURL(t *testing.T) {
	pool := NewPoolAt(t.TempDir())
	tl := pool.NewTab("s-mi")
	dir := filepath.Join(pool.shotRoot, "s-mi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// png 形态
	if err := os.WriteFile(filepath.Join(dir, "shot-0001.png"), []byte("fake-png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := tl.modelImageDataURL("s1/../s-mi/shot-0001.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("应为 png data URL：%q", got[:40])
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, "data:image/png;base64,"))
	if derr != nil || string(raw) != "fake-png-bytes" {
		t.Fatalf("data URL 解码应还原原图：%v", derr)
	}
	// jpg 形态（shrinkShot 的压缩产物）
	if err := os.WriteFile(filepath.Join(dir, "shot-0002.jpg"), []byte("fake-jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = tl.modelImageDataURL("s-mi/shot-0002.jpg")
	if err != nil || !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("jpg 应映射 jpeg MIME：%q %v", got, err)
	}
	// 文件缺失显式报错（调用方把失败原因附进 Content，不静默）
	if _, err := tl.modelImageDataURL("s-mi/shot-9999.png"); err == nil {
		t.Fatal("缺失文件应报错")
	}
}

// 尺寸上限：超过 maxModelImageBytes 拒绝进上下文（token 成本护栏）。
func TestModelImageDataURL_SizeCap(t *testing.T) {
	pool := NewPoolAt(t.TempDir())
	tl := pool.NewTab("s-cap")
	dir := filepath.Join(pool.shotRoot, "s-cap")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, maxModelImageBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "shot-big.png"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tl.modelImageDataURL("s-cap/shot-big.png"); err == nil || !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("超限应拒绝并说明：%v", err)
	}
	// 恰好在限内：放行
	ok := make([]byte, maxModelImageBytes)
	if err := os.WriteFile(filepath.Join(dir, "shot-ok.png"), ok, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tl.modelImageDataURL("s-cap/shot-ok.png"); err != nil {
		t.Fatalf("限内应放行：%v", err)
	}
}
