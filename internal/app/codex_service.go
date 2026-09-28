// Codex（ChatGPT 订阅）账号授权用例：OAuth 会话 → 凭证绑定渠道 → 请求前自动续期。
//
// 凭证纪律：授权换取的凭证（含 access/refresh token）**绝不外发前端**——
// 绑定动作整体发生在编排层（BindCodexOAuth 一次调用完成"取凭证 + 写渠道"），
// 前端只拿到脱敏摘要（邮箱 · 套餐）。
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/codexauth"
)

// DefaultCodexModel 是新建 Codex 渠道时的默认模型（可在渠道里改）。
const DefaultCodexModel = "gpt-5-codex"

// CodexBindResult 是授权/导入绑定渠道的结果（前端展示"已绑定 xx"）。
type CodexBindResult struct {
	ChannelID string `json:"channelId"`
	Name      string `json:"name"`
	Display   string `json:"display"` // 脱敏摘要（邮箱 · 套餐）
	Created   bool   `json:"created"` // true = 新建渠道，false = 更新已有渠道凭证
}

// StartCodexOAuth 发起一次账号授权：返回授权链接（UI 打开系统浏览器）+ 会话 ID。
// 桌面增强：本机 1455 回环监听已尽力启动（Listening=true 时浏览器授权后自动完成）。
func (s *ChatService) StartCodexOAuth() (codexauth.StartInfo, error) {
	if s.codexAuth == nil {
		return codexauth.StartInfo{}, errors.New("codex 授权组件未初始化")
	}
	return s.codexAuth.Start()
}

// PollCodexOAuth 查询授权状态（前端轮询实现"完成授权后自动更新"）。
func (s *ChatService) PollCodexOAuth(sessionID string) (codexauth.Status, error) {
	if s.codexAuth == nil {
		return codexauth.Status{}, errors.New("codex 授权组件未初始化")
	}
	return s.codexAuth.Poll(sessionID)
}

// BindCodexOAuth 完成绑定：可选先提交手动粘贴的回调内容（回环不可用时的兜底通道），
// 随后取走凭证并写入渠道（channelID 空 = 新建 Codex 渠道）。
func (s *ChatService) BindCodexOAuth(ctx context.Context, sessionID, channelID, name, codeOrURL string) (CodexBindResult, error) {
	if s.codexAuth == nil {
		return CodexBindResult{}, errors.New("codex 授权组件未初始化")
	}
	if strings.TrimSpace(codeOrURL) != "" {
		if _, err := s.codexAuth.Complete(ctx, sessionID, codeOrURL); err != nil {
			return CodexBindResult{}, err
		}
	}
	cred, err := s.codexAuth.Bind(sessionID)
	if err != nil {
		return CodexBindResult{}, err
	}
	return s.attachCodexCredential(channelID, name, cred)
}

// ImportCodexCredential 从粘贴内容导入凭证并绑定渠道（Token/JSON 通道：
// auth.json / access_token / refresh_token，必要时用 RT 换取新 token）。
func (s *ChatService) ImportCodexCredential(ctx context.Context, channelID, name, raw string) (CodexBindResult, error) {
	if s.codexClient == nil {
		return CodexBindResult{}, errors.New("codex 授权组件未初始化")
	}
	cred, err := codexauth.ParseImport(ctx, s.codexClient, raw)
	if err != nil {
		return CodexBindResult{}, err
	}
	return s.attachCodexCredential(channelID, name, cred)
}

// attachCodexCredential 把凭证写入渠道：channelID 空 = 新建；非空 = 更新已有渠道凭证。
func (s *ChatService) attachCodexCredential(channelID, name string, cred codexauth.Credential) (CodexBindResult, error) {
	credJSON, err := cred.JSON()
	if err != nil {
		return CodexBindResult{}, err
	}
	if strings.TrimSpace(channelID) == "" {
		nm := strings.TrimSpace(name)
		if nm == "" {
			nm = "ChatGPT · " + cred.Display()
		}
		added, err := s.AddChannel(llm.Channel{
			Name:     nm,
			Protocol: llm.ProtocolCodex,
			APIKey:   credJSON,
			Models:   []string{DefaultCodexModel},
		})
		if err != nil {
			return CodexBindResult{}, err
		}
		return CodexBindResult{ChannelID: added.ID, Name: added.Name, Display: cred.Display(), Created: true}, nil
	}

	existing, ok := s.pool.Get(channelID)
	if !ok {
		return CodexBindResult{}, fmt.Errorf("渠道不存在：%s", channelID)
	}
	existing.Credential = credJSON
	if err := s.pool.Save(existing); err != nil {
		return CodexBindResult{}, err
	}
	return CodexBindResult{ChannelID: existing.ID, Name: existing.Name, Display: cred.Display()}, nil
}

// ensureCodexFresh 请求前检查激活渠道的 Codex 凭证：临近过期则自动刷新并写回池。
// 只处理激活渠道：网关故障降档到其他渠道时其凭证可能已过期（由上游 401 显式暴露），
// 全池预刷新是后台服务的复杂度，桌面场景不值当。
// 刷新失败返回错误（明确阻断本次发送）——过期凭证发出去只会得到难解读的 401。
func (s *ChatService) ensureCodexFresh(ctx context.Context) error {
	active := s.pool.ActiveID()
	if active == "" {
		return nil
	}
	ch, ok := s.pool.Get(active)
	if !ok {
		return nil
	}
	cred, isOAuth := codexauth.Parse(ch.Credential)
	if !isOAuth || !cred.NeedsRefresh(time.Now()) {
		return nil
	}
	refreshed, err := codexauth.RefreshCredential(ctx, s.codexClient, cred)
	if err != nil {
		return fmt.Errorf("ChatGPT 凭证已过期且刷新失败：%w（请在渠道管理里重新授权）", err)
	}
	js, err := refreshed.JSON()
	if err != nil {
		return err
	}
	return s.pool.UpdateCredential(active, js)
}

// CodexCredentialInfo 是渠道凭证的展示信息（codex 渠道在渠道管理里回显"已绑定哪个账号"）。
type CodexCredentialInfo struct {
	Bound   bool   `json:"bound"`
	Display string `json:"display,omitempty"`
	Expires int64  `json:"expiresAt,omitempty"`
}

// CodexCredentialOf 读取渠道的 Codex 凭证摘要（无凭证/非 OAuth 时 Bound=false）。
func (s *ChatService) CodexCredentialOf(channelID string) CodexCredentialInfo {
	ch, ok := s.pool.Get(channelID)
	if !ok {
		return CodexCredentialInfo{}
	}
	cred, isOAuth := codexauth.Parse(ch.Credential)
	if !isOAuth {
		return CodexCredentialInfo{}
	}
	return CodexCredentialInfo{Bound: true, Display: cred.Display(), Expires: cred.ExpiresAt}
}
