//go:build windows

// 安装步骤编排与失败回滚。
//
// 为什么需要回滚：doInstall 的步骤有先后依赖，任何一步失败都可能留下半成品。
// 最恶劣的是注册表写在最后一步——一旦它失败（HKCU 写权限受限、组策略锁定等），
// 程序文件已经躺在安装目录里，而"应用和功能"里没有卸载项，用户再也卸不掉。
// 实测踩过的同款后果就记在 install.go 的 powershellExe 注释里。
//
// 为什么用"备份旧文件 + 逆操作"而不是"先装到临时目录再整体切换"：后者要在磁盘上
// 多留一份完整载荷（几十 MB），而本安装器的载荷本就内嵌在 setup exe 里，直接备份
// "将被覆盖的那两个文件"成本更低、失败点更少。

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// backupSuffix 是回滚备份文件的后缀：覆盖写之前把既有文件挪到 <path><suffix>，
// 全流程成功后删掉，失败时挪回原位。
const backupSuffix = ".tiancode-bak"

// rollback 按"已做之事"倒序撤销。
type rollback struct{ steps []func() }

// add 登记一步撤销操作。
func (rb *rollback) add(undo func()) { rb.steps = append(rb.steps, undo) }

// undo 逆序执行全部撤销。单步失败只 warn 不中断：回滚已尽力，且每步都会落
// setup.log，用户事后能查到残留了什么——中途停下反而会留下更多半成品。
func (rb *rollback) undo() {
	for i := len(rb.steps) - 1; i >= 0; i-- {
		rb.steps[i]()
	}
}

// pathExists 判断路径是否存在。
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// dirGuard 守卫安装目录：只有在"目录是我们创建的"前提下，回滚才允许删它。
type dirGuard struct{ path string }

// rollback 返回撤销步骤。非空目录不删——用户自己放进去的文件不该被回滚带走。
func (g dirGuard) rollback() func() {
	if !pathExists(g.path) {
		return func() {}
	}
	return func() {
		if err := os.Remove(g.path); err != nil {
			warn("回滚：删除空安装目录 %s 失败：%v", g.path, err)
		}
	}
}

// fileGuard 守卫一个"将被覆盖写入"的文件：写前把旧内容挪到备份路径，
// commit 删除备份，restore 删掉新内容并把旧内容挪回原位。
type fileGuard struct{ path string }

