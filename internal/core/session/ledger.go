// Package session 定义会话事件账本与崩溃恢复契约。
//
// 做什么：以追加式 JSONL 事件账本记录会话全过程；
// 崩溃恢复 = 顺序重放到最后一个完整事件，半行（写入中断）直接截断。
// 被谁依赖：internal/app（用例编排）。
// 依赖谁：仅 stdlib。
//
// 设计取舍见 docs/adr/0002-event-log-session.md：
// 旧实现每条消息对 JSON 全量读-改-写（store.go），Windows rename 冲突时
// 静默丢消息（app_chat.go `_ =` 吞错）；事件账本把最坏情况从"丢任意消息"
// 降级为"丢最后一个未写完的事件"，且恢复语义可测。
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventKind 是事件类型枚举。一经发布不得改名
// （账本文件按 Kind 字符串持久化，改名 = 旧账本不可重放）。
type EventKind string

const (
	EventSessionStart   EventKind = "session_start"
	EventUserMessage    EventKind = "user_message"
	EventAssistantDelta EventKind = "assistant_delta"   // 流式文本增量
	EventAssistantMsg   EventKind = "assistant_message" // 一条完整助手消息（恢复锚点）
	EventToolCall       EventKind = "tool_call"
	EventToolResult     EventKind = "tool_result"
	EventTurnEnd        EventKind = "turn_end"
	EventError          EventKind = "error"

	// 第 6 批（回合检查点 / 用户手动写入 / 分叉）——账本只追加，旧行永不改写。
	//
	// EventRoundCheckpoint 记录一轮结束时该轮改过的文件与"轮次开始前内容"快照
	//（供「撤回本轮」整批恢复；超限条目带 note 表示撤不回）。
	EventRoundCheckpoint EventKind = "round_checkpoint"
	// EventRoundRevert 记录一次「撤回本轮」（恢复了哪些/哪些撤不回），
	// 同时作为"该检查点已被消费"的标记（同一轮不会被撤回两次）。
	EventRoundRevert EventKind = "round_revert"
	// EventUserEdit 记录用户手动写入（代码块「应用到文件」）：Replay 投影为工具卡，
	// 派生历史时给模型一句"用户已应用过"。
	EventUserEdit EventKind = "user_edit"
	// EventFork 标记分叉：派生/投影时丢弃 (from_seq, 本事件 Seq] 区间内的事件
	//（「从这条用户消息重跑」），其后追加的新事件照常参与。
	EventFork EventKind = "fork"

	// 0.0.19（会话迁移 / 用量沉淀）——同样只追加，旧行永不改写。
	//
	// EventWorkspaceMove 记录一次「移动到空间」：覆盖归属（侧栏分组、顶栏标签、
	// 工具根同源——首次 workspace 事件仍是初始归属，最后一次 move 说了算）。
	// path 为空串 = 移出空间（会话变为纯对话归属；本地工具在下一轮下线）。
	EventWorkspaceMove EventKind = "workspace_move"
	// EventUsage 是已停用的用量统计行。旧账本里可能还有，重放与列表都跳过，不再写入。
	EventUsage EventKind = "usage"

	// EventCompaction 记录一次历史压缩（0.0.41）：折叠到底仍超预算时，把
	// (账本开头, up_to_seq) 区间的旧轮次摘要成一段文字（LLM 摘要只覆盖对话叙事，
	// 工具输出仍走确定性单行——0.0.09 用户裁决不变），事件落账后派生跳过
	// seq < up_to_seq 的叙事事件、以摘要开头。账本只追加：摘要不覆盖原文，
	// UI 投影与「撤回/分叉」语义不受影响；同一账本可多次压缩，后一次覆盖前一次。
	EventCompaction EventKind = "compaction"
)

