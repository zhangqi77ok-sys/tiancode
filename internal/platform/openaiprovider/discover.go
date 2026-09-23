package openaiprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// discoverTimeout 是模型列表拉取超时（列表接口应秒回，30s 防御病态网关）。
const discoverTimeout = 30 * time.Second

// discoverLimit 是响应体上限（防上游返回超大 JSON 打爆内存）。
const discoverLimit = 1 << 20 // 1MB

// DiscoverModels 拉取上游可用模型列表（实现 llm.ModelDiscoverer）。
// 失败返回错误且**不改动任何本地状态**（调用方据此保证"发现失败不清空已保存配置"）。
func (p *Provider) DiscoverModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(p.opts.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if p.opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.opts.APIKey)
	}

	resp, err := p.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("上游返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, discoverLimit)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	ids := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	// 排序：上游顺序不稳定，稳定顺序才能让 UI 选择不抖动
	sort.Strings(ids)
	return ids, nil
}
