package applog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 0.0.09：内部文件日志——排障不能只靠截图猜（用户要求）。
func TestApplog_WritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir("")

	Infof("turn start session=%s model=%s", "s1", "grok")
	Errorf("upstream timeout after %ds", 90)

	// 同日写进同一文件，两行都在
	files, err := filepath.Glob(filepath.Join(dir, "app-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v err=%v, want 1", files, err)
	}
	data, _ := os.ReadFile(files[0])
	content := string(data)
	if !strings.Contains(content, "INFO") || !strings.Contains(content, "turn start session=s1") {
		t.Fatalf("info 行缺失: %q", content)
	}
	if !strings.Contains(content, "ERR ") || !strings.Contains(content, "upstream timeout after 90s") {
		t.Fatalf("err 行缺失: %q", content)
	}
	if !strings.Contains(content, "#0001") || !strings.Contains(content, "#0002") {
		t.Fatalf("行序号缺失: %q", content)
	}
}

func TestApplog_DisabledByDefault(t *testing.T) {
	SetDir("") // 未初始化：写入静默丢弃，不 panic
	Infof("should be dropped")
	// 无断言：不 panic 即通过
}

func TestApplog_Concurrent(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir("")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				Infof("concurrent %d-%d", i, j)
			}
		}(i)
	}
	wg.Wait()
	files, _ := filepath.Glob(filepath.Join(dir, "app-*.log"))
	if len(files) != 1 {
		t.Fatalf("files = %v, want 1", files)
	}
	data, _ := os.ReadFile(files[0])
	lines := strings.Count(string(data), "\n")
	if lines != 32*20 {
		t.Fatalf("lines = %d, want %d（并发丢行）", lines, 32*20)
	}
}

func TestApplog_CleanupKeepsRecentDays(t *testing.T) {
	dir := t.TempDir()
	// 造 10 个旧文件（日期递减）
	base := time.Now()
	for i := 1; i <= 10; i++ {
		name := filepath.Join(dir, "app-"+base.AddDate(0, 0, -i).Format("20060102")+".log")
		if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	SetDir(dir)
	defer SetDir("")
	Infof("today's write") // 触发今天的文件创建
	// 清理后：10 个旧文件保留最近 7 个 + 今天新写入 1 个 = 8
	files, _ := filepath.Glob(filepath.Join(dir, "app-*.log"))
	if len(files) != keepDays+1 {
		t.Fatalf("files = %d, want %d", len(files), keepDays+1)
	}
}

func TestApplog_Truncate(t *testing.T) {
	got := Truncate("第一行\n第二行很长很长", 6)
	if strings.Contains(got, "\n") || !strings.Contains(got, "\\n") {
		t.Fatalf("换行未转义: %q", got)
	}
	if !strings.Contains(got, "…(共") {
		t.Fatalf("未标注原始长度: %q", got)
	}
	if Truncate("short", 10) != "short" {
		t.Fatalf("短文本不应截断")
	}
}
