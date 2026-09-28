// 渠道管理用例：列表/新增/更新/删除/激活/模型发现。
// 为什么放在编排层：这些是"用户可见用例"（校验 + 落盘 + 运行时重建的编排）。
// 存储已升级为多协议渠道池（priority/weight/model_mapping 等高级字段由管理 API 后补，
// 本层把旧 DTO（单模型/单凭证）映射到池模型，UI 契约不变。
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/adaptors"
	"tiancode/internal/platform/channels"
	"tiancode/internal/platform/codexauth"
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
			AutoBan:        c.AutoBan,
			ModelMapping:   c.ModelMapping,
			ParamOverride:  c.ParamOverride,
			HeaderOverride: c.HeaderOverride,
			Auth:           c.Auth,
		},
		HasKey: strings.TrimSpace(c.Credential) != "",
	}
	return view
}

// Channels 返回渠道脱敏视图列表与激活渠道 ID（C-CH-6：密钥不出编排层）。
// 附凭证摘要（总数/禁用数）：列表卡片直接显示"3 条 · 1 禁用"，无需逐渠道再查。
func (s *ChatService) Channels() ([]llm.ChannelView, string, error) {
	list := s.pool.List()
	views := make([]llm.ChannelView, 0, len(list))
	for _, c := range list {
		v := toView(c)
		if creds, err := s.pool.Credentials(c.ID); err == nil {
			v.CredentialCount = len(creds)
			for _, cr := range creds {
				if !cr.Enabled {
					v.CredentialDisabled++
				}
			}
		}
		views = append(views, v)
	}
	return views, s.pool.ActiveID(), nil
}

// poolChannel 把 DTO 转成池渠道（新渠道默认：default 组、priority 100、enabled）。
// models 为空时回退主模型；priority 0 视为未填（默认 100）；status 空视为 enabled。
// 0.2.19：高级字段（autoBan/映射/覆写）随行——此前 IPC 契约断层导致 UI 设置被丢弃。
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
		ID:             ch.ID,
		Type:           string(ch.Protocol),
		Name:           ch.Name,
		BaseURL:        ch.BaseURL,
		Credential:     ch.APIKey,
		Models:         models,
		Groups:         []string{channels.DefaultGroup},
		Status:         status,
		Priority:       priority,
		Weight:         ch.Weight,
		AutoBan:        ch.AutoBan,
		ModelMapping:   ch.ModelMapping,
		ParamOverride:  ch.ParamOverride,
		HeaderOverride: ch.HeaderOverride,
		Auth:           ch.Auth,
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
// 0.2.19：高级字段（autoBan/映射/覆写）随表单落盘——此前"不在本表单里，原值保留"
// 的注释掩盖了 IPC 绑定层丢字段的事实，UI 上的优先级/权重/状态/模型列表其实从未生效。
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
	existing.AutoBan = upd.AutoBan
	existing.ModelMapping = upd.ModelMapping
	existing.ParamOverride = upd.ParamOverride
	existing.HeaderOverride = upd.HeaderOverride
	existing.Auth = upd.Auth
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

// TestResult 是一次渠道连通性测试的结果（UI 行内展示：延迟/回显/错误）。
type TestResult struct {
	OK    bool   `json:"ok"`
	Ms    int64  `json:"ms"`
	Model string `json:"model"`
	Reply string `json:"reply,omitempty"` // 上游首个文本片段（截断）——证明真的回了内容
	Error string `json:"error,omitempty"`
}

// testTimeout 是单次渠道测试的超时上限（诊断请求，不合适等待过久）。
const testTimeout = 20 * time.Second

