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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
	"tiancode/internal/platform/adaptors"
	"tiancode/internal/platform/applog"
	"tiancode/internal/platform/browsertool"
	"tiancode/internal/platform/catalog"
	"tiancode/internal/platform/channels"
	"tiancode/internal/platform/codexauth"
	"tiancode/internal/platform/configfile"
	"tiancode/internal/platform/exttools"
	"tiancode/internal/platform/gateway"
	"tiancode/internal/platform/memory"
	"tiancode/internal/platform/tones"
	"tiancode/internal/platform/webfetch"
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
	// TonesPath 是语气设置；缺省 %APPDATA%\tiancode\tones.json（第 8 批）。
	TonesPath string
	// MemoryPath 是两级长期记忆目录；缺省 %APPDATA%\tiancode\memory（0.0.19）。
	// 可注入是为测试隔离（同进程多实例不互相污染，绝不读写用户真实记忆）。
	MemoryPath string
}

// ChatService 编排对话用例。
type ChatService struct {
	cfg  Config
	pool *channels.Pool   // 渠道池（多协议，含 Ability 索引）
	gw   *gateway.Gateway // 转发网关（选路/重试/协议分派），实现 llm.ProviderPort

	mu           sync.Mutex
	defaultModel string // 当前轮次使用的模型名（激活渠道的首个模型）
	ledgers      map[string]*session.Ledger
	// sessMu 是 sessTools 的专用锁（第二轮体检 R2：三个入口曾不持锁直呼
	// ensureSessionTools，与 Send/DeleteSession 的 map 读写构成并发读写竞争）。
	// 为什么不复用 mu：Send 持 mu 期间会调 ensureSessionTools，Go 互斥锁不可
	// 重入，复用即自锁。锁序纪律：恒为 mu → sessMu，任何持 sessMu 的路径
	// （ensureSessionTools/BgTasksSnapshot）不得再取 mu。
	sessMu    sync.Mutex
	sessTools map[string]*sessionTools
	// browser 是进程级共享无头浏览器（惰性启动）：每个会话从它拿独立 tab，
	// 并行会话互不串页面状态；进程与临时目录在 Close 统一收。
	browser *browsertool.Pool
	// webFetch 是共享读网页工具（不依赖工作区根，纯对话也可用）：
	// 代理配置每次执行从渠道池实时取，与 gateway 同源（改设置即生效）。
	webFetch *webfetch.Tool
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

	// 工作区检查命令状态（第 8 批）：最近一次结果（供界面列表与下一轮附注共用）
	// + "已有一次在跑就跳过"的守卫。都在内存：检查结果不是账本事实。
	checkMu      sync.Mutex
	checkRunning map[string]bool
	lastChecks   map[string]CheckResult
	checkEmit    func(CheckResult)

	// codexAuth 管理 ChatGPT 订阅账号的 OAuth 授权会话与 1455 回环监听（0.2.21）
	codexAuth   *codexauth.Manager
	codexClient *codexauth.Client

	extensions *catalog.Store
	skillTool  *exttools.SkillTool
	mcpTool    *exttools.MCPTool
	extManage  *exttools.ManageTool
	tones      *tones.Store  // 语气设置（第 8 批）：每轮拼系统提示时读一次
	memory     *memory.Store // 两级长期记忆（0.0.19）：memory 工具读写 + 每轮系统提示注入

	// running 标记正在跑轮次的会话（0.2.27）：同一会话的并发 Send 会在一份账本上
	// 交错写（Replay 顺序错乱）。前端有输入队列兜，后端必须有第二道防线。
	running map[string]struct{}

	// closeReqMu/closeReqAt 给"关窗拦截"防抖（0.0.21）：用户连点 X 或确认框开着
	// 再点 X 时，事件不重发（前端确认框不堆叠）。窗口大小不追求精确。
	closeReqMu sync.Mutex
	closeReqAt time.Time

	// Replay 分页缓存（0.3 尾屏优先，实现见 replay_service.go）：全量投影按会话
	// 缓存，键 = 账本序号水位（LastSeq 没动 = 投影没变）——向上翻页不再每页重扫。
	replayMu    sync.Mutex
	replayCache map[string]replayCacheEntry

	// 用户命令行执行中（0.0.21 关窗确认补洞）：RunUserCommand 不走 Send 轮次、
	// 不进 running 集合，但它的审批等待与命令执行同样是"关窗会打断的事"——
	// 实机实测抓到：审批等待中点 X 应用直接退出。原子标志，进入/退出各一次。
	userCmdActive atomic.Bool
}

