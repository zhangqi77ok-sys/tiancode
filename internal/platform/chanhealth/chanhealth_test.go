// 渠道健康聚合测试（0.0.23）：记录 → 日聚合 → 快照（成功率/耗时/最近错误）。
// 用注入目录的内部函数测（不碰用户真实的 channel-health 目录）。
package chanhealth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestStore 造一个隔离目录的 store（包级单例在测试里换指向）。
func newTestStore(t *testing.T) *healthStore {
	t.Helper()
	return &healthStore{dir: filepath.Join(t.TempDir(), "channel-health")}
}

func TestStore_RecordAggregatesByChannelAndDay(t *testing.T) {
	s := newTestStore(t)
	day := s.ensureDayLocked()
	now := time.Now()

	// ch-a：3 成功（100/200/300ms）+ 1 渠道级失败
	for _, ms := range []int64{100, 200, 300} {
		s.record(Outcome{ChannelID: "ch-a", OK: true, Latency: time.Duration(ms) * time.Millisecond, At: now})
	}
	s.record(Outcome{ChannelID: "ch-a", Kind: KindConnect, ErrText: "dial tcp: refused", At: now})
	// ch-b：1 成功 + 1 凭证失败
	s.record(Outcome{ChannelID: "ch-b", OK: true, Latency: time.Duration(50) * time.Millisecond, At: now})
	s.record(Outcome{ChannelID: "ch-b", Kind: KindCredential, ErrText: "401 bad key", At: now})
	_ = day

	// 只验内存聚合：record 的异步写盘与本测试并发会在 Windows 上撞 rename
	//（落盘形态由 TestDayFile_Roundtrip 单独验）
	a := s.viewLocked("ch-a")
	if a.OK != 3 || a.Fail != 1 {
		t.Fatalf("ch-a 计数错：%+v", a)
	}
	if a.Faults != 1 || a.CredFaults != 0 {
		t.Fatalf("ch-a 故障分类错：%+v", a)
	}
	if a.AvgLatencyMs != 200 {
		t.Fatalf("ch-a 平均建流耗时 = %d, want 200（只按成功样本）", a.AvgLatencyMs)
	}
	if a.LastError != "dial tcp: refused" {
		t.Fatalf("ch-a 最近错误 = %q", a.LastError)
	}
	b := s.viewLocked("ch-b")
	if b.CredFaults != 1 || b.OK != 1 {
		t.Fatalf("ch-b 分类错：%+v", b)
	}
}

// 落盘形态：writeDay 写出的 JSON 能被读回（聚合快照 → 磁盘 → 解析）。
func TestDayFile_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	snap := map[string]*bucket{
		"ch-x": {OK: 2, TotalMs: 500, LastErr: "boom", LastErrAt: 123},
	}
	if err := writeDay(dir, "2026-10-03", snap); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "2026-10-03.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f dayFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Day != "2026-10-03" || f.Channels["ch-x"] == nil {
		t.Fatalf("落盘形态错：%s", raw)
	}
	if f.Channels["ch-x"].OK != 2 || f.Channels["ch-x"].LastErr != "boom" {
		t.Fatalf("字段丢失：%s", raw)
	}
}

// 无请求的渠道：成功率 -1（读数未知），不编 100%。
func TestView_NoRequestsMeansUnknown(t *testing.T) {
	s := newTestStore(t)
	s.ensureDayLocked()
	v := s.viewLocked("never-used")
	if v.SuccessRate != -1 {
		t.Fatalf("无请求渠道的 SuccessRate = %v, want -1", v.SuccessRate)
	}
}

// 成功率的边界：全失败 = 0，全成功 = 1。
func TestView_RateEdges(t *testing.T) {
	s := newTestStore(t)
	s.ensureDayLocked()
	now := time.Now()
	for range 4 {
		s.record(Outcome{ChannelID: "all-fail", Kind: KindOther, At: now})
	}
	for range 4 {
		s.record(Outcome{ChannelID: "all-ok", OK: true, At: now})
	}
	if got := s.viewLocked("all-fail").SuccessRate; got != 0 {
		t.Fatalf("全失败成功率 = %v, want 0", got)
	}
	if got := s.viewLocked("all-ok").SuccessRate; got != 1 {
		t.Fatalf("全成功成功率 = %v, want 1", got)
	}
}

// 保留窗口：旧日文件被删，窗口内保留。
func TestPruneOld_KeepsWindow(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < keepDays+3; i++ {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		if err := os.WriteFile(filepath.Join(dir, day+".json"), []byte(`{"channels":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneOld(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != keepDays {
		t.Fatalf("保留文件数 = %d, want %d", len(entries), keepDays)
	}
}
