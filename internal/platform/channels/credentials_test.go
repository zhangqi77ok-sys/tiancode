package channels

import (
	"path/filepath"
	"testing"
)

func newCredPool(t *testing.T, cred string) *Pool {
	t.Helper()
	p := NewPool(filepath.Join(t.TempDir(), "channels.json"))
	if err := p.Save(Channel{
		ID: "c1", Type: "openai", Name: "multi", Credential: cred,
		Models: []string{"m"}, Groups: []string{DefaultGroup},
		Status: StatusEnabled, Priority: 100,
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// 凭证管理：脱敏预览（前 6 后 4；短凭证全遮）+ 启停 + 渠道状态联动 + 解禁恢复。
// 背景（0.2.19）：BanCredential 只禁不解，坏 Key 一旦被自动禁用即永久死路。
func TestPool_CredentialManagement(t *testing.T) {
	p := newCredPool(t, "sk-1234567890abcdef\nshortkey")

	creds, err := p.Credentials("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 2 {
		t.Fatalf("凭证视图条数 = %d, want 2", len(creds))
	}
	if creds[0].Preview != "sk-123…cdef" {
		t.Fatalf("长凭证预览 = %q（应前 6 后 4）", creds[0].Preview)
	}
	if creds[1].Preview != "••••••••" {
		t.Fatalf("短凭证预览 = %q（应全遮）", creds[1].Preview)
	}
	if !creds[0].Enabled || !creds[1].Enabled {
		t.Fatal("初始应全部启用")
	}

	// 禁用一条：还有可用凭证 → 渠道保持 enabled
	if err := p.SetCredentialEnabled("c1", 1, false); err != nil {
		t.Fatal(err)
	}
	if ch, _ := p.Get("c1"); ch.Status != StatusEnabled {
		t.Fatalf("部分禁用后 status = %s, want enabled", ch.Status)
	}

	// 全部禁用 → 渠道转 auto_disabled，且选路明确报无可用
	if err := p.SetCredentialEnabled("c1", 0, false); err != nil {
		t.Fatal(err)
	}
	if ch, _ := p.Get("c1"); ch.Status != StatusAutoDisabled {
		t.Fatalf("全禁后 status = %s, want auto_disabled", ch.Status)
	}
	if _, err := p.Select(Selection{Model: "m"}); err == nil {
		t.Fatal("全部凭证禁用后不应可选")
	}

	// 解禁（自动禁用后的唯一恢复途径）：渠道恢复 enabled 并可被选到
	if err := p.SetCredentialEnabled("c1", 0, true); err != nil {
		t.Fatal(err)
	}
	if ch, _ := p.Get("c1"); ch.Status != StatusEnabled {
		t.Fatalf("解禁后 status = %s, want enabled（自动禁用应随恢复解除）", ch.Status)
	}
	if _, err := p.Select(Selection{Model: "m"}); err != nil {
		t.Fatalf("解禁后应可选：%v", err)
	}

	// 越界下标必须报错，不得静默
	if err := p.SetCredentialEnabled("c1", 9, false); err == nil {
		t.Fatal("越界凭证下标必须报错")
	}
}

// 手动停用保护：凭证操作（禁用/启用）不得改写用户显式停用意图（manually_disabled）。
func TestPool_CredentialOpsRespectManualDisable(t *testing.T) {
	p := newCredPool(t, "k1\nk2")
	if err := p.SetCredentialEnabled("c1", 0, false); err != nil {
		t.Fatal(err)
	}
	c, _ := p.Get("c1")
	c.Status = StatusManuallyDisabled
	if err := p.Save(c); err != nil {
		t.Fatal(err)
	}

	// 恢复一条凭证：渠道应保持手动停用（不因"有可用凭证"被改成 enabled）
	if err := p.SetCredentialEnabled("c1", 0, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Get("c1"); got.Status != StatusManuallyDisabled {
		t.Fatalf("手动停用被凭证操作改写：%s", got.Status)
	}
}

// Probe：停用渠道也允许探测（诊断语义），且不推进轮询游标（测试不扰动生产选路节奏）。
func TestPool_ProbeSkipsStatusAndKeepsCursor(t *testing.T) {
	p := newCredPool(t, "k1\nk2\nk3")
	c, _ := p.Get("c1")
	c.Status = StatusManuallyDisabled
	if err := p.Save(c); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		sel, err := p.Probe("c1")
		if err != nil {
			t.Fatal(err)
		}
		if sel.Credential != "k1" || sel.CredentialIndex != 0 {
			t.Fatalf("第 %d 次 Probe = %q/%d（应恒取首条启用凭证且不推进）", i, sel.Credential, sel.CredentialIndex)
		}
	}

	// 游标未动：恢复启用后 Select 仍从 k1 开始轮询
	c, _ = p.Get("c1")
	c.Status = StatusEnabled
	if err := p.Save(c); err != nil {
		t.Fatal(err)
	}
	sel, err := p.Select(Selection{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Credential != "k1" {
		t.Fatalf("Probe 推进了轮询游标：Select = %q, want k1", sel.Credential)
	}

	if _, err := p.Probe("nope"); err == nil {
		t.Fatal("不存在的渠道必须报错")
	}
}
