// Package app 是用例编排层：组装内核完成用户可见用例，并把错误上抛到 UI。
//
// 做什么：ChatService 编排"对话流式"用例（发送消息 → 驱动 agent → 流式回传 → 落账本），
// 并提供会话列表查询。IO 全部经 core 端口（session 账本/llm 运行时），本包不做直接 IO。
// 被谁依赖：壳层（Wails 绑定 app/，未来 daemon 网关）。
// 依赖谁：internal/core/*；禁止直接执行文件/进程/网络 IO（见 docs/STANDARDS.md 分层表）。
//
// 契约红线：任何持久化/流式错误都必须上抛到 UI（禁止 `_ =` 丢弃）——
// 此为 arch_check R2 守卫红线；旧实现 app_chat.go:282/503 静默吞错导致丢消息无感。
//
// Pipeline 说明（ADR-0005）：M2 的 Send 是单链编排；分支节点 ≥3 时按 ADR 升级为
// 显式 Pipeline（ResolveSession→Dispatch→StreamRelay→Persist）。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
	"tiancode/internal/platform/channels"
	"tiancode/internal/platform/configfile"
	"tiancode/internal/platform/fstool"
	"tiancode/internal/platform/gittool"
	"tiancode/internal/platform/providerfactory"
	"tiancode/internal/platform/shelltool"
)

// Config 是对话服务的装配配置。
// BaseURL/APIKey/Model 是**渠道迁移来源**：首次启动若无渠道配置，用它们生成首个渠道；
// 此后渠道配置是唯一事实源（用户在应用内管理）。密钥只允许来自用户配置/环境变量，
// 禁止硬编码入库（STANDARDS §1）。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	DataDir string // 会话账本目录
	WorkDir string // 工作区（fs/shell/git 工具的受控范围）
	// ChannelsPath 是渠道配置文件路径；缺省 %APPDATA%\tiancode\channels.json。
	// 可注入是为测试隔离（同进程多实例不互相污染）。
	ChannelsPath string
}

// ChatService 编排对话用例。
type ChatService struct {
	cfg      Config
	store    *channels.Store
	factory  *providerfactory.Factory
	registry *tools.Registry

	mu      sync.Mutex
	agent   *agent.Loop // 随激活渠道重建；无激活渠道时为 nil（Send 给出可读错误）
	active  llm.Channel
	ledgers map[string]*session.Ledger
}

// NewChatService 装配编排层：渠道存储 → 工具注册表 → 按激活渠道构建 agent。
// 装配顺序刻意让"无渠道"成为合法状态：用户可以先把应用跑起来，再在设置里添加渠道
// （否则首次启动会被配置硬门槛挡死——这正是旧实现"装完点开没反应"的根因之一）。
func NewChatService(cfg Config) (*ChatService, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("chat service: data dir required")
	}
	if cfg.WorkDir == "" {
		return nil, errors.New("chat service: work dir required")
	}
	if cfg.ChannelsPath == "" {
		cfg.ChannelsPath = channels.DefaultPath()
	}
	s := &ChatService{
		cfg:     cfg,
		store:   channels.NewStore(cfg.ChannelsPath),
		factory: providerfactory.New(),
		ledgers: make(map[string]*session.Ledger),
	}

	// 工具装配：fs（读写/替换）、shell（命令，默认 120s 超时）、git（只读查看）
	registry := tools.NewRegistry()
	for _, reg := range []func() error{
		func() error { return registry.Register(fstool.New(cfg.WorkDir)) },
		func() error { return registry.Register(shelltool.New(shelltool.Options{Root: cfg.WorkDir})) },
		func() error { return registry.Register(gittool.New(cfg.WorkDir)) },
	} {
		if err := reg(); err != nil {
			return nil, fmt.Errorf("register tool: %w", err)
		}
	}
	s.registry = registry

	if err := s.bootstrapChannels(); err != nil {
		return nil, err
	}
	return s, nil
}

