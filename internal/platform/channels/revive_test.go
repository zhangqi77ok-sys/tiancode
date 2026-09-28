package channels

import "testing"

// 自动禁用是运行期健康标记：进程重启必须重新给一次机会，
// 否则单次网络抖动就会让应用永久没有可用渠道（用户只看到"发消息毫无反应"）。
func TestPool_ReviveAutoDisabled(t *testing.T) {
	p := newTestPool(t, testCh("a", "m", 100, 1))
	if err := p.AutoDisable("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Select(Selection{Model: "m"}); err == nil {
		t.Fatal("自动禁用后不得可选")
	}

	revived, err := p.ReviveAutoDisabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(revived) != 1 || revived[0] != "a" {
		t.Fatalf("revived = %v, want [a]", revived)
	}
	if _, err := p.Select(Selection{Model: "m"}); err != nil {
		t.Fatalf("恢复后应可选：%v", err)
	}

	// 持久化：磁盘上不得继续留着 auto_disabled（否则界面显示的与选路语义不一致）
	reloaded := NewPool(p.path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	ch, _ := reloaded.Get("a")
	if ch.Status != StatusEnabled {
		t.Fatalf("reload 后 status = %q, want enabled", ch.Status)
	}

	// 二次调用：已无自动禁用渠道 → 空操作且无错误
	again, err := p.ReviveAutoDisabled()
	if err != nil || len(again) != 0 {
		t.Fatalf("二次恢复应为空操作，got %v / %v", again, err)
	}
}

// 手动停用是用户显式意图：重启恢复绝不动它（与 syncStatusByCredentialsLocked 同一纪律）。
func TestPool_ReviveKeepsManualDisabled(t *testing.T) {
	man := testCh("man", "m", 100, 1)
	man.Status = StatusManuallyDisabled
	p := newTestPool(t, man)

	revived, err := p.ReviveAutoDisabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(revived) != 0 {
		t.Fatalf("手动停用的渠道不得被恢复：%v", revived)
	}
	ch, _ := p.Get("man")
	if ch.Status != StatusManuallyDisabled {
		t.Fatalf("status = %q, want manually_disabled", ch.Status)
	}
}

// 凭证级禁用标记是"手动摘除坏 Key"的显式操作：恢复渠道状态时不得连带解禁，
// 且全凭证不可用时选路仍要跳过它（不产生"看似可用、实际打不通"的假象）。
func TestPool_ReviveKeepsCredentialBan(t *testing.T) {
	c := testCh("multi", "m", 100, 1)
	c.Credential = "k1\nk2"
	p := newTestPool(t, c)
	for _, idx := range []int{0, 1} {
		if err := p.BanCredential("multi", idx); err != nil {
			t.Fatal(err)
		}
	}
	ch, _ := p.Get("multi")
	if ch.Status != StatusAutoDisabled {
		t.Fatalf("全凭证禁用后 status = %q, want auto_disabled", ch.Status)
	}

	if _, err := p.ReviveAutoDisabled(); err != nil {
		t.Fatal(err)
	}
	after, _ := p.Get("multi")
	if after.Status != StatusEnabled {
		t.Fatalf("渠道状态应恢复为 enabled，got %q", after.Status)
	}
	if after.CredentialEnabled(0) || after.CredentialEnabled(1) {
		t.Fatal("凭证禁用标记不得被连带清除（用户需在凭证管理里自行启用）")
	}
	if _, err := p.Select(Selection{Model: "m"}); err == nil {
		t.Fatal("全部凭证被禁用时不得被选中")
	}
}
