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
	"tiancode/internal/platform/fstool"
	"tiancode/internal/platform/gittool"
	"tiancode/internal/platform/openaiprovider"
	"tiancode/internal/platform/shelltool"
)

// Config 是对话服务的装配配置。
// APIKey 只允许来自环境变量或用户本地配置，禁止硬编码入库（STANDARDS §1）。
type Config struct {
	BaseURL string // OpenAI 兼容网关根地址（含 /v1）
	APIKey  string
	Model   string
	DataDir string // 会话账本目录
	WorkDir string // 工作区绝对/相对路径（fs 工具的受控范围）
}

// ChatService 编排对话用例。
type ChatService struct {
	cfg     Config
	agent   *agent.Loop
	mu      sync.Mutex
	ledgers map[string]*session.Ledger
}

// NewChatService 装配编排层：provider → runtime → 注册表(fs) → agent（构造期注入，依赖不可变）。
func NewChatService(cfg Config) (*ChatService, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("chat service: data dir required")
	}
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, errors.New("chat service: base url and model required")
	}
	if cfg.WorkDir == "" {
		return nil, errors.New("chat service: work dir required")
	}
	prov := openaiprovider.New(openaiprovider.Options{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
	})
	// 为什么总预算 10min：编码任务的推理流可达数分钟；空闲看门狗（provider 内 60s）
	// 已覆盖挂起场景，总预算只防极端失控。
	rt := llm.NewChatRuntime(prov, llm.TimeoutBudget{Total: 10 * time.Minute})
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
	return &ChatService{
		cfg:     cfg,
		agent:   agent.NewLoop(rt, cfg.Model, registry),
		ledgers: make(map[string]*session.Ledger),
	}, nil
}

// Send 发送一条用户消息，返回流式块通道（恰好一个 EndReason 终态后关闭）。
func (s *ChatService) Send(ctx context.Context, sessionID, text string) (<-chan llm.StreamChunk, error) {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return nil, fmt.Errorf("open session ledger: %w", err)
	}
	return s.agent.Run(ctx, ledger, text)
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