// Event 是账本中的最小事件单元。
// 实现契约：Seq 在单个账本内单调递增；写入方必须持久化成功后才推进内存状态
// （write-ahead：账本即事实源，内存只是缓存）。
type Event interface {
	// Seq 返回事件在账本中的序号（从 1 开始）。
	Seq() int64
	// Kind 返回事件类型。
	Kind() EventKind
	// Data 返回事件负载（原始 JSON，如 {"text":"..."}）。
	Data() json.RawMessage
	// Encode 输出该事件的单行 JSON（JSONL 账本的一行，不含换行符）。
	Encode() ([]byte, error)
}

// ErrClosed 在账本已关闭后仍被使用时返回。
var ErrClosed = errors.New("session ledger closed")

// 增量合批参数（第 1 批）：AssistantDelta 是唯一高频事件（长回复每 token 一次
// fsync 会拖死整轮），攒批到"满块或窗口到期"才刷盘；取先到者。
const (
	deltaFlushBytes  = 64 << 10               // 满一块：64KB
	deltaFlushWindow = 100 * time.Millisecond // 窗口到期
)

// Ledger 是单会话事件账本（JSONL 追加式，见 ADR-0002）。
// 账本即事实源：Append 成功后内存状态才允许推进。
// 并发约束：单实例内由 mu 串行化；同一文件允许多实例只读重放，但写入方应只有一个。
//
// 刷盘纪律（第 1 批）：AssistantDelta 攒批落盘（见 pending）；其余事件
// （用户消息/工具调用与结果/todo/终态）一律先冲掉攒批再 fsync——崩溃最多丢
// 最后一小段未刷的增量文本，绝不丢已完成的工具结果与用户消息。
type Ledger struct {
	mu      sync.Mutex
	path    string
	f       *os.File // 追加句柄；Close 后置 nil
	lastSeq int64

	// pending 是攒批中的增量行（含换行）；pendingFrom 是本批第一个字节的时刻
	// （零值 = 无 pending）。syncCount 是 fsync 次数（测试观测合批效果用）。
	pending     []byte
	pendingFrom time.Time
	syncCount   int64
}

// OpenLedger 打开（必要时创建）会话账本。
// 打开时执行修复：截断尾部半行（崩溃残留），并从完整行恢复序号水位。
func OpenLedger(dir, sessionID string) (*Ledger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	lastSeq, err := repairLedger(path)
	if err != nil {
		return nil, fmt.Errorf("repair ledger: %w", err)
	}
	// O_APPEND 保证追加原子性（单行小于内核缓冲时不与其他写交错）
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Ledger{path: path, f: f, lastSeq: lastSeq}, nil
}

// repairLedger 截断尾部半行并返回账本中的最大 Seq。
// 为什么在打开时修复：恢复语义统一收口在"打开"这一个入口，Replay 无需处理半行分支。
func repairLedger(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	lastSeq, complete, scanErr := repairScan(f)
	// 关闭错误不吞：scanErr 为空时上抛（只读句柄关闭失败通常无害，但不可静默）
	if cerr := f.Close(); scanErr == nil {
		scanErr = cerr
	}
	if scanErr != nil {
		return 0, scanErr
	}
	if complete < fileSizeOf(path) {
		// 尾部半行：截断到最后一个完整行（空账本则截为 0）。
		// 关闭读句柄后再按路径截断：Windows 上同进程句柄不阻塞 size 变更，但顺序收口更稳。
		if err := os.Truncate(path, complete); err != nil {
			return 0, err
		}
	}
	return lastSeq, nil
}

// repairScan 流式扫描账本，返回最大 Seq 与"最后一个完整行末尾"的字节偏移。
func repairScan(f *os.File) (int64, int64, error) {
	var lastSeq int64
	var complete, total int64
	err := forEachLine(f, func(line []byte, isComplete bool) error {
		total += int64(len(line))
		if !isComplete {
			return nil // 半行不计入完整区，由调用方截断
		}
		complete += int64(len(line))
		seq, err := decodeSeq(bytes.TrimSuffix(line, []byte("\n")))
		if err != nil {
			return err
		}
		if seq > lastSeq {
			lastSeq = seq
		}
		return nil
	})
	return lastSeq, complete, err
}

