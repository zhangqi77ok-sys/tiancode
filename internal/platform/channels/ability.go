package channels

import "sort"

// rebuildAbilityLocked 全量重建 Ability 索引（调用方持锁）。
// 为什么全量而非增量：渠道数个位数，全量重建 O(渠道×组×模型) 成本可忽略，
// 却消灭了"增量路径漏一种变更"这类隐蔽 bug；RebuildAbility 即修复操作。
// 禁用渠道同步禁用其全部 Ability 行（等价于不入索引）。
func (p *Pool) rebuildAbilityLocked() {
	idx := map[string]map[string][]int{}
	for i, c := range p.channels {
		if c.Status != StatusEnabled {
			continue
		}
		for _, g := range c.Groups {
			if g == "" {
				continue
			}
			gm := idx[g]
			if gm == nil {
				gm = map[string][]int{}
				idx[g] = gm
			}
			for _, m := range c.Models {
				if m == "" {
					continue
				}
				gm[m] = append(gm[m], i)
			}
		}
	}
	for _, gm := range idx {
		for m, list := range gm {
			sort.SliceStable(list, func(a, b int) bool {
				return p.channels[list[a]].Priority > p.channels[list[b]].Priority
			})
			gm[m] = list
		}
	}
	p.byGroupModel = idx
}

// RebuildAbility 全量重刷 Ability 索引（修复操作；正常变更路径已自动重建）。
func (p *Pool) RebuildAbility() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rebuildAbilityLocked()
}
