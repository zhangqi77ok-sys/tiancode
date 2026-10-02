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
// **StreamChat 永远不返回 error**（0.0.23 审计澄清）：所有失败都以终态块
// （EndError/EndCancelled）写进返回的通道。这条纪律是 runtime 层建流重试
// （MaxAttempts）与本层渠道重试**不叠加**的唯一保证——runtime 只在 provider
// 返回 error 时才重试。改成返回 error 会让单次 Send 的上游请求数翻倍。
//
// 编排层对协议零感知：类型分派只经 adaptors.Registry。
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/adaptors"
	"tiancode/internal/platform/applog"
	"tiancode/internal/platform/channels"
)

// hostOf 从 BaseURL 提取主机名（日志不记完整 URL：query 可能带敏感参数）。
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(invalid)"
	}
	return u.Host
}

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
		if ctx.Err() != nil {
			g.finishStopped(out, ctx.Err(), &forwarded)
			return
		}
		sel, err := g.Pool.Select(channels.Selection{
			Group: channels.DefaultGroup, Model: req.Model, Retry: tier, Exclude: exclude,
			// 激活渠道是软偏好：首选走它，故障后降档自动落到池内其他渠道
			Preferred: g.Pool.ActiveID(),
		})
		if err != nil {
			if lastErr != nil {
				// 已试过的渠道都失败了：真实原因在上游错误里，选路错误只作补充
				g.terminal(ctx, out, fmt.Errorf("%v（last: %v）", err, lastErr), &forwarded)
				return
			}
			// 一轮都没打出去：原因只能由池的状态解释（未配置/被禁用/不含该模型）
			g.terminal(ctx, out, g.noChannelError(req.Model, err), &forwarded)
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
			Auth:  sel.Auth,       // 渠道级鉴权配置（nil = 协议默认）
			Proxy: g.Pool.Proxy(), // 全局上游代理（空 = 直连）
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
		adaptors.ApplyHeaderOverride(hdr, sel.HeaderOverride, sel.Credential)

		// 上游请求埋点（0.0.09）：日志诊断链 = turn start → 本行 → 建流/超时/终态。
		// 只记 host/模型/协议/字节量——绝不记凭证与消息全文（见 applog 包注释）。
		applog.Infof("upstream request host=%s model=%s protocol=%s bytes=%d",
			hostOf(rc.BaseURL), rc.Model, sel.Type, len(body))
		reqStart := time.Now()
		resp, err := adv.DoRequest(ctx, rc, hdr, body)
		if err != nil {
			// 用户中断不是渠道故障：不禁用、不换渠道重试，否则点了中断请求还会再发出去。
			if ctx.Err() != nil {
				g.finishStopped(out, ctx.Err(), &forwarded)
				return
			}
			applog.Errorf("upstream connect failed host=%s elapsed=%s err=%v",
				hostOf(rc.BaseURL), time.Since(reqStart).Round(time.Millisecond), err)
			lastErr = mergeBanErr(err, g.onChannelFault(sel)) // 网络失败 = 渠道级故障
			exclude = append(exclude, sel.ChannelID)
			tier++
			// 建流前故障的短退避（0.0.11）：连接失败/429/5xx 立刻换渠道再打，
			// 对端正在限流或重启时连打只会把窗口拖长
			if !g.sleepBackoff(ctx, out, &forwarded, attempt) {
				return
			}
			continue
		}
		applog.Infof("upstream connected host=%s status=%d elapsed=%s（建流完成，流层空闲看门狗开始计时）",
			hostOf(rc.BaseURL), resp.StatusCode, time.Since(reqStart).Round(time.Millisecond))
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			msg := adaptors.ReadErrBody(resp)
			serr := fmt.Errorf("渠道 %s 上游 HTTP %d: %s", sel.ChannelID, resp.StatusCode, msg)
			resp.Body.Close()
			switch {
			case isChannelFaultStatus(resp.StatusCode):
				lastErr = mergeBanErr(serr, g.onChannelFault(sel))
				exclude = append(exclude, sel.ChannelID)
				tier++
				// 建流前故障的短退避（0.0.11）：429/5xx 立刻重打只会加重限流
				if !g.sleepBackoff(ctx, out, &forwarded, attempt) {
					return
				}
				continue
			case isCredentialFaultStatus(resp.StatusCode) && sel.CredentialIndex >= 0:
				// 凭证级故障：auto_ban 禁当前 Key；同档重试（轮询游标已推进到下一条）
				lastErr = mergeBanErr(serr, g.onCredentialFault(sel))
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
				g.finishStopped(out, ctx.Err(), &forwarded)
				return
			}
		}
		if idleRetry {
			lastErr = mergeBanErr(lastErr, g.onChannelFault(sel))
			exclude = append(exclude, sel.ChannelID)
			tier++
			// 零块空闲超时同样属"流未建立"（0.0.11）：换渠道前同样退避
			if !g.sleepBackoff(ctx, out, &forwarded, attempt) {
				return
			}
			continue
		}
		if !forwarded {
			// 零块且流已结束却没给出终态（0.0.11 修既有竞态）：用户中断恰逢上游静默
			// 关流时，此前会"无终态 close"——消费方只看到通道关闭，agent 的兜底会把
			// 主动中断渲染成错误（CI 的取消用例曾在并发压力下偶发变红）。按成因如实补发。
			if ctx.Err() != nil {
				g.finishStopped(out, ctx.Err(), &forwarded)
				return
			}
			g.terminal(ctx, out, fmt.Errorf("上游在返回任何内容前结束了流"), &forwarded)
			return
		}
		return // 终态已随流转发
	}
	g.terminal(ctx, out, fmt.Errorf("渠道重试上限已用尽（%d 次）：%v", g.MaxRetries+1, lastErr), &forwarded)
}

