# Tcode Go 插件式微内核架构设计规约 (ARCHITECTURE_GO_PLUGIN_CORE.md)

> **版本**：v2.0.0 (Go Edition)  
> **设计角色**：Tcode 首席架构师 (Go Infrastructure Architect)  
> **核心哲学**：微内核 (Micro-Kernel) + 扩展点 (SPI) + 双轨插件宿主 (Dual-Track Plugin Host) + 进程级安全崩溃隔离

---

## 🏛️ 一、架构愿景与设计哲学

在现代桌面级 AI 智能体开发工作台中，后端引擎需要承载：
- 高频多模型 API 并发调度与流式输出 (SSE / WebSocket)；
- 本地工作区文件操作、Git 物理版本控制与无窗口静默终端交互；
- 多源外部工具集成 (MCP Server / CLI Tools)；
- 严苛的 Token 计量、耗时审计与安全拦截规则。

为了彻底杜绝“单体硬编码”与“随处打补丁”的架构退化，Tcode 采用 **Go 1.22+ 插件式微内核架构**：
1. **极轻量与高性能**：编译为单个静态跨平台二进制，无运行时依赖，冷启动 $< 20\text{ms}$，常驻内存 $< 45\text{MB}$；
2. **能力全面插件化 (Everything is a Plugin)**：微内核只负责核心状态机、执行回路与事件调度；所有模型提供商、工具算子、治理规则与持久化存储全部解耦为插件；
3. **双轨插件宿主 (Dual-Track Host)**：
   - **内置高性能插件 (In-Process)**：静态编译/内存级注册，零 RPC 序列化开销；
   - **进程隔离外部插件 (Out-of-Process / MCP)**：子进程物理隔离，支持 stdio / JSON-RPC 2.0，第三方插件崩溃绝不波及微内核主进程。

---

## 📐 二、分层架构拓扑图

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                     前端表现层 (Wails v2 + Vue 3 + Pinia + Monaco Editor)                │
│   [ 单焦点主工作区 | 集成终端抽屉 | Git 控制中枢 | 模型监控大盘 | 暖色极简 UI ]           │
└───────────────────────────────────────────┬────────────────────────────────────────────┘
                                            │ Local IPC / HTTP SSE / WebSocket
                                            ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                        Tcode Go Micro-Kernel Core (微内核核心层)                         │
│                                                                                        │
│   ┌─────────────────────┐  ┌─────────────────────┐  ┌──────────────────────────────┐   │
│   │ 🔄 Dual-Loop Engine │  │ 📨 Async EventBus   │  │ 🧠 Session & Context Manager │   │
│   │ (Inner/Outer Loop)  │  │ (订阅分发/零锁无损) │  │ (Token计量/影子快照/记忆漫游)│   │
│   └──────────┬──────────┘  └──────────┬──────────┘  └──────────────┬───────────────┘   │
│              │                        │                            │                   │
│              └────────────────────────┼────────────────────────────┘                   │
│                                       ▼                                                │
│   ┌────────────────────────────────────────────────────────────────────────────────┐   │
│   │                      Plugin Host & SPI Registry (插件宿主与注册中心)            │   │
│   │      • 插件生命周期管控 (Init ➔ Start ➔ HealthCheck ➔ Stop)                    │   │
│   │      • Panic 熔断与隔离沙箱 (Safe Go Routine Recovery)                        │   │
│   │      • 动态优先级链式调度 (Priority Chain Routing)                             │   │
│   └───────────────────────────────────┬────────────────────────────────────────────┘   │
└───────────────────────────────────────┼────────────────────────────────────────────────┘
                                        │
             ┌──────────────────────────┴──────────────────────────┐
             ▼                                                     ▼
