package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sandbox 文件物理受控沙箱
type Sandbox struct {
	rootDir string
}

// NewSandbox 构造沙箱实例
func NewSandbox(rootDir string) (*Sandbox, error) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workspace root: %w", err)
	}
	clean := filepath.Clean(abs)
	return &Sandbox{rootDir: clean}, nil
}

// Root 获取工作区根目录
func (s *Sandbox) Root() string {
	return s.rootDir
}

func normalizeWindowsPath(p string) string {
	vol := filepath.VolumeName(p)
	if len(vol) > 0 {
		return strings.ToUpper(vol) + p[len(vol):]
	}
	return p
}

// ValidatePath 严格校验目标路径是否越界
func (s *Sandbox) ValidatePath(targetPath string) (string, error) {
	cleanTarget := strings.TrimSpace(targetPath)
	if cleanTarget == "" {
		return "", fmt.Errorf("SECURITY: empty path is not allowed")
	}
	if strings.Contains(cleanTarget, "\x00") {
		return "", fmt.Errorf("SECURITY: null byte in path [%s]", targetPath)
	}
	// 去除前导斜杠防止破坏 Join 或在 Windows 下跳回盘符根目录
	if !filepath.IsAbs(cleanTarget) {
		cleanTarget = strings.TrimPrefix(cleanTarget, "/")
		cleanTarget = strings.TrimPrefix(cleanTarget, "\\")
	}

	var fullPath string
	if filepath.IsAbs(cleanTarget) {
		fullPath = filepath.Clean(cleanTarget)
	} else {
		fullPath = filepath.Clean(filepath.Join(s.rootDir, cleanTarget))
	}

	normRoot := normalizeWindowsPath(s.rootDir)
	normFull := normalizeWindowsPath(fullPath)

	rel, err := filepath.Rel(normRoot, normFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("SECURITY: path [%s] escapes sandbox root [%s]", targetPath, s.rootDir)
	}

	return fullPath, nil
}

// SafeReadFile 安全读取沙箱内文件
func (s *Sandbox) SafeReadFile(path string) ([]byte, error) {
	validated, err := s.ValidatePath(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(validated)
}

// AtomicWriteFile 原子写文件防撕裂
// 流程：写同级临时文件 ➔ file.Sync() 强制落盘 ➔ 关闭句柄 ➔ os.Rename 原子覆盖
func (s *Sandbox) AtomicWriteFile(path string, content []byte) error {
	validated, err := s.ValidatePath(path)
	if err != nil {
		return err
	}
	if validated == s.rootDir {
		return fmt.Errorf("SECURITY: cannot overwrite workspace root directory [%s]", s.rootDir)
	}
	if fi, statErr := os.Stat(validated); statErr == nil && fi.IsDir() {
		return fmt.Errorf("SECURITY: cannot overwrite existing directory [%s] with regular file", validated)
	}

	dir := filepath.Dir(validated)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory [%s]: %w", dir, err)
	}

	base := filepath.Base(validated)
	tmpFile, err := os.CreateTemp(dir, fmt.Sprintf(".%s.tcode_tmp_*", base))
	if err != nil {
		return fmt.Errorf("failed to create atomic temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	isCleaned := false

	// 确保发生任何异常退出时彻底清理临时文件，杜绝 .tmp 残留
	defer func() {
		if !isCleaned {
			if tmpFile != nil {
				_ = tmpFile.Close()
			}
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		return fmt.Errorf("failed to write content to temp file: %w", err)
	}

	// 强制落盘刷新 OS 缓存区
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file to disk: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	tmpFile = nil // 句柄已关闭，但 isCleaned 为 false，发生重命名错误仍能清理

	// 原子重命名替换原文件
	if err := os.Rename(tmpName, validated); err != nil {
		// Windows 特殊兼容：尝试先清除已有文件句柄，若重命名依然失败则回退安全覆写
		_ = os.Remove(validated)
		if retryErr := os.Rename(tmpName, validated); retryErr != nil {
			_ = os.Remove(tmpName)
			isCleaned = true
			if writeErr := os.WriteFile(validated, content, 0644); writeErr != nil {
				return fmt.Errorf("atomic rename failed: %w (fallback write: %v)", err, writeErr)
			}
			return nil
		}
	}

	isCleaned = true
	return nil
}

// ListDir 列出指定目录内容（支持空路径或 "." 代表沙箱根目录）
func (s *Sandbox) ListDir(relPath string) ([]os.DirEntry, error) {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" || trimmed == "." {
		return os.ReadDir(s.rootDir)
	}
	validated, err := s.ValidatePath(relPath)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(validated)
}

// SafeCreateDir 在沙箱受控范围内安全创建目录
func (s *Sandbox) SafeCreateDir(relPath string) error {
	validated, err := s.ValidatePath(relPath)
	if err != nil {
		return err
	}
	if validated == s.rootDir {
		return nil
	}
	return os.MkdirAll(validated, 0755)
}

// SafeDelete 在沙箱受控范围内安全删除文件或目录
func (s *Sandbox) SafeDelete(relPath string) error {
	validated, err := s.ValidatePath(relPath)
	if err != nil {
		return err
	}
	if validated == s.rootDir {
		return fmt.Errorf("SECURITY: deleting workspace root [%s] is strictly forbidden", s.rootDir)
	}
	return os.RemoveAll(validated)
}

// SafeRename 在沙箱受控范围内安全重命名文件或目录
func (s *Sandbox) SafeRename(oldRel string, newRel string) error {
	oldVal, err := s.ValidatePath(oldRel)
	if err != nil {
		return fmt.Errorf("invalid source path: %w", err)
	}
	newVal, err := s.ValidatePath(newRel)
	if err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}
	if oldVal == s.rootDir || newVal == s.rootDir {
		return fmt.Errorf("SECURITY: renaming workspace root [%s] is strictly forbidden", s.rootDir)
	}
	if err := os.MkdirAll(filepath.Dir(newVal), 0755); err != nil {
		return err
	}
	return os.Rename(oldVal, newVal)
}

