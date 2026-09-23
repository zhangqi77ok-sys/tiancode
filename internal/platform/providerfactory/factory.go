// Package providerfactory 按渠道协议构造供应商适配器（Factory 模式，ADR-0005）。
//
// 做什么：把"渠道 → 协议 → 具体适配器"的分派收敛到一处；
// 加一个协议 = 加一个 case + 一个适配器包，agent 零改动。
// 被谁依赖：internal/app（按激活渠道重建 ChatRuntime）。
// 依赖谁：core/llm（端口与渠道类型）、platform 下各 provider 适配器。
//
// 纪律：未实现协议必须显式报错（含协议名），绝不静默降级——
// 静默降级会让用户以为切了供应商实际没切（配置谎言比报错更贵）。
package providerfactory

import (
	"fmt"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/openaiprovider"
)

// Factory 是供应商适配器工厂。
type Factory struct{}

// New 构造工厂。
func New() *Factory { return &Factory{} }

// NewProvider 按渠道协议构造 ProviderPort。
func (f *Factory) NewProvider(ch llm.Channel) (llm.ProviderPort, error) {
	switch ch.Protocol {
	case llm.ProtocolOpenAI:
		return openaiprovider.New(openaiprovider.Options{
			BaseURL: ch.BaseURL,
			APIKey:  ch.APIKey,
		}), nil
	default:
		return nil, fmt.Errorf("protocol %q 尚未实现（当前仅支持 %s）", ch.Protocol, llm.ProtocolOpenAI)
	}
}
