package channels

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// approvalTools 持久化语义（0.0.05 首立，0.0.28 驾驶舱扩充）：
//   - 缺字段的旧文件（旧版本/显式清空过，两者曾在磁盘同形）一次性迁移为默认清单并落盘；
//   - 字段存在（含显式 []）一律尊重；
//   - 空列表从此显式落盘为 "approvalTools": []（去掉 omitempty），"显式关闭"有独立形态；
//   - v2 → v3：mcp（可调宿主任意工具）与 browser（可提交表单）是新增的"不确认就执行
//     即危险"口子——既有**非空**清单一次性追加这两项（保留用户已有项、去重）；显式
//     空清单不受影响（用户明确关掉了审批，悄悄重开等于推翻其选择）。

const v2Fixture = `{"version":2,"channels":[{"id":"c1","type":"openai","name":"n","credential":"k","models":["m"],"groups":["default"],"status":"enabled","priority":100}],"activeId":"c1"`

const v3Fixture = `{"version":3,"channels":[{"id":"c1","type":"openai","name":"n","credential":"k","models":["m"],"groups":["default"],"status":"enabled","priority":100}],"activeId":"c1","approvalTools":["shell"]}`

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sameList(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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
	if !sameList(got, []string{"shell", "ext_manage", "mcp", "browser"}) {
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
	if got := p2.ApprovalTools(); !sameList(got, []string{"shell", "ext_manage", "mcp", "browser"}) {
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
	writeFixture(t, path, v3Fixture) // v3 文件不再迁移：迁移只发生在 v2 → v3 这一次

	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	got := p.ApprovalTools()
	if !sameList(got, []string{"shell"}) {
		t.Fatalf("v3 自定义清单应原样保留，got %v", got)
	}
}

// v2 → v3 迁移：mcp/browser 追加进既有清单尾部，用户已有项与顺序原样保留。
func TestPool_ApprovalToolsV2ListAppendsMcpBrowser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	writeFixture(t, path, v2Fixture+`,"approvalTools":["shell"]}`)

	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	got := p.ApprovalTools()
	if !sameList(got, []string{"shell", "mcp", "browser"}) {
		t.Fatalf("v2 清单应一次性补入 mcp/browser，got %v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version": 3`) {
		t.Fatalf("迁移必须升版本号落盘（此后用户删项不再被补回）：%s", data)
	}

	// 迁移过的文件（已是 v3）：用户把 browser 移出后，重启不得被补回
	p2 := NewPool(path)
	if err := p2.Load(); err != nil {
		t.Fatal(err)
	}
	if err := p2.SetApprovalTools([]string{"shell", "mcp"}); err != nil {
		t.Fatal(err)
	}
	p3 := NewPool(path)
	if err := p3.Load(); err != nil {
		t.Fatal(err)
	}
	if got := p3.ApprovalTools(); !sameList(got, []string{"shell", "mcp"}) {
		t.Fatalf("v3 之后的用户选择必须受尊重，got %v", got)
	}
}

// v2 → v3 迁移去重：清单里已有的项不重复追加（顺序保持"先到先得"）。
func TestPool_ApprovalToolsV2MigrationDedupes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	writeFixture(t, path, v2Fixture+`,"approvalTools":["browser","shell"]}`)

	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	got := p.ApprovalTools()
	if !sameList(got, []string{"browser", "shell", "mcp"}) {
		t.Fatalf("已有项去重且顺序保留，got %v", got)
	}
}

// v2 显式空清单不参与补口子迁移（用户显式关掉审批的独立形态，0.0.05 起受尊重）。
func TestPool_ApprovalToolsV2ExplicitEmptyNotResurrected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	writeFixture(t, path, v2Fixture+`,"approvalTools":[]}`)

	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	if got := p.ApprovalTools(); len(got) != 0 {
		t.Fatalf("显式空清单必须保持为空，got %v", got)
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
