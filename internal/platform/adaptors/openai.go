package adaptors

import (
	"context"
	"net/http"
	"strings"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/netproxy"
	"tiancode/internal/platform/openaiprovider"
)

// OpenAI 是 OpenAI 兼容协议适配器（/chat/completions + SSE）。
// 鉴权：Bearer + credential；兼容上游（DeepSeek/通义/Kimi/Ollama 等）不写新转换，
// 直接复用本适配器，只配置 base_url 与凭证。
type OpenAI struct {
	// HTTPClient 可注入（测试用 httptest）；nil 用默认客户端。
	HTTPClient *http.Client
	// IdleTimeout 空闲看门狗阈值；<=0 取引擎默认 60s。
	IdleTimeout time.Duration
}

var _ Adaptor = (*OpenAI)(nil)

func (OpenAI) Type() string           { return "openai" }
func (OpenAI) DefaultBaseURL() string { return "https://api.openai.com/v1" }

func (OpenAI) GetRequestURL(rc RouteContext) string {
	return WithAuthQuery(strings.TrimSuffix(rc.BaseURL, "/")+"/chat/completions", rc)
}

// SetupHeaders 设置内容头与鉴权：鉴权形态由渠道配置决定（缺省 Bearer 令牌，
// 可配 api-key 头 / 无前缀 Authorization / URL 参数 / 无鉴权）；空凭证合法
// （部分本地网关如 Ollama 不需要）。
func (OpenAI) SetupHeaders(rc RouteContext, hdr http.Header) error {
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Accept", "text/event-stream")
	return ApplyAuth(rc, hdr, AuthDefaultBearer)
}

func (a OpenAI) ConvertRequest(rc RouteContext, req llm.ChatRequest) ([]byte, error) {
	req.Model = rc.Model // 上游真实模型名（mapping 已由网关应用）
	return openaiprovider.MarshalChatRequest(req)
}

func (a OpenAI) DoRequest(ctx context.Context, rc RouteContext, hdr http.Header, body []byte) (*http.Response, error) {
	client, err := netproxy.Client(a.HTTPClient, rc.Proxy)
	if err != nil {
		return nil, err
	}
	return DoJSON(ctx, client, a.GetRequestURL(rc), hdr, body)
}

// ConvertResponse 复用 openaiprovider 的流引擎（空闲看门狗/发送逃生/恰好一个终态）。
func (a OpenAI) ConvertResponse(ctx context.Context, rc RouteContext, resp *http.Response) (<-chan llm.StreamChunk, error) {
	idle := a.IdleTimeout
	if idle <= 0 {
		idle = IdleTimeout
	}
	p := openaiprovider.New(openaiprovider.Options{BaseURL: rc.BaseURL, IdleTimeout: idle})
	return p.ParseSSE(ctx, resp.Body), nil
}