// AnyRunning 报告是否还有"关窗会打断的事"（0.0.21 关窗确认的判据）：
// 任一会话在跑轮次（写账本/调工具），或用户命令行在审批等待/执行中——
// 后者是实机实测抓到的漏判路径（RunUserCommand 不经过 Send 轮次）。
func (s *ChatService) AnyRunning() bool {
	s.mu.Lock()
	running := len(s.running) > 0
	s.mu.Unlock()
	return running || s.userCmdActive.Load()
}

// ShouldConfirmClose 报告这次关窗是否需要先问用户：有轮次在跑才拦；
// 1 秒内的重复关窗不重发事件（确认框开着时连点 X 不堆叠）。
func (s *ChatService) ShouldConfirmClose() bool {
	if !s.AnyRunning() {
		return false
	}
	s.closeReqMu.Lock()
	defer s.closeReqMu.Unlock()
	if time.Since(s.closeReqAt) < time.Second {
		return false
	}
	s.closeReqAt = time.Now()
	return true
}

// NewChatService 装配编排层：渠道存储 → 工具注册表 → 按激活渠道构建 agent。
// 装配顺序刻意让"无渠道"成为合法状态：用户可以先把应用跑起来，再在设置里添加渠道
// （否则首次启动会被配置硬门槛挡死——这正是旧实现"装完点开没反应"的根因之一）。
func NewChatService(cfg Config) (*ChatService, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("chat service: data dir required")
	}
	// 内部文件日志（0.0.09 用户要求）：排障不能只靠截图猜。按天轮转、保留 7 天，
	// 关键链路埋点（轮次/上游请求/看门狗/审批问答）——设置页可一键打开日志目录。
	applog.SetDir(filepath.Join(cfg.DataDir, "logs"))
	applog.Infof("chatservice init datadir=%s", cfg.DataDir)
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
		replayCache:      make(map[string]replayCacheEntry),
	}

	// 工具装配：fs（读写/替换）、shell（命令，默认 120s 超时）、git（只读查看）
	s.extensions = catalog.New(cfg.ExtensionsPath)
	s.tones = tones.New(cfg.TonesPath)
	s.memory = memory.NewStore(cfg.MemoryPath)
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
	// 浏览器进程惰性：首个会话首次 open 才拉起。截图根 = 数据目录下的
	// browser-shots（与 ReadBrowserShot 同一取法，相对路径才有同一基准）。
	s.browser = browsertool.NewPoolAt(browsertool.ShotRootUnder(cfg.DataDir))
	// 读网页走与网关同源的全局代理（传函数不传值：代理设置改动无需重建服务）
	s.webFetch = webfetch.New(s.pool.Proxy)

	if err := s.bootstrapChannels(); err != nil {
		return nil, err
	}
	return s, nil
}

