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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
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
	cfg  Config
	pool *channels.Pool   // 渠道池（多协议，含 Ability 索引）
	gw   *gateway.Gateway // 转发网关（选路/重试/协议分派），实现 llm.ProviderPort

	mu           sync.Mutex
	defaultModel string // 当前轮次使用的模型名（激活渠道的首个模型）
	ledgers      map[string]*session.Ledger
	// sessTools 是会话级受控工具集（0.2.36 审计 R1）：fs/shell/git/search 按
	// 会话持有、根由账本归属固定——不再用"全局单例 + 发送前切根"（并行竞态
	// 与误杀另一路 shell）。MCP/技能/扩展是共享单例，不随会话克隆。
	sessTools map[string]*sessionTools
	// revived 是本次启动从"自动禁用"恢复的渠道展示名（壳层写日志；空 = 无）。
	// 为什么留痕：恢复动作改变了用户上次看到的渠道状态，静默改变用户配置观感不可接受。
	revived []string

	// 审批闸门状态（ADR-0007，默认关闭）
	approvalTools    []string                       // 需要审批的工具名（空 = 关闭）
	approvalEmit     func(ApprovalEvent)            // 事件回调（壳层注入）
	pendingApprovals map[string]chan agent.Decision // 未决请求：ID → 答复通道

	// 文件变更确认（0.0.10）：write/replace 落盘前的人为确认（应用/跳过）。
	// pendingEdits: 确认 ID → 答复通道；editEmit 由壳层注入（chat:edit 事件）。
	pendingEdits map[string]chan bool
	editEmit     func(EditEvent)

	// 问答交互状态（ask_user，0.2.15）：与审批同构的"问 → 等 → 答"配对
	askEmit     func(AskEvent)         // 事件回调（壳层注入）
	pendingAsks map[string]chan string // 未决请求：ID → 答复通道

	// codexAuth 管理 ChatGPT 订阅账号的 OAuth 授权会话与 1455 回环监听（0.2.21）
	codexAuth   *codexauth.Manager
	codexClient *codexauth.Client

	extensions *catalog.Store
	skillTool  *exttools.SkillTool
	mcpTool    *exttools.MCPTool
	extManage  *exttools.ManageTool

	// running 标记正在跑轮次的会话（0.2.27）：同一会话的并发 Send 会在一份账本上
	// 交错写（Replay 顺序错乱）。前端有输入队列兜，后端必须有第二道防线。
	running map[string]struct{}
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
		running:          make(map[string]struct{}),
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
	// 扩展自管理（0.2.26）：模型可以自己增删 MCP/Skill。与设置面板走同一个
	// extensions.json 实例；保存后的钩子关闭旧 MCP 会话，让下一次调用按新配置拉起。
	s.extManage = exttools.NewManage(s.extensions, s.mcpTool, func() {
		if s.mcpTool != nil {
			s.mcpTool.Close()
		}
	})
	// 不再在启动时构建全局工具集（0.2.36 审计 R1）：受控工具按会话在首轮
	// 组装（根取账本归属 / 新会话取用户当前选择），启动只需要校验渠道池。
	s.sessTools = make(map[string]*sessionTools)

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
	if err := reg.Register(s.mcpTool); err != nil {
		return err
	}
	if s.extManage != nil {
		// 扩展自管理（0.2.26）：用户让 AI"加个 MCP/技能"时，模型自己完成配置
		return reg.Register(s.extManage)
	}
	return nil
}

