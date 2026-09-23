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
type RuntimePolicy struct {
	// MaxAttempts 流开始前的最大尝试次数（含首次）。
	// 为什么默认语义为 2：个人工具单渠道，对"建立流"重试一次足矣；
	// 进入流式后的失败一律不重试（见 Chat 契约）。
	MaxAttempts int
}

// DefaultRuntimePolicy 返回 MVP 默认策略。
func DefaultRuntimePolicy() RuntimePolicy { return RuntimePolicy{MaxAttempts: 2} }

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

// TimeoutBudget 是 Runtime 施加的超时预算（C-RT-3）。
// 分层对应关系：连接层超时由 provider 的 HTTPClient 决定；空闲层由 provider 的
// 空闲看门狗决定；总时长层由本预算经 ctx 截止时间统一强制——三层互不替代。
type TimeoutBudget struct {
	// Total 是单次调用总预算（覆盖建流与流式全程）。<=0 表示不设总预算。
	Total time.Duration
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
			// terminalOnCtxDone 区分 callCtx 结束的两种成因——它们语义完全不同：
			//   1) 调用方取消（用户点"中断"）：这不是错误。必须上报 EndCancelled，
			//      否则 UI 会把主动中断渲染成"错误"（契约 C-APP-2 要求 EndCancelled）。
			//   2) 总时长预算到期（C-RT-3）：这才是错误终态。
			// 判据用**外层 ctx**：外层已结束即为取消；否则是预算到期。
			terminalOnCtxDone := func() StreamChunk {
				if err := ctx.Err(); err != nil {
					return StreamChunk{EndReason: EndCancelled, Err: err}
				}
				return StreamChunk{EndReason: EndError, Err: callCtx.Err()}
			}
			for {
				select {
				case c, ok := <-ch:
					if !ok {
						return
					}
					select {
					case wrapped <- c:
					case <-callCtx.Done():
						// 预算到期/被取消且消费方停读：尽力补发终态后退出（close 由 defer 兜底）
						select {
						case wrapped <- terminalOnCtxDone():
						default:
						}
						return
					}
				case <-callCtx.Done():
					select {
					case wrapped <- terminalOnCtxDone():
					default:
					}
					return
				}
			}
		}()
		return wrapped, nil
	}
	return nil, fmt.Errorf("chat runtime: all %d attempt(s) failed: %w", policy.MaxAttempts, lastErr)
}