// onChannelFault 渠道级故障的 auto_ban 处置：多凭证只禁当前一条；单凭证禁整个渠道。
// 返回值是禁用动作自身的失败（如持久化失败）——由调用方并入终态错误文本，
// 绝不静默丢弃（禁用失败意味着下次请求可能仍打到坏渠道，用户必须能看见）。
func (g *Gateway) onChannelFault(sel channels.Selected) error {
	if !sel.AutoBan {
		return nil
	}
	if sel.CredentialIndex >= 0 {
		return g.Pool.BanCredential(sel.ChannelID, sel.CredentialIndex)
	}
	return g.Pool.AutoDisable(sel.ChannelID)
}

// onCredentialFault 凭证级故障的 auto_ban 处置：只禁当前这条 Key（失败语义同上）。
func (g *Gateway) onCredentialFault(sel channels.Selected) error {
	if !sel.AutoBan || sel.CredentialIndex < 0 {
		return nil
	}
	return g.Pool.BanCredential(sel.ChannelID, sel.CredentialIndex)
}

// mergeBanErr 把 auto_ban 自身的失败并入 lastErr，随终态错误可见。
func mergeBanErr(lastErr, banErr error) error {
	if banErr == nil {
		return lastErr
	}
	return fmt.Errorf("%v; auto_ban: %v", lastErr, banErr)
}

// sleepBackoff 在两次渠道级重试之间等待 llm.BackoffFor(attempt)（0.0.11）。
// 只被"流未建立"的失败路径调用（连接失败 / 429 / 5xx / 零块空闲超时）——
// 流中途失败一律透传，绝不重放（见包注释的重试语义）。
// 等待期间被用户取消走 finishStopped（那是取消，不是渠道故障）；返回 false
// 表示调用方应立刻 return，不再进入下一轮尝试。
func (g *Gateway) sleepBackoff(ctx context.Context, out chan llm.StreamChunk, forwarded *bool, attempt int) bool {
	d := llm.BackoffFor(attempt)
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		g.finishStopped(out, ctx.Err(), forwarded)
		return false
	}
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

