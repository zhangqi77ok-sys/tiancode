package channels

// MinContextLimit 返回已启用渠道中已声明上下文上限（token）的最小值；全部未声明返回 0。
// 为什么取最小：运行时选路可能落到任一条启用渠道，按最小上限裁剪上下文才能保证
// 不被任何一条拒绝；未声明的渠道不参与（未配置 ≠ 上限为零），避免"一条没填"
// 把所有渠道的治理全部关掉。
func (p *Pool) MinContextLimit() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	min := 0
	for _, c := range p.channels {
		if c.Status != StatusEnabled || c.ContextLimit <= 0 {
			continue
		}
		if min == 0 || c.ContextLimit < min {
			min = c.ContextLimit
		}
	}
	return min
}
