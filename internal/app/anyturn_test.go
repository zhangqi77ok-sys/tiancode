// AnyTurnRunning（0.0.38 升级退出握手）：忙/空闲两态判定。
package app

import "testing"

func TestAnyTurnRunning(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	if s.AnyTurnRunning() {
		t.Fatal("新服务应无进行中回合")
	}
	// 注入一个"在跑"的会话（与 sendCore 的登记同一字段）
	s.mu.Lock()
	s.running["s-x"] = struct{}{}
	s.mu.Unlock()
	if !s.AnyTurnRunning() {
		t.Fatal("登记回合后应报告忙")
	}
}