// TestChannel 用一条最小请求验证渠道连通性（参照 new-api Test Connection：走真实链路）。
// 链路：Probe（跳过状态过滤、不扰动轮询游标）→ 适配器五段（含 model_mapping 与覆写）→ 真实 HTTP。
// 刻意不经 gateway 的 auto_ban：手动测试是诊断行为，失败不产生副作用——
// 否则"测一次坏渠道就被自动禁用还得手动救"，测试把渠道测坏了是反直觉的。
func (s *ChatService) TestChannel(ctx context.Context, id string) (TestResult, error) {
	ch, ok := s.pool.Get(id)
	if !ok {
		return TestResult{}, fmt.Errorf("渠道不存在：%s", id)
	}
	model := ""
	if len(ch.Models) > 0 {
		model = ch.Models[0]
	}
	if model == "" {
		return TestResult{}, errors.New("渠道未声明模型，无法测试")
	}
	adv, err := s.registryAdaptor(ch.Type)
	if err != nil {
		return TestResult{}, err
	}
	// 凭证临期先续期：否则诊断结果反映的是过期凭证（误导用户"渠道坏了"）。
	// 续期失败不阻断测试——真实失败原因（401 等）由测试结果本身呈现。
	if cred, isOAuth := codexauth.Parse(ch.Credential); isOAuth && cred.NeedsRefresh(time.Now()) {
		if refreshed, rerr := codexauth.RefreshCredential(ctx, s.codexClient, cred); rerr == nil {
			if js, jerr := refreshed.JSON(); jerr == nil {
				if uerr := s.pool.UpdateCredential(id, js); uerr != nil {
					return TestResult{Model: model, Error: "凭证续期写回失败：" + uerr.Error()}, nil
				}
				ch.Credential = js
			}
		}
	}
	sel, err := s.pool.Probe(id)
	if err != nil {
		return TestResult{}, err
	}
	rc := adaptors.RouteContext{
		ChannelID: sel.ChannelID, Type: sel.Type, BaseURL: sel.BaseURL,
		Credential: sel.Credential, Extra: sel.Extra,
		HeaderOverride: sel.HeaderOverride, ParamOverride: sel.ParamOverride,
		Auth: sel.Auth, Model: model,
		Proxy: s.pool.Proxy(), // 全局上游代理（测试与生产同一出口策略）
	}
	if mapped := sel.ModelMapping[model]; mapped != "" {
		rc.Model = mapped // 测试必须用上游真实模型名（映射后的），否则"生产可用、测试报错"
	}
	if rc.BaseURL == "" {
		base, err := s.gw.Reg.DefaultBaseURL(sel.Type)
		if err != nil {
			return TestResult{}, err
		}
		rc.BaseURL = base
	}

	req := llm.ChatRequest{Model: model, Messages: []llm.Message{{Role: "user", Content: "ping"}}}
	body, err := adv.ConvertRequest(rc, req)
	if err != nil {
		return TestResult{Model: model, Error: "构造请求失败：" + err.Error()}, nil
	}
	if body, err = adaptors.ApplyParamOverride(body, sel.ParamOverride); err != nil {
		return TestResult{Model: model, Error: err.Error()}, nil
	}
	hdr := http.Header{}
	if err := adv.SetupHeaders(rc, hdr); err != nil {
		return TestResult{Model: model, Error: "构造鉴权头失败：" + err.Error()}, nil
	}
	adaptors.ApplyHeaderOverride(hdr, sel.HeaderOverride, sel.Credential)

	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	resp, err := adv.DoRequest(tctx, rc, hdr, body)
	if err != nil {
		return TestResult{Model: model, Ms: ms(), Error: err.Error()}, nil
	}
	// 正常路径由 ConvertResponse 的流阶段接管 body；本 defer 兜底错误/提前返回路径
	//（http.Body.Close 幂等，重复关闭无害）
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.TrimSpace(string(b))
		if detail == "" && readErr != nil {
			detail = readErr.Error() // 错误详情读取失败也要给出原因，不留空错误
		}
		return TestResult{Model: model, Ms: ms(), Error: fmt.Sprintf("HTTP %d：%s", resp.StatusCode, detail)}, nil
	}
	stream, err := adv.ConvertResponse(tctx, rc, resp)
	if err != nil {
		return TestResult{Model: model, Ms: ms(), Error: err.Error()}, nil
	}
	reply := ""
	for chunk := range stream {
		if reply == "" && chunk.Delta != "" {
			reply = chunk.Delta
		}
		if chunk.EndReason != llm.EndNone {
			if chunk.EndReason == llm.EndDone {
				return TestResult{OK: true, Ms: ms(), Model: model, Reply: trimRunes(reply, 60)}, nil
			}
			msg := "流未正常结束"
			if chunk.Err != nil {
				msg = chunk.Err.Error()
			}
			return TestResult{Model: model, Ms: ms(), Error: msg}, nil
		}
	}
	// 流关闭但未见终态：显式报错而非假装成功（与流式三终态纪律一致）
	return TestResult{Model: model, Ms: ms(), Error: "上游流提前关闭（无终态）"}, nil
}

// trimRunes 按 rune 截断（中文安全），超出加省略号。
func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ChannelCredentials 返回渠道的凭证管理视图（脱敏预览 + 启用态）。
func (s *ChatService) ChannelCredentials(id string) ([]channels.CredentialInfo, error) {
	return s.pool.Credentials(id)
}

// SetCredentialEnabled 启用/禁用单条凭证（禁用=手动摘除坏 Key；
// 启用=自动禁用后的恢复途径），渠道状态按剩余可用凭证数自动联动。
func (s *ChatService) SetCredentialEnabled(id string, index int, enabled bool) error {
	return s.pool.SetCredentialEnabled(id, index, enabled)
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
