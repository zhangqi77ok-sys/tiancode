package app

import "tiancode/internal/app"

// 目录树绑定：列出这场对话工作区内一层目录（右栏「目录」tab，Wails 自动暴露）。

// ListWorkspaceDir 列出 sessionID 这场对话工作区内 relPath 目录的下一层条目
// （目录在前、名称次序，条目带 name/isDir/modTime）。relPath 为空 = 根本身；
// 路径校验（含符号链接解析）在编排层 ChatService.ListWorkspaceDir，壳层只转发不吞错。
func (b *Bind) ListWorkspaceDir(sessionID, relPath string) ([]app.DirEntry, error) {
	return b.chat.ListWorkspaceDir(sessionID, relPath)
}
