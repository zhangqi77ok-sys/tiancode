// 跨会话搜索测试（0.0.23）：跨账本命中 / 归属过滤 / 锚点定位 / 上限有界 / 只读。
package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// seedSearchLedger 落一个带归属的账本（首条 workspace 定归属）。
func seedSearchLedger(t *testing.T, dir, id, ws string, events [][2]string) {
	t.Helper()
	l, err := session.OpenLedger(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if ws != "" {
		if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ev := range events {
		var data map[string]any
		switch ev[0] {
		case "user":
			data = map[string]any{"text": ev[1]}
			if _, err := l.Append(session.EventUserMessage, data); err != nil {
				t.Fatal(err)
			}
		case "assistant":
			data = map[string]any{"text": ev[1]}
			if _, err := l.Append(session.EventAssistantMsg, data); err != nil {
				t.Fatal(err)
			}
		case "delta":
			data = map[string]any{"text": ev[1]}
			if _, err := l.Append(session.EventAssistantDelta, data); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSearchSessions_CrossLedger(t *testing.T) {
	s, ws := newMiniService(t)
	dir := s.cfg.DataDir
	seedSearchLedger(t, dir, "s-a", ws, [][2]string{
		{"user", "帮我查一下 goroutine 泄漏"},
		{"assistant", "goroutine 泄漏通常来自 channel 未关闭"},
		{"user", "那内存占用呢"},
		{"assistant", "内存要看堆快照"},
	})
	seedSearchLedger(t, dir, "s-b", "", [][2]string{
		{"user", "完全无关的问题"},
		{"assistant", "另一个会话里的 goroutine 讨论"},
	})

	hits, _, err := s.SearchSessions("goroutine", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3（两个会话各命中）: %+v", len(hits), hits)
	}
	// 归属与标题：s-a 有归属、s-b 纯对话
	withWS, noWS := 0, 0
	for _, h := range hits {
		if h.Workspace == ws {
			withWS++
		} else {
			noWS++
		}
	}
	if withWS != 2 || noWS != 1 {
		t.Fatalf("归属分布错：withWS=%d noWS=%d（%+v）", withWS, noWS, hits)
	}

	// 锚点：助手命中的 anchorSeq 必须是它前面那轮 user 的 seq（跳转靠它）
	for _, h := range hits {
		if h.Role == "assistant" && h.AnchorSeq <= 0 {
			t.Fatalf("助手命中缺锚点：%+v", h)
		}
	}

	// 大小写不敏感 + 片段截断
	hits, _, err = s.SearchSessions("GOROUTINE", "", 0)
	if err != nil || len(hits) != 3 {
		t.Fatalf("大小写不敏感失效：%d %v", len(hits), err)
	}
	if !strings.Contains(hits[0].Snippet, "goroutine") && !strings.Contains(hits[0].Snippet, "GOROUTINE") {
		t.Fatalf("片段应含命中词：%q", hits[0].Snippet)
	}
}

func TestSearchSessions_WorkspaceFilterAndLimits(t *testing.T) {
	s, ws := newMiniService(t)
	dir := s.cfg.DataDir
	seedSearchLedger(t, dir, "s-w", ws, [][2]string{{"user", "目标词在这里"}})
	seedSearchLedger(t, dir, "s-p", `D:\other`, [][2]string{{"user", "目标词在那里"}})

	// 归属过滤
	hits, _, err := s.SearchSessions("目标词", ws, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "s-w" {
		t.Fatalf("归属过滤失效：%+v", hits)
	}
	// 命中数上限
	hits, _, err = s.SearchSessions("目标词", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("limit 未生效：%d", len(hits))
	}
	// 空查询显式报错
	if _, _, err := s.SearchSessions("  ", "", 0); err == nil {
		t.Fatal("空查询必须报错")
	}
}

// 搜索绝不能碰账本的写/repair 路径：回合进行中的半行被跳过，且文件不被截断。
func TestSearchSessions_ReadOnlyDuringActiveTurn(t *testing.T) {
	s, _ := newMiniService(t)
	dir := s.cfg.DataDir
	l, err := session.OpenLedger(dir, "s-live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "关键词 出现"}); err != nil {
		t.Fatal(err)
	}
	// 手工追加半行（模拟崩溃/进行中的残行）
	f, err := os.OpenFile(filepath.Join(dir, "s-live.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":99,"kind":"user_message","data":{"text":"半行`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	hits, _, err := s.SearchSessions("关键词", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("半行不应影响完整行命中：%+v", hits)
	}
	// 文件未被截断/改写（只读纪律）
	raw, err := os.ReadFile(filepath.Join(dir, "s-live.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), `"text":"半行`) {
		t.Fatalf("搜索改写了账本（半行被截）: %q", string(raw[len(raw)-40:]))
	}
	_ = l.Close()
}

// delta 形态（旧账本/中途快照）：相邻 delta 合并成一条命中。
func TestSearchSessions_MergesDeltas(t *testing.T) {
	s, _ := newMiniService(t)
	seedSearchLedger(t, s.cfg.DataDir, "s-d", "", [][2]string{
		{"user", "问题"},
		{"delta", "前半段 "},
		{"delta", "含关键词后半段"},
	})
	hits, _, err := s.SearchSessions("关键词", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Role != "assistant" {
		t.Fatalf("delta 合并命中异常：%+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "前半段") {
		t.Fatalf("片段应含合并后的上下文：%q", hits[0].Snippet)
	}
	_ = context.Background() // 保持 context 导入（与其它测试同风格）
}

// seedOversizedLedger 造一个明显超过 perLedgerBytes 的账本。
// 目标词分处头尾两端：头部落在扫描窗口内，尾部落在 4MB 之外（扫不到）。
func seedOversizedLedger(t *testing.T, dir, id, headWord, tailWord string) {
	t.Helper()
	chunk := strings.Repeat("填充内容用于撑大账本", 700) // 约 21KB
	events := make([][2]string, 0, 251)
	events = append(events, [2]string{"user", headWord + " " + chunk})
	for i := 0; i < 249; i++ {
		events = append(events, [2]string{"user", chunk})
	}
	events = append(events, [2]string{"user", tailWord})
	seedSearchLedger(t, dir, id, "", events)
	info, err := os.Stat(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= perLedgerBytes {
		t.Fatalf("测试前提不成立：账本 %d 字节，未超上限 %d", info.Size(), perLedgerBytes)
	}
}

// 长账本的截断必须诚实化。此前两处缺陷让「扫描已截断」标注恒不出现、界面把
// 「没扫到」说成「没有」：
//   ① 标志在触顶之后才置位，此前构造的命中都带 false → 改由 defer 统一回填；
//   ② 聚合层用 `_` 丢弃截断返回值 → 改回传 SearchStats。
func TestSearchSessions_LongLedgerReportsTruncation(t *testing.T) {
	s, ws := newMiniService(t)
	dir := s.cfg.DataDir
	seedOversizedLedger(t, dir, "s-big", "前部目标词", "尾部目标词")

	// ① 头部 4MB 内的词扫得到，但命中必须带截断标注（尾部没扫，不是全部）
	hits, stats, err := s.SearchSessions("前部目标词", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("头部 4MB 内的词应能命中")
	}
	if stats.TruncatedLedgers != 1 {
		t.Fatalf("被截断账本数 = %d, want 1", stats.TruncatedLedgers)
	}
	for _, h := range hits {
		if !h.Truncated {
			t.Fatalf("截断账本的命中未标注 Truncated：%+v", h)
		}
	}

	// ② 尾部的词扫不到。旧实现此时界面只能显示"没有会话里包含这个搜索词"——
	// 统计必须让调用方能区分"没扫到"与"不存在"
	hitsTail, statsTail, err := s.SearchSessions("尾部目标词", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hitsTail) != 0 {
		t.Fatalf("尾部词在 4MB 之外，不应命中：%+v", hitsTail)
	}
	if statsTail.TruncatedLedgers == 0 {
		t.Fatal("尾部词搜不到时必须回传被截断账本数，否则界面只能说'没有'")
	}
	if statsTail.ByteLimitPerLedger != perLedgerBytes {
		t.Fatalf("ByteLimitPerLedger = %d, want %d（文案要写'只扫了前 4MB'，数字由后端给）",
			statsTail.ByteLimitPerLedger, perLedgerBytes)
	}

	// 对照：限定工作区时，归属不匹配的大账本已被排除在候选集外，不该报截断
	// （否则文案误报"有长会话没扫完"，而那本账本根本没进结果）
	_, statsScoped, err := s.SearchSessions("尾部目标词", ws, 0)
	if err != nil {
		t.Fatal(err)
	}
	if statsScoped.TruncatedLedgers != 0 {
		t.Fatalf("限定工作区后被排除的账本不该计入截断：%+v", statsScoped)
	}
}
