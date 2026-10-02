// Replay 分页（0.3 长会话减重）：切会话先返回投影的**最后一屏**，用户向上滚动
// 再分页补更早的消息。
//
// 为什么不把全量投影一次给前端：长会话（实测 8.3MB 账本 / 7000 条增量）全量
// 投影 + 序列化 + 前端挂载都要秒级；而用户打开会话第一眼只需要最后一屏。
// DOM 窗口（前端 messageWindow）继续负责"挂哪些回合"，这里负责"取哪些消息"。
//
// 缓存：全量投影按会话缓存，键 = 账本序号水位（Ledger.LastSeq）。新事件只会
// 追加在账本尾部，投影的前缀下标因此稳定——翻页锚点（from）跨请求有效；
// 水位没动时直接复用缓存，向上翻页不再每页重扫整本账本。
package app

import (
	"fmt"
	"os"
	"path/filepath"
)

// 页大小默认与硬顶：默认值对齐前端 DOM 窗口的初始/增量块（40 条）。
const (
	defaultReplayLimit = 40
	maxReplayLimit     = 200
)

// ReplayPage 是一页投影结果：messages 为切片，total 是投影总条数，
// from 是本页第一条在投影中的下标（0 = 前面没有更早的消息了）。
type ReplayPage struct {
	Messages []ChatMessage `json:"messages"`
	Total    int           `json:"total"`
	From     int           `json:"from"`
}

// replayCacheEntry 是单个会话的投影缓存：水位 + 全量投影。
type replayCacheEntry struct {
	lastSeq int64
	msgs    []ChatMessage
}

// replayCacheMax 是缓存会话数上限（超限整体清空）：投影按会话各占一份内存，
// 用量随会话数线性涨，封顶比精确 LRU 简单且足够（活跃会话远小于此数）。
const replayCacheMax = 64

// clampLimit 归一化页大小：非法值取默认，超值截到硬顶（防止一次拉爆内存）。
func clampLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}

// projectSession 返回会话的全量投影（带水位缓存）。
// 返回的切片是缓存本体，调用方只读、绝不可改写。
func (s *ChatService) projectSession(sessionID string) ([]ChatMessage, int64, error) {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return nil, 0, err
	}
	if size := ledgerSizeOf(s, sessionID); size > maxLedgerProjectionBytes {
		return nil, 0, fmt.Errorf("账本过大（%d MB），超过投影上限（%d MB）：无法载入该会话，请删除或手工归档 %s",
			size>>20, maxLedgerProjectionBytes>>20, sessionID)
	}
	seq := ledger.LastSeq()
	s.replayMu.Lock()
	if c, ok := s.replayCache[sessionID]; ok && c.lastSeq == seq {
		s.replayMu.Unlock()
		return c.msgs, seq, nil
	}
	s.replayMu.Unlock()
	msgs, err := s.Replay(sessionID)
	if err != nil {
		return nil, 0, err
	}
	s.replayMu.Lock()
	if len(s.replayCache) >= replayCacheMax {
		s.replayCache = make(map[string]replayCacheEntry)
	}
	s.replayCache[sessionID] = replayCacheEntry{lastSeq: seq, msgs: msgs}
	s.replayMu.Unlock()
	return msgs, seq, nil
}

// pageOf 取投影的 [from-limit, from) 切片（下标越界自动收敛到合法区间）。
func pageOf(msgs []ChatMessage, from, limit int) ReplayPage {
	if from > len(msgs) {
		from = len(msgs)
	}
	if from < 0 {
		from = 0
	}
	start := from - limit
	if start < 0 {
		start = 0
	}
	out := make([]ChatMessage, from-start)
	copy(out, msgs[start:from])
	return ReplayPage{Messages: out, Total: len(msgs), From: start}
}

// maxLedgerProjectionBytes 是投影前的大小防线（0.0.19 底层债修复）：Replay 把
// 整本账本读进内存，无上限的账本（异常增长/失控轮次）会一路吃到 OOM——
// 到达硬顶显式报错并给出出路（删除会话 / 联系排障），绝不静默吞掉可用内存。
const maxLedgerProjectionBytes = 256 << 20

// ledgerSizeOf 可注入以便测试（生产实现 stat 账本文件）。
var ledgerSizeOf = func(s *ChatService, sessionID string) int64 {
	info, err := os.Stat(filepath.Join(s.cfg.DataDir, sessionID+".jsonl"))
	if err != nil {
		return 0
	}
	return info.Size()
}

// ReplayTail 返回投影的最后 limit 条（切会话首屏）。
func (s *ChatService) ReplayTail(sessionID string, limit int) (ReplayPage, error) {
	msgs, _, err := s.projectSession(sessionID)
	if err != nil {
		return ReplayPage{}, err
	}
	return pageOf(msgs, len(msgs), clampLimit(limit, defaultReplayLimit, maxReplayLimit)), nil
}

// ReplayOlder 返回投影中 [from-limit, from) 的一页（向上滚动补更早的历史）。
// from 是前端已载入部分"从末尾往前数"的位置；from <= 0 说明前面没有了。
func (s *ChatService) ReplayOlder(sessionID string, from, limit int) (ReplayPage, error) {
	msgs, _, err := s.projectSession(sessionID)
	if err != nil {
		return ReplayPage{}, err
	}
	return pageOf(msgs, from, clampLimit(limit, defaultReplayLimit, maxReplayLimit)), nil
}
