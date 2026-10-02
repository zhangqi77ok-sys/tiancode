// Package chanhealth 汇总各渠道的近 N 天健康读数（成功率 / 建流耗时 / 最近错误）。
//
// 为什么需要（0.0.23 审查第 8 项）：故障切换是自动的（gateway 降档重试 +
// auto_ban），但用户**看不到**它切了什么——"刚才那句怎么那么慢/是不是又换节点了"
// 无从判断。渠道管理里加一列成功率与平均耗时，选路行为就有了对照。
//
// 记什么：每次上游请求一行（渠道、成败、原因分类、建流耗时）。
// **不记**：BaseURL 全路径、凭证、消息内容（与 applog 同一纪律）。
//
// 落盘形态：按日一个 JSON（configfile.Dir()/channel-health/YYYY-MM-DD.json），
// 每次请求异步合并写（读-改-写，带锁）。为什么不是攒一次写：进程可能被强杀
// （用户点关闭/任务管理器），攒着写会丢最近的数据，而健康读数最需要的是"刚刚"。
// 为什么不是每请求一个文件：一天几百行事件文件没有读取价值，只有聚合值有意义。
package chanhealth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// Kind 是失败分类（成功用 ""）。分类而非原样错误串：界面按类给建议，
// 原文只作最近一次的补充。
const (
	KindConnect     = "connect"      // 建流失败（网络/DNS/代理）
	KindChannel5xx  = "server"       // 429/5xx
	KindCredential  = "credential"   // 401/403
	KindConvert     = "convert"      // 响应解析失败
	KindIdleTimeout = "idle_timeout" // 流层空闲看门狗收束
	KindOther       = "other"        // 其它上游错误
)

// Outcome 是一次上游请求的观测结果。
type Outcome struct {
	ChannelID string
	OK        bool
	Kind      string // 失败分类；OK=true 时留空
	Latency   time.Duration
	ErrText   string // 仅保留最近一次的截断原文（诊断用）
	At        time.Time
}

// bucket 是一个渠道某天的聚合。
type bucket struct {
	OK         int    `json:"ok"`
	Fail       int    `json:"fail"`
	Faults     int    `json:"faults"`     // 渠道级故障（connect/server）
	CredFaults int    `json:"credFaults"` // 凭证级
	TotalMs    int64  `json:"totalMs"`    // 建流耗时累计（毫秒）
	LastErr    string `json:"lastErr,omitempty"`
	LastErrAt  int64  `json:"lastErrAt,omitempty"` // Unix 毫秒
}

// dayFile 是某天的落盘形态。
type dayFile struct {
	Day      string             `json:"day"`
	Channels map[string]*bucket `json:"channels"`
}

// View 是给界面的一行读数。
type View struct {
	ChannelID    string  `json:"channelID"`
	OK           int     `json:"ok"`
	Fail         int     `json:"fail"`
	Faults       int     `json:"faults"`
	CredFaults   int     `json:"credFaults"`
	SuccessRate  float64 `json:"successRate"`  // 0..1（无请求 = -1：读数未知不编造）
	AvgLatencyMs int64   `json:"avgLatencyMs"` // 建流平均耗时（只按成功样本）
	LastError    string  `json:"lastError,omitempty"`
	LastErrorAt  int64   `json:"lastErrorAt,omitempty"`
}

// keepDays 是保留窗口（近 7 天）；更老的文件在写入时顺手删掉。
const keepDays = 8

// store 是包级单例：gateway 埋点直接调 Record（低频、极小临界区）。
var store = &healthStore{dir: healthDir()}

func healthDir() string { return filepath.Join(configfile.Dir(), "channel-health") }

type healthStore struct {
	mu  sync.Mutex
	dir string
	mem map[string]*bucket // 今天（内存镜像：避免每次请求都读盘）
	day string
}

func (s *healthStore) ensureDayLocked() string {
	today := time.Now().Format("2006-01-02")
	if s.day != today || s.mem == nil {
		s.day = today
		s.mem = s.loadDayLocked(today)
	}
	return today
}

func (s *healthStore) loadDayLocked(day string) map[string]*bucket {
	raw, err := os.ReadFile(filepath.Join(s.dir, day+".json"))
	if err != nil {
		return map[string]*bucket{}
	}
	var f dayFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return map[string]*bucket{} // 坏文件按空处理（健康读数不是关键数据，不阻塞）
	}
	if f.Channels == nil {
		f.Channels = map[string]*bucket{}
	}
	return f.Channels
}

