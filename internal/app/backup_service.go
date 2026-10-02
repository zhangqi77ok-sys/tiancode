// 数据备份与迁移（0.0.23 审查第 11 项）：一键导出/导入整个用户数据目录。
//
// 为什么值得做：本地优先应用的数据是无价的——会话账本、记忆、渠道、语气、扩展
// 全在 %APPDATA%\tiancode。换机/重装/迁移现在只能手工拷目录（用户做得到，但
// 容易漏、会拷到一半），而"拷漏了"是那种事后才发现的损失。
//
// 三条纪律：
//   - **导出即快照**：只读打包，不改任何运行态（不 repair 账本、不 flush）；
//   - **导入先预检再落地**：选文件后先给摘要（文件数/会话数/清单版本），用户确认
//     才写；落地前自动把当前数据另存一份 .bak（手抖兜底，导入失败也能退回）；
//   - **解压必须防 zip slip**：条目路径规范化后必须仍在目标目录内（防恶意/畸形
//     zip 把文件写到目标外），条目数与单文件大小都有硬顶。
package app

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// 备份清单（zip 根目录的 manifest.json）：导入方据此识别"是不是 tiancode 的备份"
// 与"是哪一版格式"，而不是靠猜目录名。
const backupManifest = "manifest.json"

// 备份硬顶（防一次操作读爆内存/磁盘）。
const (
	maxBackupBytes   = 512 << 20 // 打包输入总字节上限
	maxBackupEntries = 200000    // 条目数上限
	maxBackupFile    = 64 << 20  // 单文件上限（防 zip 炸弹）
)

// backupManifestBody 是清单内容。
type backupManifestBody struct {
	App        string   `json:"app"`        // 恒为 "tiancode"
	Format     int      `json:"format"`     // 备份格式版本（当前 1）
	Version    string   `json:"version"`    // 打包时的应用版本（登记用）
	ExportedAt int64    `json:"exportedAt"` // Unix 毫秒
	Files      []string `json:"files"`      // 相对路径清单（人可读 + 预检用）
	TotalBytes int64    `json:"totalBytes"`
}

// BackupPreview 是导入预检结果（前端确认框展示，Apply 前不再改数据）。
type BackupPreview struct {
	Path         string `json:"path"`
	Valid        bool   `json:"valid"`
	Format       int    `json:"format"`
	Version      string `json:"version"`
	ExportedAt   int64  `json:"exportedAt"`
	FileCount    int    `json:"fileCount"`
	SessionCount int    `json:"sessionCount"`
	TotalBytes   int64  `json:"totalBytes"`
	Reason       string `json:"reason"` // 无效原因（Valid=false 时给用户看）
}

// ExportBackup 把整个用户数据目录打包到 dest（zip）。调用方负责选路径。
// 不含运行时锁：只读遍历，运行中的账本最多少扫到半行（那是真的半行，不是损坏）。
func (s *ChatService) ExportBackup(dest, version string) (backupManifestBody, error) {
	var m backupManifestBody
	src := configfile.Dir()
	if _, err := os.Stat(src); err != nil {
		return m, fmt.Errorf("数据目录不可用：%w", err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return m, fmt.Errorf("创建备份文件失败：%w", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	m = backupManifestBody{App: "tiancode", Format: 1, Version: version, ExportedAt: time.Now().UnixMilli()}
	walkErr := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err // 读不到就整体失败：备份必须完整，宁可不做
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			return nil // 目录条目不需要（空目录恢复时会自动建）
		}
		if !info.Mode().IsRegular() {
			return nil // 跳过符号链接/设备等：备份要的是数据，不是目录结构怪癖
		}
		m.TotalBytes += info.Size()
		if m.TotalBytes > maxBackupBytes {
			return fmt.Errorf("数据总量超过 %d MB 上限：先清理旧会话日志或分批备份", maxBackupBytes>>20)
		}
		m.Files = append(m.Files, filepath.ToSlash(rel))
		w, werr := zw.Create(filepath.ToSlash(rel))
		if werr != nil {
			return werr
		}
		src, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer src.Close()
		_, cerr := io.Copy(w, src)
		return cerr
	})
	if walkErr != nil {
		zw.Close()
		os.Remove(dest) // 半截备份留在盘上比没有更危险（会被误当成可恢复的）
		return m, walkErr
	}
	// 清单最后写（放最后：解压方先看到它就知道包完整了）
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		zw.Close()
		os.Remove(dest)
		return m, err
	}
	w, err := zw.Create(backupManifest)
	if err != nil {
		zw.Close()
		os.Remove(dest)
		return m, err
	}
	if _, err := w.Write(raw); err != nil {
		zw.Close()
		os.Remove(dest)
		return m, err
	}
	if err := zw.Close(); err != nil {
		os.Remove(dest)
		return m, err
	}
	return m, nil
}