// bootstrapChannels 加载渠道池；首次运行从 Config 迁移（C-CH-1）。
// 为什么要迁移：老用户升级不该被迫二次配置（config.json 里已有网关信息）。
// EnsureBuiltinMCP 幂等补齐内置 MCP（缺才补，同名用户配置不劫持）。
// 为什么由入口显式调用而不放构造里：构造是热路径（测试大量构造且未必隔离
// ExtensionsPath），写盘副作用会污染真机数据；失败上抛——清单写不进去属于
// 启动期持久化故障，静默继续会变成"内置能力看起来有其实没有"。
func (s *ChatService) EnsureBuiltinMCP() error {
	if s.extensions == nil {
		return errors.New("扩展存储未初始化")
	}
	return s.extensions.EnsureBuiltin()
}

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
// 三行环境事实（sessionFacts，0.0.06）+ 语气（tones，第 8 批）。环境事实随会话根
// 固定——本轮 root 已定（账本归属或用户顶栏），每个执行步骤都带着走；绝不含任何密钥。
func (s *ChatService) applyExtensionPreface(ctx context.Context, ag *agent.Loop, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ag == nil {
		return nil
	}
	facts := sessionFacts(root)
	// 内置网页读取进系统说明：读网页正文是模型此前缺失的能力（查文档/查报错是
	// 硬伤），能力边界与参数由 webfetch 包自己写（Preface）。拼进 facts 的头部，
	// 下面所有组合点自然都带上；内容是常量，不破坏 prompt cache 幂等。
	facts = webfetch.Preface() + "\n\n" + facts
	// 语气段每轮读一次并快照进本轮提示：回合内改设置不影响这一轮（逐字相同是
	// prompt cache 与"文本未变不替换"的前提，改动下一轮生效）。读不出来显式阻断——
	// 静默当成"没有语气"，用户会以为自己是照设置回答的。
	tone, err := s.toneSection()
	if err != nil {
		return err
	}
	// 记忆段（0.0.19）：两级长期记忆每轮读一次并快照进本轮提示（与语气同一纪律——
	// 回合内模型经 memory 工具改记忆，不改变这一轮已注入的内容，下一轮生效）。
	// 没有任何记忆 = 空串（整段不出现，不占上下文）。读取失败显式阻断：
	// 静默当成"没有记忆"，用户以为模型记得的事它其实忘了，比没有记忆更糟。
	mem, err := s.memorySection(root)
	if err != nil {
		return err
	}
	if mem != "" {
		if tone != "" {
			tone = tone + "\n\n" + mem
		} else {
			tone = mem
		}
	}
	if s.extensions == nil {
		ag.SetPreface(facts + "\n\n" + tone)
		ag.SetPrefaceFn(func() string { return facts + "\n\n" + tone }) // 每步刷新语义一致（值固定）
		return nil
	}
	file, err := s.extensions.Load()
	if err != nil {
		return err
	}
	// 只告诉模型有什么、怎么调用。不在发消息时启动 MCP：用不用由模型决定。
	ag.SetPreface(exttools.Preface(file) + "\n\n" + facts + "\n\n" + tone)
	// 动态 preface（0.2.33）：每个执行步骤实时取——扩展在回合内被 ManageTool
	// 增删后，模型在后续步骤立即看到最新清单（添加当回合即可用，不必等下一轮）。
	// 语气段不参与实时刷新（用上面的快照）：同一回合里它必须逐字不变。
	ag.SetPrefaceFn(func() string {
		f, err := s.extensions.Load()
		if err != nil {
			return facts + "\n\n" + tone // 清单读取失败：至少保留环境事实与语气，不打断回合
		}
		return exttools.Preface(f) + "\n\n" + facts + "\n\n" + tone
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

// ProbeMcpServer 连接一台**已启用**的 MCP 服务器并返回它公布的工具名（第 8 批）：
// 输入框 / 菜单里选中服务器后当场列出工具，点名字即绑定 server+tool——不必再花一轮
// 让模型调 tool=list。
//
// 这不是一次对话发送：不写账本、不产生工具卡、不经过模型。远程服务器（http / 只有 URL）
// 由 ProbeServer 返回现成的「不支持自动列工具」错误原文，调用方据此保留手打工具名；
// 错误一律原样上抛（不包一层自己的话），界面显示的就是工具层给的原因。
func (s *ChatService) ProbeMcpServer(ctx context.Context, name string) ([]string, error) {
	server := strings.TrimSpace(name)
	if server == "" {
		return nil, errors.New("服务器名不能为空")
	}
	if s.mcpTool == nil || s.extensions == nil {
		return nil, errors.New("MCP 未装配")
	}
	f, err := s.extensions.Load()
	if err != nil {
		return nil, fmt.Errorf("读取扩展清单失败：%w", err)
	}
	for _, spec := range f.MCP {
		if !spec.Enabled || spec.Name != server {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return s.mcpTool.ProbeServer(pctx, spec)
	}
	return nil, fmt.Errorf("没有名为 %q 的已启用 MCP 服务器", server)
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
	// 超时预算（第 3 批分层）：
	//   - FirstByte 3min：思考型模型建流后可能长时间无输出（推理阶段不产生增量）——
	//     首字节单独给预算，不与"流中途挂起"（适配器空闲看门狗 60s）混为一谈；
	//   - Total 30min：单次调用全程放宽（旧 10min 会把慢思考/长编码掐成错误）；
	//     到期终态写明"本地预算用尽"，绝不伪装成上游错误。
	rt := llm.NewChatRuntime(s.gw, llm.TimeoutBudget{FirstByte: 3 * time.Minute, Total: 30 * time.Minute})
	ag := agent.NewLoop(rt, model, registry)
	var asker agent.Asker = &uiAsker{svc: s, sessionID: sessionID}
	if watch != nil {
		asker = &watchAsker{inner: asker, watch: watch, sessionID: sessionID}
	}
	ag.SetAsker(asker) // 问答通道常开（无 UI 时模型收到引导性结果）
	if watch != nil && approver != nil {
		approver = &watchApprover{inner: approver, watch: watch}
	}
	ag.SetApprover(approver) // 审批器随轮注入，策略变更对下一轮生效
	// 上下文预算（第 2 批 + 阶段 5-2）：取已启用渠道声明上限的最小值——运行时选路可能
	// 落到任一条，按最小上限裁剪才能保证不超任何一条；**全部未声明时退回保守默认值**
	// （未声明 ≠ 无限：不折叠就是每轮重发整段历史，请求体会一路涨到上游不响应的量级）。
	// 来源随预算一起下发，界面据此写明「未配置，按默认值」。
	budget, byDefault := resolveContextBudget(s.pool.MinContextLimit())
	ag.SetContextBudget(budget, byDefault)
	return ag
}

// DeleteSession 删除会话及其账本文件。
// 打开中的账本必须先关闭：Windows 上句柄未释放时删除会失败（与旧实现 rename 失败同源）。
// 工具集收尾在 sessMu 内完成到账本文件删除：与 ensureSessionTools 的存活校验互斥，
// 杜绝"校验通过 → 文件被删 → 重建孤儿工具集"的竞态窗口。
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
	// 先 cancel 会话级 ctx 再关工具（第二轮体检 R2）：在途浏览器操作立即中断、
	// 释放 tab 锁——否则 Close 会阻塞在同一把锁上，同步 IPC 卡死 UI 最长 60 秒。
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if st, ok := s.sessTools[sessionID]; ok {
		if st.cancel != nil {
			st.cancel()
		}
		if err := closeToolIfCloser(st.shell); err != nil {
			return fmt.Errorf("终止会话后台任务失败：%w", err)
		}
		_ = closeToolIfCloser(st.browser) // 会话的浏览器 tab 随会话关掉（无失败路径）
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
	return s.SendWithAttachments(ctx, sessionID, text, nil, "")
}

// SendWithAttachments 发送带附件的用户消息（0.0.10）：附件先物化（校验/落位/记引用），
// 再走与 Send 相同的轮次链路（payload 带附件引用，derive 重建上下文时还原图片与内联内容）。
// forceTool（第 7 批）是本轮"模型开口前必须先调用"的工具，JSON 形如
// {"name":"skill","arguments":{"name":"x"}}；空串 = 不强制（行为与旧版一致）。
// 用户在输入框里指定了技能或 MCP 工具时才有值——参数由前端给定，模型改不了。
func (s *ChatService) SendWithAttachments(ctx context.Context, sessionID, text string, rawAtts []IncomingAttachment, forceTool string) (<-chan llm.StreamChunk, error) {
	atts, err := s.materializeAttachments(sessionID, rawAtts)
	if err != nil {
		return nil, err
	}
	forced, err := parseForcedTool(forceTool)
	if err != nil {
		return nil, err
	}
	return s.sendCore(ctx, sessionID, text, atts, forced)
}

// parseForcedTool 解析强制工具参数（第 7 批）。白名单只放 skill / mcp：强制调用绝不能
// 变成"绕过模型与审批直接跑 fs/shell"的口子。非法输入显式报错，不静默降级。
func parseForcedTool(raw string) (*agent.ForcedTool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("强制工具参数格式错误：%w", err)
	}
	name := strings.TrimSpace(p.Name)
	switch name {
	case "skill":
		args := strings.TrimSpace(string(p.Arguments))
		if args == "" || args == "null" {
			args = "{}"
		}
		return &agent.ForcedTool{Name: name, Arguments: args}, nil
	case "mcp":
		// 第 8 批：菜单只给 server/tool，参数由模型填（钉住语义）。旧消息（第 7 批界面
		// 里用户手写过参数 JSON）仍带着 arguments —— 那份原样透传，走程序代发。
		var spec struct {
			Server    string          `json:"server"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(p.Arguments, &spec); err != nil {
			return nil, fmt.Errorf("MCP 强制调用参数格式错误：%w", err)
		}
		server, tool := strings.TrimSpace(spec.Server), strings.TrimSpace(spec.Tool)
		if server == "" || tool == "" {
			return nil, errors.New("MCP 强制调用需要 server 与 tool")
		}
		args := strings.TrimSpace(string(spec.Arguments))
		if args == "null" {
			args = ""
		}
		return &agent.ForcedTool{Name: "mcp", Server: server, Tool: tool, Arguments: args}, nil
	default:
		return nil, fmt.Errorf("不支持的强制工具：%q（只支持 skill / mcp）", name)
	}
}

func (s *ChatService) sendCore(ctx context.Context, sessionID, text string, atts []session.UserAttachment, forced *agent.ForcedTool) (<-chan llm.StreamChunk, error) {
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
	// 轮次开始埋点（0.0.09）：排障第一现场——文本只记长度与首 60 字（不复制全文），
	// 附件分开记（图片/文件），@ 引用数单独记（用户报"没效果"时先看这里）
	turnStart := time.Now()
	applog.Infof("turn start session=%s root=%q text=%q atts=%d attRefs=%d",
		sessionID, root, applog.Truncate(text, 60), len(atts), len(atAtts))
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
	// 轮次检查点（第 6 批）：开始收集"本轮首次修改某文件前"的快照，供「撤回本轮」。
	// 收尾落账在转发 goroutine 的 defer 里（覆盖所有终态路径）。
	if st != nil && st.fs != nil {
		st.fs.BeginRound()
	}
	registry, err := s.assembleRegistry(st)
	if err != nil {
		release()
		return nil, err
	}
	applog.Infof("turn model=%s", model)
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
	watch := newZeroEventWatch(firstEventTimeout, inlineAttachmentNote(atts))
	runCtx, cancelRun := context.WithCancel(ctx)
	// 纪律（0.2.29 两次实测教训）：Send 是"返回通道即返回"的长调用——defer 在这里
	// 一律等于"立刻执行"：cancelRun 不能 defer（会当场取消整轮），watchStopped
	// 也不能 defer close（看门狗会在 Send 返回后立刻失去值守）。两者的生命周期
	// 都到"流收尾"为止：由转发 goroutine（或流未建立的错误路径）显式收尾。
	watchStopped := make(chan struct{})
	ag := s.newAgentWith(model, registry, approver, sessionID, watch)
	ag.SetForcedTool(forced) // 第 7 批：本轮开口前先调用指定的技能 / MCP 工具
	// 第 8 批：最近一次工作区检查有位置引用时，本轮请求末尾附一条"不是用户原话"的
	// 说明（只存在于本次请求，不落账本、不进系统提示）。
	ag.SetTrailingNote(func() string { return s.checkNote(sessionID) })
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
					applog.Errorf("watchdog fired session=%s idle=%s window=%s（上游黑洞/挂死，注入终态）",
						sessionID, time.Since(watch.start.Load().(time.Time)).Round(time.Second), watch.window)
					cancelRun() // 尽力打断上游（HTTP/工具读全部响应 ctx）
					inject <- llm.StreamChunk{EndReason: llm.EndError, Err: errors.New(watch.timeoutMessage())}
					return
				}
			}
		}
	}()
	go func() {
		// 工作区检查（第 8 批）：**回合收尾之后**跑一次用户配的检查命令——正常、错误、
		// 取消、看门狗超时各条终态路径都经过这里，与"chat:terminal 之后"同义。
		// 放最后一个 defer（最先注册 = 最后执行），异步执行不拖住通道关闭；
		// 未配置 = 不产生任何进程。
		defer func() { go s.RunCheckAndEmit(sessionID) }()
		defer close(out)
		defer release()
		defer cancelRun()         // 流收尾后释放本轮 ctx 资源（防泄漏；绝不提前取消）
		defer close(watchStopped) // 流收尾：看门狗退场
		// 轮次检查点落账（第 6 批）：任何终态路径都经过这里——本轮改过的文件与
		// "轮次开始前内容"一次性落账本，供「撤回本轮」。落账失败只记日志
		//（轮次已收尾，不因检查点失败改写终态）。
		defer func() {
			if st == nil || st.fs == nil {
				return
			}
			cps := st.fs.EndRound()
			if len(cps) == 0 {
				return
			}
			if _, err := ledger.Append(session.EventRoundCheckpoint, map[string]any{"files": cps}); err != nil {
				applog.Errorf("append round checkpoint failed session=%s err=%v", sessionID, err)
			}
		}()
		for {
			select {
			case c, ok := <-stream:
				if !ok {
					applog.Infof("turn end session=%s reason=stream-closed（无终态块，异常路径）", sessionID)
					return // 上游流关闭（契约保证终态已发）
				}
				watch.mark() // 任何块（增量/工具/清单）都算活动
				out <- c
				if c.EndReason != llm.EndNone {
					applog.Infof("turn end session=%s reason=%s err=%v elapsed=%s",
						sessionID, c.EndReason, c.Err, time.Since(turnStart).Round(time.Millisecond))
					return
				}
			case c := <-inject:
				applog.Infof("turn end session=%s reason=%s err=%v elapsed=%s（看门狗注入）",
					sessionID, c.EndReason, c.Err, time.Since(turnStart).Round(time.Millisecond))
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
	// 驾驶舱数据（0.0.28，browser 工具，历史恢复与实时事件同构）：截图相对路径
	// + 页面 URL + 控制台尾部。旧账本事件缺省为空；前端拿 Shot 经壳层
	// ReadBrowserShot 读图。
	Shot    string   `json:"shot,omitempty"`
	PageURL string   `json:"url,omitempty"`
	Console []string `json:"console,omitempty"`
	// 附件（0.0.10）：用户消息的图片/文件（重放后仍能显示；图片带 DataURL）
	Attachments []ChatAttachment `json:"attachments,omitempty"`
	// 问答卡（role="ask"）：问题与选项来自 tool_call 参数，答案在 Content
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
	// Seq 是账本事件序号（第 6 批）：用户消息投影携带它——前端「从这条消息重跑」
	// 以它为分叉锚点（RerunFrom）。
	Seq int64 `json:"seq,omitempty"`
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
	// 分叉区间（第 6 批）：「从这条用户消息重跑」丢弃的事件不参与投影
	drops, err := ledger.ForkDrops()
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
		if session.ForkDropped(drops, ev.Seq()) {
			return nil // 被重跑丢弃的区间：不投影、不占轮次
		}
		evSeq++
		switch ev.Kind() {
		case session.EventUserEdit:
			// 用户手动写入（代码块「应用到文件」，第 6 批）：投影为工具卡——
			// 重启/切回会话后这张卡仍在（此前只活在内存里，重启即消失）。
			flushSegment()
			var p struct {
				Path  string `json:"path"`
				IsNew bool   `json:"is_new"`
				Bytes int    `json:"bytes"`
				Diff  string `json:"diff"`
				Note  string `json:"note"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			op := "edit"
			if p.IsNew {
				op = "write"
			}
			out = append(out, ChatMessage{
				Role: "tool", ToolName: "fs", Status: "success",
				Content:  fmt.Sprintf("written %s (%d bytes)", p.Path, p.Bytes),
				Title:    p.Path,
				Op:       op,
				Diff:     p.Diff,
				UndoNote: p.Note,
			})
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
			msg.Seq = ev.Seq() // 第 6 批：「从这条消息重跑」的分叉锚点
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
				UndoNote string   `json:"undo_note"`
				Shot     string   `json:"shot"`
				URL      string   `json:"url"`
				Console  []string `json:"console"`
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
				Shot: p.Shot, PageURL: p.URL, Console: p.Console,
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
	// 各会话的浏览器 tab 一并关掉，再收共享的浏览器进程与临时目录。
	// 退出同样先 cancel 会话级 ctx（与 DeleteSession 同纪律：在途浏览器动作先断）。
	s.mu.Lock()
	s.sessMu.Lock()
	for _, st := range s.sessTools {
		if st.cancel != nil {
			st.cancel()
		}
		if err := closeToolIfCloser(st.shell); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = closeToolIfCloser(st.browser)
	}
	s.sessMu.Unlock()
	s.mu.Unlock()
	if s.browser != nil {
		if err := s.browser.Close(); err != nil && firstErr == nil {
			firstErr = err // 浏览器进程收不掉必须可见（与 MCP 同款纪律）
		}
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
