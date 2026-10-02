package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// Meta 是账本的元数据投影（列表/导出用，不加载消息体）。
type Meta struct {
	Title     string // 最后一次 session_renamed
	Workspace string // 归属工作区：首个 workspace 事件，被最后一次 workspace_move 覆盖（0.0.19 迁移）
	Pinned    bool   // 最后一次 session_pinned
	Events    int    // 完整事件数（诊断用）
	Skipped   int    // 跳过的不完整/损坏行数（>0 说明账本尾部有未完成写入或坏行）

	// 0.0.19 用量沉淀：所有 usage 事件累计（一轮可能多行——ReAct 每次模型调用
	// 各报一次）。只做统计展示，不参与对话重放。
	UsagePrompt     int64
	UsageCompletion int64
	UsageTotal      int64
}

// ReadMeta 只读扫描账本聚合元数据（0.2.36 审计 R2）。
// 为什么必须只读：回合进行中前端会刷新会话列表——此前元数据读取走 OpenLedger
// （写模式 + repair），会把末尾尚未写完的 delta 半行截掉（Truncate），
// 内存序号与文件内容从此错位——这是数据损坏。这里绝不 Truncate/repair：
//   - 末尾半行（无换行结尾）跳过并计数——这是回合进行中刷新列表的常态；
//   - 中间损坏行跳过并计数（单条坏数据不牵连整次读取，Skipped 可观测）；
//   - 文件不存在 = 空元数据（新会话，不是错误）。
//
// repair 只应发生在 ChatService 首次打开会话（确认没有活句柄）的一次性修复路径。
func ReadMeta(dir, sessionID string) (Meta, error) {
	var m Meta
	if err := validateSessionID(sessionID); err != nil {
		return m, err
	}
	data, err := os.ReadFile(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, err
	}
	lines := bytes.Split(data, []byte("\n"))
	if !bytes.HasSuffix(data, []byte("\n")) {
		// 尾部半行：未被截断（绝不 Truncate），仅跳过
		lines = lines[:len(lines)-1]
		m.Skipped++
	}
	wsSeen := false
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			Kind EventKind       `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			m.Skipped++ // 损坏行：跳过，不牵连整次读取
			continue
		}
		switch rec.Kind {
		case EventSessionRenamed:
			var p struct {
				Title string `json:"title"`
			}
			if json.Unmarshal(rec.Data, &p) == nil {
				m.Title = p.Title
			}
		case EventSessionPinned:
			var p struct {
				Pinned bool `json:"pinned"`
			}
			if json.Unmarshal(rec.Data, &p) == nil {
				m.Pinned = p.Pinned
			}
		case EventWorkspace:
			if !wsSeen { // 初始归属取首个（与侧栏分组、firstWorkspaceOfLedger 一致）
				var p struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(rec.Data, &p) == nil {
					m.Workspace = p.Path
					wsSeen = true
				}
			}
		case EventWorkspaceMove:
			// 迁移（0.0.19）：最后一次 move 覆盖归属——含显式空串（移出空间 = 纯对话）。
			// 与"首个快照固定"不冲突：快照是发送时的事实，move 是用户事后的显式决定。
			var p struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(rec.Data, &p) == nil {
				m.Workspace = p.Path
				wsSeen = true
			}
		case EventUsage:
			var p struct {
				Prompt     int64 `json:"prompt"`
				Completion int64 `json:"completion"`
				Total      int64 `json:"total"`
			}
			if json.Unmarshal(rec.Data, &p) == nil {
				m.UsagePrompt += p.Prompt
				m.UsageCompletion += p.Completion
				m.UsageTotal += p.Total
			}
		}
		m.Events++
	}
	return m, nil
}