// PreviewImport 只读预检：不是 tiancode 备份 / 格式不认识 / 体积异常都如实报出，
// 这一步绝不碰用户数据。
func PreviewImport(src string) BackupPreview {
	pv := BackupPreview{Path: src}
	info, err := os.Stat(src)
	if err != nil {
		pv.Reason = "读不到文件：" + errTextBrief(err)
		return pv
	}
	if info.Size() > maxBackupBytes {
		pv.Reason = fmt.Sprintf("文件超过 %d MB 上限", maxBackupBytes>>20)
		return pv
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		pv.Reason = "不是有效的 zip：" + errTextBrief(err)
		return pv
	}
	defer zr.Close()
	if len(zr.File) > maxBackupEntries {
		pv.Reason = "条目数异常（疑似畸形包）"
		return pv
	}
	for _, zf := range zr.File {
		if zf.Name == backupManifest {
			rc, oerr := zf.Open()
			if oerr != nil {
				pv.Reason = "清单不可读"
				return pv
			}
			raw, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
			rc.Close()
			var m backupManifestBody
			if err := json.Unmarshal(raw, &m); err != nil {
				pv.Reason = "清单解析失败"
				return pv
			}
			if m.App != "tiancode" {
				pv.Reason = "这不是 tiancode 的备份（app=" + m.App + "）"
				return pv
			}
			if m.Format != 1 {
				pv.Reason = fmt.Sprintf("备份格式 v%d 暂不支持（当前支持 v1）", m.Format)
				return pv
			}
			pv.Valid = true
			pv.Format = m.Format
			pv.Version = m.Version
			pv.ExportedAt = m.ExportedAt
			pv.TotalBytes = m.TotalBytes
		}
		if strings.HasPrefix(zf.Name, "sessions/") && strings.HasSuffix(zf.Name, ".jsonl") {
			pv.SessionCount++
		}
	}
	if !pv.Valid {
		pv.Reason = "包里没有 manifest.json（不是本应用导出的备份）"
		return pv
	}
	pv.FileCount = countNonManifest(zr)
	return pv
}

// countNonManifest 数数据条目（不含清单与目录条目）。
func countNonManifest(zr *zip.ReadCloser) int {
	n := 0
	for _, zf := range zr.File {
		if zf.Name == backupManifest || strings.HasSuffix(zf.Name, "/") {
			continue
		}
		n++
	}
	return n
}

// ApplyImport 落地导入：先给当前数据打 .bak 快照，再逐条写入。
// overwrite=false 时已存在的文件一律跳过（"只补新"模式）。
// 返回本次写入/跳过的文件数（前端如实展示，不谎报"全部恢复"）。
func (s *ChatService) ApplyImport(src string, overwrite bool) (written, skipped int, err error) {
	pv := PreviewImport(src)
	if !pv.Valid {
		return 0, 0, errors.New("备份不可用：" + pv.Reason)
	}
	// 落地前快照：导入是破坏性动作，手抖/坏包都要能退回
	bak := fmt.Sprintf("%s\\tiancode-backup-before-import-%s.zip",
		filepath.Dir(src), time.Now().Format("20060102-150405"))
	if _, err := s.ExportBackup(bak, "pre-import"); err != nil {
		return 0, 0, fmt.Errorf("导入前快照失败（已中止导入）：%w", err)
	}

	zr, zerr := zip.OpenReader(src)
	if zerr != nil {
		return 0, 0, fmt.Errorf("打开备份失败：%w", zerr)
	}
	defer zr.Close()
	dest := configfile.Dir()
	for _, zf := range zr.File {
		if zf.Name == backupManifest || strings.HasSuffix(zf.Name, "/") {
			continue
		}
		// zip slip 防护：规范化后必须仍在目标目录内
		target, serr := safeJoin(dest, zf.Name)
		if serr != nil {
			return written, skipped, serr
		}
		if !overwrite {
			if _, exists := os.Stat(target); exists == nil {
				skipped++
				continue
			}
		}
		if zf.UncompressedSize64 > maxBackupFile {
			return written, skipped, fmt.Errorf("条目 %s 超过 %d MB 上限", zf.Name, maxBackupFile>>20)
		}
		if err := extractOne(zf, target); err != nil {
			return written, skipped, fmt.Errorf("写入 %s 失败：%w", zf.Name, err)
		}
		written++
	}
	return written, skipped, nil
}

// safeJoin 把 zip 条目名解析为目标目录内的路径；越界（zip slip）显式拒绝。
func safeJoin(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("备份条目路径非法（疑似 zip slip）：%s", name)
	}
	target := filepath.Join(dest, clean)
	rel, err := filepath.Rel(dest, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("备份条目越出数据目录：%s", name)
	}
	return target, nil
}

// extractOne 单条解压（先写同目录临时文件再原子改名：断电/失败不留半截文件）。
func extractOne(zf *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	// 限量读：超过单文件硬顶立刻报错（防 zip 炸弹在写盘后才炸）
	raw, err := io.ReadAll(io.LimitReader(rc, maxBackupFile+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxBackupFile {
		return fmt.Errorf("解压后超过 %d MB 上限", maxBackupFile>>20)
	}
	return atomicfile.WriteFileAtomic(target, raw, 0o644)
}

func errTextBrief(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len([]rune(s)) > 120 {
		return string([]rune(s)[:120]) + "…"
	}
	return s
}
