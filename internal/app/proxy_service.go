// 全局上游代理用例（0.2.22）：授权端点与推理端点共用同一出口策略。
//
// 背景（0.2.21 实机）：ChatGPT 授权页在浏览器里能登录（浏览器走代理），
// 但应用内换 token 是直连 —— OpenAI 按出口地区返回 403
// unsupported_country_region_territory。代理不是"高级选项"而是可用性前提。
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"tiancode/internal/platform/netproxy"
)

// Proxy 返回全局上游代理配置（空 = 直连）。
func (s *ChatService) Proxy() string { return s.pool.Proxy() }

// SetProxy 设置全局上游代理（http/https）。
// 变更后清空代理客户端缓存：确保后续连接用新代理，而不是复用旧代理的连接池。
func (s *ChatService) SetProxy(proxy string) error {
	if err := s.pool.SetProxy(proxy); err != nil {
		return err
	}
	netproxy.Reset()
	return nil
}

// ProxyInfo 是代理探测结果（出口 IP 与地区，供用户判断节点是否符合上游要求）。
type ProxyInfo struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
}

// CheckProxy 探测代理可用性并返回出口信息。
// 走 chatgpt.com 的 Cloudflare trace（能拿到 ip=/loc= 说明链路通，且直接看到出口地区）；
// 失败原因显式区分"代理连不上"与"上游返回非 2xx"，避免用户把网络问题当配置问题。
func (s *ChatService) CheckProxy(ctx context.Context, proxy string) (ProxyInfo, error) {
	client, err := netproxy.Client(nil, proxy)
	if err != nil {
		return ProxyInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/cdn-cgi/trace", nil)
	if err != nil {
		return ProxyInfo{}, err
	}
	// 与推理一致的客户端标识（部分节点对陌生 UA 也会拦）
	req.Header.Set("User-Agent", "codex-tui/0.146.0")
	resp, err := client.Do(req)
	if err != nil {
		if strings.TrimSpace(proxy) == "" {
			return ProxyInfo{}, fmt.Errorf("直连探测失败：%w（国内网络通常需要配置代理）", err)
		}
		return ProxyInfo{}, fmt.Errorf("代理连接失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if err != nil {
		return ProxyInfo{}, fmt.Errorf("读取探测响应失败：%w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProxyInfo{}, fmt.Errorf("探测请求返回 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	info := ProxyInfo{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "ip="); ok {
			info.IP = v
		}
		if v, ok := strings.CutPrefix(line, "loc="); ok {
			info.Country = v
		}
	}
	if info.IP == "" {
		return ProxyInfo{}, errors.New("探测响应缺少出口 IP（链路可能被中间设备改写）")
	}
	return info, nil
}
