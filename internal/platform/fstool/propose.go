package fstool

import (
	"fmt"
	"os"

	"tiancode/internal/platform/atomicfile"
)

// ProposeResult 是一次「应用到文件」写入的结果（供界面渲染写入回执卡片，
// 并落账本供 Replay 与「撤回本轮」使用——第 6 批）。
type ProposeResult struct {
	Path  string // 工作区相对路径
	IsNew bool   // true = 新建（写入前不存在）
	Diff  string // 短 diff（新建为 +全文；无变化为空串）
	Bytes int    // 写入字节数
	// OldExists/OldContent 是写入前的状态（上限内才有内容；超限为空并由 Note 说明）：
	// 账本 EventUserEdit 携带它，供撤回与审计。
	OldExists  bool
	OldContent string
	// Note 非空表示撤回数据不可用（写入前内容超限）。
	Note string
}

// ProposeWrite 把外部来源的内容（代码块「应用到文件」）直接写入工作区文件。
// 与模型 write 的区别与取舍（0.0.10 的确认卡链路已移除：改了就是改了）：
//   - 目标路径由用户显式指定（用户授权高于模型的整读门卫）：已存在文件可直接覆盖；
//   - 仍保留外部改动检测（读快照后被其他程序改过即拒绝，原文件不动）；
//   - 写入前内容随结果返回（上限内）：编排层落账本，支持后续撤回与 Replay。
func (t *Tool) ProposeWrite(path, content string) (ProposeResult, error) {
	// 参数上限与模型写入同一硬顶：超限整次失败、不截断（原子写保证不留半个文件）
	if len(content) > maxWriteBytes {
		return ProposeResult{}, fmt.Errorf("content too large (%d bytes > %d)：请拆分写入", len(content), maxWriteBytes)
	}
	full, err := t.resolve(path)
	if err != nil {
		return ProposeResult{}, err
	}
	existed := false
	var old string
	var note string
	var sizeAtPropose int64
	var modAtPropose int64
	if info, statErr := os.Stat(full); statErr == nil {
		existed = true
		sizeAtPropose, modAtPropose = info.Size(), info.ModTime().UnixNano()
		if info.Size() > maxWriteBytes {
			// 超限：不为撤回多读一份超大文件（与单文件撤销同一上限）
			note = "写入前的内容超过上限，未保存撤回数据"
		} else if data, readErr := os.ReadFile(full); readErr == nil {
			old = string(data)
		} else {
			return ProposeResult{}, fmt.Errorf("read before write failed: %w", readErr)
		}
	} else if !os.IsNotExist(statErr) {
		return ProposeResult{}, fmt.Errorf("stat before write failed: %w", statErr)
	}
	// 外部改动检测：快照读取到落盘之间文件被其他程序改过 → 拒绝（原文件不动）
	if existed {
		info2, statErr := os.Stat(full)
		if statErr != nil || info2.Size() != sizeAtPropose || info2.ModTime().UnixNano() != modAtPropose {
			return ProposeResult{}, fmt.Errorf("文件已被其他程序修改，写入失败（原文件未动）")
		}
	}
	if err := atomicfile.WriteFileAtomic(full, []byte(content), 0o600); err != nil {
		return ProposeResult{}, err
	}
	// 写入成功：内容已知全文，后续模型 write 无需重读
	t.mu.Lock()
	t.lastRead[full] = true
	t.mu.Unlock()
	return ProposeResult{
		Path: path, IsNew: !existed, Diff: diffText(path, old, content), Bytes: len(content),
		OldExists: existed, OldContent: old, Note: note,
	}, nil
}