// decodeSeq 解析一行事件 JSON 的 Seq。完整行损坏不是"断尾"，是数据损坏——
// 显式失败而非静默跳过（行为契约与旧实现逐字一致）。
func decodeSeq(line []byte) (int64, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return 0, nil
	}
	var rec struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal(line, &rec); err != nil {
		return 0, fmt.Errorf("corrupt complete line: %w", err)
	}
	return rec.Seq, nil
}

// fileSizeOf 返回文件大小；失败（含不存在）返回 0——此时无内容可截断。
func fileSizeOf(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// forEachLine 流式逐行扫描账本文件。
// 为什么不用 os.ReadFile：账本可达数 MB（实测 3.36MB / 30,351 行），repair 与
// Replay 又会多次触发，整文件读入内存既费内存也费拷贝；逐行扫描把内存占用
// 从 O(整文件) 降为 O(单行)。
// 为什么不用 bufio.Scanner：AssistantDelta 合批块 64KB、事件行上探 MB 级，
// Scanner 默认 64KB token 上限会截断超长行；这里以 ReadSlice 手工攒行，
// 短行零拷贝（直接交付内部缓冲的分片），超长行落入跨行复用的 scratch，
// 单行长度没有上限。
// 行语义与旧的 TrimSuffix+Split 完全一致：以 \n 分界；末尾没有换行符的字节
// 作为半行交付一次（complete=false，repair 截断它、Replay 照常解析）。
func forEachLine(f *os.File, handle func(line []byte, isComplete bool) error) error {
	r := bufio.NewReaderSize(f, 64<<10)
	var scratch []byte
	for {
		frag, err := r.ReadSlice('\n')
		switch {
		case err == nil:
			if herr := handle(frag, true); herr != nil {
				return herr
			}
		case err == bufio.ErrBufferFull:
			// 行比读缓冲长：攒进 scratch 直到行尾（scratch 跨行复用，避免每行一次堆分配）
			scratch = append(scratch[:0], frag...)
			for {
				frag, err = r.ReadSlice('\n')
				scratch = append(scratch, frag...)
				if err != bufio.ErrBufferFull {
					break
				}
			}
			if err != nil && err != io.EOF {
				return err
			}
			if herr := handle(scratch, err == nil); herr != nil {
				return herr
			}
		case err == io.EOF:
			if len(frag) == 0 {
				return nil
			}
			if herr := handle(frag, false); herr != nil {
				return herr
			}
		default:
			return err
		}
	}
}

// Append 追加一个事件。
// 契约 C-SES-1/4：成功返回时事件行已持久化（AssistantDelta 例外：成功返回仅表示
// 已进入攒批缓冲，见包内 deltaFlushBytes/deltaFlushWindow——但紧随其后的任一
// 非增量事件、Replay、Close 都会把缓冲一并 fsync）；任何失败返回错误且不推进序号水位。
func (l *Ledger) Append(kind EventKind, data any) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil, ErrClosed
	}
	ev := &event{seqN: l.lastSeq + 1, kindV: kind, data: data}
	line, err := ev.Encode()
	if err != nil {
		return nil, err
	}
	l.pending = append(l.pending, line...)
	l.pending = append(l.pending, '\n')
	if l.pendingFrom.IsZero() {
		l.pendingFrom = time.Now()
	}
	// 刷盘判定：增量事件攒批（满块/窗口到期才刷）；其余事件立即刷
	if kind != EventAssistantDelta || len(l.pending) >= deltaFlushBytes || time.Since(l.pendingFrom) >= deltaFlushWindow {
		if err := l.flushLocked(); err != nil {
			return nil, err
		}
	}
	l.lastSeq = ev.seqN
	return ev, nil
}

