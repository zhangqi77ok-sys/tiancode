// Runtime 运行时抽象（ADR-0005：Strategy + Facade，钉在"渠道与策略"这一真实变化点上）。
//
// 做什么：ChatRuntime 位于 agent 与 ProviderPort 之间，封装"选哪个渠道、失败怎么重试、
// 超时预算多少"的运行时策略；agent 只依赖 ChatRuntime，不感知渠道存在。
// 被谁依赖：internal/core/agent。
// 依赖谁：本包 ProviderPort（仅 stdlib）。
//
// 关键纪律（借 new-api controller/relay.go 的重试循环）：
// 重试/降级只发生在流开始之前；一旦首块已发出，绝不换渠道，失败按原样上抛——
// 流中换渠道会造成"半段回答来自模型 A、半段来自模型 B"的上下文撕裂。
package llm

import (
	"context"
	"fmt"
	"time"
)

// RuntimePolicy 是一次调用的运行时策略。
// MVP 为单渠道：MaxAttempts 固定语义，重试的是"建立流"这一动作而非渠道切换；
// 多渠道时在此扩展（Strategy 变化点，见 ADR-0005），ChatRuntime 接口不变。
//
// 与 gateway 的分工（0.0.23 审计澄清，防"重试次数说不清"）：
// 本层的重试**只在 provider 返回 error 时**发生（机制性失败：连不上、参数非法、
// 建流前就炸）。provider 用**终态块**（EndError/EndCancelled）表达失败时一律不重试。
// 生产 provider 是 gateway（NewChatRuntime(s.gw, …)），而 gateway 永远不返回 error——
// 它把任何失败都写成终态块（见 gateway.StreamChat），所以生产路径上：
//
//	一次 Send 的上游请求数 = gateway 渠道级重试（默认 MaxRetries=3 → 最多 4 次，
//	另受可用档位数约束）；本层 MaxAttempts 默认 2 **实际不触发**（恒为 1）。
//
// 保留 MaxAttempts 是给未来非 gateway provider（本地模型直连、测试替身）的契约。
// 任何"让 gateway 改返回 error"的改动都会让请求数翻倍——先改这里的语义再改代码。
type RuntimePolicy struct {
	// MaxAttempts 流开始前的最大尝试次数（含首次）。
	// 为什么默认语义为 2：个人工具单渠道，对"建立流"重试一次足矣；
	// 进入流式后的失败一律不重试（见 Chat 契约）。
	MaxAttempts int
}

// DefaultRuntimePolicy 返回 MVP 默认策略。
func DefaultRuntimePolicy() RuntimePolicy { return RuntimePolicy{MaxAttempts: 2} }

// RetryBackoff 是建流重试的退避表（0.0.11）：瞬时故障（网关抖动、连接池耗尽、
// 上游重启窗口）下立刻重打大概率再失败，且对上游不友好。**导出**是为了让同层
// 编排者复用同一节奏——gateway 的渠道级重试（429/5xx/连接失败）也用这张表，
// 避免两套退避值各自漂移。
// 包级变量：测试注入短值（时序敏感测试的既有做法，见 docs/TESTING.md）。
var RetryBackoff = []time.Duration{200 * time.Millisecond, 500 * time.Millisecond}

// BackoffFor 返回第 n 次重试（从 0 起）之前的等待时长；超出表长取最后一档。
// 返回 0 表示不等待（表为空）。
func BackoffFor(n int) time.Duration {
	if len(RetryBackoff) == 0 {
		return 0
	}
	if n < 0 {
		n = 0
	}
	if n >= len(RetryBackoff) {
		n = len(RetryBackoff) - 1
	}
	return RetryBackoff[n]
}

// ChatRuntime 是模型调用运行时端口：agent 唯一可见的模型入口（Facade）。
type ChatRuntime interface {
	// Chat 按策略执行一次流式对话调用。
	//
	// 契约（C-RT-1~4，见 docs/CONTRACTS.md）：
	//   - C-RT-1 流开始前的失败（连接失败/HTTP 非 2xx）按策略重试，对调用方透明；
	//   - C-RT-2 首块发出后的失败不重试、不换渠道，直接透传上游 EndReason 终态；
	//   - C-RT-3 施加连接/空闲/总时长三层超时预算，ProviderPort 实现不得绕过；
	//   - C-RT-4 调用方（agent）不感知渠道与重试的存在。
	Chat(ctx context.Context, req ChatRequest, policy RuntimePolicy) (<-chan StreamChunk, error)
}

// TimeoutBudget 是 Runtime 施加的超时预算（C-RT-3；第 3 批分层：等待首字节 /
// 流式全程分开）。
// 分层对应关系：连接层超时由 provider 的 HTTPClient 决定；空闲层由 provider 的
// 空闲看门狗决定（流建立后计时）；"等待首字节"与"总时长"两层由本预算强制——
// 四层互不替代。
type TimeoutBudget struct {
	// Total 是单次调用总预算（覆盖建流与流式全程）。<=0 表示不设总预算。
	Total time.Duration
	// FirstByte 是"建流完成 → 首个数据块"的独立预算（<=0 = 不单独限制）。
	// 为什么独立（第 3 批）：思考型模型在首字节前可能长时间无输出（推理阶段不产生
	// 任何增量），这与"流中途挂起"是两种成因——前者要给足时间，后者由适配器空闲
	// 看门狗收束。总预算一视同仁会把"慢思考"掐成错误。
	FirstByte time.Duration
}

