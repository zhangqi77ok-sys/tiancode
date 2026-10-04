// 跨会话搜索（0.0.23）：在**所有**账本里找消息命中，供 Ctrl+Shift+F 面板用。
//
// 为什么值得做：会话内搜索（0.0.19 Ctrl+F）解决"这一场里我说了什么"，
// 但真实问题常是"我上周让模型查过什么来着"——那在另一场会话里。
//
// 三条纪律（都来自既有约定，不是新发明）：
//   - **只读**：绝不 OpenLedger（那是写模式 + repair，会截断回合进行中的半行）。
//     这里 os.Open 只读 + 逐行解析，坏行/半行跳过（与 ReadMeta 同纪律）。
//   - **有界**：账本数、单账本字节、命中数三个上限——本地优先应用不能因为
//     一次搜索把 8MB×N 的账本全读进内存（0.0.13 实测最大账本 8.3MB）。
//   - **可定位**：每条命中带所属轮的用户消息 seq 作锚点，点击复用 0.0.21 的
//     时间线跳转链路（selectSession + jumpToSeq），不新造定位机制。
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// 搜索有界常量（数字来自 0.0.13 实测：最大账本 8.3MB）。
const (
	// maxSearchLedgers 是参与扫描的账本数上限（按最后活跃倒序取最近的）。
	maxSearchLedgers = 100
	// perLedgerBytes 是单账本扫描字节上限：超限截断（命中如实标注）。
	perLedgerBytes = 4 << 20
	// defaultSearchLimit / maxSearchLimit 是命中数默认与硬顶。
	defaultSearchLimit = 50
	maxSearchLimit     = 200
	// snippetContext 是命中片段前后各留的字符数（够认出不截断）。
	snippetContext = 30
	// searchTitleRunes 是会话标题截断长度（与 session.Title 同规则：20 字）。
	searchTitleRunes = 20
)

// SearchStats 是本次搜索的扫描统计。
//
// 为什么必须透出给界面：单账本有字节上限（perLedgerBytes），超限的账本**只扫了开头
// 一段**，尾部（往往正是最近的对话）根本没进候选窗口。此前这个事实止步于
// scanLedgerForSearch 内部——聚合层用 `_` 丢掉返回值，界面上那句「没有会话里包含
// 这个搜索词」于是把「没扫到」说成了「不存在」。零命中时必须区分这两种含义。
//
// ByteLimitPerLedger 随统计一起回传：文案要写「只扫了前 4MB」，这个数字由后端
// 给出，前端不复制一份常量（复制就会漂）。
type SearchStats struct {
	ScannedLedgers     int `json:"scannedLedgers"`
	TruncatedLedgers   int `json:"truncatedLedgers"`
	ByteLimitPerLedger int `json:"byteLimitPerLedger"`
}

// SearchHit 是一条跨会话搜索命中。
type SearchHit struct {
	SessionID    string `json:"sessionID"`
	SessionTitle string `json:"sessionTitle"`
	Workspace    string `json:"workspace"`
	LastActiveMs int64  `json:"lastActiveMs"`
	Role         string `json:"role"`      // user | assistant
	AnchorSeq    int64  `json:"anchorSeq"` // 所属轮的用户消息 seq（跳转锚点）
	Snippet      string `json:"snippet"`   // 命中片段（超长截断并加省略号）
	// Truncated 标记"本账本因字节上限只扫了开头一段"，因此**这条命中不是全部**。
	// 由 scanLedgerForSearch 统一回填（不逐条快照：触顶时已收集的命中都要标）。
	Truncated bool `json:"truncated"`
}

// searchLedgerFile 是一个待扫账本。
type searchLedgerFile struct {
	id    string
	path  string
	mtime time.Time
}

// SearchSessions 在所有账本里搜 query（大小写不敏感子串匹配，中文直接子串）。
// workspaceFilter 非空时只返回归属匹配的账本命中；limit <= 0 取默认。
//
// 第二个返回值是扫描统计：调用方**必须**把它透给界面。超长账本只扫了前
// perLedgerBytes 字节，零命中时的含义是「没扫到」而不是「不存在」——两个返回值
// 缺一，界面就会拿「没扫到」说成「没有」。
func (s *ChatService) SearchSessions(query, workspaceFilter string, limit int) ([]SearchHit, SearchStats, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, SearchStats{}, fmt.Errorf("搜索词为空")
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	} else if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	files, err := s.searchLedgerFiles()
	if err != nil {
		return nil, SearchStats{}, err
	}
	lower := strings.ToLower(q)
	hits := make([]SearchHit, 0, limit)
	var stats SearchStats
	for _, f := range files {
		if len(hits) >= limit {
			break
		}
		stats.ScannedLedgers++
		found, truncated, err := scanLedgerForSearch(f, workspaceFilter, lower, limit-len(hits))
		if err != nil {
			continue // 单账本读失败（权限/占用）不整批失败
		}
		if truncated {
			stats.TruncatedLedgers++
		}
		hits = append(hits, found...)
	}
	stats.ByteLimitPerLedger = perLedgerBytes
	return hits, stats, nil
}

