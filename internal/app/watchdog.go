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

// zeroEventWatch 是一轮对话的"零事件看门狗"共享状态（0.2.29）。
//
// 背景：上游黑洞、代理 CONNECT 挂死这类场景会让一轮对话**零输出永久挂起**——
// 流层空闲看门狗要等流建立后才计时，ResponseHeaderTimeout 管不到代理 CONNECT
// 阶段（Go Transport 的已知行为），实机表现就是"发送后永久运行中，无任何反馈"。
//
// 纪律：**只对"从未有过任何可见活动"的轮次生效**。流式增量、工具卡、任务清单、
// 等待审批、等待答复都算活动——模型已在输出/工具已在执行/正在等用户的轮次
// 绝不被误杀（长任务靠 ctx 取消链与用户手动中断覆盖）。
type zeroEventWatch struct {
	start    time.Time
	window   time.Duration // 看门狗阈值快照：构造时从 firstEventTimeout 取值（见 timeoutMessage 的竞态注）
	sawEvent atomic.Bool
	timedOut atomic.Bool
}

// mark 记录一次可见活动。
func (w *zeroEventWatch) mark() { w.sawEvent.Store(true) }

// timeoutMessage 是看门狗触发后给用户的明确说明。
//
// 读快照字段而非包级变量：包级 firstEventTimeout 会被测试注入短值、收尾时恢复，
// 而看门狗 goroutine 可能活到测试收尾之后——直读全局会与下一个测试的写入
// 构成数据竞态（CI -race 抓到）。
func (w *zeroEventWatch) timeoutMessage() string {
	return fmt.Sprintf("响应超时：%d 秒内没有任何数据，已自动中断（上游无响应或网络挂起；请检查渠道地址与代理设置后重试）",
		int(w.window.Seconds()))
}

// watchApprover 把"等待审批答复"计入看门狗活跃：模型第一轮直接调工具时，
// 审批卡先于任何流式增量出现——等待用户答复期间绝不能被看门狗误杀。
type watchApprover struct {
	inner agent.Approver
	watch *zeroEventWatch
}

func (w *watchApprover) Review(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	w.watch.mark()
	return w.inner.Review(ctx, req)
}

// watchAsker 与 watchApprover 同构：等待用户作答也算活动。
type watchAsker struct {
	inner agent.Asker
	watch *zeroEventWatch
}

func (w *watchAsker) Ask(ctx context.Context, req agent.AskRequest) (string, error) {
	w.watch.mark()
	return w.inner.Ask(ctx, req)
}
