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

import "context"

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
