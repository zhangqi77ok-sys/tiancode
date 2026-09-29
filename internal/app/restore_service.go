package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tiancode/internal/core/session"
	"tiancode/internal/platform/atomicfile"
)

// restore 是账本 tool_result 事件里 undo 字段的形态（0.0.07）。
// 只在后端恢复接口使用：旧全文绝不投影给前端，也绝不进模型上下文。
type restore struct {
	Path       string `json:"path"`
	OldExists  bool   `json:"old_exists"`
	OldContent string `json:"old_content"`
	NewSHA256  string `json:"new_sha256"`
}

// RestoreToolWrite 恢复一次 write/replace **写入前**的内容（0.0.07）。
//
// 流程与纪律：
//   - 在会话账本里找 callID 对应 tool_result 的 undo 快照（旧全文只存在账本里，
//     不在模型上下文、不在前端内存）；
//   - 恢复前比对"写入后内容哈希"与当前文件：不一致 = 用户（或别的进程）改过，
//     **拒绝恢复**——宁可拒绝也不覆盖别人的改动；
//   - 新建文件的恢复 = 删除该文件（只能删这次写入创建的那一个路径）；
//   - 恢复走原子写（失败不留半个文件）；成功后追加一条 tool_result 事件，
//     让界面显式显示"已恢复"（绝不静默覆盖）。
func (s *ChatService) RestoreToolWrite(sessionID, callID string) (string, error) {
	if strings.TrimSpace(callID) == "" {
		return "", fmt.Errorf("callID 为空，无法定位这次写入")
	}
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return "", err
	}

	// 单次只读扫描：撤销快照 + 会话归属根
	var found *restore
	root, owned := "", false
	err = ledger.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventWorkspace:
			if owned {
				return nil
			}
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err == nil {
				root, owned = p.Path, true
			}
		case session.EventToolResult:
			if found != nil {
				return nil
			}
			var p struct {
				ID   string   `json:"id"`
				Undo *restore `json:"undo"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err == nil && p.ID == callID && p.Undo != nil {
				found = p.Undo
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == nil {
		return "", fmt.Errorf("这次写入没有可用的恢复数据（可能超出上限未保存，或不是文件写入）")
	}
	if !owned || normalizeWorkspace(root) == "" {
		return "", fmt.Errorf("会话没有工作区归属，无法定位文件位置")
	}
	root = normalizeWorkspace(root)

	// 路径守卫：账本里的路径按会话根重新解析，越界一律拒绝（与 fs.resolve 同纪律）
	if filepath.IsAbs(found.Path) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(found.Path)), "../") {
		return "", fmt.Errorf("恢复数据里的路径不合法：%s", found.Path)
	}
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(found.Path)))
	if !strings.HasPrefix(filepath.ToSlash(full), filepath.ToSlash(root)+"/") && filepath.ToSlash(full) != filepath.ToSlash(root) {
		return "", fmt.Errorf("恢复路径越出工作区：%s", found.Path)
	}

	// "被人改过"检测：当前内容哈希 ≠ 写入后哈希 → 拒绝
	cur, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("读取当前文件失败：%w", err)
	}
	sum := sha256.Sum256(cur)
	if hex.EncodeToString(sum[:]) != found.NewSHA256 {
		return "", fmt.Errorf("文件在这次写入后被改过（内容与写入时不一致），拒绝恢复以免覆盖你的改动；如确需回退请先确认当前文件内容")
	}

	// 执行恢复：旧存在 → 写回旧全文；新建 → 删除这次创建的文件
	if found.OldExists {
		if err := atomicfile.WriteFileAtomic(full, []byte(found.OldContent), 0o600); err != nil {
			return "", fmt.Errorf("恢复写入失败：%w", err)
		}
	} else if err := os.Remove(full); err != nil {
		return "", fmt.Errorf("删除新建文件失败：%w", err)
	}

	// 追加可见事件（无配对 tool_call，derive 投影自动跳过——不进模型上下文）
	note := fmt.Sprintf("已恢复 %s 到本次写入前的内容", found.Path)
	if !found.OldExists {
		note = fmt.Sprintf("已删除本次新建的 %s（恢复写入前状态）", found.Path)
	}
	if _, err := ledger.Append(session.EventToolResult, map[string]any{
		"id": fmt.Sprintf("restore-%s", callID), "name": "fs",
		"content": note, "is_error": false, "title": found.Path, "op": "edit",
	}); err != nil {
		return "", fmt.Errorf("记录恢复事件失败：%w", err)
	}
	return note, nil
}
