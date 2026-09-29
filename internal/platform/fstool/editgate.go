package fstool

import (
	"context"
	"errors"
	"os"
	"time"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/atomicfile"
)

// EditGate 是文件写入的**人为确认**端口（0.0.10）：write/replace 在真正落盘前
// 把将要发生的变更交给界面（路径 + 短 diff + 新建/修改），用户点「应用」才执行
// Apply。未确认就不落盘；取消按跳过；没有超时自动应用。
//
// gate 为 nil 时直接执行 Apply（工具单元测试 / 无界面装配的既有行为）。
// Apply 必须自验：执行前重新核对文件状态（外部改动检测），不一致就报错且
// 原文件不动——从提案到确认之间文件可能被其他程序改过。
type EditGate interface {
	ConfirmEdit(ctx context.Context, p EditProposal) (applied bool, undo *UndoSnapshot, err error)
}

// EditProposal 是一次待确认的文件变更。
type EditProposal struct {
	Path  string // 工作区相对路径（展示用）
	Diff  string // 短 diff（给界面与用户核对）
	IsNew bool   // true = 新建文件（没有旧内容可恢复）
	// CallID 由 agent 在分派前注入（Tool.SetCallID）：确认卡与工具卡经同一
	// CallID 配对——用户确认后终态事件原地更新这张卡。
	CallID string
	// Apply 执行真正的落盘。返回本次写入的撤销快照（旧全文 + 写入后哈希）。
	// 内部自验失败（外部改动等）返回错误，原文件保持不变。
	Apply func() (*UndoSnapshot, error)
}

// UndoSnapshot 与 tools.UndoData 同一类型（结果字段直接透传）。
type UndoSnapshot = tools.UndoData

// ProposeWrite 把外部来源的内容（如"应用到文件"的代码块）变成一次待确认变更
// （0.0.10）：走与 write 相同的确认链路——先给人看 diff，确认才落盘，取消不写。
// 目标文件由用户显式选择，绕过模型的整读门卫（用户确认是更高授权）；
// 保留外部改动检测：提案后文件被改过，应用即拒绝。
// 返回值与 Execute 同构：IsError/Content 供调用方展示。
func (t *Tool) ProposeWrite(ctx context.Context, path, content string) error {
	full, err := t.resolve(path)
	if err != nil {
		return err
	}
	existed := false
	var sizeAtPropose int64
	var modAtPropose time.Time
	var old string
	if info, statErr := os.Stat(full); statErr == nil {
		existed = true
		sizeAtPropose, modAtPropose = info.Size(), info.ModTime()
		if data, readErr := os.ReadFile(full); readErr == nil {
			old = string(data)
		}
	}
	diff := diffText(path, old, content)
	applyFn := func() (*UndoSnapshot, error) {
		if existed {
			info2, err := os.Stat(full)
			if err != nil || info2.Size() != sizeAtPropose || !info2.ModTime().Equal(modAtPropose) {
				return nil, errors.New("文件已被其他程序修改，应用失败（原文件未动）")
			}
		}
		if err := atomicfile.WriteFileAtomic(full, []byte(content), 0o600); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.lastRead[full] = true
		t.mu.Unlock()
		return &UndoSnapshot{Path: path, OldExists: existed, OldContent: old, NewSHA256: sha256Hex([]byte(content))}, nil
	}
	if t.gate == nil {
		_, err := applyFn()
		return err
	}
	applied, _, err := t.gate.ConfirmEdit(ctx, EditProposal{
		Path: path, Diff: diff, IsNew: !existed, CallID: t.callID, Apply: applyFn,
	})
	if err != nil {
		return err
	}
	if !applied {
		return errors.New("用户跳过了这次修改，文件未修改")
	}
	return nil
}
