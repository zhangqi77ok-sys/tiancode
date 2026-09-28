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
	"os"
	"strings"
	"sync"
	"time"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
	"tiancode/internal/platform/adaptors"
	"tiancode/internal/platform/catalog"
	"tiancode/internal/platform/channels"
	"tiancode/internal/platform/codexauth"
	"tiancode/internal/platform/configfile"
	"tiancode/internal/platform/exttools"
	"tiancode/internal/platform/gateway"
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
	// ExtensionsPath 是 MCP/Skill 清单；缺省 %APPDATA%\tiancode\extensions.json。
	ExtensionsPath string
}

// ChatService 编排对话用例。
type ChatService struct {
	cfg      Config
	pool     *channels.Pool   // 渠道池（多协议，含 Ability 索引）
	gw       *gateway.Gateway // 转发网关（选路/重试/协议分派），实现 llm.ProviderPort
	registry *tools.Registry

	mu           sync.Mutex
	defaultModel string // 当前轮次使用的模型名（激活渠道的首个模型）
	ledgers      map[string]*session.Ledger
	// revived 是本次启动从"自动禁用"恢复的渠道展示名（壳层写日志；空 = 无）。
	// 为什么留痕：恢复动作改变了用户上次看到的渠道状态，静默改变用户配置观感不可接受。
	revived []string

	// 审批闸门状态（ADR-0007，默认关闭）
	approvalTools    []string                       // 需要审批的工具名（空 = 关闭）
	approvalEmit     func(ApprovalEvent)            // 事件回调（壳层注入）
	pendingApprovals map[string]chan agent.Decision // 未决请求：ID → 答复通道

	// 问答交互状态（ask_user，0.2.15）：与审批同构的"问 → 等 → 答"配对
	askEmit     func(AskEvent)         // 事件回调（壳层注入）
	pendingAsks map[string]chan string // 未决请求：ID → 答复通道

	// codexAuth 管理 ChatGPT 订阅账号的 OAuth 授权会话与 1455 回环监听（0.2.21）
	codexAuth   *codexauth.Manager
	codexClient *codexauth.Client

	extensions *catalog.Store
	skillTool  *exttools.SkillTool
	mcpTool    *exttools.MCPTool
}

// NewChatService 装配编排层：渠道存储 → 工具注册表 → 按激活渠道构建 agent。
// 装配顺序刻意让"无渠道"成为合法状态：用户可以先把应用跑起来，再在设置里添加渠道
// （否则首次启动会被配置硬门槛挡死——这正是旧实现"装完点开没反应"的根因之一）。
func NewChatService(cfg Config) (*ChatService, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("chat service: data dir required")
	}
	// 工作区可为空 = 纯对话模式（默认态：无本地文件工具，只保留 todo/ask 交互工具）。
	// 非空必须是已存在的目录：fs/shell/git 的受控根都指向它，
	// 不存在时"启动看似正常、首次读写才报错"最难排查（实机事故）。
	if cfg.WorkDir != "" {
		if info, err := os.Stat(cfg.WorkDir); err != nil {
			return nil, fmt.Errorf("工作区不可用（%s）：请把配置里的 workspace 改成已存在的目录", cfg.WorkDir)
		} else if !info.IsDir() {
			return nil, fmt.Errorf("工作区不是目录（%s）", cfg.WorkDir)
		}
	}
	if cfg.ChannelsPath == "" {
		cfg.ChannelsPath = channels.DefaultPath()
	}
	pool := channels.NewPool(cfg.ChannelsPath)
	if err := pool.Load(); err != nil {
		return nil, err
	}
	reg := adaptors.NewRegistry()
	if err := reg.Register(adaptors.OpenAI{}); err != nil {
		return nil, err
	}
	if err := reg.Register(adaptors.Anthropic{}); err != nil {
		return nil, err
	}
	if err := reg.Register(adaptors.Codex{}); err != nil {
		return nil, err
	}
	codexClient := codexauth.NewClient(nil)
	codexClient.ProxyFunc = pool.Proxy // 授权端点与推理端点共用同一出口（全局代理）
	s := &ChatService{
		cfg:              cfg,
		pool:             pool,
		gw:               gateway.New(pool, reg),
		codexAuth:        codexauth.NewManager(codexClient),
		codexClient:      codexClient,
		ledgers:          make(map[string]*session.Ledger),
		pendingApprovals: make(map[string]chan agent.Decision),
		pendingAsks:      make(map[string]chan string),
	}

	// 工具装配：fs（读写/替换）、shell（命令，默认 120s 超时）、git（只读查看）
	s.extensions = catalog.New(cfg.ExtensionsPath)
	s.skillTool = exttools.NewSkill(func() catalog.File {
		f, err := s.extensions.Load()
		if err != nil {
			return catalog.File{}
		}
		return f
	})
	s.mcpTool = exttools.NewMCP(s.skillTool.Load)
	registry, err := newRegistry(cfg.WorkDir)
	if err != nil {
		return nil, err
	}
	if err := s.attachExtensions(registry); err != nil {
		return nil, err
	}
	s.registry = registry

	if err := s.bootstrapChannels(); err != nil {
		return nil, err
	}
	return s, nil
}

