// 渠道管理用例：列表/新增/更新/删除/激活/模型发现。
// 为什么放在编排层：这些是"用户可见用例"（校验 + 落盘 + 运行时重建的编排），
// 而渠道数据本身与持久化分别在 core/llm（类型校验）与 platform/channels（存储）。
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/channels"
)

// Presets 返回内置渠道模板（只读，供设置面板下拉）。
func (s *ChatService) Presets() []channels.Preset { return channels.Presets() }

// Channels 返回渠道脱敏视图列表与激活渠道 ID（C-CH-6：密钥不出编排层）。
func (s *ChatService) Channels() ([]llm.ChannelView, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.store.Load()
	if err != nil {
		return nil, "", err
	}
	views := make([]llm.ChannelView, 0, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		views = append(views, ch.Sanitized())
	}
	return views, cfg.ActiveID, nil
}

// AddChannel 新增渠道：校验 + 协议可用性检查通过才落盘。
// 若当前无激活渠道，新渠道自动激活（用户加了第一个渠道就该能直接用）。
func (s *ChatService) AddChannel(ch llm.Channel) (llm.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.store.Load()
	if err != nil {
		return llm.Channel{}, err
	}
	if ch.ID == "" {
		ch.ID = channels.NewID()
	}
	if err := llm.ValidateChannel(ch); err != nil {
		return llm.Channel{}, err
	}
	// 协议可用性在落盘前检查：避免"存进去但用不了"的半配置状态
	if _, err := s.factory.NewProvider(ch); err != nil {
		return llm.Channel{}, err
	}
	cfg.Channels = append(cfg.Channels, ch)
	if cfg.ActiveID == "" {
		cfg.ActiveID = ch.ID
	}
	if err := s.store.Save(cfg); err != nil {
		return llm.Channel{}, err
	}
	if cfg.ActiveID == ch.ID {
		if err := s.activate(ch); err != nil {
			return llm.Channel{}, err
		}
	}
	return ch, nil
}

// UpdateChannel 更新渠道。
// 密钥留空 = 保持原密钥：UI 不回显密钥（脱敏），因此"未改动"必然表现为空值，
// 若把空值当清空写入，用户每次保存都会丢密钥。
func (s *ChatService) UpdateChannel(id string, upd llm.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.store.Load()
	if err != nil {
		return err
	}
	idx := -1
	for i, ch := range cfg.Channels {
		if ch.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("渠道不存在：%s", id)
	}
	if strings.TrimSpace(upd.APIKey) == "" {
		upd.APIKey = cfg.Channels[idx].APIKey
	}
	upd.ID = id
	if err := llm.ValidateChannel(upd); err != nil {
		return err
	}
	if _, err := s.factory.NewProvider(upd); err != nil {
		return err
	}
	cfg.Channels[idx] = upd
	if err := s.store.Save(cfg); err != nil {
		return err
	}
	if cfg.ActiveID == id {
		return s.activate(upd)
	}
	return nil
}

// DeleteChannel 删除渠道。激活渠道必须拒绝——否则运行时下一步就无渠道可用，
// 会比"先切换再删除"更难解释（C-CH-2）。
func (s *ChatService) DeleteChannel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.store.Load()
	if err != nil {
		return err
	}
	if cfg.ActiveID == id {
		return errors.New("激活渠道不可删除：请先切换到其他渠道")
	}
	kept := make([]llm.Channel, 0, len(cfg.Channels))
	found := false
	for _, ch := range cfg.Channels {
		if ch.ID == id {
			found = true
			continue
		}
		kept = append(kept, ch)
	}
	if !found {
		return fmt.Errorf("渠道不存在：%s", id)
	}
	cfg.Channels = kept
	return s.store.Save(cfg)
}

// SetActiveChannel 切换激活渠道并重建运行时（C-CH-3）。
// 顺序刻意如此：先构建运行时验证可用，再落盘激活项——
// 避免"配置说已激活，运行时却构建失败"的不一致状态。
func (s *ChatService) SetActiveChannel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.store.Load()
	if err != nil {
		return err
	}
	var target llm.Channel
	found := false
	for _, ch := range cfg.Channels {
		if ch.ID == id {
			target, found = ch, true
			break
		}
	}
	if !found {
		return fmt.Errorf("渠道不存在：%s", id)
	}
	if err := s.activate(target); err != nil {
		return err
	}
	cfg.ActiveID = id
	return s.store.Save(cfg)
}

// DiscoverModels 拉取指定渠道上游的模型列表（供 UI 的"同步模型"）。
// 关键纪律（C-CH-4）：纯读操作，不落盘——发现失败绝不能影响已保存配置。
func (s *ChatService) DiscoverModels(ctx context.Context, ch llm.Channel) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 密钥留空表示复用已保存渠道的密钥（UI 不回显密钥）
	if strings.TrimSpace(ch.APIKey) == "" && ch.ID != "" {
		cfg, err := s.store.Load()
		if err != nil {
			return nil, err
		}
		for _, saved := range cfg.Channels {
			if saved.ID == ch.ID {
				ch.APIKey = saved.APIKey
				break
			}
		}
	}
	prov, err := s.factory.NewProvider(ch)
	if err != nil {
		return nil, err
	}
	discoverer, ok := prov.(llm.ModelDiscoverer)
	if !ok {
		return nil, fmt.Errorf("协议 %s 不支持模型发现", ch.Protocol)
	}
	return discoverer.DiscoverModels(ctx)
}
