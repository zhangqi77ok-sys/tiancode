package channels

import "fmt"

// AutoDisable 把渠道标为上游故障自动禁用（单凭证渠道的 auto_ban 路径；
// 多凭证渠道走 BanCredential，全部凭证失败时由其转 auto_disabled）。
func (p *Pool) AutoDisable(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID == id {
			c.Status = StatusAutoDisabled
			p.rebuildAbilityLocked()
			return p.persistLocked()
		}
	}
	return fmt.Errorf("渠道不存在：%s", id)
}