// noChannelError 把"无可用渠道"翻译成用户能照着做的说明。
//
// 为什么必须翻译：这条错误会直接渲染在对话气泡里。原始文本
// "无可用渠道：default / grok-4.7（retry=0）" 对用户零信息量——他既不知道是自己没配、
// 渠道被系统禁用了，还是模型名填错了，于是整个应用表现为"发消息没反应"（实机反馈）。
// 选路失败的原因只有池知道，所以翻译放在这里（协议无关，不涉及任何适配器细节）。
func (g *Gateway) noChannelError(model string, err error) error {
	if !errors.Is(err, channels.ErrNoChannel) {
		return err
	}
	all := g.Pool.List()
	if len(all) == 0 {
		return fmt.Errorf("尚未配置任何模型渠道：请在「渠道管理」中新增渠道并设为激活，再发送消息")
	}
	reasons := make([]string, 0, len(all))
	for _, c := range all {
		switch {
		case c.Status == channels.StatusAutoDisabled:
			reasons = append(reasons, fmt.Sprintf(
				"渠道「%s」已被自动禁用（上游故障或网络失败触发）；在「渠道管理」点「编辑」把状态改回「启用」，或用「测试」先确认上游是否恢复",
				c.DisplayName()))
		case c.Status == channels.StatusManuallyDisabled:
			reasons = append(reasons, fmt.Sprintf(
				"渠道「%s」处于手动停用状态；在「渠道管理」里改为「启用」",
				c.DisplayName()))
		case !hasModel(c, model):
			reasons = append(reasons, fmt.Sprintf(
				"渠道「%s」不包含模型「%s」（该渠道现有模型：%s）",
				c.DisplayName(), model, strings.Join(c.Models, "、")))
		default:
			reasons = append(reasons, fmt.Sprintf(
				"渠道「%s」当前不可用（状态 %s；若为多凭证渠道，请检查凭证是否全部被禁用）",
				c.DisplayName(), channels.StatusLabel(c.Status)))
		}
	}
	return fmt.Errorf("没有可用于模型「%s」的渠道：%s", model, strings.Join(reasons, "；"))
}

// hasModel 报告渠道是否声明了该模型（空模型名视为"任意"，避免误报不含）。
func hasModel(c channels.Channel, model string) bool {
	if model == "" {
		return true
	}
	for _, m := range c.Models {
		if m == model {
			return true
		}
	}
	return false
}

// finishStopped 在调用方已取消时收束。不走 emit：emit 同时监听 ctx.Done，
// ctx 已经结束时终态块会被丢掉，前端就收不到“已取消”。
func (g *Gateway) finishStopped(out chan llm.StreamChunk, err error, forwarded *bool) {
	reason := llm.EndError
	if errors.Is(err, context.Canceled) {
		reason = llm.EndCancelled
	}
	select {
	case out <- llm.StreamChunk{EndReason: reason, Err: err}:
		*forwarded = true
	case <-time.After(500 * time.Millisecond):
	}
}

// terminal 投递恰好一个 EndError 终态块并收束（转发开始后不会走到这里）。
func (g *Gateway) terminal(ctx context.Context, out chan llm.StreamChunk, err error, forwarded *bool) {
	if *forwarded {
		return
	}
	g.emit(ctx, out, llm.StreamChunk{EndReason: llm.EndError, Err: err}, forwarded)
}

// emit 转发一个块（带逃生）；终态后由调用方 return 触发 close(out)。
//
// 终态块绝不能走 ctx.Done 逃生：select 在多个就绪分支里随机挑一个，
// ctx 已取消时终态会被随机丢弃——消费方只看到通道关闭、没有任何终态
// （EndReason=0，违反"恰好一个终态"契约，CI 的取消测试当场抓到）。
// 与 finishStopped 同一纪律：终态限时阻塞投递，宁可多等 500ms；
// 消费方真的离开了，上层的 synthetic 终态兜底（C-APP-2）仍会收束 UI。
//
// 非终态块（0.2.27 修复）：只保留"投递 / 取消"两条路径，绝不限时丢弃——
// agent 逐块 write-ahead 落账，丢一块就是账本永久缺字且无任何提示。
// 消费方慢（如落盘被 AV 扫描拖住）只应表现为反压，不应丢数据。
func (g *Gateway) emit(ctx context.Context, out chan llm.StreamChunk, c llm.StreamChunk, forwarded *bool) {
	if c.EndReason != llm.EndNone {
		select {
		case out <- c:
			*forwarded = true
		case <-time.After(500 * time.Millisecond):
		}
		return
	}
	select {
	case out <- c:
		*forwarded = true
	case <-ctx.Done():
	}
}
