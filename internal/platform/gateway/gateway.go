// Package gateway 是多协议模型渠道的转发编排层（薄，不含协议逻辑）。
//
// 流程（spec 固定路径）：解析入站请求 → selector 选渠道（写上下文）→ model_mapping
// 改写 → adaptor.ConvertRequest → SetupHeaders → DoRequest → ConvertResponse →
// 可重试错误降优先级重试（排除当前渠道）→ auto_ban 禁用渠道/凭证。
//
// 重试语义：
//   - 渠道级故障（网络失败、429、5xx）：排除当前渠道，tier+1 降档重试；
//   - 凭证级故障（401/403 且多凭证）：auto_ban 时禁用当前凭证，同档重试
//     （轮询游标已推进，同一渠道会给出下一条凭证）；
//   - 流已建立（任何块已转发）后绝不重试——防上下文撕裂与内容重复；
//   - 上限 MaxRetries 次后以终态错误收束。
//
// 编排层对协议零感知：类型分派只经 adaptors.Registry。
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/adaptors"
	"tiancode/internal/platform/channels"
)

// Gateway 实现 llm.ProviderPort：对 agent 而言它就是一个"供应商"。
type Gateway struct {
	Pool *channels.Pool
	Reg  *adaptors.Registry
	// Client 可注入（测试用 httptest）；nil 用默认客户端。
	Client *http.Client
	// MaxRetries 是故障后的额外重试次数上限（实际还受可用档位数约束）。
	MaxRetries int
}

var _ llm.ProviderPort = (*Gateway)(nil)

// New 构造网关（MaxRetries<=0 取 3）。
func New(pool *channels.Pool, reg *adaptors.Registry) *Gateway {
	return &Gateway{Pool: pool, Reg: reg, MaxRetries: 3}
}

// StreamChat 发起流式对话补全（实现 ProviderPort）。
// req.Model 是下游模型名；分组固定 default（多调用方分组是后续管理 API 的扩展点）。
func (g *Gateway) StreamChat(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	out := make(chan llm.StreamChunk)
	go g.forward(ctx, req, out)
	return out, nil
}

