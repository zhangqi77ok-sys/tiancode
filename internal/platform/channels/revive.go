package channels

// ReviveAutoDisabled 把"自动禁用"的渠道恢复为启用，返回被恢复的渠道展示名。
//
// 为什么需要（实机事故）：auto_disabled 是**运行期**健康标记——网络中断、上游 429/5xx、
// 空闲超时都会置位（见 gateway.onChannelFault）。它却被持久化进 channels.json，
// 于是单次瞬时故障会让应用**永久**没有可用渠道：后续每次发送都只得到"无可用渠道"，
// 用户看到的是"发消息毫无反应、没法进行对话"，且没有任何自动恢复途径。
// 进程重启是天然的重新评估点：给每条被自动禁用的渠道一次机会；若上游确实还坏着，
// 第一次请求会立刻再次自动禁用，而本轮错误是可见的（前端错误终态必须落到气泡上）。
//
// 边界（尊重用户显式意图）：
//   - manually_disabled 绝不动（那是用户点出来的）；
//   - 凭证级禁用标记也绝不动（用户可手动摘除坏 Key），因此本函数只翻渠道状态；
//     若渠道是"全部凭证被禁用"才变成的 auto_disabled，翻回启用后选路仍会因
//     无可用凭证而跳过它——此时错误文本会引导用户去凭证管理里启用。
func (p *Pool) ReviveAutoDisabled() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var revived []string
	for _, c := range p.channels {
		if c.Status != StatusAutoDisabled {
			continue
		}
		c.Status = StatusEnabled
		revived = append(revived, c.DisplayName())
	}
	if len(revived) == 0 {
		return nil, nil
	}
	p.rebuildAbilityLocked()
	if err := p.persistLocked(); err != nil {
		// 恢复动作自身的失败必须上抛：内存里已经启用、磁盘上还是禁用，
		// 下次启动的观感会与本次不一致（不静默）。
		return revived, err
	}
	return revived, nil
}
