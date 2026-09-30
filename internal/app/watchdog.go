package app

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"tiancode/internal/core/agent"
)

// firstEventTimeout 是零事件看门狗的阈值（包级变量：测试注入短值）。
var firstEventTimeout = 90 * time.Second

// zeroEventWatch 是一轮对话的"零事件看门狗"共享状态（0.2.29 引入，0.0.07 重写语义）。
//
// 背景：上游黑洞、代理 CONNECT 挂死这类场景会让一轮对话**零输出永久挂起**——
// 流层空闲看门狗要等流建立后才计时，ResponseHeaderTimeout 管不到代理 CONNECT
// 阶段（Go Transport 的已知行为），实机表现就是"发送后永久运行中，无任何反馈"。
//
// 语义（0.0.07 滑动窗口，取代旧"一次性豁免"）：
//   - mark = 刷新计时起点：任何可见活动（流式增量/工具卡/任务清单）都把窗口推到"现在"；
//     旧实现 mark 置一次性标志，导致答复后紧跟的一张工具卡把整轮永久豁免
//     （实机：ask 答复后请求挂在中转上，界面"运行中"38 分钟无终态）；
//   - pause/resume：等待审批/等待答复不是活动，而是**挂起判定**——等多久都不算
//     超时（用户去吃个饭回来再答是正常的），但答复返回必须 resume 重新计时；
//   - ticker：未挂起且距上次活动超过 window → 注入终态。
type zeroEventWatch struct {
	start    atomic.Value // time.Time：最近一次可见活动（或 resume）的时刻
	paused   atomic.Bool  // true = 正在等待审批/答复：判定挂起
	timedOut atomic.Bool
	window   time.Duration // 看门狗阈值快照：构造时从 firstEventTimeout 取值（见 timeoutMessage 的竞态注）
}

func newZeroEventWatch(window time.Duration) *zeroEventWatch {
	w := &zeroEventWatch{window: window}
	w.start.Store(time.Now())
	return w
}

// mark 记录一次可见活动：刷新计时起点（滑动窗口）。
func (w *zeroEventWatch) mark() { w.start.Store(time.Now()) }

// pause 挂起判定（等待审批/答复期间等多久都不超时）。
func (w *zeroEventWatch) pause() { w.paused.Store(true) }

// resume 恢复判定并重新计时（答复返回后，黑洞仍受保护）。
func (w *zeroEventWatch) resume() {
	w.start.Store(time.Now())
	w.paused.Store(false)
}

// timedOutFlag 读取后清零由调用方处理；这里只暴露查询。
func (w *zeroEventWatch) hasTimedOut() bool { return w.timedOut.Load() }

// timeoutMessage 是看门狗触发后给用户的明确说明。
//
// 读快照字段而非包级变量：包级 firstEventTimeout 会被测试注入短值、收尾时恢复，
// 而看门狗 goroutine 可能活到测试收尾之后——直读全局会与下一个测试的写入
// 构成数据竞态（CI -race 抓到）。
func (w *zeroEventWatch) timeoutMessage() string {
	return fmt.Sprintf("响应超时：%d 秒内没有任何数据，已自动中断（上游无响应或网络挂起；请检查渠道地址与代理设置后重试）",
		int(w.window.Seconds()))
}

// watchApprover 把"等待审批答复"处理为判定挂起：进入等待 pause，答复返回 resume。
type watchApprover struct {
	inner agent.Approver
	watch *zeroEventWatch
}

func (w *watchApprover) Review(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	w.watch.pause()
	d, err := w.inner.Review(ctx, req)
	// 无论答复还是取消都 resume：取消路径轮次即将收束，resume 无副作用；
	// 答复路径之后的推理黑洞重新受看门狗保护（0.0.07 实机修复）
	w.watch.resume()
	return d, err
}

// watchAsker 与 watchApprover 同构：等待用户作答 = 挂起判定，答复返回 = 重新计时。
type watchAsker struct {
	inner agent.Asker
	watch *zeroEventWatch
}

func (w *watchAsker) Ask(ctx context.Context, req agent.AskRequest) (string, error) {
	w.watch.pause()
	ans, err := w.inner.Ask(ctx, req)
	// 0.0.07 实机：答复后带图请求挂在中转上，旧"一次性 mark"语义让看门狗永久
	// 豁免，轮次卡"运行中"38 分钟无终态。resume 后黑洞在窗口内照常收束。
	w.watch.resume()
	return ans, err
}