// prepare 把既有文件**复制**到备份路径，原文件保持不动。
//
// 为什么是复制而不是 rename：rename 会让目标路径出现一个"文件不在"的空窗，
// 此刻若进程被杀或机器断电，用户就有一个凭空消失的程序。复制则保证任何中断点上
// 都还有一个可用的 exe（旧的或新的），代价只是一次本地复制。
func (g *fileGuard) prepare() error {
	if !pathExists(g.path) {
		return nil
	}
	src, err := os.Open(g.path)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(g.path + backupSuffix)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// restore 撤销本次覆盖：删掉当前内容，再把备份挪回原位。
func (g *fileGuard) restore() {
	// 先删当前内容：它可能是本次写入的新版，也可能是首次安装的新文件——
	// 两种情况都该删，不能因为"没有备份"就把它留在盘上。
	if err := os.Remove(g.path); err != nil && !os.IsNotExist(err) {
		warn("回滚：删除 %s 失败：%v", g.path, err)
		return
	}
	bak := g.path + backupSuffix
	if !pathExists(bak) {
		return // 首次安装：没有旧内容可恢复
	}
	if err := os.Rename(bak, g.path); err != nil {
		warn("回滚：恢复 %s 失败：%v", g.path, err)
	}
}

// commit 删除备份：全流程成功，旧内容不再需要。
func (g *fileGuard) commit() {
	if err := os.Remove(g.path + backupSuffix); err != nil && !os.IsNotExist(err) {
		warn("删除备份 %s 失败：%v", g.path+backupSuffix, err)
	}
}

// installedFiles 记录 installFiles 的产出与需要 commit 的守卫。
type installedFiles struct {
	setupPath string
	guards    []fileGuard
}

// installFiles 写入应用 exe 与卸载入口（安装器自身的副本），返回 setup 路径。
// 两个文件都先备份再覆盖，撤销步骤登记到 rb。
func installFiles(dir string, rb *rollback) (installedFiles, error) {
	// 载荷非空校验放在这里而不是 doInstall：本函数是唯一写 appPath 的地方，
	// 校验跟着写入走才不会出现"上层查过、下层没查"的漏网。
	if len(appBinary) == 0 {
		return installedFiles{}, fmt.Errorf("安装包损坏：内嵌程序为空（请重新运行 release.ps1 构建）")
	}
	appPath := filepath.Join(dir, appExeName)
	appGuard := fileGuard{path: appPath}
	if err := appGuard.prepare(); err != nil {
		return installedFiles{}, fmt.Errorf("备份既有应用文件：%w", err)
	}
	rb.add(appGuard.restore)
	if err := writeFileAtomic(appPath, appBinary); err != nil {
		return installedFiles{}, fmt.Errorf("写入应用文件：%w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return installedFiles{}, fmt.Errorf("定位安装器：%w", err)
	}
	setupPath := filepath.Join(dir, setupCopyName)
	setupGuard := fileGuard{path: setupPath}
	if !samePath(self, setupPath) { // 从安装目录里跑自己时不必覆盖自己
		if err := setupGuard.prepare(); err != nil {
			return installedFiles{}, fmt.Errorf("备份既有卸载入口：%w", err)
		}
		rb.add(setupGuard.restore)
		data, err := os.ReadFile(self)
		if err != nil {
			return installedFiles{}, fmt.Errorf("读取安装器：%w", err)
		}
		if err := writeFileAtomic(setupPath, data); err != nil {
			return installedFiles{}, fmt.Errorf("写入卸载入口：%w", err)
		}
	}
	return installedFiles{
		setupPath: setupPath,
		guards:    []fileGuard{appGuard, setupGuard},
	}, nil
}

// createInstallShortcuts 创建快捷方式，并把删除动作登记为撤销步骤。
func createInstallShortcuts(dir string, desktopIcon bool, rb *rollback) error {
	appPath := filepath.Join(dir, appExeName)
	for _, s := range installShortcuts(desktopIcon) {
		if err := createShortcut(s.lnk, appPath, s.workDir); err != nil {
			if s.required {
				return fmt.Errorf("创建快捷方式 %s：%w", filepath.Base(s.lnk), err)
			}
			// 便利项失败不中断安装（应用与卸载项均可就位），但必须可见：
			// 静默降级会让用户事后才发现入口缺失，而那时已无从判断原因。
			warn("未创建快捷方式 %s：%v", filepath.Base(s.lnk), err)
			continue
		}
		lnk := s.lnk // 显式捕获：撤销步骤是闭包，别让它的存活性依赖循环变量语义
		rb.add(func() { removeShortcutOnRollback(lnk) })
	}
	return nil
}

// removeShortcutOnRollback 回滚时删除本次创建的 .lnk。
func removeShortcutOnRollback(lnk string) {
	if err := os.Remove(lnk); err != nil && !os.IsNotExist(err) {
		warn("回滚：删除快捷方式 %s 失败：%v", lnk, err)
	}
}

// writeUninstallStep 写卸载注册项；先抓取原值以便回滚还原。
func writeUninstallStep(dir, setupPath string, rb *rollback) error {
	g := newRegistryGuard(uninstallKey)
	g.capture()
	rb.add(g.restore)
	if err := writeUninstallEntry(dir, setupPath); err != nil {
		return fmt.Errorf("写入卸载注册项：%w", err)
	}
	return nil
}

// registryGuard 守卫一个卸载注册项：写入前抓取原值，失败时还原。
// 原本没有这个键时，restore 把它删掉——否则会留下一个指向空目录的卸载项。
//
// 键名是字段而非常量：单测用临时键验证原值还原，不必去碰用户真实的
// Uninstall\tiancode（install-smoke 的教训：动那个键会牵连用户已装的版本）。
type registryGuard struct {
	key     string
	existed bool
	values  map[string]string
}

// newRegistryGuard 返回针对指定键的守卫。
func newRegistryGuard(key string) *registryGuard { return &registryGuard{key: key} }

// capture 记录键的当前状态。键不存在是正常情况（首次安装），不视为错误；
// 读不到值时留空即可——writeUninstallEntry 只写字符串值，回滚按字符串还原。
func (g *registryGuard) capture() {
	k, err := registry.OpenKey(registry.CURRENT_USER, g.key, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return
	}
	g.existed = true
	g.values = make(map[string]string, len(names))
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil {
			g.values[n] = v
		}
	}
}

// restore 还原注册项：原本不存在则删除键，原本存在则把抓到的值写回。
func (g *registryGuard) restore() {
	if !g.existed {
		if err := registry.DeleteKey(registry.CURRENT_USER, g.key); err != nil && err != registry.ErrNotExist {
			warn("回滚：删除卸载注册项失败：%v", err)
		}
		return
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, g.key, registry.WRITE)
	if err != nil {
		warn("回滚：重建卸载注册项失败：%v", err)
		return
	}
	defer k.Close()
	for n, v := range g.values {
		if err := k.SetStringValue(n, v); err != nil {
			warn("回滚：恢复注册项值 %s 失败：%v", n, err)
		}
	}
}

// doInstall 安装：备份 → 写应用 exe → 复制自身（卸载入口）→ 快捷方式 → 注册表项 → 提交备份。
// desktopIcon 为 false 时跳过桌面快捷方式（仅开始菜单），供受限环境或脚本化部署选择。
//
// 任一步失败都会回滚到"等于没装过"，并把错误包装成"已回滚"——
// 让调用方与用户知道磁盘状态，不是一个半安装。
func doInstall(dir string, desktopIcon bool) (err error) {
	appendSetupLog(fmt.Sprintf("install v%s dir=%s desktopIcon=%v", version, dir, desktopIcon))

	var rb rollback
	defer func() {
		if err != nil {
			rb.undo()
			err = fmt.Errorf("安装失败，已回滚到安装前状态：%w", err)
		}
	}()

	dirExisted := pathExists(dir)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建安装目录：%w", err)
	}
	if !dirExisted {
		rb.add(dirGuard{path: dir}.rollback())
	}

	files, err := installFiles(dir, &rb)
	if err != nil {
		return err
	}
	if err = createInstallShortcuts(dir, desktopIcon, &rb); err != nil {
		return err
	}
	if err = writeUninstallStep(dir, files.setupPath, &rb); err != nil {
		return err
	}
	for _, g := range files.guards {
		g.commit()
	}
	return nil
}