// searchLedgerFiles 列出参与扫描的账本（最近活跃优先，最多 maxSearchLedgers 个）。
func (s *ChatService) searchLedgerFiles() ([]searchLedgerFile, error) {
	dir := s.cfg.DataDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取会话目录失败：%w", err)
	}
	files := make([]searchLedgerFile, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue // stat 失败只丢这个账本
		}
		files = append(files, searchLedgerFile{
			id:    strings.TrimSuffix(name, ".jsonl"),
			path:  filepath.Join(dir, name),
			mtime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
	if len(files) > maxSearchLedgers {
		files = files[:maxSearchLedgers]
	}
	return files, nil
}

// searchEvent 是扫描用的最小事件投影（只解关心的字段）。
type searchEvent struct {
	Seq  int64  `json:"seq"`
	Kind string `json:"kind"`
	Data struct {
		Text string `json:"text"`
		Path string `json:"path"`
	} `json:"data"`
}

// scanLedgerForSearch 扫一个账本。单遍扫描同时产出标题/归属（避免为拿标题
// 再读一遍账本——0.0.13 实测单账本可达 8.3MB，双读不划算）。
//
// truncated 报告"本账本因字节上限只扫了开头 perLedgerBytes 一段"。它一旦为 true，
// 该账本的**所有**命中都会被标注 Truncated（见下方 defer）：尾部没有进候选窗口，
// 所以扫到的这些不是全部。调用方不得丢弃这个返回值。
func scanLedgerForSearch(f searchLedgerFile, workspaceFilter, lowerQ string, room int) (hits []SearchHit, truncated bool, err error) {
	fh, err := os.Open(f.path) // 只读：绝不碰 OpenLedger 的写/repair 路径
	if err != nil {
		return nil, false, err
	}
	defer fh.Close()

	var workspace, firstUser string
	var anchorSeq int64       // 当前轮的用户消息 seq（助手命中的锚点）
	var delta strings.Builder // 相邻 assistant_delta 累积（兜底旧账本/中途形态）

	// 触顶时把本账本已收集的命中全部标注出来——它们不是全部（尾部根本没进候选
	// 窗口）。必须在触顶那一刻才置位，此前构造的命中都带 false，所以**不能**在
	// 构造命中时快照 truncated（那正是「扫描已截断」标注恒不出现的原因）。
	// 用 defer 覆盖全部返回路径：字节触顶、命中数满、归属过滤。
	defer func() {
		if !truncated {
			return
		}
		for i := range hits {
			hits[i].Truncated = true
		}
	}()

	addHit := func(role string, seq int64, text string) {
		if text == "" || !strings.Contains(strings.ToLower(text), lowerQ) {
			return
		}
		anchor := seq
		if role == "assistant" {
			anchor = anchorSeq
		}
		hits = append(hits, SearchHit{
			SessionID:    f.id,
			SessionTitle: searchTitle(firstUser),
			Workspace:    workspace,
			LastActiveMs: f.mtime.UnixMilli(),
			Role:         role,
			AnchorSeq:    anchor,
			Snippet:      snippetAround(text, lowerQ),
		})
	}
	flushDelta := func() {
		if delta.Len() == 0 {
			return
		}
		addHit("assistant", anchorSeq, delta.String())
		delta.Reset()
	}

	readBytes := int64(0)
	buf := make([]byte, 64*1024)
	var carry []byte
	for {
		n, rerr := fh.Read(buf)
		if n > 0 {
			readBytes += int64(n)
			if readBytes > perLedgerBytes {
				truncated = true
				break
			}
			lines := strings.Split(string(append(carry, buf[:n]...)), "\n")
			carry = []byte(lines[len(lines)-1]) // 末段可能是半行，留到下轮
			for _, line := range lines[:len(lines)-1] {
				if len(hits) >= room {
					return hits, truncated, nil
				}
				var ev searchEvent
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if err := json.Unmarshal([]byte(line), &ev); err != nil {
					continue // 坏行跳过（与 ReadMeta 同纪律，不牵连整账本）
				}
				switch ev.Kind {
				case "workspace":
					if workspace == "" {
						workspace = ev.Data.Path
					}
				case "user_message":
					flushDelta() // 结算上一轮未闭合的 delta
					anchorSeq = ev.Seq
					if firstUser == "" {
						firstUser = ev.Data.Text
					}
					addHit("user", ev.Seq, ev.Data.Text)
				case "assistant_message":
					delta.Reset() // 已合并形态（0.0.3）：delta 缓冲作废
					addHit("assistant", ev.Seq, ev.Data.Text)
				case "assistant_delta":
					delta.WriteString(ev.Data.Text)
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	flushDelta()
	// 归属过滤放在扫完之后：workspace 事件的位置不保证在前（首个才是归属，
	// 提前判会漏），多扫的代价可控（有字节/账本数双顶）。
	if workspaceFilter != "" && !strings.EqualFold(workspace, workspaceFilter) {
		// 返回 truncated=false：归属不匹配的账本已被排除，对**本次搜索**不构成
		// 完整性缺口。若照实上报，限定工作区搜索时界面会误报"有长会话没扫完"——
		// 而那本账本的结果根本没进候选集。
		return nil, false, nil
	}
	return hits, truncated, nil
}

// searchTitle 按 session.Title 的规则截断（空白归一 + 20 字）。
func searchTitle(firstUser string) string {
	t := strings.Join(strings.Fields(firstUser), " ")
	if utf8.RuneCountInString(t) > searchTitleRunes {
		return string([]rune(t)[:searchTitleRunes])
	}
	return t
}

// snippetAround 取命中位置前后各 snippetContext 字；超长加省略号（按 rune 不切碎汉字）。
func snippetAround(text, lowerQ string) string {
	idx := strings.Index(strings.ToLower(text), lowerQ)
	if idx < 0 {
		return text
	}
	runes := []rune(text)
	// 把字节下标换成 rune 下标
	head := utf8.RuneCountInString(text[:idx])
	start := head - snippetContext
	if start < 0 {
		start = 0
	}
	end := head + utf8.RuneCountInString(lowerQ) + snippetContext
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	if end > len(runes) {
		end = len(runes)
	}
	return prefix + string(runes[start:end]) + suffix
}
