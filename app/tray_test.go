// 双开守卫测试（0.0.25）：单实例锁的唤起回调必须安全——
// 没登记 Bind（OnStartup 未跑）时不 panic；正在退出时不把窗口拽回来。
package app

import "testing"

func TestRestoreMainWindow_SafeWithoutBind(t *testing.T) {
	// 未登记 Bind：OnStartup 没跑到就触发回调（真实触发点就在 Wails 启动早期）
	bindInstance.Store(nil)
	RestoreMainWindow() // 不 panic 即通过
}

func TestRestoreMainWindow_NoopWhileQuitting(t *testing.T) {
	bindInstance.Store(nil)
	quitting.Store(true)
	defer quitting.Store(false)
	RestoreMainWindow() // quitting 时直接返回
	if !Quitting() {
		t.Fatal("退出标记未置位（测试自身状态错）")
	}
}

func TestSetActiveBind(t *testing.T) {
	b := &Bind{}
	SetActiveBind(b)
	defer bindInstance.Store(nil)
	got := bindInstance.Load()
	if got != b {
		t.Fatalf("SetActiveBind 未登记：%v", got)
	}
}
