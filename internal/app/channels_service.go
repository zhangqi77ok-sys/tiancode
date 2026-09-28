// 渠道管理用例：列表/新增/更新/删除/激活/模型发现。
// 为什么放在编排层：这些是"用户可见用例"（校验 + 落盘 + 运行时重建的编排）。
// 存储已升级为多协议渠道池（priority/weight/model_mapping 等高级字段由管理 API 后补，
// 本层把旧 DTO（单模型/单凭证）映射到池模型，UI 契约不变。
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/adaptors"
	"tiancode/internal/platform/channels"
	"tiancode/internal/platform/openaiprovider"
)

// Presets 返回内置渠道模板（只读，供设置面板下拉）。
func (s *ChatService) Presets() []channels.Preset { return channels.Presets() }

// toView 把池渠道映射为脱敏视图（密钥不出编排层；池能力面随视图带出供设置面板管理）。
func toView(c channels.Channel) llm.ChannelView {
	model := ""
	if len(c.Models) > 0 {
		model = c.Models[0]
	}
	view := llm.ChannelView{
		Channel: llm.Channel{
			ID: c.ID, Name: c.Name, Protocol: llm.Protocol(c.Type),
			BaseURL: c.BaseURL, Model: model,
			Models: c.Models, Priority: c.Priority, Weight: c.Weight, Status: c.Status,
		},
		HasKey: strings.TrimSpace(c.Credential) != "",
	}
	return view
}

// Channels 返回渠道脱敏视图列表与激活渠道 ID（C-CH-6：密钥不出编排层）。
func (s *ChatService) Channels() ([]llm.ChannelView, string, error) {
	list := s.pool.List()
	views := make([]llm.ChannelView, 0, len(list))
	for _, c := range list {
		views = append(views, toView(c))
	}
	return views, s.pool.ActiveID(), nil
}

// poolChannel 把 DTO 转成池渠道（新渠道默认：default 组、priority 100、enabled）。
// models 为空时回退主模型；priority 0 视为未填（默认 100）；status 空视为 enabled。
func poolChannel(ch llm.Channel) channels.Channel {
	models := cleanModels(ch.Models)
	if len(models) == 0 && strings.TrimSpace(ch.Model) != "" {
		models = []string{strings.TrimSpace(ch.Model)}
	}
	priority := ch.Priority
	if priority == 0 {
		priority = 100
	}
	status := ch.Status
	if status == "" {
		status = channels.StatusEnabled
	}
	return channels.Channel{
		ID:         ch.ID,
		Type:       string(ch.Protocol),
		Name:       ch.Name,
		BaseURL:    ch.BaseURL,
		Credential: ch.APIKey,
		Models:     models,
		Groups:     []string{channels.DefaultGroup},
		Status:     status,
		Priority:   priority,
		Weight:     ch.Weight,
	}
}

// cleanModels 归一化模型列表（去空白/去重/保序）。
func cleanModels(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// AddChannel 新增渠道：校验 + 协议可用性检查通过才落盘。
// 若当前无激活渠道，新渠道自动激活（用户加了第一个渠道就该能直接用）。
func (s *ChatService) AddChannel(ch llm.Channel) (llm.Channel, error) {
	if ch.ID == "" {
		ch.ID = channels.NewID()
	}
	if err := llm.ValidateChannel(ch); err != nil {
		return llm.Channel{}, err
	}
	pc := poolChannel(ch)
	if _, err := s.registryAdaptor(pc.Type); err != nil {
		return llm.Channel{}, err
	}
	if err := s.pool.Save(pc); err != nil {
		return llm.Channel{}, err
	}
	if s.pool.ActiveID() == "" {
		if err := s.SetActiveChannel(ch.ID); err != nil {
			return llm.Channel{}, err
		}
	}
	return ch, nil
}

// registryAdaptor 校验协议类型已有适配器（避免"存进去但用不了"的半配置状态）。
func (s *ChatService) registryAdaptor(typ string) (adaptors.Adaptor, error) {
	return s.gw.Reg.Get(typ)
}

// UpdateChannel 更新渠道。密钥留空 = 保持原密钥：UI 不回显密钥（脱敏）。
// 高级字段（priority/weight/mapping/extra 等）不在本表单里，原值保留。
func (s *ChatService) UpdateChannel(id string, upd llm.Channel) error {
	existing, ok := s.pool.Get(id)
	if !ok {
		return fmt.Errorf("渠道不存在：%s", id)
	}
	if strings.TrimSpace(upd.APIKey) != "" {
		existing.Credential = upd.APIKey
	}
	existing.Type = string(upd.Protocol)
	existing.Name = upd.Name
	existing.BaseURL = upd.BaseURL
	models := cleanModels(upd.Models)
	if len(models) == 0 && strings.TrimSpace(upd.Model) != "" {
		models = []string{strings.TrimSpace(upd.Model)}
	}
	existing.Models = models
	if upd.Priority != 0 {
		existing.Priority = upd.Priority
	}
	existing.Weight = upd.Weight
	if upd.Status != "" {
		existing.Status = upd.Status
	}
	if err := llm.ValidateChannel(upd); err != nil {
		return err
	}
	if _, err := s.registryAdaptor(existing.Type); err != nil {
		return err
	}
	if err := s.pool.Save(existing); err != nil {
		return err
	}
	if s.pool.ActiveID() == id {
		return s.activate(existing.Models[0])
	}
	return nil
}

// DeleteChannel 删除渠道。激活渠道必须拒绝——否则运行时下一步就无渠道可用（C-CH-2）。
func (s *ChatService) DeleteChannel(id string) error {
	if s.pool.ActiveID() == id {
		return errors.New("激活渠道不可删除：请先切换到其他渠道")
	}
	return s.pool.Delete(id)
}

// SetActiveChannel 切换激活渠道并重建运行时（C-CH-3）。
// 顺序刻意如此：先构建运行时验证可用，再落盘激活项。
func (s *ChatService) SetActiveChannel(id string) error {
	ch, ok := s.pool.Get(id)
	if !ok {
		return fmt.Errorf("渠道不存在：%s", id)
	}
	if len(ch.Models) == 0 {
		return fmt.Errorf("渠道 %s 未声明任何模型", id)
	}
	if err := s.activate(ch.Models[0]); err != nil {
		return err
	}
	return s.pool.SetActive(id)
}

// DiscoverModels 拉取指定渠道上游的模型列表（供 UI 的"同步模型"）。
// 关键纪律（C-CH-4）：纯读操作，不落盘——发现失败绝不能影响已保存配置。
func (s *ChatService) DiscoverModels(ctx context.Context, ch llm.Channel) ([]string, error) {
	// 密钥留空表示复用已保存渠道的凭证首行（UI 不回显密钥）
	if strings.TrimSpace(ch.APIKey) == "" && ch.ID != "" {
		if saved, ok := s.pool.Get(ch.ID); ok {
			keys := splitFirstLine(saved.Credential)
			ch.APIKey = keys
		}
	}
	if string(ch.Protocol) != "openai" {
		return nil, fmt.Errorf("协议 %s 不支持模型发现", ch.Protocol)
	}
	prov := openaiprovider.New(openaiprovider.Options{BaseURL: ch.BaseURL, APIKey: ch.APIKey})
	return prov.DiscoverModels(ctx)
}

// splitFirstLine 取多行凭证的首行（模型发现只需要一条可用凭证）。
func splitFirstLine(cred string) string {
	for _, part := range strings.Split(cred, "\n") {
		if part = strings.TrimSpace(part); part != "" {
			return part
		}
	}
	return ""
}
