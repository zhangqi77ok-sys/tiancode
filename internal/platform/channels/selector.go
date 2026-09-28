package channels

import (
	"errors"
	"fmt"

	"tiancode/internal/core/llm"
)

// Selection 是一次选路请求。
type Selection struct {
	Group   string   // 调用方分组（空 = default）
	Model   string   // 下游模型名
	Retry   int      // 重试序号：0 首选最高档；n 降 n 档；超出用最低档（重试是降档，不是同档再抽）
	Exclude []string // 已失败必须跳过的渠道
	// ChannelID 指定渠道（如异步任务必须回到原渠道）：优先于随机选择，
	// 但仍须属于该 group+model、status=enabled 且未在 exclude 里。
	ChannelID string
	// Preferred 是软偏好（桌面应用的"激活渠道"）：仅在 retry=0 且该渠道属于当前
	// group+model 且可用时优先选它；失败降档后自然落到池内其他渠道。
	// 与 ChannelID 的区别：ChannelID 是硬指定（不可用即报错），Preferred 不可用就正常分档。
	Preferred string
}

// Selected 是选中结果（写入请求上下文，之后任何层从上下文读取，不再查库）。
type Selected struct {
	ChannelID       string
	Type            string
	BaseURL         string            // 渠道配置值（默认地址由网关按适配器解析）
	Credential      string            // 选出的单条凭证（不透明）
	CredentialIndex int               // 多凭证下标；单凭证 = -1
	ModelMapping    map[string]string // 下游模型名 → 上游真实模型名
	ParamOverride   map[string]any
	HeaderOverride  map[string]string
	Extra           map[string]string
	AutoBan         bool
	Priority        int
	Auth            *llm.AuthConfig // 渠道级鉴权配置（nil = 协议默认）
}

// ErrNoChannel 是明确的无可用渠道错误。
var ErrNoChannel = errors.New("无可用渠道")

// Select 按分组与模型选出一条渠道（spec 选路规则 1~5）。
// 只读 Ability 索引 + 候选集过滤，绝不在热路径上扫描全部渠道或临时计算分组×模型。
func (p *Pool) Select(sel Selection) (Selected, error) {
	if sel.Group == "" {
		sel.Group = DefaultGroup
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	excluded := map[string]bool{}
	for _, id := range sel.Exclude {
		excluded[id] = true
	}

	// 规则 1：只考虑该 group、该 model、enabled，且未被排除、仍有可用凭证的渠道
	type cand struct {
		ch *Channel
	}
	var cands []*Channel
	for _, idx := range p.byGroupModel[sel.Group][sel.Model] {
		ch := p.channels[idx]
		if ch.Status != StatusEnabled || excluded[ch.ID] {
			continue
		}
		keys := splitCredential(ch.Credential)
		if remainingEnabled(keys, ch.CredentialState) == 0 {
			continue // 规则 5：全部凭证不可用 → 这条渠道视为不可用
		}
		cands = append(cands, ch)
	}
	if len(cands) == 0 {
		return Selected{}, fmt.Errorf("%w：%s / %s（retry=%d）", ErrNoChannel, sel.Group, sel.Model, sel.Retry)
	}

	// 规则 4：指定渠道优先于随机（硬指定）；软偏好只在 retry=0 生效，
	// 失败降档（retry>0 或 preferred 已在 exclude）后自然回落到正常分档。
	var chosen *Channel
	switch {
	case sel.ChannelID != "":
		for _, ch := range cands {
			if ch.ID == sel.ChannelID {
				chosen = ch
				break
			}
		}
		if chosen == nil {
			return Selected{}, fmt.Errorf("%w：指定渠道 %s 不可用于 %s / %s（或已禁用/失败）", ErrNoChannel, sel.ChannelID, sel.Group, sel.Model)
		}
	case sel.Preferred != "" && sel.Retry == 0:
		for _, ch := range cands {
			if ch.ID == sel.Preferred {
				chosen = ch
				break
			}
		}
	}
	if chosen == nil {
		chosen = p.pickByTier(cands, sel.Retry)
		if chosen == nil {
			return Selected{}, fmt.Errorf("%w：%s / %s（retry=%d）", ErrNoChannel, sel.Group, sel.Model, sel.Retry)
		}
	}
	return p.pickCredential(chosen), nil
}

// pickByTier 实现规则 2+3：按 priority 降序分档，retry=n 落第 n 档（超出用最低档），
// 同档内按 weight 加权随机（0 视为默认权重）。
func (p *Pool) pickByTier(cands []*Channel, retry int) *Channel {
	tiers := map[int][]*Channel{}
	prios := []int{}
	for _, c := range cands {
		if _, ok := tiers[c.Priority]; !ok {
			prios = append(prios, c.Priority)
		}
		tiers[c.Priority] = append(tiers[c.Priority], c)
	}
	sortDesc(prios)
	if len(prios) == 0 {
		return nil
	}
	tier := retry
	if tier < 0 {
		tier = 0
	}
	if tier >= len(prios) {
		tier = len(prios) - 1 // 降档到底：超出重试上限后固定在最低档
	}
	pool := tiers[prios[tier]]
	total := 0
	weights := make([]int, len(pool))
	for i, c := range pool {
		w := c.Weight
		if w <= 0 {
			w = DefaultWeight
		}
		weights[i] = w
		total += w
	}
	pick := p.rand(total)
	for i, w := range weights {
		if pick < w {
			return pool[i]
		}
		pick -= w
	}
	return pool[len(pool)-1]
}

// pickCredential 规则 5：单凭证原样返回（下标 -1）；多凭证按轮询取一条未禁用的，
// 轮询下标随选取推进并持久化（credential_state）。
func (p *Pool) pickCredential(ch *Channel) Selected {
	sel := Selected{
		ChannelID:       ch.ID,
		Type:            ch.Type,
		BaseURL:         ch.BaseURL,
		CredentialIndex: -1,
		ModelMapping:    ch.ModelMapping,
		ParamOverride:   ch.ParamOverride,
		HeaderOverride:  ch.HeaderOverride,
		Extra:           ch.Extra,
		AutoBan:         ch.AutoBan,
		Priority:        ch.Priority,
		Auth:            ch.Auth,
	}
	keys := splitCredential(ch.Credential)
	if len(keys) == 1 {
		sel.Credential = keys[0]
		return sel
	}
	enabled := []int{}
	for i := range keys {
		if ch.CredentialEnabled(i) {
			enabled = append(enabled, i)
		}
	}
	if len(enabled) == 0 {
		sel.Credential = keys[0] // 不会发生：候选过滤已剔除；兜底返回原值
		return sel
	}
	if ch.CredentialState == nil {
		ch.CredentialState = &CredentialState{Disabled: make([]bool, len(keys))}
	}
	start := ch.CredentialState.Next % len(keys)
	pick := enabled[0]
	for k := 0; k < len(keys); k++ {
		i := (start + k) % len(keys)
		if ch.CredentialEnabled(i) {
			pick = i
			break
		}
	}
	ch.CredentialState.Next = (pick + 1) % len(keys)
	sel.Credential = keys[pick]
	sel.CredentialIndex = pick
	return sel
}

func sortDesc(list []int) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] > list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
