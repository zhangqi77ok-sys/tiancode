package channels

import (
	"errors"
	"testing"
)

func newTestPool(t *testing.T, chs ...Channel) *Pool {
	t.Helper()
	p := NewPool(t.TempDir() + "/channels.json")
	for _, c := range chs {
		if err := p.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func testCh(id, model string, prio, weight int) Channel {
	return Channel{ID: id, Type: "openai", Name: id, Credential: "key-" + id,
		Models: []string{model}, Groups: []string{DefaultGroup},
		Status: StatusEnabled, Priority: prio, Weight: weight}
}

// 规则 2：retry=0 用最高档；retry=1 降一档；超出用最低档——重试是降档，不是同档再抽。
func TestSelect_TieredByRetry(t *testing.T) {
	p := newTestPool(t,
		testCh("hi", "m", 100, 1),
		testCh("mid", "m", 50, 1),
		testCh("lo", "m", 10, 1),
	)
	cases := []struct{ retry int; want string }{{0, "hi"}, {1, "mid"}, {2, "lo"}, {9, "lo"}}
	for _, tc := range cases {
		got, err := p.Select(Selection{Model: "m", Retry: tc.retry})
		if err != nil {
			t.Fatalf("retry=%d: %v", tc.retry, err)
		}
		if got.ChannelID != tc.want {
			t.Fatalf("retry=%d got %q want %q（重试必须降档）", tc.retry, got.ChannelID, tc.want)
		}
	}
}

// 规则 3：同档加权随机（随机源可注入）+ exclude 跳过。
func TestSelect_WeightedAndExclude(t *testing.T) {
	p := newTestPool(t, testCh("a", "m", 100, 1), testCh("b", "m", 100, 1))
	p.rand = func(int) int { return 1 } // 总权重 200：pick=1 落在第二个候选
	if got, _ := p.Select(Selection{Model: "m"}); got.ChannelID != "b" {
		t.Fatalf("got %q want b（注入随机源后必须可复现）", got.ChannelID)
	}
	if got, _ := p.Select(Selection{Model: "m", Exclude: []string{"b"}}); got.ChannelID != "a" {
		t.Fatalf("got %q want a（exclude 必须跳过）", got.ChannelID)
	}
}

// 规则 4：指定渠道优先于随机，但仍须属于该 group+model、enabled、未排除。
func TestSelect_PinnedChannel(t *testing.T) {
	p := newTestPool(t, testCh("a", "m", 100, 1), testCh("b", "m", 100, 1), testCh("other", "m2", 100, 1))
	if got, _ := p.Select(Selection{Model: "m", ChannelID: "b"}); got.ChannelID != "b" {
		t.Fatalf("got %q want b", got.ChannelID)
	}
	if _, err := p.Select(Selection{Model: "m", ChannelID: "other"}); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("指定非本模型渠道应 ErrNoChannel，got %v", err)
	}
	if err := p.Save(testCh("b2", "m", 100, 1)); err != nil {
		t.Fatal(err)
	}
	// 禁用后不可被指定
	dis := testCh("b", "m", 100, 1)
	dis.Status = StatusManuallyDisabled
	if err := p.Save(dis); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Select(Selection{Model: "m", ChannelID: "b"}); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("禁用渠道被指定：%v", err)
	}
}

// 规则 5 + 多凭证：单凭证原样（下标 -1）；多凭证轮询；全禁后渠道不可用并转 auto_disabled。
func TestSelect_MultiCredentialRoundRobin(t *testing.T) {
	p := NewPool(t.TempDir() + "/channels.json")
	if err := p.Save(Channel{ID: "multi", Type: "openai", Name: "multi",
		Credential: "k1\nk2\nk3", Models: []string{"m"}, Groups: []string{DefaultGroup},
		Status: StatusEnabled, Priority: 100}); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		key string; idx int
	}{{"k1", 0}, {"k2", 1}, {"k3", 2}, {"k1", 0}}
	for _, w := range want {
		got, err := p.Select(Selection{Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Credential != w.key || got.CredentialIndex != w.idx {
			t.Fatalf("轮询 got %q/%d want %q/%d", got.Credential, got.CredentialIndex, w.key, w.idx)
		}
	}
	// 单凭证渠道：原样返回且下标 -1
	if err := p.Save(testCh("single", "s", 100, 1)); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Select(Selection{Model: "s"}); got.Credential != "key-single" || got.CredentialIndex != -1 {
		t.Fatalf("单凭证 got %q/%d", got.Credential, got.CredentialIndex)
	}
	// 禁 k2 → 轮询跳过 k2
	if err := p.BanCredential("multi", 1); err != nil {
		t.Fatal(err)
	}
	g, _ := p.Select(Selection{Model: "m"})
	if g.Credential != "k3" {
		t.Fatalf("禁用后 got %q want k3", g.Credential)
	}
	// 全部禁用 → 渠道视为不可用并转 auto_disabled
	if err := p.BanCredential("multi", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.BanCredential("multi", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Select(Selection{Model: "m"}); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("全禁后应 ErrNoChannel，got %v", err)
	}
	c, _ := p.Get("multi")
	if c.Status != StatusAutoDisabled {
		t.Fatalf("全禁后 status = %q, want auto_disabled", c.Status)
	}
}