// chatRuntime 是 ChatRuntime 的默认实现（Strategy 变化点收敛于渠道策略，见 ADR-0005）。
type chatRuntime struct {
	provider ProviderPort
	budget   TimeoutBudget
}

// NewChatRuntime 构造运行时：provider 为唯一上游端口，budget 为总时长预算。
func NewChatRuntime(p ProviderPort, budget TimeoutBudget) ChatRuntime {
	return &chatRuntime{provider: p, budget: budget}
}

// Chat 按策略执行一次流式调用（实现 ChatRuntime，契约 C-RT-1~4）。
func (r *chatRuntime) Chat(ctx context.Context, req ChatRequest, policy RuntimePolicy) (<-chan StreamChunk, error) {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		// 退避（第 4 批）：只在**流开始前**失败的重试之间生效（流已吐内容后
		// 绝不重放，见 C-RT-2——重复执行工具比失败更贵）。表外次数用最后一档。
		if attempt > 1 {
			select {
			case <-time.After(BackoffFor(attempt - 2)):
			case <-ctx.Done():
				return nil, fmt.Errorf("chat runtime: cancelled during retry backoff: %w", ctx.Err())
			}
		}
		// 为什么每次尝试独立 ctx：预算针对"单次尝试"，失败即随本次一起释放
		callCtx, cancel := context.WithTimeout(ctx, r.budget.Total)
		ch, err := r.provider.StreamChat(callCtx, req)
		if err != nil {
			// 流开始前失败（C-RT-1）：释放本次预算后重试，对调用方透明
			cancel()
			lastErr = err
			continue
		}
		// 流已建立（C-RT-2）：绝不重试。转接 channel 并托管预算 ctx 生命周期——
		// relay 在流关闭（或预算到期）时调用 cancel，防止 context 泄漏。
		wrapped := make(chan StreamChunk)
		go func() {
			defer close(wrapped)
			defer cancel()
			// 首字节看门狗（第 3 批）：建流完成到首个数据块的独立预算。
			// 思考型模型的推理阶段可能长时间无增量——这段等待与"流中途挂起"成因不同，
			// 单独计时并单独报错（见下），不与总预算混为一谈。
			firstSeen := false
			var fbTimer *time.Timer
			var fbC <-chan time.Time
			if r.budget.FirstByte > 0 {
				fbTimer = time.NewTimer(r.budget.FirstByte)
				defer fbTimer.Stop()
				fbC = fbTimer.C
			}
			// terminalOnCtxDone 区分 callCtx 结束的两种成因——它们语义完全不同：
			//   1) 调用方取消（用户点"中断"）：这不是错误。必须上报 EndCancelled，
			//      否则 UI 会把主动中断渲染成"错误"（契约 C-APP-2 要求 EndCancelled）。
			//   2) 总时长预算到期（C-RT-3）：错误终态，且文案必须写明"本地预算用尽"——
			//      绝不伪装成上游 500/断流（第 3 批：用户看到"预算用尽"才知道该重试还是换渠道）。
			// 判据用**外层 ctx**：外层已结束即为取消；否则是预算到期。
			terminalOnCtxDone := func() StreamChunk {
				if err := ctx.Err(); err != nil {
					return StreamChunk{EndReason: EndCancelled, Err: err}
				}
				return StreamChunk{EndReason: EndError, Err: fmt.Errorf(
					"本轮上游调用超出总时长预算（%s）已被本地中止：这不是上游返回的错误，可重试或检查渠道/网络",
					r.budget.Total)}
			}
			// emitFinal 限时补发终态（绝不 default 直接丢：消费方只是暂时不在接收点上）。
			// 与 gateway.finishStopped 同一纪律：真离开的消费方 500ms 后放行。
			emitFinal := func(c StreamChunk) {
				select {
				case wrapped <- c:
				case <-time.After(500 * time.Millisecond):
				}
			}
			for {
				select {
				case c, ok := <-ch:
					if !ok {
						return
					}
					if !firstSeen {
						firstSeen = true
						if fbTimer != nil {
							fbTimer.Stop()
						}
					}
					select {
					case wrapped <- c:
					case <-callCtx.Done():
						emitFinal(terminalOnCtxDone())
						return
					}
				case <-fbC:
					// 首字节预算用尽：取消上游，报明确终态（可重试/可调大预算）
					if !firstSeen {
						cancel()
						emitFinal(StreamChunk{EndReason: EndError, Err: fmt.Errorf(
							"等待上游首个数据超过 %s（建流后长时间无输出）：思考型模型的首字节可能较慢，可重试或调大等待预算",
							r.budget.FirstByte)})
						return
					}
				case <-callCtx.Done():
					emitFinal(terminalOnCtxDone())
					return
				}
			}
		}()
		return wrapped, nil
	}
	return nil, fmt.Errorf("chat runtime: all %d attempt(s) failed: %w", policy.MaxAttempts, lastErr)
}
