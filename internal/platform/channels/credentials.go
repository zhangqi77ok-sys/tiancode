package channels

import (
	"fmt"
	"strings"
)

// CredentialInfo 是单条凭证的管理视图（脱敏：绝不外发明文）。
// 参照 new-api 多 key 管理：每条 key 可单独查看状态、禁用、恢复、删除。
type CredentialInfo struct {
	Index   int    `json:"index"`
	Preview string `json:"preview"` // 前 6 后 4 脱敏（如 sk-abc…wxyz）
	Enabled bool   `json:"enabled"`
}

// Credentials 返回渠道的凭证管理视图（脱敏预览 + 启用态）。
// 单凭证渠道也返回一条（UI 得以显示凭证存在与否）。
func (p *Pool) Credentials(id string) ([]CredentialInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID != id {
			continue
		}
		keys := splitCredential(c.Credential)
		out := make([]CredentialInfo, 0, len(keys))
		for i, k := range keys {
			out = append(out, CredentialInfo{
				Index:   i,
				Preview: MaskCredential(k),
				Enabled: c.CredentialEnabled(i),
			})
		}
		return out, nil
	}
	return nil, fmt.Errorf("渠道不存在：%s", id)
}

// SetCredentialEnabled 启用/禁用指定凭证（禁用用于手动摘除坏 Key；
// 启用是自动禁用后的唯一恢复途径——0.2.19 前 BanCredential 只禁不解，坏 Key 永久禁用）。
// 渠道状态联动：全部凭证不可用 → auto_disabled；重新有可用凭证 → 从 auto_disabled 恢复。
// 手动停用的渠道（manually_disabled）不因凭证操作被动切换状态（尊重用户显式意图）。
func (p *Pool) SetCredentialEnabled(id string, idx int, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID != id {
			continue
		}
		keys := splitCredential(c.Credential)
		if idx < 0 || idx >= len(keys) {
			return fmt.Errorf("凭证下标越界：%d（共 %d 条）", idx, len(keys))
		}
		ensureStateLocked(c, len(keys))
		c.CredentialState.Disabled[idx] = !enabled
		p.syncStatusByCredentialsLocked(c, keys)
		p.rebuildAbilityLocked()
		return p.persistLocked()
	}
	return fmt.Errorf("渠道不存在：%s", id)
}

// Probe 构造"测试用"的选中结果：跳过渠道状态过滤（停用渠道也允许测试），
// 取第一条启用凭证，且**不推进轮询游标**（测试是诊断行为，不得扰动生产选路节奏）。
func (p *Pool) Probe(id string) (Selected, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID != id {
			continue
		}
		keys := splitCredential(c.Credential)
		sel := Selected{
			ChannelID:       c.ID,
			Type:            c.Type,
			BaseURL:         c.BaseURL,
			CredentialIndex: -1,
			ModelMapping:    c.ModelMapping,
			ParamOverride:   c.ParamOverride,
			HeaderOverride:  c.HeaderOverride,
			Extra:           c.Extra,
			AutoBan:         c.AutoBan,
			Priority:        c.Priority,
		}
		if len(keys) == 1 {
			sel.Credential = keys[0]
			return sel, nil
		}
		for i := range keys {
			if c.CredentialEnabled(i) {
				sel.Credential = keys[i]
				sel.CredentialIndex = i
				return sel, nil
			}
		}
		// 全禁用：仍用首条测试（失败结果会让用户明白为什么没有可用凭证）
		sel.Credential = keys[0]
		return sel, nil
	}
	return Selected{}, fmt.Errorf("渠道不存在：%s", id)
}

// ensureStateLocked 保证凭证状态表长度与凭证数一致（增长时保留既有禁用标记与轮询游标）。
func ensureStateLocked(c *Channel, n int) {
	if c.CredentialState != nil && len(c.CredentialState.Disabled) >= n {
		return
	}
	grown := make([]bool, n)
	next := 0
	if c.CredentialState != nil {
		copy(grown, c.CredentialState.Disabled)
		next = c.CredentialState.Next
	}
	c.CredentialState = &CredentialState{Disabled: grown, Next: next}
}

// syncStatusByCredentialsLocked 按"剩余可用凭证数"联动渠道状态：
// 全不可用 → auto_disabled；有可用凭证且当前是 auto_disabled → 恢复 enabled。
// manually_disabled 是用户显式意图，两种方向都不动它。
func (p *Pool) syncStatusByCredentialsLocked(c *Channel, keys []string) {
	if c.Status == StatusManuallyDisabled {
		return
	}
	if remainingEnabled(keys, c.CredentialState) == 0 {
		c.Status = StatusAutoDisabled
	} else if c.Status == StatusAutoDisabled {
		c.Status = StatusEnabled
	}
}

// MaskCredential 凭证脱敏预览（前 6 后 4；短凭证全遮）。
// 为什么导出：app 层组装凭证视图时复用同一脱敏纪律（两处实现必然漂移）。
func MaskCredential(k string) string {
	if k == "" {
		return "（空）"
	}
	runes := []rune(k)
	if len(runes) <= 10 {
		return strings.Repeat("•", len(runes))
	}
	return string(runes[:6]) + "…" + string(runes[len(runes)-4:])
}