// bootstrapChannels 加载渠道池；首次运行从 Config 迁移（C-CH-1）。
// 为什么要迁移：老用户升级不该被迫二次配置（config.json 里已有网关信息）。
func (s *ChatService) bootstrapChannels() error {
	// 审批策略（用户设置）与渠道无关，必须**在任一提前返回之前**恢复：
	// 曾放在 active 检查之后，导致"没有渠道时策略丢失"（测试当场抓到）。
	s.approvalTools = s.pool.ApprovalTools()
	// 自动禁用是运行期健康标记（网络抖动/上游 5xx 都会置位）：进程重启即重新给一次机会，
	// 否则单次瞬时故障会让应用永久无渠道可用——用户只看到"发消息没反应"（实机事故）。
	revived, err := s.pool.ReviveAutoDisabled()
	if err != nil {
		return fmt.Errorf("恢复自动禁用渠道失败：%w", err)
	}
	s.revived = revived
	model, ok := s.pool.DefaultModel()
	if !ok {
		migrated, ok2 := channels.MigrateFromConfig(configfile.File{
			BaseURL: s.cfg.BaseURL, APIKey: s.cfg.APIKey, Model: s.cfg.Model,
		})
		if !ok2 {
			return nil // 无渠道且无迁移来源：合法状态，UI 引导添加
		}
		if err := s.pool.Save(migrated); err != nil {
			return fmt.Errorf("persist migrated channel: %w", err)
		}
		if err := s.pool.SetActive(migrated.ID); err != nil {
			return err
		}
		model = migrated.Models[0]
	}
	return s.activate(model)
}

// RevivedChannels 返回本次启动从"自动禁用"恢复的渠道展示名（供壳层写启动日志；空 = 无）。
func (s *ChatService) RevivedChannels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.revived))
	copy(out, s.revived)
	return out
}

func (s *ChatService) attachExtensions(reg *tools.Registry) error {
	if s.skillTool == nil || s.mcpTool == nil {
		return nil
	}
	if err := reg.Register(s.skillTool); err != nil {
		return err
	}
	return reg.Register(s.mcpTool)
}

func (s *ChatService) applyExtensionPreface(ctx context.Context, ag *agent.Loop) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.extensions == nil || ag == nil {
		return nil
	}
	file, err := s.extensions.Load()
	if err != nil {
		return err
	}
	// 只告诉模型有什么、怎么调用。不在发消息时启动 MCP：用不用由模型决定。
	ag.SetPreface(exttools.Preface(file))
	return nil
}

// Extensions 返回 MCP 与 Skill 清单。
func (s *ChatService) Extensions() (catalog.File, error) {
	if s.extensions == nil {
		return catalog.File{}, nil
	}
	return s.extensions.Load()
}

// SaveExtensions 保存清单。下一轮对话会把启用项告诉模型。
func (s *ChatService) SaveExtensions(f catalog.File) error {
	if s.extensions == nil {
		return errors.New("扩展存储未初始化")
	}
	if s.mcpTool != nil {
		s.mcpTool.Close()
	}
	return s.extensions.Save(f)
}