// forward 是转发主循环（在独立 goroutine 内执行，恰好一个终态后 close(out)）。
func (g *Gateway) forward(ctx context.Context, req llm.ChatRequest, out chan llm.StreamChunk) {
	// 契约：发出终态块后必须 close——defer 保证所有返回路径收束
	defer close(out)
	forwarded := false
	exclude := []string{}
	tier := 0
	var lastErr error

	for attempt := 0; attempt <= g.MaxRetries; attempt++ {
		sel, err := g.Pool.Select(channels.Selection{
			Group: channels.DefaultGroup, Model: req.Model, Retry: tier, Exclude: exclude,
		})
		if err != nil {
			g.terminal(ctx, out, fmt.Errorf("%v（last: %v）", err, lastErr), &forwarded)
			return
		}
		adv, err := g.Reg.Get(sel.Type)
		if err != nil {
			g.terminal(ctx, out, err, &forwarded)
			return
		}
		rc := adaptors.RouteContext{
			ChannelID: sel.ChannelID, Type: sel.Type, BaseURL: sel.BaseURL,
			Credential: sel.Credential, Extra: sel.Extra,
			HeaderOverride: sel.HeaderOverride, ParamOverride: sel.ParamOverride,
		}
		// model_mapping：下游模型名 → 上游真实模型名；无映射原样传递
		rc.Model = req.Model
		if mapped := sel.ModelMapping[req.Model]; mapped != "" {
			rc.Model = mapped
		}
		if sel.BaseURL == "" {
			base, err := g.Reg.DefaultBaseURL(sel.Type)
			if err != nil {
				g.terminal(ctx, out, err, &forwarded)
				return
			}
			rc.BaseURL = base
		}
		body, err := adv.ConvertRequest(rc, req)
		if err != nil {
			g.terminal(ctx, out, fmt.Errorf("转换请求失败（%s）： %w", sel.Type, err), &forwarded)
			return
		}
		if body, err = adaptors.ApplyParamOverride(body, sel.ParamOverride); err != nil {
			g.terminal(ctx, out, err, &forwarded)
			return
		}
		hdr := http.Header{}
		if err := adv.SetupHeaders(rc, hdr); err != nil {
			g.terminal(ctx, out, fmt.Errorf("构造鉴权头失败（%s）：%w", sel.Type, err), &forwarded)
			return
		}
		adaptors.ApplyHeaderOverride(hdr, sel.HeaderOverride)

		resp, err := adv.DoRequest(ctx, rc, hdr, body)
		if err != nil {
			lastErr = err
			g.onChannelFault(sel) // 网络失败 = 渠道级故障
			exclude = append(exclude, sel.ChannelID)
			tier++
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			msg := adaptors.ReadErrBody(resp)
			serr := fmt.Errorf("渠道 %s 上游 HTTP %d: %s", sel.ChannelID, resp.StatusCode, msg)
			resp.Body.Close()
			switch {
			case isChannelFaultStatus(resp.StatusCode):
				lastErr = serr
				g.onChannelFault(sel)
				exclude = append(exclude, sel.ChannelID)
				tier++
				continue
			case isCredentialFaultStatus(resp.StatusCode) && sel.CredentialIndex >= 0:
				// 凭证级故障：auto_ban 禁当前 Key；同档重试（轮询游标已推进到下一条）
				lastErr = serr
				g.onCredentialFault(sel)
				continue
			default:
				g.terminal(ctx, out, serr, &forwarded)
				return
			}
		}

		st, err := adv.ConvertResponse(ctx, rc, resp)
		if err != nil {
			g.terminal(ctx, out, err, &forwarded)
			return
		}
		idleRetry := false
		for c := range st {
			if c.EndReason != llm.EndNone {
				// 零块已转发时空闲超时：换渠道降档重试；其余终态透传（流已建立不重试）
				if !forwarded && c.EndReason == llm.EndIdleTimeout && attempt < g.MaxRetries {
					idleRetry = true
					lastErr = c.Err
					break
				}
				g.emit(ctx, out, c, &forwarded)
				return
			}
			select {
			case out <- c:
				forwarded = true
			case <-ctx.Done():
				return
			}
		}
		if idleRetry {
			g.onChannelFault(sel)
			exclude = append(exclude, sel.ChannelID)
			tier++
			continue
		}
		return // 终态已随流转发
	}
	g.terminal(ctx, out, fmt.Errorf("渠道重试上限已用尽（%d 次）：%v", g.MaxRetries+1, lastErr), &forwarded)
}

// onChannelFault 渠道级故障的 auto_ban 处置：多凭证只禁当前一条；单凭证禁整个渠道。
func (g *Gateway) onChannelFault(sel channels.Selected) {
	if !sel.AutoBan {
		return
	}
	if sel.CredentialIndex >= 0 {
		_ = g.Pool.BanCredential(sel.ChannelID, sel.CredentialIndex)
		return
	}
	_ = g.Pool.AutoDisable(sel.ChannelID)
}

// onCredentialFault 凭证级故障的 auto_ban 处置：只禁当前这条 Key。
func (g *Gateway) onCredentialFault(sel channels.Selected) {
	if !sel.AutoBan || sel.CredentialIndex < 0 {
		return
	}
	_ = g.Pool.BanCredential(sel.ChannelID, sel.CredentialIndex)
}

// isChannelFaultStatus 报告状态码是否属于渠道级可重试故障（429 / 5xx）。
func isChannelFaultStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		(code >= http.StatusInternalServerError && code <= http.StatusGatewayTimeout)
}

// isCredentialFaultStatus 报告状态码是否属于凭证级故障（401 / 403）。
func isCredentialFaultStatus(code int) bool {
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}

// terminal 投递恰好一个 EndError 终态块并收束（转发开始后不会走到这里）。
func (g *Gateway) terminal(ctx context.Context, out chan llm.StreamChunk, err error, forwarded *bool) {
	if *forwarded {
		return
	}
	g.emit(ctx, out, llm.StreamChunk{EndReason: llm.EndError, Err: err}, forwarded)
}

// emit 转发一个块（带逃生）；终态后由调用方 return 触发 close(out)。
func (g *Gateway) emit(ctx context.Context, out chan llm.StreamChunk, c llm.StreamChunk, forwarded *bool) {
	select {
	case out <- c:
		if c.EndReason != llm.EndNone {
			*forwarded = true
		}
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
	}
}
