package sandbox

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Snapshot 快照元数据
type Snapshot struct {
	ID        string    `json:"id"`
	CommitSHA string    `json:"commit_sha"`
	TreeSHA   string    `json:"tree_sha"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// SnapshotManager Git 管道影子快照管理器
type SnapshotManager struct {
	rootDir string
}

// NewSnapshotManager 构造快照管理器
func NewSnapshotManager(rootDir string) *SnapshotManager {
	return &SnapshotManager{rootDir: rootDir}
}

// execGit 在工作区根目录下执行 Git 命令
func (m *SnapshotManager) execGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.rootDir
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW 杜绝控制台闪烁黑框
			HideWindow:    true,
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s failed: %v, stderr: %s", strings.Join(args, " "), err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// CreateSnapshot 使用 Git Plumbing 管道毫秒级生成孤立快照
func (m *SnapshotManager) CreateSnapshot(reason string) (*Snapshot, error) {
	// 1. 生成树对象 Tree SHA (即便几十万文件也是毫秒级)
	treeSHA, err := m.execGit("write-tree")
	if err != nil {
		return nil, fmt.Errorf("failed to git write-tree: %w", err)
	}

	// 2. 生成独立 Commit 对象 (不关联任何已有分支，不污染分支历史)
	commitMsg := fmt.Sprintf("tcode-snapshot: %s (%s)", reason, time.Now().Format("2006-01-02 15:04:05"))
	commitSHA, err := m.execGit("commit-tree", treeSHA, "-m", commitMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to git commit-tree: %w", err)
	}

	// 3. 记录引用到 .git/refs/tcode/snapshots/
	snapshotID := fmt.Sprintf("snap_%d", time.Now().UnixMilli())
	refPath := fmt.Sprintf("refs/tcode/snapshots/%s", snapshotID)
	if _, err := m.execGit("update-ref", refPath, commitSHA); err != nil {
		return nil, fmt.Errorf("failed to update-ref [%s]: %w", refPath, err)
	}

	return &Snapshot{
		ID:        snapshotID,
		CommitSHA: commitSHA,
		TreeSHA:   treeSHA,
		Message:   reason,
		CreatedAt: time.Now(),
	}, nil
}

// ListSnapshots 获取历史快照清单
func (m *SnapshotManager) ListSnapshots() ([]Snapshot, error) {
	out, err := m.execGit("for-each-ref", "--format=%(refname:short)|%(objectname)|%(contents:subject)|%(committerdate:iso8601)", "refs/tcode/snapshots")
	if err != nil {
		return nil, nil // 无快照引用时返回空
	}

	lines := strings.Split(out, "\n")
	snapshots := make([]Snapshot, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 4 {
			refShort := parts[0]
			id := strings.TrimPrefix(refShort, "refs/tcode/snapshots/")
			id = strings.TrimPrefix(id, "tiancode/snapshots/")

			created, _ := time.Parse("2006-01-02 15:04:05 -0700", parts[3])
			snapshots = append(snapshots, Snapshot{
				ID:        id,
				CommitSHA: parts[1],
				Message:   parts[2],
				CreatedAt: created,
			})
		}
	}

	return snapshots, nil
}

// RollbackFile 秒级回退单个或多个文件至快照状态
func (m *SnapshotManager) RollbackFile(commitSHA string, filePath string) error {
	_, err := m.execGit("checkout", commitSHA, "--", filePath)
	if err != nil {
		return fmt.Errorf("failed to rollback file [%s] to commit [%s]: %w", filePath, commitSHA, err)
	}
	return nil
}

// RollbackLatest 将指定文件回退到最近一次影子快照状态（Harness 状态回溯安全网）。
// 用于 TDD 校验未过时自动撤销已落盘的文件；无可用快照时返回错误交由调用方处理。
func (m *SnapshotManager) RollbackLatest(filePath string) error {
	snaps, err := m.ListSnapshots()
	if err != nil || len(snaps) == 0 {
		return fmt.Errorf("no snapshot available to rollback [%s]: %w", filePath, err)
	}
	latest := snaps[0]
	for _, s := range snaps[1:] {
		if s.CreatedAt.After(latest.CreatedAt) {
			latest = s
		}
	}
	return m.RollbackFile(latest.CommitSHA, filePath)
}
