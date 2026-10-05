// 渠道管理的 IPC 绑定：把前端调用转发给 internal/app 用例层。
// 为什么在这里定义 DTO 而不是直接把领域类型丢给前端：
// 前端契约必须显式且稳定（字段名即前端 API），领域类型的演进不应直接击穿到 UI。
package app

import (
	"tiancode/internal/app"
	"tiancode/internal/core/llm"
)

// ChannelDTO 是渠道的 IPC 视图（密钥永不出现在这里，只有 HasKey）。
// 0.2.19 契约修复：池能力面（models/priority/weight/status）与高级字段
// （autoBan/modelMapping/paramOverride/headerOverride）此前只存在于前端类型，
// 绑定层解析时静默丢弃（json 未知字段不报错）——UI 上的设置从未生效。
// 本 DTO 与 frontend/src/wails.ts 的 ChannelDTO 一一对应，改一处必须同步另一处。
type ChannelDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	HasKey   bool   `json:"hasKey"`
	Active   bool   `json:"active"`

	Models   []string `json:"models"`
	Priority int      `json:"priority"`
	Weight   int      `json:"weight"`
	Status   string   `json:"status"`

	AutoBan        bool              `json:"autoBan"`
	ModelMapping   map[string]string `json:"modelMapping,omitempty"`
	ParamOverride  map[string]any    `json:"paramOverride,omitempty"`
	HeaderOverride map[string]string `json:"headerOverride,omitempty"`

	// Auth 是渠道级鉴权配置（0.2.20）：nil = 协议默认（openai → Bearer；anthropic → x-api-key）
	Auth *llm.AuthConfig `json:"auth,omitempty"`

	// ContextLimit 是渠道声明的上下文上限（token；0 = 未配置）。见 llm.Channel 注释。
	ContextLimit int `json:"contextLimit"`

	// 凭证摘要：列表卡片显示"N 条 · M 禁用"（逐条管理走 ListCredentials）
	CredentialCount    int `json:"credentialCount"`
	CredentialDisabled int `json:"credentialDisabled"`
}

// ChannelListDTO 是渠道列表结果（含激活渠道 ID）。
type ChannelListDTO struct {
	Channels []ChannelDTO `json:"channels"`
	ActiveID string       `json:"activeId"`
}

// CredentialDTO 是单条凭证的管理视图（脱敏预览；明文永不出现在这里）。
type CredentialDTO struct {
	Index   int    `json:"index"`
	Preview string `json:"preview"`
	Enabled bool   `json:"enabled"`
}

// PresetDTO 是内置渠道模板（供设置面板下拉）。
type PresetDTO struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	BaseURL        string `json:"baseUrl"`
	SuggestedModel string `json:"suggestedModel"`
}

