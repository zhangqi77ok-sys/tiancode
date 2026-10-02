// 渠道健康读数绑定（0.0.23）：渠道管理里"近 7 天成功率 / 平均建流耗时"的数据源。
package app

import (
	"tiancode/internal/platform/chanhealth"
)

// ChannelHealthView 是单个渠道的健康读数（JSON 直出，无二次映射）。
type ChannelHealthView = chanhealth.View

// ChannelHealth 返回近 days 天（默认 7，上限 30）的按渠道聚合。
// 故障切换是自动的（降档重试 + auto_ban），这一层让切换行为**可见**。
func (b *Bind) ChannelHealth(days int) (map[string]ChannelHealthView, error) {
	views := chanhealth.Snapshot(days)
	if views == nil {
		views = map[string]ChannelHealthView{}
	}
	return views, nil
}
