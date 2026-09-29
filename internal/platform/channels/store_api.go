package channels

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Save 新增或更新渠道并重建其 Ability 行（upsert：ID 相同即更新）。
// 默认值：status 空 = enabled；weight<=0 存 0（选择时视为 DefaultWeight）。
// 凭证状态不随 Save 丢失：管理表单不携带它。
func (p *Pool) Save(ch Channel) error {
	if ch.ID == "" {
		return errors.New("渠道 ID 不能为空")
	}
	if ch.Status == "" {
		ch.Status = StatusEnabled
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	found := false
	for i, c := range p.channels {
		if c.ID == ch.ID {
			ch.CredentialState = c.CredentialState
			p.channels[i] = &ch
			found = true
			break
		}
	}
	if !found {
		p.channels = append(p.channels, &ch)
	}
	p.rebuildAbilityLocked()
	return p.persistLocked()
}

// Delete 删除渠道并重建索引。
func (p *Pool) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := make([]*Channel, 0, len(p.channels))
	found := false
	for _, c := range p.channels {
		if c.ID == id {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return fmt.Errorf("渠道不存在：%s", id)
	}
	p.channels = kept
	if p.activeID == id {
		p.activeID = ""
	}
	p.rebuildAbilityLocked()
	return p.persistLocked()
}

// UpdateCredential 只更新渠道凭证（OAuth 自动续期用）：不动其他字段与凭证状态表。
// 为什么单独一个方法：Save 是全量 upsert（调用方要组装整个渠道），续期只换一个字符串。
func (p *Pool) UpdateCredential(id, credential string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID == id {
			c.Credential = credential
			return p.persistLocked()
		}
	}
	return fmt.Errorf("渠道不存在：%s", id)
}

// Get 取渠道（含凭证等敏感字段；仅网关与编排层内部使用，对外视图必须脱敏）。
func (p *Pool) Get(id string) (Channel, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID == id {
			return *c, true
		}
	}
	return Channel{}, false
}

// List 返回全部渠道（含敏感字段；对外视图必须脱敏）。
func (p *Pool) List() []Channel {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Channel, 0, len(p.channels))
	for _, c := range p.channels {
		out = append(out, *c)
	}
	return out
}

// ActiveID 返回激活渠道 ID。
func (p *Pool) ActiveID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activeID
}

// SetActive 设置激活渠道。
func (p *Pool) SetActive(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID == id {
			p.activeID = id
			return p.persistLocked()
		}
	}
	return fmt.Errorf("渠道不存在：%s", id)
}

// DefaultModel 返回激活渠道的首个模型（当前 UI 无模型选择器，先以渠道主模型为请求模型）。
func (p *Pool) DefaultModel() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID == p.activeID && len(c.Models) > 0 {
			return c.Models[0], true
		}
	}
	return "", false
}

// ApprovalTools 返回审批策略。
func (p *Pool) ApprovalTools() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.approvalTools))
	copy(out, p.approvalTools)
	return out
}

// SetApprovalTools 保存审批策略。
// nil 规范化为空切片：persistLocked 落盘必须是 "approvalTools": []（显式关闭的
// 独立形态），null 虽语义等价但磁盘形态要唯一。
func (p *Pool) SetApprovalTools(tools []string) error {
	if tools == nil {
		tools = []string{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.approvalTools = tools
	return p.persistLocked()
}

// Proxy 返回全局上游代理（空 = 直连）。
func (p *Pool) Proxy() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.proxy
}

// SetProxy 保存全局上游代理。只接受 http/https 代理（socks5 需要额外依赖，未支持——
// 显式拒绝优于"静默按直连处理"：静默会让地区封锁类错误极难排查）。
func (p *Pool) SetProxy(proxy string) error {
	proxy = strings.TrimSpace(proxy)
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Host == "" {
			return fmt.Errorf("代理地址无效：%q（示例 http://127.0.0.1:7897）", proxy)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
		default:
			return fmt.Errorf("不支持的代理协议 %q（当前仅支持 http/https 代理；Clash 的 mixed/HTTP 端口均可）", u.Scheme)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxy = proxy
	return p.persistLocked()
}

// splitCredential 按行拆分多凭证（换行分隔约定）：去空行与首尾空白。
// 空凭证返回一个空串元素（单凭证形态——部分本地网关无需密钥）。
func splitCredential(cred string) []string {
	out := []string{}
	for _, part := range strings.Split(cred, "\n") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// BanCredential 禁用指定下标的凭证；全部凭证不可用时渠道转 auto_disabled
// （spec auto_ban 语义：多凭证只禁当前一条，全部失败才禁渠道）。
// 恢复途径：SetCredentialEnabled（凭证管理 UI 的"启用"）。
func (p *Pool) BanCredential(id string, idx int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID != id {
			continue
		}
		keys := splitCredential(c.Credential)
		ensureStateLocked(c, len(keys))
		if idx >= 0 && idx < len(c.CredentialState.Disabled) {
			c.CredentialState.Disabled[idx] = true
		}
		p.syncStatusByCredentialsLocked(c, keys)
		p.rebuildAbilityLocked()
		return p.persistLocked()
	}
	return fmt.Errorf("渠道不存在：%s", id)
}

// remainingEnabled 统计仍启用的凭证数。
func remainingEnabled(keys []string, st *CredentialState) int {
	n := 0
	for i := range keys {
		if st == nil || i >= len(st.Disabled) || !st.Disabled[i] {
			n++
		}
	}
	return n
}