// record 是 Record 的内部版本（store 显式传入，便于测试隔离目录）。
func (s *healthStore) record(o Outcome) {
	if strings.TrimSpace(o.ChannelID) == "" {
		return
	}
	if o.At.IsZero() {
		o.At = time.Now()
	}
	s.mu.Lock()
	day := s.ensureDayLocked()
	b := s.mem[o.ChannelID]
	if b == nil {
		b = &bucket{}
		s.mem[o.ChannelID] = b
	}
	applyOutcome(b, o)
	snapshot := s.cloneMemLocked()
	s.mu.Unlock()

	go func() {
		_ = writeDay(s.dir, day, snapshot)
		pruneOld(s.dir)
	}()
}

// applyOutcome 把一次观测并进桶（Record 与 store.record 共用）。
func applyOutcome(b *bucket, o Outcome) {
	if o.OK {
		b.OK++
		b.TotalMs += o.Latency.Milliseconds()
		return
	}
	b.Fail++
	switch o.Kind {
	case KindConnect, KindChannel5xx:
		b.Faults++
	case KindCredential:
		b.CredFaults++
	}
	if text := strings.TrimSpace(o.ErrText); text != "" {
		b.LastErr = truncateRunes(text, 200)
		b.LastErrAt = o.At.UnixMilli()
	}
}

// cloneMemLocked 复制内存桶（写盘快照用；调用方持锁）。
func (s *healthStore) cloneMemLocked() map[string]*bucket {
	out := make(map[string]*bucket, len(s.mem))
	for k, v := range s.mem {
		cp := *v
		out[k] = &cp
	}
	return out
}

// viewLocked 把桶换算成视图（含成功率与平均耗时；调用方持锁）。
func (s *healthStore) viewLocked(id string) View {
	b := s.mem[id]
	if b == nil {
		return View{ChannelID: id, SuccessRate: -1}
	}
	return bucketToView(id, *b)
}

// bucketToView 桶 → 视图（平均耗时只按成功样本；无请求 = -1 不编造）。
func bucketToView(id string, b bucket) View {
	v := View{
		ChannelID:   id,
		OK:          b.OK,
		Fail:        b.Fail,
		Faults:      b.Faults,
		CredFaults:  b.CredFaults,
		LastError:   b.LastErr,
		LastErrorAt: b.LastErrAt,
	}
	if total := b.OK + b.Fail; total > 0 {
		v.SuccessRate = float64(b.OK) / float64(total)
	} else {
		v.SuccessRate = -1
	}
	if b.OK > 0 {
		v.AvgLatencyMs = b.TotalMs / int64(b.OK)
	}
	return v
}

// Record 记录一次请求（异步落盘：调用方是请求热路径，不能等 IO）。
func Record(o Outcome) { store.record(o) }

// writeDay 原子写某天的聚合文件。
func writeDay(dir, day string, m map[string]*bucket) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(dayFile{Day: day, Channels: m}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFileAtomic(filepath.Join(dir, day+".json"), raw, 0o644)
}

// pruneOld 删掉窗口外的日文件（只保留 keepDays 个；文件名即日期，字典序=时间序）。
func pruneOld(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".json") && len(n) == len("2006-01-02")+len(".json") {
			names = append(names, n)
		}
	}
	if len(names) <= keepDays {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keepDays] {
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// Snapshot 返回近 days 天的按渠道聚合（读盘聚合，跨天相加）。
func Snapshot(days int) map[string]View {
	if days <= 0 || days > 30 {
		days = 7
	}
	dir := healthDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]View{} // 还没有任何记录 = 正常起点
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > days {
		names = names[:days]
	}
	out := map[string]*View{}
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		var f dayFile
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		for id, b := range f.Channels {
			if b == nil {
				continue
			}
			v := out[id]
			if v == nil {
				v = &View{ChannelID: id, SuccessRate: -1}
				out[id] = v
			}
			v.OK += b.OK
			v.Fail += b.Fail
			v.Faults += b.Faults
			v.CredFaults += b.CredFaults
			v.AvgLatencyMs += b.TotalMs
			if b.LastErrAt >= v.LastErrorAt {
				v.LastErrorAt = b.LastErrAt
				v.LastError = b.LastErr
			}
		}
	}
	// 平均耗时只在样本上算；成功率无请求 = -1（界面写"暂无数据"，不编 100%）
	views := make(map[string]View, len(out))
	for id, v := range out {
		views[id] = bucketToView(id, bucket{
			OK:         v.OK,
			Fail:       v.Fail,
			Faults:     v.Faults,
			CredFaults: v.CredFaults,
			TotalMs:    v.AvgLatencyMs, // 暂存累计值，下面按 OK 均摊
			LastErr:    v.LastError,
			LastErrAt:  v.LastErrorAt,
		})
	}
	return views
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
