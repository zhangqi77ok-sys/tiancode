package channels

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.0.05：approvalTools 持久化语义修复——
//   - 缺字段的旧文件（旧版本/显式清空过，两者曾在磁盘同形）一次性迁移为默认清单并落盘；
//   - 字段存在（含显式 []）一律尊重；
//   - 空列表从此显式落盘为 "approvalTools": []（去掉 omitempty），"显式关闭"有独立形态。

const v2Fixture = `{"version":2,"channels":[{"id":"c1","type":"openai","name":"n","credential":"k","models":["m"],"groups":["default"],"status":"enabled","priority":100}],"activeId":"c1"`

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 缺字段旧文件：Load 后内存为默认清单，且文件已回写含 "approvalTools" 字段（迁移只做一次）。
func TestPool_ApprovalToolsMissingFieldMigratesToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	writeFixture(t, path, v2Fixture+`}`)

	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	got := p.ApprovalTools()
	if len(got) != 2 || got[0] != "shell" || got[1] != "ext_manage" {
		t.Fatalf("缺字段应迁移为默认清单，got %v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"approvalTools"`) {
		t.Fatalf("迁移必须落盘（此后显式关闭才有独立形态）：%s", data)
	}

	// 二次 Load：字段已在 → 迁移不再触发，默认清单原样保留
	p2 := NewPool(path)
	if err := p2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := p2.ApprovalTools(); len(got) != 2 || got[0] != "shell" {
		t.Fatalf("二次 Load 后清单应保持：%v", got)
	}
}

// 显式空清单（字段存在）：受尊重，绝不偷偷打开。
func TestPool_ApprovalToolsExplicitEmptyRespected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	writeFixture(t, path, v2Fixture+`,"approvalTools":[]}`)

	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	if got := p.ApprovalTools(); len(got) != 0 {
		t.Fatalf("显式 [] 必须受尊重，got %v", got)
	}
}

// 自定义清单：原样保留。
func TestPool_ApprovalToolsCustomListKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	writeFixture(t, path, v2Fixture+`,"approvalTools":["shell"]}`)

	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	got := p.ApprovalTools()
	if len(got) != 1 || got[0] != "shell" {
		t.Fatalf("自定义清单应原样保留，got %v", got)
	}
}

// 持久化往返：SetApprovalTools(nil)（用户显式关闭）后文件必须含 "approvalTools": []——
// 空列表落盘不省略，重启 Load 后仍为空（"关了又自己开"与"关了变缺字段"都不允许）。
func TestPool_ApprovalToolsEmptyListPersistedExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	p := NewPool(path)
	if err := p.Load(); err != nil { // 新装：默认清单 + 落盘
		t.Fatal(err)
	}
	if err := p.SetApprovalTools(nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"approvalTools": []`) {
		t.Fatalf("显式关闭必须落盘为空数组：%s", data)
	}
	reloaded := NewPool(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ApprovalTools(); len(got) != 0 {
		t.Fatalf("重启后显式关闭状态丢失：%v", got)
	}
}