┌──────────────────────────────────────────┐   ┌─────────────────────────────────────────┐
│     内置高性能插件 (In-Process Plugins)    │   │    进程隔离外部插件 (Out-of-Process MCP) │
│     (基于 Go Interface 原生编译/静态注册) │   │     (基于 stdio / JSON-RPC 2.0 / gRPC)  │
│                                          │   │                                         │
│  ┌─────────────────┐ ┌─────────────────┐ │   │  ┌─────────────────┐ ┌────────────────┐ │
│  │ 🌐 Provider SPI │ │ 🛡️ Rail SPI     │ │   │  │ 🔌 MCP Tool SPI │ │ 🐍 Python/Node │ │
│  │  • DeepSeek     │ │  • SafetyGuard  │ │   │  │  • Filesystem   │ │    外部子进程  │ │
│  │  • Claude       │ │  • TDDAutoHeal  │ │   │  │  • SQLite/PG    │ │    独立沙箱    │ │
│  │  • OpenAI/Gemini│ │  • TokenBudget  │ │   │  │  • Git Server   │ │    崩溃不扩散  │ │
│  └─────────────────┘ └─────────────────┘ │   │  └─────────────────┘ └────────────────┘ │
└──────────────────────────────────────────┘   └─────────────────────────────────────────┘
```

---

## 🔌 三、四大核心插件扩展点契约 (SPI Contracts)

### 3.1 根插件接口与生命周期 (`Plugin`)
所有接入微内核的插件必须实现根接口，规范其生命周期：

```go
package plugin

import "context"

// PluginType 插件分类枚举
type PluginType string

const (
	TypeProvider PluginType = "provider" // 模型网关驱动插件
	TypeTool     PluginType = "tool"     // 工具与算子插件
	TypeRail     PluginType = "rail"     // 生命周期治理与安全拦截插件
	TypeStorage  PluginType = "storage"  // 状态与快照存储插件
)