// flushLocked 把攒批缓冲一次写入并 fsync（调用方持锁）。
// 失败语义：Write 一旦提交（返回 n>0）就绝不重发缓冲——宁可丢尾部，
// 也不能把同一行写两遍让 Replay 看到重复事件。
func (l *Ledger) flushLocked() error {
	if len(l.pending) == 0 {
		return nil
	}
	buf := l.pending
	l.pending = l.pending[:0]
	l.pendingFrom = time.Time{}
	if _, err := l.f.Write(buf); err != nil {
		return err
	}
	if err := l.f.Sync(); err != nil {
		return err
	}
	l.syncCount++
	return nil
}

// Flush 立即把攒批的增量刷盘（幂等；已关闭时为空操作——Close 时已刷过）。
// 为什么需要显式入口：取消/错误终态路径没有"关键事件"来冲掉攒批的增量，
// 用户中断后进程被杀时已产生的部分输出绝不能丢（C-APP-2 取消保留事件）。
func (l *Ledger) Flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	return l.flushLocked()
}

// NextSeq 返回下一次 Append 将使用的序号，不推进水位。
// 单写入方在写入 tool_call 前用它生成 call-{seq}；账本不支持并发双写。
func (l *Ledger) NextSeq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq + 1
}

// LastSeq 返回当前序号水位（0.3：Replay 分页缓存的失效判据——水位没动，
// 投影就没变）。只读，不推进水位。
func (l *Ledger) LastSeq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq
}

// Replay 按写入顺序重放账本中所有完整事件。
// visit 返回错误则中止重放并原样上抛。
// 返回值命名（err）：defer 里的句柄关闭错误要在无扫描错误时上抛，不静默吞掉。
func (l *Ledger) Replay(visit func(Event) error) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// 读事实源前先冲掉攒批：Append 已成功返回的增量必须能被 Replay 看到
	//（read-your-writes；否则同实例的 Replay 会漏掉最近一段未刷的增量文本）。
	if err := l.flushLocked(); err != nil {
		return err
	}
	// 错误可见：打开/关闭都不吞（defer 关闭无法上抛，这里用命名收口）
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	// 行语义与旧实现逐字一致：末尾半行（打开修复后外部又追加的不完整行）
	// 照常按行解析——JSON 损坏显式失败，不静默跳过。
	return forEachLine(f, func(line []byte, _ bool) error {
		line = bytes.TrimSuffix(line, []byte("\n"))
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}
		var rec struct {
			Seq  int64           `json:"seq"`
			Kind EventKind       `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("replay corrupt line: %w", err)
		}
		return visit(&event{seqN: rec.Seq, kindV: rec.Kind, data: rec.Data})
	})
}

// Close 关闭追加句柄（先刷掉攒批）。Close 后 Append 返回 ErrClosed。
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	// 正常退出绝不丢已确认的增量：刷盘失败仍要释放句柄（错误照常上抛）
	flushErr := l.flushLocked()
	err := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return flushErr
}

// event 是 Event 的最小实现。
// Data 使用 any 写入 / json.RawMessage 重放：两种来源 Encode 后字节等价。
type event struct {
	seqN  int64     `json:"-"`
	kindV EventKind `json:"-"`
	data  any       `json:"-"`
}

func (e *event) Seq() int64      { return e.seqN }
func (e *event) Kind() EventKind { return e.kindV }

// Data 返回事件负载。写入侧（any）与重放侧（RawMessage）均序列化为原始 JSON，
// 保证两类来源对消费方（如 deriveMessages）字节等价。
func (e *event) Data() json.RawMessage {
	b, err := json.Marshal(e.data)
	if err != nil {
		return nil
	}
	return b
}

// Encode 输出该事件的单行 JSON（不含换行符）。
func (e *event) Encode() ([]byte, error) {
	return json.Marshal(struct {
		Seq  int64     `json:"seq"`
		Kind EventKind `json:"kind"`
		Data any       `json:"data,omitempty"`
	}{e.seqN, e.kindV, e.data})
}
