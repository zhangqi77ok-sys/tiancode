// 升级退出握手（0.0.38）用例：忙拒绝 / 空闲接受（退出动作经注入桩计数）。
package app

import (
	"testing"
)

type stubTurns struct{ running bool }

func (s stubTurns) AnyTurnRunning() bool { return s.running }

// 忙（任一会话有进行中回合）：拒绝退出——quitting 不置位、不调 Quit。
func TestHandleUpgradeExit_BusyRefuses(t *testing.T) {
	oldQuit := requestQuit
	quitCalled := 0
	requestQuit = func() { quitCalled++ }
	defer func() {
		requestQuit = oldQuit
		quitting.Store(false)
	}()

	// 空闲：接受并请求退出
	quitting.Store(false)
	HandleUpgradeExit(stubTurns{running: false})
	if !Quitting() || quitCalled != 1 {
		t.Fatalf("空闲实例应置 quitting 并请求退出：quitting=%v quit=%d", Quitting(), quitCalled)
	}

	// 忙：拒绝——quitting 不置位、不调 Quit（绝不中断正在跑的任务）
	quitting.Store(false)
	HandleUpgradeExit(stubTurns{running: true})
	if Quitting() || quitCalled != 1 {
		t.Fatalf("忙实例必须拒绝退出：quitting=%v quit=%d", Quitting(), quitCalled)
	}
}
