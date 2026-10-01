package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/session"
)

// 账本规模量化（0.0.12 稳定批审计）：长会话（约 10MB 账本）切回时的
// Replay 全链路耗时——打开修复 + ForkDrops + 投影 + Wails 绑定层的 JSON 序列化。
// 上个会话的审计提到"UI 卡死"与"账本流式化"P0 但无定义；这里拿数据定位
// 卡顿是否在账本读取/投影/序列化链路上。阈值是环境宽容值，重点是 t.Logf 数据。
func TestReplayScale_10MBLedger(t *testing.T) {
	if testing.Short() {
		t.Skip("规模量化在 -short 下跳过")
	}
	dir := t.TempDir()
	// 第一次构造：造一个约 10MB 的账本（长回复场景：几千条中文增量）
	s1 := newChannelService(t, newScaleConfig(dir))
	l, err := s1.ledgerFor("s-scale")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]any{"text": "生成一个长文档"}); err != nil {
		t.Fatal(err)
	}
	const deltas = 4000
	payload := strings.Repeat("这是一段流式输出的正文内容。", 24) // ≈ 1.1KB/条
	for i := 0; i < deltas; i++ {
		if _, err := l.Append(session.EventAssistantDelta, map[string]any{"text": payload}); err != nil {
			t.Fatal(err)
		}
	}
	// assistant_msg 以完整正文为恢复锚点（与真实 agent 收尾一致：锚点文本=delta 累计）
	if _, err := l.Append(session.EventAssistantMsg, map[string]any{"text": strings.Repeat(payload, deltas)}); err != nil {
		t.Fatal(err)
	}
	l.Close() // 模拟应用退出（释放句柄）

	// 第二次构造 = 重启后切回该会话的真实路径：OpenLedger（含修复扫描）→ Replay
	s2 := newChannelService(t, newScaleConfig(dir))
	defer s2.Close()

	start := time.Now()
	msgs, err := s2.Replay("s-scale")
	if err != nil {
		t.Fatal(err)
	}
	readCost := time.Since(start)

	start = time.Now()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	serCost := time.Since(start)

	var ledgerBytes int64 = 0
	if n, err := fileSize(dir + "/s-scale.jsonl"); err == nil {
		ledgerBytes = n
	}
	for i, m := range msgs {
		t.Logf("msg[%d] role=%s len=%d", i, m.Role, len(m.Content))
	}
	t.Logf("账本=%.1fMB | Replay投影=%v | JSON序列化=%v | 消息=%d条 | 投影JSON=%.1fMB",
		float64(ledgerBytes)/1e6, readCost, serCost, len(msgs), float64(len(b))/1e6)
	// 防退化阈值（环境宽容值，量级参考：实测 8.3MB 账本 Replay≈0.38s/序列化≈15ms）：
	// Go 侧全链路一旦进入秒级说明读取/投影出现回归。
	if readCost+serCost > 3*time.Second {
		t.Fatalf("账本重放全链路 %v 超过 3s（回归）", readCost+serCost)
	}
	if len(msgs) != 2 || len(msgs[1].Content) != deltas*len(payload) {
		t.Fatalf("长回复投影不完整：msgs=%d assistant=%d 字，want %d 字", len(msgs), len(msgs[1].Content), deltas*len(payload))
	}
}

func newScaleConfig(dir string) Config {
	return Config{DataDir: dir, BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}
}

func fileSize(p string) (int64, error) {
	info, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
