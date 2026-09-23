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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
)

// SessionEvent 是账本中的最小事件单元。
// 实现契约：Seq 在单个账本内单调递增；写入方必须持久化成功后才推进内存状态
// （write-ahead：账本即事实源，内存只是缓存）。
type SessionEvent interface {
	// Seq 返回事件在账本中的序号（从 1 开始）。
	Seq() int64
	// Kind 返回事件类型。
	Kind() EventKind
	// Encode 输出该事件的单行 JSON（JSONL 账本的一行，不含换行符）。
	Encode() ([]byte, error)
}

// ErrClosed 在账本已关闭后仍被使用时返回。
var ErrClosed = errors.New("session ledger closed")

// Ledger 是单会话事件账本（JSONL 追加式，见 ADR-0002）。
// 账本即事实源：Append 成功（含 fsync）后内存状态才允许推进。
// 并发约束：单实例内由 mu 串行化；同一文件允许多实例只读重放，但写入方应只有一个。
type Ledger struct {
	mu      sync.Mutex
	path    string
	f       *os.File // 追加句柄；Close 后置 nil
	lastSeq int64
}

// OpenLedger 打开（必要时创建）会话账本。
// 打开时执行修复：截断尾部半行（崩溃残留），并从完整行恢复序号水位。
func OpenLedger(dir, sessionID string) (*Ledger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
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
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var complete []byte
	if lastNL := bytes.LastIndexByte(data, '\n'); lastNL >= 0 {
		complete = data[:lastNL+1]
	}
	if len(complete) != len(data) {
		// 尾部半行：截断到最后一个完整行（空账本则截为 0）
		if err := os.Truncate(path, int64(len(complete))); err != nil {
			return 0, err
		}
	}
	var lastSeq int64
	for _, line := range bytes.Split(bytes.TrimSuffix(complete, []byte("\n")), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			Seq int64 `json:"seq"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			// 完整行损坏不是"断尾"，是数据损坏——显式失败而非静默跳过
			return 0, fmt.Errorf("corrupt complete line: %w", err)
		}
		if rec.Seq > lastSeq {
			lastSeq = rec.Seq
		}
	}
	return lastSeq, nil
}

// Append 追加一个事件并 fsync 落盘。
// 契约 C-SES-1/4：成功返回时事件行已持久化；任何失败返回错误且不推进序号水位。
func (l *Ledger) Append(kind EventKind, data any) (SessionEvent, error) {
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
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	if err := l.f.Sync(); err != nil {
		return nil, err
	}
	l.lastSeq = ev.seqN
	return ev, nil
}

// Replay 按写入顺序重放账本中所有完整事件。
// visit 返回错误则中止重放并原样上抛。
func (l *Ledger) Replay(visit func(SessionEvent) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			Seq  int64           `json:"seq"`
			Kind EventKind       `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("replay corrupt line: %w", err)
		}
		if err := visit(&event{seqN: rec.Seq, kindV: rec.Kind, data: rec.Data}); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭追加句柄。Close 后 Append 返回 ErrClosed。
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// event 是 SessionEvent 的最小实现。
// Data 使用 any 写入 / json.RawMessage 重放：两种来源 Encode 后字节等价。
type event struct {
	seqN  int64     `json:"-"`
	kindV EventKind `json:"-"`
	data  any       `json:"-"`
}

func (e *event) Seq() int64      { return e.seqN }
func (e *event) Kind() EventKind { return e.kindV }

// Encode 输出该事件的单行 JSON（不含换行符）。
func (e *event) Encode() ([]byte, error) {
	return json.Marshal(struct {
		Seq  int64     `json:"seq"`
		Kind EventKind `json:"kind"`
		Data any       `json:"data,omitempty"`
	}{e.seqN, e.kindV, e.data})
}
