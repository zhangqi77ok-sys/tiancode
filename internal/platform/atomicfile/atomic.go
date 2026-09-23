// Package atomicfile 提供原子写文件：temp + Sync + Rename，Windows rename 冲突时备份式替换回退。
//
// 做什么：WriteFileAtomic 保证目标文件任何时刻要么是旧内容要么是新内容，
// 且旧版本在回退路径中以 .bak 保留（原数据不丢）。
// 被谁依赖：internal/core/session（快照/索引）、internal/app。
// 依赖谁：仅 stdlib。
//
// 为什么需要备份回退：Windows 下目标文件被占用（杀软扫描/并发读）时 os.Rename
// 报 AccessDenied；旧实现（legacy internal/session/store.go:399-402）在此直接失败
// 且上层 `_ =` 吞错导致丢消息。本包把该场景降级为可恢复（契约 C-SES-5）。
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic 原子写入 data 到 path。
// 流程：同级临时文件 → Sync 强制落盘 → Rename 原子覆盖；
// Rename 失败（典型为 Windows 目标被占用）→ 备份式替换回退。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomicWith(path, data, perm, os.Rename)
}

// writeFileAtomicWith 是可注入 rename 的实现体（测试用故障注入锁定回退路径）。
func writeFileAtomicWith(path string, data []byte, perm os.FileMode, rename func(from, to string) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// 为什么用同级临时文件：Rename 跨卷会退化为拷贝，失去原子性
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp_*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := rename(tmpPath, path); err == nil {
		return nil
	}

	// 回退：旧文件挪成 .bak 腾出目标名，再重试 rename（原数据保留在 .bak）
	bak := path + ".bak"
	if err := rename(path, bak); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("atomic rename failed and backup move also failed: %w", err)
	}
	if err := rename(tmpPath, path); err != nil {
		// 尽力还原 .bak 到目标路径，保证调用方数据仍在原位
		if restore := rename(bak, path); restore != nil {
			return fmt.Errorf("rename failed (%v); backup restore failed (%v); backup kept at %s", err, restore, bak)
		}
		return fmt.Errorf("atomic rename failed after backup move: %w", err)
	}
	return nil
}
