// 数据备份测试（0.0.23）：导出→预检→导入闭环 + zip slip 防护 + 坏包拒绝。
// 数据目录用独立 APPDATA 隔离（configfile.Dir() 读环境变量——实测走查时用过
// 同一手法的正式路径），绝不碰用户真实数据。
package app

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withIsolatedAppData 把 %APPDATA% 指向临时目录（t.Setenv 自动复原）。
func withIsolatedAppData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	return filepath.Join(dir, "tiancode")
}

// seedDataDir 造一份"用户数据"：配置 + 两个会话 + 一条记忆。
func seedDataDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"baseUrl":"https://x/v1","apiKey":"k","model":"m"}`)
	write("channels.json", `{"version":3,"channels":[],"activeId":"","approvalTools":[]}`)
	write("memory/global.md", "回复用中文")
	write("sessions/s-1.jsonl", `{"seq":1,"kind":"user_message","data":{"text":"一"}}`+"\n")
	write("sessions/s-2.jsonl", `{"seq":1,"kind":"user_message","data":{"text":"二"}}`+"\n")
}

func TestBackup_ExportPreviewImportRoundtrip(t *testing.T) {
	dir := withIsolatedAppData(t)
	seedDataDir(t, dir)
	s, _ := newMiniService(t)

	dest := filepath.Join(t.TempDir(), "backup.zip")
	m, err := s.ExportBackup(dest, "0.0.23-test")
	if err != nil {
		t.Fatal(err)
	}
	if m.App != "tiancode" || m.Format != 1 || len(m.Files) < 5 {
		t.Fatalf("清单不对：%+v", m)
	}

	// 预检：有效 + 两个会话
	pv := PreviewImport(dest)
	if !pv.Valid {
		t.Fatalf("预检判为无效：%s", pv.Reason)
	}
	if pv.SessionCount != 2 || pv.FileCount < 5 {
		t.Fatalf("预检统计错：%+v", pv)
	}

	// 改掉现有数据（模拟"新机器上什么都没有"或"要覆盖"）
	if err := os.Remove(filepath.Join(dir, "sessions", "s-1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory", "global.md"), []byte("被改过了"), 0o600); err != nil {
		t.Fatal(err)
	}

	written, skipped, err := s.ApplyImport(dest, true)
	if err != nil {
		t.Fatal(err)
	}
	if written < 5 || skipped != 0 {
		t.Fatalf("写入统计错：written=%d skipped=%d", written, skipped)
	}
	// 数据回来了
	if _, err := os.Stat(filepath.Join(dir, "sessions", "s-1.jsonl")); err != nil {
		t.Fatalf("会话未恢复：%v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "memory", "global.md"))
	if string(raw) != "回复用中文" {
		t.Fatalf("记忆未恢复：%q", raw)
	}
}

// 只补新模式：已存在文件跳过，不覆盖。
func TestBackup_ApplyImport_SkipExisting(t *testing.T) {
	dir := withIsolatedAppData(t)
	seedDataDir(t, dir)
	s, _ := newMiniService(t)

	dest := filepath.Join(t.TempDir(), "b.zip")
	if _, err := s.ExportBackup(dest, "t"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory", "global.md"), []byte("本机改的"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 再删掉一个会话：删掉的要补回，改过的要跳过（两个方向各证一次）
	if err := os.Remove(filepath.Join(dir, "sessions", "s-2.jsonl")); err != nil {
		t.Fatal(err)
	}
	written, skipped, err := s.ApplyImport(dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if written != 1 || skipped == 0 {
		t.Fatalf("只补新模式统计错：written=%d skipped=%d（删掉的应补回）", written, skipped)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "s-2.jsonl")); err != nil {
		t.Fatalf("删掉的会话未被补回：%v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "memory", "global.md"))
	if string(raw) != "本机改的" {
		t.Fatalf("只补新模式却覆盖了本机文件：%q", raw)
	}
}

// zip slip：条目路径越界必须被拒（防畸形/恶意包写到数据目录外）。
func TestBackup_ZipSlipRejected(t *testing.T) {
	dir := withIsolatedAppData(t)
	seedDataDir(t, dir)
	s, _ := newMiniService(t)

	evil := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(evil)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../escaped.txt") // 越界条目
	_, _ = w.Write([]byte("pwned"))
	zw.Close()
	f.Close()

	if _, _, err := s.ApplyImport(evil, true); err == nil {
		t.Fatal("zip slip 必须被拒绝")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped.txt")); err == nil {
		t.Fatal("越界文件被写到了数据目录外")
	}
}

// 坏包/非本应用包：预检如实报原因，导入拒绝。
func TestBackup_RejectsForeignZip(t *testing.T) {
	dir := withIsolatedAppData(t)
	seedDataDir(t, dir)
	s, _ := newMiniService(t)

	foreign := filepath.Join(t.TempDir(), "foreign.zip")
	f, _ := os.Create(foreign)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("readme.txt")
	_, _ = w.Write([]byte("not a backup"))
	zw.Close()
	f.Close()

	pv := PreviewImport(foreign)
	if pv.Valid || pv.Reason == "" {
		t.Fatalf("外部包应判为无效并给原因：%+v", pv)
	}
	if _, _, err := s.ApplyImport(foreign, true); err == nil {
		t.Fatal("导入外部包必须拒绝")
	}

	// 根本不是 zip
	junk := filepath.Join(t.TempDir(), "junk.zip")
	if err := os.WriteFile(junk, []byte("not a zip at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pv := PreviewImport(junk); pv.Valid || !strings.Contains(pv.Reason, "zip") {
		t.Fatalf("坏包应报 zip 错误：%+v", pv)
	}
}