// bootstrapChannels 加载渠道配置；首次运行从 Config 迁移（C-CH-1）。
// 为什么要迁移：老用户升级不该被迫二次配置（config.json 里已有网关信息）。
func (s *ChatService) bootstrapChannels() error {
	cfg, err := s.store.Load()
	if err != nil {
		return err
	}
	if len(cfg.Channels) == 0 {
		migrated, ok := channels.MigrateFromConfig(configfile.File{
			BaseURL: s.cfg.BaseURL, APIKey: s.cfg.APIKey, Model: s.cfg.Model,
		})
		if !ok {
			return nil // 无渠道且无迁移来源：合法状态，UI 引导添加
		}
		if err := s.store.Save(migrated); err != nil {
			return fmt.Errorf("persist migrated channel: %w", err)
		}
		cfg = migrated
	}
	active, ok := cfg.Active()
	if !ok {
		return nil // 有渠道但无激活项：同样合法（等用户选择）
	}
	return s.activate(active)
}

// activate 由渠道构建运行时与 agent；这是唯一与"具体渠道"耦合的装配点。
func (s *ChatService) activate(ch llm.Channel) error {
	prov, err := s.factory.NewProvider(ch)
	if err != nil {
		return err
	}
	// 为什么总预算 10min：编码任务的推理流可达数分钟；空闲看门狗（provider 内 60s）
	// 已覆盖挂起场景，总预算只防极端失控。
	rt := llm.NewChatRuntime(prov, llm.TimeoutBudget{Total: 10 * time.Minute})
	s.agent = agent.NewLoop(rt, ch.Model, s.registry)
	s.active = ch
	return nil
}

// DeleteSession 删除会话及其账本文件。
// 打开中的账本必须先关闭：Windows 上句柄未释放时删除会失败（与旧实现 rename 失败同源）。
func (s *ChatService) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.ledgers[sessionID]; ok {
		if err := l.Close(); err != nil {
			return fmt.Errorf("关闭会话账本失败：%w", err)
		}
		delete(s.ledgers, sessionID)
	}
	return session.DeleteSession(s.cfg.DataDir, sessionID)
}

// Send 发送一条用户消息，返回流式块通道（恰好一个 EndReason 终态后关闭）。
func (s *ChatService) Send(ctx context.Context, sessionID, text string) (<-chan llm.StreamChunk, error) {
	ledger, err := s.ledgerFor(sessionID) // 注意：先取账本（内部加锁），再读 agent，避免自锁
	if err != nil {
		return nil, fmt.Errorf("open session ledger: %w", err)
	}
	s.mu.Lock()
	ag := s.agent
	s.mu.Unlock()
	if ag == nil {
		return nil, errors.New("尚未配置模型渠道：请在设置中新增渠道并设为默认")
	}
	return ag.Run(ctx, ledger, text)
}

// ListSessions 返回全部会话 ID。
func (s *ChatService) ListSessions() ([]string, error) {
	return session.ListSessions(s.cfg.DataDir)
}

// ChatMessage 是投影给前端的已确认消息（user/assistant 锚点）。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Replay 把会话账本投影为已确认消息列表，供前端恢复历史。
// 只投影 user_message / assistant_message 锚点——被取消轮次的 delta 不进历史
// （与 agent.deriveMessages 同一语义，见 core/agent）。
func (s *ChatService) Replay(sessionID string) ([]ChatMessage, error) {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return nil, err
	}
	var out []ChatMessage
	err = ledger.Replay(func(ev session.Event) error {
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		switch ev.Kind() {
		case session.EventUserMessage:
			out = append(out, ChatMessage{Role: "user", Content: p.Text})
		case session.EventAssistantMsg:
			out = append(out, ChatMessage{Role: "assistant", Content: p.Text})
		}
		return nil
	})
	return out, err
}

// ledgerFor 复用每会话的账本句柄：账本由服务持有至进程退出，
// 避免 agent 异步使用期间被提前关闭（Windows 下句柄关闭即不可再追加）。
func (s *ChatService) ledgerFor(sessionID string) (*session.Ledger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.ledgers[sessionID]; ok {
		return l, nil
	}
	l, err := session.OpenLedger(s.cfg.DataDir, sessionID)
	if err != nil {
		return nil, err
	}
	s.ledgers[sessionID] = l
	return l, nil
}

// Close 关闭全部缓存的账本句柄（应用退出时调用；Windows 下不关闭会导致数据文件无法删除）。
func (s *ChatService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for id, l := range s.ledgers {
		if err := l.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.ledgers, id)
	}
	return firstErr
}