// ChannelInput 是新增/更新渠道的入参。
// APIKey 留空在更新语义下表示"保持原密钥"（前端不回显密钥）。
type ChannelInput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`

	Models   []string `json:"models"`
	Priority int      `json:"priority"`
	Weight   int      `json:"weight"`
	Status   string   `json:"status"`

	AutoBan        bool              `json:"autoBan"`
	ModelMapping   map[string]string `json:"modelMapping"`
	ParamOverride  map[string]any    `json:"paramOverride"`
	HeaderOverride map[string]string `json:"headerOverride"`
	Auth           *llm.AuthConfig   `json:"auth"`
	// ContextLimit 是上下文上限（token；0 = 不限）——估算口径见渠道表单说明。
	ContextLimit int `json:"contextLimit"`
}

func (in ChannelInput) toDomain() llm.Channel {
	return llm.Channel{
		ID:       in.ID,
		Name:     in.Name,
		Protocol: llm.Protocol(in.Protocol),
		BaseURL:  in.BaseURL,
		Model:    in.Model,
		APIKey:   in.APIKey,

		Models:   in.Models,
		Priority: in.Priority,
		Weight:   in.Weight,
		Status:   in.Status,

		AutoBan:        in.AutoBan,
		ModelMapping:   in.ModelMapping,
		ParamOverride:  in.ParamOverride,
		HeaderOverride: in.HeaderOverride,
		Auth:           in.Auth,
		ContextLimit:   in.ContextLimit,
	}
}

// fromView 把领域视图映射为 IPC DTO（唯一映射点：ListChannels 与 AddChannel 共用）。
func fromView(v llm.ChannelView, activeID string) ChannelDTO {
	return ChannelDTO{
		ID: v.ID, Name: v.Name, Protocol: string(v.Protocol),
		BaseURL: v.BaseURL, Model: v.Model, HasKey: v.HasKey,
		Active: v.ID == activeID,

		Models:   v.Models,
		Priority: v.Priority,
		Weight:   v.Weight,
		Status:   v.Status,

		AutoBan:        v.AutoBan,
		ModelMapping:   v.ModelMapping,
		ParamOverride:  v.ParamOverride,
		HeaderOverride: v.HeaderOverride,
		Auth:           v.Auth,
		ContextLimit:   v.ContextLimit,

		CredentialCount:    v.CredentialCount,
		CredentialDisabled: v.CredentialDisabled,
	}
}

// ListChannels 返回渠道列表（脱敏）与激活渠道 ID。
func (b *Bind) ListChannels() (ChannelListDTO, error) {
	views, activeID, err := b.chat.Channels()
	if err != nil {
		return ChannelListDTO{}, err
	}
	out := ChannelListDTO{Channels: make([]ChannelDTO, 0, len(views)), ActiveID: activeID}
	for _, v := range views {
		out.Channels = append(out.Channels, fromView(v, activeID))
	}
	return out, nil
}

// ChannelPresets 返回内置渠道模板。
func (b *Bind) ChannelPresets() ([]PresetDTO, error) {
	ps := b.chat.Presets()
	out := make([]PresetDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, PresetDTO{
			Key: p.Key, Name: p.Name, Protocol: string(p.Protocol),
			BaseURL: p.BaseURL, SuggestedModel: p.SuggestedModel,
		})
	}
	return out, nil
}

// AddChannel 新增渠道；首个渠道自动成为激活渠道。
func (b *Bind) AddChannel(in ChannelInput) (ChannelDTO, error) {
	ch, err := b.chat.AddChannel(in.toDomain())
	if err != nil {
		return ChannelDTO{}, err
	}
	return ChannelDTO{
		ID: ch.ID, Name: ch.Name, Protocol: string(ch.Protocol),
		BaseURL: ch.BaseURL, Model: ch.Model, HasKey: ch.APIKey != "",
	}, nil
}

// UpdateChannel 更新渠道（APIKey 留空表示保持原密钥）。
func (b *Bind) UpdateChannel(in ChannelInput) error {
	return b.chat.UpdateChannel(in.ID, in.toDomain())
}

// DeleteChannel 删除渠道（激活渠道会被拒绝，需先切换）。
func (b *Bind) DeleteChannel(id string) error {
	return b.chat.DeleteChannel(id)
}

// SetActiveChannel 切换激活渠道（下一次发送即走新渠道）。
func (b *Bind) SetActiveChannel(id string) error {
	return b.chat.SetActiveChannel(id)
}

// DiscoverModels 按渠道信息拉取上游模型列表（不落盘，失败不影响已保存配置）。
func (b *Bind) DiscoverModels(in ChannelInput) ([]string, error) {
	return b.chat.DiscoverModels(b.appCtx(), in.toDomain())
}

// TestChannel 测试渠道连通性（走真实链路，失败无副作用——不触发 auto_ban）。
func (b *Bind) TestChannel(id string) (app.TestResult, error) {
	return b.chat.TestChannel(b.appCtx(), id)
}

// ListCredentials 返回渠道凭证管理视图（脱敏预览 + 启用态）。
func (b *Bind) ListCredentials(id string) ([]CredentialDTO, error) {
	infos, err := b.chat.ChannelCredentials(id)
	if err != nil {
		return nil, err
	}
	out := make([]CredentialDTO, 0, len(infos))
	for _, it := range infos {
		out = append(out, CredentialDTO{Index: it.Index, Preview: it.Preview, Enabled: it.Enabled})
	}
	return out, nil
}

// SetCredentialEnabled 启用/禁用单条凭证（启用=自动禁用后的恢复途径）。
func (b *Bind) SetCredentialEnabled(id string, index int, enabled bool) error {
	return b.chat.SetCredentialEnabled(id, index, enabled)
}

// SetActiveModel 激活指定渠道的指定模型（Composer 模型选择器，0.2.28）。
func (b *Bind) SetActiveModel(id string, model string) error {
	return b.chat.SetActiveModel(id, model)
}
