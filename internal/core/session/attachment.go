package session

import (
	"os"
	"path/filepath"
)

// UserAttachment 是用户消息携带的附件（0.0.10）。账本只记引用：
//   - 图片：字节存到附件目录，Path 是相对账本目录的附件文件路径；
//   - 来自用户磁盘的文件：Path 记发送时的绝对路径（不复制原文件，除非是
//     剪贴板里没有路径的临时文件——那种写进附件目录，Path 相对）。
//
// Inline：full = 内容已内联进消息文本；path = 只附路径（未内联，需要时用 fs 读取）。
type UserAttachment struct {
	Kind      string `json:"kind"`      // image | file
	Name      string `json:"name"`      // 原始文件名
	MediaType string `json:"mediaType"` // image/png 等（按内容嗅探，不只信扩展名）
	Path      string `json:"path"`      // 相对账本目录（附件目录内）或绝对路径
	Inline    string `json:"inline"`    // full | path | none
	Size      int64  `json:"size"`
}

// AttachmentDir 返回会话的附件目录（账本在 dataDir 下，附件在其下 att/<sid>/）。
func AttachmentDir(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "att", sessionID)
}

// ResolveAttachmentPath 把账本里的附件引用解析为绝对路径：
// 相对路径相对账本目录（sessions/）解析；绝对路径原样。
func (l *Ledger) ResolveAttachmentPath(ref string) string {
	if filepath.IsAbs(ref) {
		return ref
	}
	return filepath.Join(filepath.Dir(l.path), ref)
}

// Dir 返回账本所在目录（附件相对路径的解析基准）。
func (l *Ledger) Dir() string { return filepath.Dir(l.path) }

// DeleteAttachments 删除会话的附件目录（随会话删除一起调用；幂等）。
func DeleteAttachments(dataDir, sessionID string) error {
	return os.RemoveAll(AttachmentDir(dataDir, sessionID))
}
