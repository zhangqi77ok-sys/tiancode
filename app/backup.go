// 备份与恢复绑定（0.0.23）：弹系统对话框选路径，落地逻辑在编排层。
package app

import (
	"path/filepath"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"tiancode/internal/app"
)

// 备份文件名带版本与时间（用户一眼知道"这是哪天的备份"）。
func backupFileName(version string) string {
	return "tiancode-backup-" + version + "-" + time.Now().Format("20060102-150405") + ".zip"
}

// ExportBackupTo 弹保存对话框 → 打包整个用户数据目录 → 返回备份文件路径。
// 用户取消对话框 = 显式的"不备份"，返回空串（前端不报错）。
func (b *Bind) ExportBackupTo(version string) (string, error) {
	dest, err := wruntime.SaveFileDialog(b.AppCtx, wruntime.SaveDialogOptions{
		DefaultFilename: backupFileName(version),
		Title:           "导出 tiancode 数据备份",
	})
	if err != nil {
		return "", err
	}
	if dest == "" {
		return "", nil // 用户取消
	}
	if _, err := b.chat.ExportBackup(dest, version); err != nil {
		return "", err
	}
	return dest, nil
}

// PickBackupFile 弹打开对话框 → 返回所选路径（不预检：预检由前端确认前再调）。
func (b *Bind) PickBackupFile() (string, error) {
	return wruntime.OpenFileDialog(b.AppCtx, wruntime.OpenDialogOptions{
		Title: "选择 tiancode 备份文件",
	})
}

// PreviewBackup 只读预检导入包（不碰用户数据；结果给确认框展示）。
func (b *Bind) PreviewBackup(path string) (app.BackupPreview, error) {
	return app.PreviewImport(path), nil
}

// BackupApplyResult 是恢复结果（前端据此如实展示，不谎报"全部恢复"）。
type BackupApplyResult struct {
	File    string `json:"file"`    // 备份文件名
	Written int    `json:"written"` // 实际写入条数
	Skipped int    `json:"skipped"` // 跳过（只补新模式下已存在）
}

// ApplyBackup 落地导入（用户确认后调用）。
// 应用需重启才能完整看到恢复后的账本/渠道——前端在结果里明确提示。
func (b *Bind) ApplyBackup(path string, overwrite bool) (BackupApplyResult, error) {
	written, skipped, err := b.chat.ApplyImport(path, overwrite)
	if err != nil {
		return BackupApplyResult{}, err
	}
	return BackupApplyResult{File: filepath.Base(path), Written: written, Skipped: skipped}, nil
}
