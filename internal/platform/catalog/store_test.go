package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Update 的 Load→改→Save 原子性（0.2.27）：界面保存与 AI（ext_manage）两条写路径
// 各开各的 Update，谁都不许把对方的改动整表抹掉。
func TestStore_UpdateAtomicNoLostUpdate(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "extensions.json"))
	if err := s.Update(func(f *File) error {
		f.MCP = append(f.MCP, Server{ID: "a", Name: "a", Enabled: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(f *File) error {
		f.Skills = append(f.Skills, Skill{ID: "s", Name: "s", Enabled: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MCP) != 1 || len(got.Skills) != 1 {
		t.Fatalf("两次 Update 应各保留一项：mcp=%d skills=%d", len(got.MCP), len(got.Skills))
	}

	// mutate 返回错误：错误上抛且不写盘
	sentinel := errors.New("校验失败")
	if err := s.Update(func(*File) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("mutate 错误应上抛：%v", err)
	}
	got2, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got2.MCP) != 1 || len(got2.Skills) != 1 {
		t.Fatal("失败的 Update 不得改动文件")
	}
}

// 并发 Update：每个写者追加一项，最终全部在（无丢更新）。
// 这是"UI 与 AI 并发写"的直接模拟——此前各自的 Load/Save 会互相覆盖。
func TestStore_UpdateConcurrent(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "extensions.json"))
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Update(func(f *File) error {
				f.Skills = append(f.Skills, Skill{ID: fmt.Sprintf("k%d", i), Name: fmt.Sprintf("k%d", i), Enabled: true})
				return nil
			})
			if err != nil {
				t.Errorf("Update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != n {
		t.Fatalf("并发 Update 丢更新：%d/%d", len(got.Skills), n)
	}
}

// 损坏文件：Load/Update 必须报错（不能静默当空清单——"我的技能全没了"）。
func TestStore_CorruptFileReportsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extensions.json")
	s := New(path)
	if err := s.Save(File{MCP: []Server{{ID: "x", Name: "x", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	// 写坏文件
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("损坏文件 Load 必须报错")
	}
	if err := s.Update(func(*File) error { return nil }); err == nil {
		t.Fatal("损坏文件 Update 必须报错（不得静默覆盖用户数据）")
	}
}
