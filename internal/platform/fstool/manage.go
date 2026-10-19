package fstool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"tiancode/internal/core/tools"
)

// delete / move（2026-10-19）：此前 fs 没有删除与移动，模型只能走 shell（默认要审批，
// 而且 cmd 的 del/move 没有任何撤回能力）。这里的两个动作都进轮次检查点：
//   - 文本文件（上限内、UTF-8/GBK）：删除前全文进检查点，「撤回本轮」按原编码重建；
//   - 二进制 / 超限文件：照常执行，但检查点注明"无法撤回"，结果里也明说；
//   - 只删文件或空目录：非空目录拒绝（递归删除不可撤回，交给用户或 shell+审批）。

// deleteEntry 删除一个文件或空目录。
func (t *Tool) deleteEntry(path string) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	if full == t.root {
		return bizErrf("不能删除工作区根目录"), nil
	}
	info, err := os.Lstat(full)
	if err != nil {
		return bizErrf("delete failed: %v", err), nil
	}
	if info.IsDir() {
		entries, rerr := os.ReadDir(full)
		if rerr != nil {
			return bizErrf("delete failed: %v", rerr), nil
		}
		if len(entries) > 0 {
			return bizErrf("%s 不是空目录（%d 项）：fs.delete 只删文件或空目录，递归删除不可撤回，请逐个删除或向用户确认后用 shell", path, len(entries)), nil
		}
		if err := os.Remove(full); err != nil {
			return bizErrf("delete failed: %v", err), nil
		}
		return tools.ToolResult{Content: fmt.Sprintf("deleted empty directory %s", path)}, nil
	}
	old, enc, recoverable := t.snapshotForRemoval(full, info.Size())
	if err := os.Remove(full); err != nil {
		return bizErrf("delete failed: %v", err), nil
	}
	t.forgetRead(full)
	t.noteRemoval(path, old, enc, recoverable)
	res := tools.ToolResult{Content: fmt.Sprintf("deleted %s (%d bytes)", path, info.Size())}
	if recoverable {
		res.Diff = diffText(path, old, "")
		res.Content += "；可用「撤回本轮」恢复"
	} else {
		res.Content += "；该文件是二进制或超过上限，撤回本轮无法恢复它"
	}
	return res, nil
}

// moveEntry 把文件从 path 移到 to（目标必须不存在；只移动文件）。
func (t *Tool) moveEntry(path, to string) (tools.ToolResult, error) {
	if to == "" {
		return bizErrf("move 需要 to（目标路径，相对工作区）"), nil
	}
	src, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	dst, err := t.resolve(to)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Lstat(src)
	if err != nil {
		return bizErrf("move failed: %v", err), nil
	}
	if info.IsDir() {
		return bizErrf("%s 是目录：fs.move 只移动文件（目录移动请逐个文件或向用户确认后用 shell）", path), nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return bizErrf("目标 %s 已存在：move 不覆盖已有文件（先确认再删除目标）", to), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return bizErrf("move failed: %v", err), nil
	}
	old, enc, recoverable := t.snapshotForRemoval(src, info.Size())
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return bizErrf("move failed: %v", err), nil
	}
	if err := os.Rename(src, dst); err != nil {
		return bizErrf("move failed: %v", err), nil
	}
	// 整读标记随文件走：移动不改内容
	t.mu.Lock()
	t.lastRead[dst] = t.lastRead[src]
	delete(t.lastRead, src)
	t.mu.Unlock()
	t.noteRemoval(path, old, enc, recoverable)
	// 目标是本轮新建：撤回 = 删除目标（哈希比对防误删用户之后的改动）
	if data, rerr := os.ReadFile(dst); rerr == nil {
		t.noteRound(to, "", "", false, true, sha256Hex(data))
	}
	res := tools.ToolResult{Content: fmt.Sprintf("moved %s → %s", path, to)}
	if !recoverable {
		res.Content += "；该文件是二进制或超过上限，撤回本轮只会删除目标、无法重建源文件"
	}
	return res, nil
}

// snapshotForRemoval 读取将被删除/移走的文件全文供检查点（文本且上限内才可恢复）。
func (t *Tool) snapshotForRemoval(full string, size int64) (string, string, bool) {
	if size > maxWriteBytes {
		return "", "", false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", "", false
	}
	enc, err := detectEncoding(data)
	if err != nil {
		return "", "", false
	}
	text, err := decodeText(data, enc)
	if err != nil {
		return "", "", false
	}
	return text, enc, true
}

// noteRemoval 记录"本轮删除了这个文件"的检查点（LastSHA256=deletedMarker）。
func (t *Tool) noteRemoval(path, old, enc string, recoverable bool) {
	t.noteRound(path, old, enc, true, recoverable, deletedMarker)
	if !recoverable {
		t.mu.Lock()
		if cp, ok := t.roundCP[path]; ok && cp.Note == "" {
			cp.Note = "无法撤回：删除前的文件是二进制或超过上限，未保存快照"
		}
		t.mu.Unlock()
	}
}

// forgetRead 清掉整读标记（文件已不存在）。
func (t *Tool) forgetRead(full string) {
	t.mu.Lock()
	delete(t.lastRead, full)
	t.mu.Unlock()
}
