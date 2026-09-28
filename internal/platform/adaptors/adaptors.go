// Package adaptors 是多协议模型渠道的协议适配器层。
//
// 做什么：维护 type → Adaptor 注册表；每个适配器把"统一入站请求（OpenAI Chat 形态）"
// 转成该协议的上游请求（鉴权、URL、报文），再把上游响应（含 SSE 流）转回流式块。
// 厂商差异只允许出现在适配器里——选路（channels.Select）、Ability 索引与重试循环
// （gateway）对协议零感知。
// 被谁依赖：internal/platform/gateway（编排转发）。
// 依赖谁：core/llm（端口）、platform/openaiprovider（OpenAI 流引擎复用）。
//
// 凭证纪律：RouteContext.Credential 是不透明字符串，只有适配器的 SetupHeaders
// 解释它（Bearer / x-api-key / OAuth JSON / AK|SK 复合……）；选路与持久化层不解析。
package adaptors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"tiancode/internal/core/llm"
)

// RouteContext 是"选中渠道"的上下文快照（选路层产出，适配器只读）。
// 与 spec 的上下文字段一一对应；Model 已应用 model_mapping（上游真实模型名）。
type RouteContext struct {
	ChannelID      string
	Type           string
	BaseURL        string            // 已解析默认地址（空值由网关按适配器默认补齐）
	Credential     string            // 选出的单条凭证（不透明）
	Model          string            // 上游真实模型名（mapping 后）
	Extra          map[string]string // 协议专用配置（如 azure api-version）
	HeaderOverride map[string]string
	ParamOverride  map[string]any
	// Auth 是渠道级鉴权配置（0.2.20）：nil/空 = 协议默认；解释权在本层（ApplyAuth）。
	Auth *llm.AuthConfig
}

// Adaptor 是同步对话协议的适配器接口（转换边界，参照 new-api Adaptor 形态）。
// 五段职责：URL → 鉴权头 → 请求体 → 发送 → 响应转回。流式契约与 ProviderPort 相同：
// 返回的 channel 恰好一个终态后关闭；resp.Body 由 ConvertResponse 的流阶段负责关闭。
type Adaptor interface {
	Type() string
	// DefaultBaseURL 返回该协议的默认上游根地址（渠道 base_url 为空时使用）。
	DefaultBaseURL() string
	GetRequestURL(rc RouteContext) string
	// SetupHeaders 解释凭证并设置鉴权/内容头；HeaderOverride 由网关在此之后合并。
	SetupHeaders(rc RouteContext, hdr http.Header) error
	// ConvertRequest 把统一请求转为该协议的上游请求体（rc.Model 已是上游模型名）。
	ConvertRequest(rc RouteContext, req llm.ChatRequest) ([]byte, error)
	DoRequest(ctx context.Context, rc RouteContext, hdr http.Header, body []byte) (*http.Response, error)
	// ConvertResponse 把上游响应转回流式块（SSE 协议在此逐事件解析）。
	ConvertResponse(ctx context.Context, rc RouteContext, resp *http.Response) (<-chan llm.StreamChunk, error)
}

// Registry 维护 type → Adaptor。业务代码只调用接口，绝不 switch 协议。
type Registry struct {
	mu     sync.RWMutex
	byType map[string]Adaptor
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry { return &Registry{byType: map[string]Adaptor{}} }

// Register 登记适配器；重复 type 报错（装配期暴露，不留半注册状态）。
func (r *Registry) Register(a Adaptor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byType[a.Type()]; dup {
		return fmt.Errorf("adaptor type %q 已注册", a.Type())
	}
	r.byType[a.Type()] = a
	return nil
}

// Get 取适配器；未知 type 显式报错（绝不静默降级）。
func (r *Registry) Get(typ string) (Adaptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byType[typ]
	if !ok {
		return nil, fmt.Errorf("未知渠道类型 %q（已注册：%s）", typ, r.registeredLocked())
	}
	return a, nil
}

// DefaultBaseURL 返回该类型的默认上游地址；未知 type 报错。
func (r *Registry) DefaultBaseURL(typ string) (string, error) {
	a, err := r.Get(typ)
	if err != nil {
		return "", err
	}
	return a.DefaultBaseURL(), nil
}

func (r *Registry) registeredLocked() string {
	names := make([]string, 0, len(r.byType))
	for k := range r.byType {
		names = append(names, k)
	}
	// 仅用于错误提示，插入排序保证可读
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return strings.Join(names, ", ")
}

// DoJSON 是同步适配器共用的 POST 发送：JSON 体 + 预置头，返回原始响应。
// 协议差异只在头与 URL，不在发送机制；非 2xx 由网关按状态码分类处置。
func DoJSON(ctx context.Context, client *http.Client, url string, hdr http.Header, body []byte) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return client.Do(req)
}

// ErrBodyLimit 是读取错误响应正文的上限（错误信息带回调用方，防巨响应）。
const ErrBodyLimit = 8192

// ReadErrBody 读取错误响应的有限正文（调用方负责 Close）。
func ReadErrBody(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, ErrBodyLimit))
	return strings.TrimSpace(string(body))
}

