// 渠道管理的 IPC 绑定：把前端调用转发给 internal/app 用例层。
// 为什么在这里定义 DTO 而不是直接把领域类型丢给前端：
// 前端契约必须显式且稳定（字段名即前端 API），领域类型的演进不应直接击穿到 UI。
package app

import (
	"tiancode/internal/core/llm"
)

// ChannelDTO 是渠道的 IPC 视图（密钥永不出现在这里，只有 HasKey）。
type ChannelDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	HasKey   bool   `json:"hasKey"`
	Active   bool   `json:"active"`
}

// ChannelListDTO 是渠道列表结果（含激活渠道 ID）。
type ChannelListDTO struct {
	Channels []ChannelDTO `json:"channels"`
	ActiveID string       `json:"activeId"`
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
}

func (in ChannelInput) toDomain() llm.Channel {
	return llm.Channel{
		ID:       in.ID,
		Name:     in.Name,
		Protocol: llm.Protocol(in.Protocol),
		BaseURL:  in.BaseURL,
		Model:    in.Model,
		APIKey:   in.APIKey,
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
		out.Channels = append(out.Channels, ChannelDTO{
			ID: v.ID, Name: v.Name, Protocol: string(v.Protocol),
			BaseURL: v.BaseURL, Model: v.Model, HasKey: v.HasKey,
			Active: v.ID == activeID,
		})
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