// Plugin 插件基础元数据与生命周期标准接口
type Plugin interface {
	ID() string                             // 唯一标识符 (例如: "provider.deepseek", "tool.git")
	Name() string                           // 人类可读名称 (例如: "DeepSeek-V4 Direct Ingress")
	Version() string                        // 语义化版本号 (SemVer)
	Type() PluginType                       // 插件分类
	Init(ctx context.Context, config []byte) error // 读取配置初始化
	Start(ctx context.Context) error        // 启动后台健康检测或长连接
	Stop(ctx context.Context) error         // 优雅停机与资源释放
}
```

---

### 3.2 扩展点一：模型网关插件 (`ProviderPlugin`)
抽象大模型调用协议，支持厂商动态扩展与实时流式输出：

```go
// ProviderPlugin 模型网关驱动插件
type ProviderPlugin interface {
	Plugin
	// StreamChat 流式对话推理，返回只读事件通道或错误
	StreamChat(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
	// Ping 探活与延迟测速 (TTFT 探针)
	Ping(ctx context.Context) (latencyMs int64, err error)
	// ListAvailableModels 获取该网关可用模型列表 (支持自动拉取与静态声明)
	ListAvailableModels(ctx context.Context) ([]ModelDescriptor, error)
}

// StreamChunk 流式输出结构体
type StreamChunk struct {
	DeltaContent string     `json:"delta_content"` // 增量文本
	Thinking     string     `json:"thinking"`      // 思考过程内容 (如 Claude 3.7 Thinking / R1)
	ToolCalls    []ToolCallChunk `json:"tool_calls,omitempty"` // 工具调用增量分片
	Usage        *TokenUsage `json:"usage,omitempty"`
	Error        error      `json:"-"`
}
```

#### 3.2.1 OpenAI 官方规范与 Anthropic Claude Messages 原生双轨适配架构

Tcode 拒绝仅支持单一协议或使用粗糙的黑盒代理，在微内核原生内置对 **OpenAI** 与 **Anthropic** 两大行业事实标准的 100% 协议还原：

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│              Tcode 微内核规范抽象层 (Canonical Intermediate Representation)             │
│            CanonicalMessage (Role, ContentParts: Text/Thinking/ToolUse/ToolResult)     │
└───────────────────────────┬────────────────────────────────┬───────────────────────────┘
                            │                                │
        ┌───────────────────┴───────────────┐    ┌───────────┴───────────────────────┐
        ▼                                   ▼    ▼                                   ▼
┌──────────────────────────────────────────────┐ ┌──────────────────────────────────────────────┐
│  OpenAI 驱动 (plugins/provider/openai)       │ │  Claude 驱动 (plugins/provider/claude)       │
│  • 适用: GPT-4o / DeepSeek / vLLM / Ollama   │ │  • 适用: Claude 3.5 Sonnet / 3.7 Thinking    │
├──────────────────────────────────────────────┤ ├──────────────────────────────────────────────┤
│  [ 请求协议 ]                                │ │  [ 请求协议 ]                                │
│  • POST /v1/chat/completions                 │ │  • POST /v1/messages                         │
│  • messages 数组含 system/user/assistant/tool│ │  • 顶层独立 system 字段 (严格提取分离)       │
│  • tools: [{ type: "function", function }]   │ │  • messages 仅允许 user/assistant 严格交替   │
│  • stream_options: { include_usage: true }   │ │  • tools: [{ name, description, input_schema}]│
│                                              │ │  • beta: prompt-caching, max-tokens-3-5...   │
├──────────────────────────────────────────────┤ ├──────────────────────────────────────────────┤
│  [ 流式 SSE 协议帧 ]                         │ │  [ 流式 SSE 协议帧 (有限状态机解码) ]        │
│  • data: {"choices":[{"delta":{...}}]}       │ │  • event: message_start (输入 Token 统计)    │
│  • data: [DONE] 显式退出标桩                 │ │  • event: content_block_start (text/thinking)│
│  • 内部流式工具碎片拼装器 (Tool Reassembler) │ │  • event: content_block_delta (思考流解包)   │
│                                              │ │  • event: content_block_stop / message_delta │
├──────────────────────────────────────────────┤ ├──────────────────────────────────────────────┤
│  [ 高级工程特性 ]                            │ │  [ 高级工程特性 ]                            │
│  • 429 Retry-After 自动抖动退避重试          │ │  • Claude 3.7 Extended Thinking 独立解包     │
│  • 统一兼容 One-API / New-API 聚合端点       │ │  • Prompt Caching 命中 Tokens 精准成本核算   │
└──────────────────────────────────────────────┘ └──────────────────────────────────────────────┘
```

---

### 3.3 扩展点二：工具算子插件 (`ToolPlugin`)
统一本地受控工具与外部 MCP 工具的执行接口：

```go
// ToolPlugin 工具与算子插件
type ToolPlugin interface {
	Plugin
	// Definition 暴露给大模型的 Tool Schema (遵循 OpenAPI 3 / JSON Schema 契约)
	Definition() ToolDefinition
	// Execute 执行算子并返回结果 (受 context 超时与取消严格控制)
	Execute(ctx context.Context, paramsJSON []byte) (*ToolResult, error)
}

type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ToolResult struct {
	Content string `json:"content"` // 工具产出文本
	IsError bool   `json:"is_error"`
}
```

---

### 3.4 扩展点三：执行治理轨道拦截器 (`RailPlugin`)
切入 Agent 双环生命周期的各个固定节点，实现无侵入审计、拦截与自愈：

```go
// RailPlugin 核心执行回路治理轨道插件
type RailPlugin interface {
	Plugin
	Priority() int // 执行优先级 (0-100，数值越大越先执行，P-100 为最高阻断权)

	// 生命周期切入钩子
	OnBeforeObserve(ctx context.Context, session *SessionContext) error
	OnBeforeReason(ctx context.Context, session *SessionContext, prompt *string) error
	OnBeforeAct(ctx context.Context, session *SessionContext, action *AgentAction) (*Decision, error)
	OnAfterAct(ctx context.Context, session *SessionContext, result *ToolResult) error
	OnVerify(ctx context.Context, session *SessionContext) (passed bool, feedback string, err error)
}

// Decision 拦截决断结果
type Decision struct {
	Allow       bool   `json:"allow"`
	Intercepted bool   `json:"intercepted"`
	Reason      string `json:"reason"`
}
```

---

## 🛡️ 四、插件宿主引擎与 Panic 隔离屏障

微内核最核心的职责是**保护主进程高可用**。任何第三方或有缺陷的插件发生 `panic` 时，宿主引擎必须将其捕获、隔离并记录，绝不导致主工作台崩溃：

```go
package host

import (
	"context"
	"fmt"
	"sync"
	"tcode/pkg/plugin"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]plugin.ProviderPlugin
	tools     map[string]plugin.ToolPlugin
	rails     []plugin.RailPlugin
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]plugin.ProviderPlugin),
		tools:     make(map[string]plugin.ToolPlugin),
		rails:     make([]plugin.RailPlugin, 0),
	}
}

// Register 统一安全注册入口
func (r *Registry) Register(p plugin.Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch p.Type() {
	case plugin.TypeProvider:
		r.providers[p.ID()] = p.(plugin.ProviderPlugin)
	case plugin.TypeTool:
		r.tools[p.ID()] = p.(plugin.ToolPlugin)
	case plugin.TypeRail:
		rail := p.(plugin.RailPlugin)
		r.rails = append(r.rails, rail)
		r.sortRailsByPriority()
	default:
		return fmt.Errorf("unknown plugin type: %s", p.Type())
	}
	return nil
}

// SafeExecuteAction 带 panic 恢复与超时控制的安全算子调度
func (r *Registry) SafeExecuteAction(ctx context.Context, toolID string, args []byte) (res *plugin.ToolResult, err error) {
	r.mu.RLock()
	tool, exists := r.tools[toolID]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("tool plugin not found: %s", toolID)
	}

	// 🛡️ Panic 隔离屏障：单个插件崩溃绝不击垮微内核
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("tool plugin [%s] panicked: %v", toolID, rec)
		}
	}()

	return tool.Execute(ctx, args)
}
```

---

## 📁 五、标准 Go 工程目录规范 (Standard Layout)

```text
agent-learning/
├── cmd/
│   └── tcode-daemon/              # 桌面微内核主守护进程入口 (main.go)
├── internal/
│   ├── core/                      # 微内核核心逻辑 (不对外暴露)
│   │   ├── loop/                  # 统一双环执行引擎 (Inner/Outer Loop)
│   │   ├── eventbus/              # 零拷贝事件驱动总线 (EventBus)
│   │   ├── session/               # 会话状态机与 Token 计量看板
│   │   └── sandbox/               # 工作区安全沙箱与影子 Git 快照
│   ├── host/                      # 插件宿主引擎与 Registry
│   │   ├── loader_inproc.go       # 内置插件装载器
│   │   ├── loader_mcp.go          # 外部进程 MCP (stdio/sse) 适配装载器
│   │   └── guard.go               # Panic 隔离与超时看门狗
│   └── transport/                 # 表现层适配 (Wails IPC / Events / Stdio)
├── pkg/
│   ├── plugin/                    # 核心 SPI 接口契约定义 (公共只读)
│   │   ├── spi.go                 # Plugin / Provider / Tool / Rail 接口
│   │   └── types.go               # 通用 DTO / Chunk / Action 数据契约
│   └── protocol/                  # 序列化、JSON-RPC 2.0 与 MCP 协议解码
├── plugins/                       # 开箱即用官方插件库
│   ├── provider/                  # 模型网关实现 (双轨上游协议原生驱动)
│   │   ├── openai/                # OpenAI 官方协议族驱动 (GPT-4o, DeepSeek, SiliconFlow, vLLM, Ollama)
│   │   └── claude/                # Anthropic 官方协议族驱动 (Claude 3.5, Claude 3.7 Thinking, Prompt Caching)
│   ├── tool/                      # 基础算子实现
│   │   ├── fs/                    # 影子受控文件读写
│   │   ├── git/                   # 高级 Git 暂存与分支算子
│   │   └── term/                  # pwsh 终端管道交互算子
│   └── rail/                      # 治理拦截器实现
│       ├── safety/                # 越界高危操作防御轨 (P-100)
│       ├── tdd/                   # 测试自愈红绿循环轨 (P-80)
│       └── audit/                 # Token 消耗与首字延迟审计轨 (P-20)
└── go.mod                         # Go 1.22+ 模块依赖声明
```

---

## 🚀 六、落地演进路线

1. **Phase 1 (契约先行与骨架就绪)**：
   - 确立 `pkg/plugin/spi.go` 接口定义，实现 `internal/host/registry.go`；
   - Wails v2 原生双向 IPC 绑定与事件流。
2. **Phase 2 (官方核心插件内置化)**：
   - 将 `DeepSeek`、`Anthropic`、`Git 暂存控制`、`pwsh 终端管道` 作为原生 In-Process 插件挂载；
   - 接入 MCP 外部进程管理器 (`loader_mcp.go`)。
3. **Phase 3 (微内核独立守护交付)**：
   - 编译输出 `tcode-daemon.exe` 单静态二进制，替换旧有重量宿主；
   - 内存降至 30MB 级，冷启动达成毫秒级极速响应。