// IdleTimeout 是流式解析的默认空闲看门狗阈值（与 openaiprovider 保持一致）。
const IdleTimeout = 60 * time.Second

// ApplyParamOverride 把 param_override 深度合并进请求体顶层（网关在 ConvertRequest 之后调用）。
// 语义：override 的键覆盖/新增到 body 顶层；值原样写入（不做协议感知校验——那是配置者的责任）。
func ApplyParamOverride(body []byte, override map[string]any) ([]byte, error) {
	if len(override) == 0 {
		return body, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("param_override 合并失败（请求体非对象）：%w", err)
	}
	for k, v := range override {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("param_override[%s] 序列化失败：%w", k, err)
		}
		m[k] = raw
	}
	return json.Marshal(m)
}

// ApplyHeaderOverride 把 header_override 合并进请求头（网关在 SetupHeaders 之后调用）。
// 0.2.20：值支持 {api_key} 占位符——用户可用头覆写把任意头变成鉴权头
// （如 "api-key: {api_key}"、无 Bearer 前缀的 "Authorization: {api_key}"），
// 且多 Key 轮询下凭证始终是最新选出的那条（写死密钥会破坏轮询）。
func ApplyHeaderOverride(hdr http.Header, override map[string]string, credential string) {
	for k, v := range override {
		hdr.Set(k, RenderAuthValue(v, credential))
	}
}

// ---- 渠道级鉴权（0.2.20）----

// AuthDefaultBearer 是 Bearer 形态协议（openai 及兼容族）的默认鉴权。
// 为什么默认值仍由适配器给出：鉴权形态是协议知识（Bearer 头 vs x-api-key），
// 渠道级配置只是覆盖项——不配置时行为与历史完全一致（向后兼容）。
var AuthDefaultBearer = llm.AuthConfig{Type: llm.AuthBearer}

// AuthDefaultAnthropicKey 是 Anthropic Messages 协议的默认鉴权（x-api-key）。
var AuthDefaultAnthropicKey = llm.AuthConfig{Type: llm.AuthHeader, Name: "x-api-key", Value: "{api_key}"}

// ApplyAuth 构造上游鉴权头：渠道显式配置优先，否则用协议默认（def）。
// 语义（对齐 new-api AdvancedCustom 的 Auth 并提升为通用能力）：
//   - nil / default → def
//   - none          → 不设任何鉴权头（覆盖协议默认）
//   - bearer        → Authorization: Bearer <凭证>
//   - header        → Name: Render(Value)（Value 空 = 仅凭证）
//   - query         → 不设头（由 WithAuthQuery 拼 URL；此处仅校验名称）
//
// 空凭证一律不设鉴权头（本地网关如 Ollama 合法）。query 型在此校验名称——
// SetupHeaders 在 URL 构造之前被调用，缺名必须显式报错而非静默裸奔。
func ApplyAuth(rc RouteContext, hdr http.Header, def llm.AuthConfig) error {
	auth := def
	if rc.Auth != nil && rc.Auth.Type != "" && rc.Auth.Type != llm.AuthDefault {
		auth = *rc.Auth
	}
	switch auth.Type {
	case llm.AuthNone:
		return nil
	case llm.AuthQuery:
		if strings.TrimSpace(auth.Name) == "" {
			return fmt.Errorf("鉴权方式 query 缺少 URL 参数名称")
		}
		return nil
	case llm.AuthBearer:
		if rc.Credential != "" {
			hdr.Set("Authorization", "Bearer "+rc.Credential)
		}
		return nil
	case llm.AuthHeader:
		if strings.TrimSpace(auth.Name) == "" {
			return fmt.Errorf("鉴权方式 header 缺少请求头名称")
		}
		if rc.Credential != "" {
			hdr.Set(auth.Name, RenderAuthValue(auth.Value, rc.Credential))
		}
		return nil
	default:
		return fmt.Errorf("不支持的鉴权方式 %q（可用：default/bearer/header/query/none）", auth.Type)
	}
}

// WithAuthQuery 把 query 型鉴权参数拼进 URL（GetRequestURL 内调用，自动选 ? 或 &）。
// 非 query 型、无凭证、缺名称时原样返回（缺名称由 ApplyAuth 显式报错）。
func WithAuthQuery(url string, rc RouteContext) string {
	auth := rc.Auth
	if auth == nil || auth.Type != llm.AuthQuery || rc.Credential == "" {
		return url
	}
	name := strings.TrimSpace(auth.Name)
	if name == "" {
		return url
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return url + sep + neturl.QueryEscape(name) + "=" + neturl.QueryEscape(RenderAuthValue(auth.Value, rc.Credential))
}

// RenderAuthValue 渲染鉴权值模板：{api_key} → 凭证；空模板 = 仅凭证本身。
// 与 new-api 的 header override 词法一致（只支持 {api_key}）：模板能力越大，
// 误配置与注入面越大；需要更多变量时再加。
func RenderAuthValue(tpl, credential string) string {
	if tpl == "" {
		return credential
	}
	return strings.ReplaceAll(tpl, "{api_key}", credential)
}
