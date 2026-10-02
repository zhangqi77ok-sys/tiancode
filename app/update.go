// 自更新绑定（0.0.20）：检查 GitHub 最新 release → 用户确认 → 下载安装包 →
// 拉起安装器（-quiet -relaunch）→ 应用自退。真正的网络与版本比较在
// internal/platform/selfupdate，本层只编排与把决定权交给用户。
package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"tiancode/internal/platform/selfupdate"
)

// Version 是当前运行版本（release.ps1 经 main.go 注入；"dev" = 本地构建不检查）。
// 包级变量而非 New 参数：不破坏既有 New(chat) 调用面（bind_test 锁定的契约）。
var Version = "dev"

// UpdateInfo 是检查结果（与 selfupdate.Info 同构透传）。
type UpdateInfo = selfupdate.Info

// CheckUpdate 查询最新 release 并比较版本。
func (b *Bind) CheckUpdate() (UpdateInfo, error) {
	return selfupdate.Check(Version, b.chat.Proxy)
}

// ApplyUpdate 下载最新安装包并拉起安装器（-quiet -relaunch），随后应用自退——
// 安装器会先优雅关闭运行中的旧版（closeRunningApp，幂等），覆盖安装后重启新版。
// 前置：调用方必须已经过用户确认（本层不再弹窗）。
func (b *Bind) ApplyUpdate() error {
	info, err := selfupdate.Check(Version, b.chat.Proxy)
	if err != nil {
		return err
	}
	if !info.HasUpdate {
		return fmt.Errorf("当前已是最新版本（%s）", info.Current)
	}
	if info.AssetURL == "" {
		return fmt.Errorf("最新 release（%s）没有挂安装包，请到发布页手动下载：%s", info.Latest, info.PageURL)
	}
	dest := filepath.Join(os.TempDir(), "tiancode-update", info.AssetName)
	if err := selfupdate.Download(b.appCtx(), info.AssetURL, dest, info.AssetSize, b.chat.Proxy); err != nil {
		return err
	}
	// 拉起安装器（隐藏窗口；安装器自己会优雅关闭本应用再覆盖安装再重启新版）
	cmd := exec.Command(dest, "-quiet", "-relaunch")
	hideConsole(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("拉起安装器失败：%w", err)
	}
	// 给安装器一点启动时间后自退；安装器的 closeRunningApp 对"已退出"幂等。
	go func() {
		if ctx := b.appCtx(); ctx != nil {
			wruntime.Quit(ctx)
		}
	}()
	return nil
}