// activate 记录当前默认模型。这是唯一与"具体渠道"耦合的装配点：
// 多协议网关承担选路/重试/协议分派，这里只需要知道用哪个模型名发问。
//
// 为什么不再在这里构建 agent：agent.Loop 的 phase 是"单轮互斥"锁（ADR-0005），
// 整应用共用一个 Loop 就意味着同时只能跑一轮——多会话并行对话（0.2.25）要求
// 每轮独立。改为每次 Send 现场构建（见 newAgent），循环除 phase 外无状态，
// 会话历史都在账本里，按轮新建只是一个小结构体。
func (s *ChatService) activate(model string) error {
	s.defaultModel = model
	return nil
}

// newAgentWith 用锁内取好的快照构建这一轮独立的 ReAct 循环（多会话并行的前提，
// 见 activate 注释）。sessionID 随轮注入审批器/问答器：后台会话的"要审批/在提问"
// 事件必须能归属到它自己的会话，否则会错插进当前正在看的会话里。
func (s *ChatService) newAgentWith(model string, registry *tools.Registry, approver agent.Approver, sessionID string) *agent.Loop {
	// 为什么总预算 10min：编码任务的推理流可达数分钟；空闲看门狗（适配器内 60s）
	// 已覆盖挂起场景，总预算只防极端失控。
	rt := llm.NewChatRuntime(s.gw, llm.TimeoutBudget{Total: 10 * time.Minute})
	ag := agent.NewLoop(rt, model, registry)
	ag.SetAsker(&uiAsker{svc: s, sessionID: sessionID}) // 问答通道常开（无 UI 时模型收到引导性结果）
	ag.SetApprover(approver)                            // 审批器随轮注入，策略变更对下一轮生效
	return ag
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
// 多会话并行（0.2.25）：不同会话可以同时各跑各的轮次——每轮独立 agent、独立账本句柄，
// 互不串流；同一会话的并发发送仍由前端排队（同一 Loop 的 phase 互斥兜底）。
func (s *ChatService) Send(ctx context.Context, sessionID, text string) (<-chan llm.StreamChunk, error) {
	ledger, err := s.ledgerFor(sessionID) // 注意：先取账本（内部加锁），再读状态，避免自锁
	if err != nil {
		return nil, fmt.Errorf("open session ledger: %w", err)
	}
	// 锁内只取快照（模型名/注册表/审批器），构建与网络都在锁外做：
	// Send 是长调用（流式全程），持锁会卡死切换渠道/工作区等管理操作。
	s.mu.Lock()
	model := s.defaultModel
	registry := s.registry
	approver := s.approverFor(sessionID)
	s.mu.Unlock()
	if model == "" {
		return nil, errors.New("尚未配置模型渠道：请在设置中新增渠道并设为默认")
	}
	ag := s.newAgentWith(model, registry, approver, sessionID)
	if err := s.applyExtensionPreface(ctx, ag); err != nil {
		return nil, err
	}
	// ChatGPT 订阅凭证临期先自动续期（失败明确阻断：过期凭证发出去只会得到难解读的 401）
	if err := s.ensureCodexFresh(ctx); err != nil {
		return nil, err
	}
	// 记录本轮工作区快照：侧栏按空间分组取账本首个 workspace 事件，
	// 会话归属 = 首次发送时的工作区（每轮都记，归属语义不受中途切换影响）。
	if _, err := ledger.Append(session.EventWorkspace, map[string]string{"path": s.Workspace()}); err != nil {
		return nil, fmt.Errorf("记录工作区快照失败：%w", err)
	}
	return ag.Run(ctx, ledger, text)
}

// ListSessions 返回全部会话 ID。
func (s *ChatService) ListSessions() ([]string, error) {
	return session.ListSessions(s.cfg.DataDir)
}

// ChatMessage 是投影给前端的已确认消息（user/assistant 锚点与 tool 卡）。
type ChatMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ToolName string `json:"toolName,omitempty"`
	Status   string `json:"status,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	// 工具卡语义标签与结构化 diff（历史恢复与实时事件同构）：
	// 此前 Replay 不投影 Diff，历史工具卡丢失"变更预览"（实时有、重启后消失）。
	Title string `json:"title,omitempty"`
	Op    string `json:"op,omitempty"`
	Diff  string `json:"diff,omitempty"`
	// 问答卡（role="ask"）：问题与选项来自 tool_call 参数，答案在 Content
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// Replay 把会话账本投影为已确认消息列表，供前端恢复历史。
// 投影 user_message / tool_result / assistant_message / todo。
// 思考与文本按"轮次"分段（ReAct 叙事）：以 tool_call 为边界 flush——
// 每轮的思考与中间文本归属产生它们的轮次，不再全部挂到最后一条 assistant。
func (s *ChatService) Replay(sessionID string) ([]ChatMessage, error) {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return nil, err
	}
	var out []ChatMessage
	var segText, segThinking strings.Builder
	// ask_user 的问答卡需要 tool_call（问题/选项）与 tool_result（答案）跨事件配对
	type pendingAsk struct {
		question string
		options  []string
	}
	askCalls := map[string]pendingAsk{}
	flushSegment := func() {
		if strings.TrimSpace(segText.String()) != "" || segThinking.String() != "" {
			out = append(out, ChatMessage{Role: "assistant", Content: segText.String(), Thinking: segThinking.String()})
		}
		segText.Reset()
		segThinking.Reset()
	}
	err = ledger.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventUserMessage:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			out = append(out, ChatMessage{Role: "user", Content: p.Text})
		case session.EventAssistantDelta:
			var p struct {
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			segText.WriteString(p.Text)
			segThinking.WriteString(p.Thinking)
		case session.EventToolCall:
			flushSegment()
			var p struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			if p.Name == "ask_user" {
				var a struct {
					Question string   `json:"question"`
					Options  []string `json:"options"`
				}
				if err := json.Unmarshal([]byte(p.Arguments), &a); err != nil {
					return err
				}
				askCalls[p.ID] = pendingAsk{question: a.Question, options: a.Options}
			}
		case session.EventToolResult:
			var p struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Content string `json:"content"`
				IsError bool   `json:"is_error"`
				Title   string `json:"title"`
				Op      string `json:"op"`
				Diff    string `json:"diff"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			if p.Name == "todo" {
				return nil // 任务卡由 EventTodo 承载（单卡原地更新），工具结果卡不重复投影
			}
			if p.Name == "ask_user" {
				// 问答卡：问题/选项取配对的 tool_call，答案即结果内容；孤儿结果用 Title 兜底
				question, options := p.Title, []string(nil)
				if pa, ok := askCalls[p.ID]; ok {
					question, options = pa.question, pa.options
					delete(askCalls, p.ID)
				}
				out = append(out, ChatMessage{
					Role: "ask", Content: p.Content, Question: question, Options: options,
				})
				return nil
			}
			st := "success"
			if p.IsError {
				st = "error"
			}
			out = append(out, ChatMessage{
				Role: "tool", Content: p.Content, ToolName: p.Name, Status: st,
				Title: p.Title, Op: p.Op, Diff: p.Diff,
			})
		case session.EventTodo:
			var p struct {
				Items []struct {
					Text   string `json:"text"`
					Status string `json:"status"`
				} `json:"items"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			b, err := json.Marshal(p.Items)
			if err != nil {
				return err
			}
			// 单卡原地更新：保留首次出现位置，内容取最新快照（与实时 UI 语义一致）
			replaced := false
			for i := range out {
				if out[i].Role == "todo" {
					out[i].Content = string(b)
					replaced = true
					break
				}
			}
			if !replaced {
				out = append(out, ChatMessage{Role: "todo", Content: string(b)})
			}
		case session.EventAssistantMsg:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			segText.Reset() // 锚点文本为准（与 delta 累计应等价），思考用累计值
			out = append(out, ChatMessage{Role: "assistant", Content: p.Text, Thinking: segThinking.String()})
			segThinking.Reset()
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
	var firstErr error
	// MCP 子进程必须随应用一起收掉：它们是 npx/node 链（cmd.exe → node.exe → server），
	// 留着就是任务管理器里一串无主的 node.exe（每个服务器 2~4 个进程，Playwright 更重）。
	// 此前只关了账本与 codex 监听，实测应用退出后 node/cmd 仍在跑——用户看到的是
	// "关掉应用还有一堆 node 进程"，且下次启动会再拉一份，越积越多。
	if s.mcpTool != nil {
		s.mcpTool.Close()
	}
	if s.codexAuth != nil {
		firstErr = s.codexAuth.Close() // 释放 1455 回环监听（失败向上传播）
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, l := range s.ledgers {
		if err := l.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.ledgers, id)
	}
	return firstErr
}