// applyExtensionPreface 组装每步系统说明：技能/MCP 清单（exttools.Preface）+
// 三行环境事实（sessionFacts，0.0.06）。环境事实随会话根固定——本轮 root 已
// 定（账本归属或用户顶栏），每个执行步骤都带着走；绝不含任何密钥。
func (s *ChatService) applyExtensionPreface(ctx context.Context, ag *agent.Loop, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ag == nil {
		return nil
	}
	facts := sessionFacts(root)
	if s.extensions == nil {
		ag.SetPreface(facts)
		ag.SetPrefaceFn(func() string { return facts }) // 每步刷新语义一致（值固定）
		return nil
	}
	file, err := s.extensions.Load()
	if err != nil {
		return err
	}
	// 只告诉模型有什么、怎么调用。不在发消息时启动 MCP：用不用由模型决定。
	ag.SetPreface(exttools.Preface(file) + "\n\n" + facts)
	// 动态 preface（0.2.33）：每个执行步骤实时取——扩展在回合内被 ManageTool
	// 增删后，模型在后续步骤立即看到最新清单（添加当回合即可用，不必等下一轮）。
	ag.SetPrefaceFn(func() string {
		f, err := s.extensions.Load()
		if err != nil {
			return facts // 清单读取失败：至少保留环境事实，不打断回合
		}
		return exttools.Preface(f) + "\n\n" + facts
	})
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
// 顺序：先写盘成功、再关闭旧 MCP 会话——写失败时旧会话保持可用（不留半初始化观感），
// 关闭失败并入返回错误（不静默）。
func (s *ChatService) SaveExtensions(f catalog.File) error {
	if s.extensions == nil {
		return errors.New("扩展存储未初始化")
	}
	if err := s.extensions.Save(f); err != nil {
		return err
	}
	if s.mcpTool != nil {
		if err := s.mcpTool.Close(); err != nil {
			return fmt.Errorf("扩展已保存，但关闭旧 MCP 会话失败：%w", err)
		}
	}
	return nil
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
// watch 非空时把"等待审批/等待答复"计入零事件看门狗的活跃（防误杀，见 zeroEventWatch）。
func (s *ChatService) newAgentWith(model string, registry *tools.Registry, approver agent.Approver, sessionID string, watch *zeroEventWatch) *agent.Loop {
	// 为什么总预算 10min：编码任务的推理流可达数分钟；空闲看门狗（适配器内 60s）
	// 已覆盖挂起场景，总预算只防极端失控。
	rt := llm.NewChatRuntime(s.gw, llm.TimeoutBudget{Total: 10 * time.Minute})
	ag := agent.NewLoop(rt, model, registry)
	var asker agent.Asker = &uiAsker{svc: s, sessionID: sessionID}
	if watch != nil {
		asker = &watchAsker{inner: asker, watch: watch}
	}
	ag.SetAsker(asker) // 问答通道常开（无 UI 时模型收到引导性结果）
	if watch != nil && approver != nil {
		approver = &watchApprover{inner: approver, watch: watch}
	}
	ag.SetApprover(approver) // 审批器随轮注入，策略变更对下一轮生效
	return ag
}

// DeleteSession 删除会话及其账本文件。
// 打开中的账本必须先关闭：Windows 上句柄未释放时删除会失败（与旧实现 rename 失败同源）。
func (s *ChatService) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.running[sessionID]; busy {
		// 运行中删除会让正在写账本的轮次踩空（句柄关闭/文件被删），显式拒绝
		return errors.New("该会话正在运行：请先中断再删除")
	}
	if l, ok := s.ledgers[sessionID]; ok {
		if err := l.Close(); err != nil {
			return fmt.Errorf("关闭会话账本失败：%w", err)
		}
		delete(s.ledgers, sessionID)
	}
	// 会话级工具集一并清掉（0.2.36 审计 R1）：后台任务表随会话消失后
	// bg_status/bg_kill 已不可达，进程必须在这里收干净。
	if st, ok := s.sessTools[sessionID]; ok {
		if err := closeToolIfCloser(st.shell); err != nil {
			return fmt.Errorf("终止会话后台任务失败：%w", err)
		}
		delete(s.sessTools, sessionID)
	}
	return session.DeleteSession(s.cfg.DataDir, sessionID)
}

// Send 发送一条用户消息，返回流式块通道（恰好一个 EndReason 终态后关闭）。
// 多会话并行（0.2.25）：不同会话可以同时各跑各的轮次——每轮独立 agent、独立账本句柄，
// 互不串流。同一会话的并发发送由后端显式拒绝（0.2.27）：一份账本上交错写会让
// Replay 顺序错乱——此前注释宣称"Loop 的 phase 互斥兜底"，但每轮新建 Loop，
// 跨轮根本没有保护，只剩前端队列在兜。
// Send 发送一条用户消息，返回流式块通道（恰好一个 EndReason 终态后关闭）。
func (s *ChatService) Send(ctx context.Context, sessionID, text string) (<-chan llm.StreamChunk, error) {
	return s.SendWithAttachments(ctx, sessionID, text, nil)
}

// SendWithAttachments 发送带附件的用户消息（0.0.10）：附件先物化（校验/落位/记引用），
// 再走与 Send 相同的轮次链路（payload 带附件引用，derive 重建上下文时还原图片与内联内容）。
func (s *ChatService) SendWithAttachments(ctx context.Context, sessionID, text string, rawAtts []IncomingAttachment) (<-chan llm.StreamChunk, error) {
	atts, err := s.materializeAttachments(sessionID, rawAtts)
	if err != nil {
		return nil, err
	}
	return s.sendCore(ctx, sessionID, text, atts)
}

func (s *ChatService) sendCore(ctx context.Context, sessionID, text string, atts []session.UserAttachment) (<-chan llm.StreamChunk, error) {
	ledger, err := s.ledgerFor(sessionID) // 注意：先取账本（内部加锁），再读状态，避免自锁
	if err != nil {
		return nil, fmt.Errorf("open session ledger: %w", err)
	}
	// 会话归属根（0.2.36 审计 R1）：账本里记过（首个 workspace 事件）就用它，
	// 没记过（新会话）取用户当前顶栏选择的路径（可为空 = 纯对话，不套默认目录）。
	// 根随会话固定——不切全局、不动别的会话的工具集。
	root, owned := firstWorkspaceOfLedger(ledger)
	if !owned {
		root = s.Workspace()
	}
	// 幂等规范化：旧账本可能存过带尾斜杠/未解析的形式（0.2.36 审计 R5——
	// 同一目录的两种写法不能当成两个项目）
	root = normalizeWorkspace(root)
	// @ 文件引用（0.0.11）：消息文本里的 @相对路径 真正读成附件交给模型——
	// 此前 @ 只在输入框弹菜单、插入裸路径文本，模型不理会就是"没效果"。
	// 引用不存在/越界时静默保留原文（用户可能就在谈论 @ 符号本身）。
	atAtts, err := s.resolveAtReferences(sessionID, root, text)
	if err != nil {
		return nil, err // running 尚未注册，无需占位释放
	}
	atts = append(atts, atAtts...)
	// 锁内只取快照（模型名/注册表/审批器），构建与网络都在锁外做：
	// Send 是长调用（流式全程），持锁会卡死切换渠道/工作区等管理操作。
	s.mu.Lock()
	if _, busy := s.running[sessionID]; busy {
		s.mu.Unlock()
		return nil, errors.New("该会话已有进行中的回合：请等待完成或点中断后再发送")
	}
	s.running[sessionID] = struct{}{}
	model := s.defaultModel
	approver := s.approverFor(sessionID)
	// 会话级工具集：同会话复用（shell 任务表跨轮存活）；组装一轮的注册表
	st, err := s.ensureSessionTools(sessionID, root)
	if err != nil {
		delete(s.running, sessionID)
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	// 占位释放：之后每个提前返回都必须调用（漏一个会让该会话永久"忙"）
	release := func() {
		s.mu.Lock()
		delete(s.running, sessionID)
		s.mu.Unlock()
	}
	registry, err := s.assembleRegistry(st)
	if err != nil {
		release()
		return nil, err
	}
	if model == "" {
		release()
		return nil, errors.New("尚未配置模型渠道：请在设置中新增渠道并设为默认")
	}
	// 零事件看门狗（0.2.29）：Send 后 firstEventTimeout 内**没有任何可见活动**
	// （流式增量/工具卡/任务清单/等待审批/等待答复）就取消本轮——上游黑洞、
	// 代理 CONNECT 挂死这类零输出场景，流层空闲看门狗与响应头超时都管不到
	// （CONNECT 阶段不受 ResponseHeaderTimeout 约束是 Go Transport 的已知行为），
	// 实机表现是"发送后永久运行中，无任何反馈"。只对零事件生效：模型已在输出、
	// 工具已在执行、正在等用户答复的轮次都算活动，绝不误杀长任务。
	watch := newZeroEventWatch(firstEventTimeout)
	runCtx, cancelRun := context.WithCancel(ctx)
	// 纪律（0.2.29 两次实测教训）：Send 是"返回通道即返回"的长调用——defer 在这里
	// 一律等于"立刻执行"：cancelRun 不能 defer（会当场取消整轮），watchStopped
	// 也不能 defer close（看门狗会在 Send 返回后立刻失去值守）。两者的生命周期
	// 都到"流收尾"为止：由转发 goroutine（或流未建立的错误路径）显式收尾。
	watchStopped := make(chan struct{})
	ag := s.newAgentWith(model, registry, approver, sessionID, watch)
	// 以下三个前置失败路径都在看门狗/分发 goroutine 启动之前：就地释放 runCtx
	//（看门狗未启动，无需 close(watchStopped)）
	if err := s.applyExtensionPreface(ctx, ag, root); err != nil {
		release()
		cancelRun()
		return nil, err
	}
	// ChatGPT 订阅凭证临期先自动续期（失败明确阻断：过期凭证发出去只会得到难解读的 401）
	if err := s.ensureCodexFresh(ctx); err != nil {
		release()
		cancelRun()
		return nil, err
	}
	// 记录本轮工作区快照：侧栏按空间分组与发送归属都取账本**首个** workspace
	// 事件（0.2.36 审计 R1）——新会话在这里落下它的归属（= 用户当时顶栏路径），
	// 之后每轮记的是同一个 root，归属语义不受中途切换影响。
	if _, err := ledger.Append(session.EventWorkspace, map[string]string{"path": root}); err != nil {
		release()
		cancelRun()
		return nil, fmt.Errorf("记录工作区快照失败：%w", err)
	}
	stream, err := ag.Run(runCtx, ledger, text, atts...)
	if err != nil {
		release()
		cancelRun()         // 流未建立：本轮 ctx 资源就地释放
		close(watchStopped) // 看门狗退场（与转发 goroutine 的收尾互斥：此路径不启动转发）
		if watch.timedOut.Load() {
			// 看门狗触发的取消：流还没建立就断了——返回明确超时错误而不是裸的 context canceled
			return nil, errors.New(watch.timeoutMessage())
		}
		return nil, err
	}
	// 分发层：stream（上游流）与 inject（看门狗注入）双源，先到先得。
	// 为什么不直接转发 stream 再做终态映射：上游黑洞/代理挂死下，gateway 建流
	// 失败路径对取消的响应不可假设（实测可无限期不发终态）——看门狗触发后
	// **直接注入终态**，对上游任何行为零依赖。被打断的上游由 cancelRun 收尾
	//（turn 的 forward/emitTerminal 都有 ctx.Done 逃生，不会泄漏 goroutine）。
	out := make(chan llm.StreamChunk)
	inject := make(chan llm.StreamChunk, 1)
	go func() { // 看门狗：零事件超时 → 注入明确终态（用户没点中断，"已中断"文案会让人困惑）
		// 用 watch.window 快照而非包级变量：本 goroutine 可能活到测试收尾之后，
		// 直读会被测试注入/恢复的全局写竞态击中（CI -race 抓到，详见 watchdog.go 注）。
		t := time.NewTicker(watch.window / 4)
		defer t.Stop()
		for {
			select {
			case <-watchStopped:
				return
			case <-t.C:
				// 滑动窗口判定（0.0.07）：未挂起且距最近一次可见活动超过阈值。
				// mark 刷新起点、pause/resume 挂起等待——不再有"一次性豁免"。
				if !watch.paused.Load() && time.Since(watch.start.Load().(time.Time)) > watch.window {
					watch.timedOut.Store(true)
					cancelRun() // 尽力打断上游（HTTP/工具读全部响应 ctx）
					inject <- llm.StreamChunk{EndReason: llm.EndError, Err: errors.New(watch.timeoutMessage())}
					return
				}
			}
		}
	}()
	go func() {
		defer close(out)
		defer release()
		defer cancelRun()         // 流收尾后释放本轮 ctx 资源（防泄漏；绝不提前取消）
		defer close(watchStopped) // 流收尾：看门狗退场
		for {
			select {
			case c, ok := <-stream:
				if !ok {
					return // 上游流关闭（契约保证终态已发）
				}
				watch.mark() // 任何块（增量/工具/清单）都算活动
				out <- c
				if c.EndReason != llm.EndNone {
					return
				}
			case c := <-inject:
				out <- c
				return
			}
		}
	}()
	return out, nil
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
	// 撤销元数据（0.0.07）：CallID 定位账本事件，HasUndo 决定是否显示"恢复写入前"。
	// 旧全文不投影给前端——恢复走后端 RestoreToolWrite（防覆盖用户改动的比对在那边）。
	CallID   string `json:"callId,omitempty"`
	HasUndo  bool   `json:"hasUndo,omitempty"`
	UndoPath string `json:"undoPath,omitempty"`
	UndoNote string `json:"undoNote,omitempty"`
	// 附件（0.0.10）：用户消息的图片/文件（重放后仍能显示；图片带 DataURL）
	Attachments []ChatAttachment `json:"attachments,omitempty"`
	// 问答卡（role="ask"）：问题与选项来自 tool_call 参数，答案在 Content
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// ChatAttachment 是重放后气泡里仍可显示的附件引用（0.0.10）。
// 图片带 DataURL（重放时读附件文件生成）；文件带名字与路径。
type ChatAttachment struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType,omitempty"`
	DataURL   string `json:"dataUrl,omitempty"`
	Path      string `json:"path,omitempty"`
	Inline    string `json:"inline,omitempty"`
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
	// 任务清单生命周期（0.0.06 实测反馈"任务清单一直显示旧的"）：todo 卡只在
	// 它仍是"当前任务"时投影——之后如果出现了新的用户消息（= 新任务开始），
	// 旧清单从重放投影中移除。否则重启/切回会话时，上一任务半程中断的清单会
	// 在悬浮件复活（"全部完成退场"的前端规则只兜得住全 done 的快照）。
	evSeq := 0
	lastUserSeq := -1
	todoSeq := map[int]int{} // out 索引 -> 该卡最新快照的事件序号
	flushSegment := func() {
		if strings.TrimSpace(segText.String()) != "" || segThinking.String() != "" {
			out = append(out, ChatMessage{Role: "assistant", Content: segText.String(), Thinking: segThinking.String()})
		}
		segText.Reset()
		segThinking.Reset()
	}
	err = ledger.Replay(func(ev session.Event) error {
		evSeq++
		switch ev.Kind() {
		case session.EventUserMessage:
			var p struct {
				Text        string                   `json:"text"`
				Attachments []session.UserAttachment `json:"attachments"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			msg := ChatMessage{Role: "user", Content: p.Text}
			// 附件（0.0.10）：重放后气泡仍能显示图片/文件名——图片带 DataURL
			//（重放时读附件文件），文件带名字与路径
			for _, a := range p.Attachments {
				ca := ChatAttachment{Name: a.Name, MediaType: a.MediaType, Path: a.Path, Inline: a.Inline, Kind: a.Kind}
				if a.Kind == "image" {
					if b, err := os.ReadFile(ledger.ResolveAttachmentPath(a.Path)); err == nil {
						ca.DataURL = "data:" + a.MediaType + ";base64," + base64.StdEncoding.EncodeToString(b)
					}
				}
				msg.Attachments = append(msg.Attachments, ca)
			}
			out = append(out, msg)
			lastUserSeq = evSeq
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
				Undo    *struct {
					Path      string `json:"path"`
					OldExists bool   `json:"old_exists"`
					NewSHA256 string `json:"new_sha256"`
				} `json:"undo"`
				UndoNote string `json:"undo_note"`
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
				CallID: p.ID, HasUndo: p.Undo != nil,
			})
			if p.Undo != nil {
				out[len(out)-1].UndoPath = p.Undo.Path
			}
			out[len(out)-1].UndoNote = p.UndoNote
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
					todoSeq[i] = evSeq
					replaced = true
					break
				}
			}
			if !replaced {
				out = append(out, ChatMessage{Role: "todo", Content: string(b)})
				todoSeq[len(out)-1] = evSeq
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
	// 生命周期收尾：todo 之后出现过新的用户消息 → 该清单已不属于当前任务，移除
	if lastUserSeq >= 0 {
		var dead []int
		for idx, seq := range todoSeq {
			if seq < lastUserSeq {
				dead = append(dead, idx)
			}
		}
		sort.Ints(dead)
		for i := len(dead) - 1; i >= 0; i-- {
			out = append(out[:dead[i]], out[dead[i]+1:]...)
		}
	}
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
		if err := s.mcpTool.Close(); err != nil && firstErr == nil {
			firstErr = err // 关闭失败可见（不再静默：坏连接必须能被替换）
		}
	}
	if s.codexAuth != nil {
		if err := s.codexAuth.Close(); err != nil && firstErr == nil {
			firstErr = err // 释放 1455 回环监听；不盖掉前面的 MCP 关闭错误（0.2.35 审计#3）
		}
	}
	// 各会话的 shell 后台任务随应用一起收（0.2.36 审计 R1：按会话持有后逐个收）：
	// 此前只关 MCP/Codex/账本，后台命令成为孤儿进程——与"退出后残留 node.exe"同类。
	s.mu.Lock()
	for _, st := range s.sessTools {
		if err := closeToolIfCloser(st.shell); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.mu.Unlock()
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
