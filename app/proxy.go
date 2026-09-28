// 全局上游代理设置的 IPC 绑定。
package app

import (
	"tiancode/internal/app"
)

// GetProxy 返回全局上游代理配置（空 = 直连）。
func (b *Bind) GetProxy() string { return b.chat.Proxy() }

// SetProxy 设置全局上游代理（http/https；空串 = 恢复直连）。
func (b *Bind) SetProxy(proxy string) error { return b.chat.SetProxy(proxy) }

// CheckProxy 探测代理出口（返回出口 IP 与地区），用于确认节点是否符合上游要求。
func (b *Bind) CheckProxy(proxy string) (app.ProxyInfo, error) {
	return b.chat.CheckProxy(b.appCtx(), proxy)
}
