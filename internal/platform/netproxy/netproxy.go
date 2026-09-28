// Package netproxy 按配置构造上游 HTTP 客户端（代理支持）。
//
// 为什么单独成包：适配器层（adaptors）与 OAuth 授权层（codexauth）都要用它，
// 而 adaptors 依赖 codexauth（解析凭证）——放在任一层都会成环；本包只依赖 stdlib。
//
// 为什么构造失败必须显式报错：把无效代理"静默按直连处理"，会让用户得到
// "配了代理仍被地区封锁"这类极难排查的错误（0.2.21 实机：OpenAI 返回
// unsupported_country_region_territory，而请求其实根本没走代理）。
package netproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// clients 按代理 URL 缓存 HTTP 客户端（同代理复用连接池；代理数量少，不淘汰）。
var clients sync.Map // proxyURL → *http.Client

// Client 返回按代理配置的 HTTP 客户端（空代理 = 直连，返回 base 或默认客户端）。
//
// 注意：不设 http.Client.Timeout（流式响应不能用整体超时）；
// 各阶段超时由 Transport 参数控制。仅支持 http/https 代理（socks5 需额外依赖）。
func Client(base *http.Client, proxy string) (*http.Client, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		if base != nil {
			return base, nil
		}
		return http.DefaultClient, nil
	}
	if v, ok := clients.Load(proxy); ok {
		c, _ := v.(*http.Client)
		return c, nil
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("代理地址无效：%q（示例 http://127.0.0.1:7897）", proxy)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("不支持的代理协议 %q（当前仅支持 http/https 代理；Clash 的 mixed/HTTP 端口均可）", u.Scheme)
	}
	tr := &http.Transport{
		Proxy: http.ProxyURL(u),
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	c := &http.Client{Transport: tr}
	clients.Store(proxy, c)
	return c, nil
}

// Reset 清空客户端缓存（代理配置变更后调用：确保后续连接用新的代理设置，
// 而不是复用旧代理的连接池）。
func Reset() {
	clients.Range(func(k, _ any) bool {
		clients.Delete(k)
		return true
	})
}
