package main

import (
	"tiancode/internal/mcp"

	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"syscall"

	"tiancode/internal/agent"
	"tiancode/internal/config"
	"tiancode/internal/core/loop"
	"tiancode/internal/core/sandbox"
	"tiancode/internal/host"
	"tiancode/internal/llm"
	"tiancode/internal/session"
	"tiancode/plugins/provider/openai"
	"tiancode/plugins/provider/anthropic"
	"tiancode/plugins/provider/azure"
	"tiancode/plugins/provider/gemini"
	"tiancode/plugins/provider/grok"
	"tiancode/plugins/provider/ollama"
	safetyrail "tiancode/plugins/rail/safety"
	fstool "tiancode/plugins/tool/fs"
	gittool "tiancode/plugins/tool/git"
	searchtool "tiancode/plugins/tool/search"
	terminaltool "tiancode/plugins/tool/terminal"
	archtool "tiancode/plugins/tool/arch"
	"tiancode/plugins/tool/ask_user"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)



// pruneHistoricalOutput 对历史冗长工具输出进行原位折叠修剪，保留上下文拓扑与公共前缀哈希
func pruneHistoricalOutput(content string, maxChars int) string {
	if len(content) <= maxChars {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= 12 {
		return content[:maxChars] + "...\n[Output pruned for KV cache efficiency]"
	}
	// 保留前 8 行与后 2 行关键信息，中间折叠行数
	head := strings.Join(lines[:8], "\n")
	tail := strings.Join(lines[len(lines)-2:], "\n")
	prunedLines := len(lines) - 10
	return fmt.Sprintf("%s\n\n[... %d lines folded/pruned for KV cache efficiency ...]\n\n%s", head, prunedLines, tail)
}

// buildConversationWindow 动态构建模型多轮会话上下文窗口
// 基于两级微创修剪策略 (In-Place Output Pruning)，维持严格单向追加 (Append-Only) 保证公共前缀 KV Cache
func buildConversationWindow(systemPrompt string, history []session.SessionMessage, maxHistoryChars int) []llm.Message {
	conversation := []llm.Message{
		{Role: "system", Content: systemPrompt},
	}

	if len(history) == 0 {
		return conversation
	}

	// 1. 双阈值原位修剪历史冗长输出 (In-Place Output Pruning)
	// 对倒数 2 条以前的历史消息，单条若超过 1,000 字符原位折叠，保留前后骨架与角色拓扑
	processed := make([]llm.Message, len(history))
	totalChars := 0
	for i, m := range history {
		content := m.Content
		if len(history) > 3 && i < len(history)-2 && len(content) > 1000 {
			content = pruneHistoricalOutput(content, 1000)
		}
		processed[i] = llm.Message{
			Role:    m.Role,
			Content: content,
		}
		totalChars += len(content)
	}

	// 2. 若全量历史在预算内，保持严格正序单向追加 (Append-Only，100% 保持前缀 KV Cache)
	if totalChars <= maxHistoryChars {
		conversation = append(conversation, processed...)
		return conversation
	}

	// 3. 超极端情况（如数十轮巨型上下文），从头部安全削减早期轮次
	// 严格角色对齐原则：裁剪后首条消息必须是 "user" 角色（杜绝孤立 assistant 或 tool 导致大模型 400 报错）
	startIndex := 0
	for startIndex < len(processed)-2 && totalChars > maxHistoryChars {
		totalChars -= len(processed[startIndex].Content)
		startIndex++
	}

	for startIndex < len(processed)-1 && processed[startIndex].Role != "user" {
		startIndex++
	}

	conversation = append(conversation, processed[startIndex:]...)
	return conversation
}

// buildLLMToolsFromRegistry 动态从 Registry 中提取所有已注册插件算子声明，并转换为大模型工具契约格式
// 保证新增算子“即注册即生效”，并严格按字母升序排序以保证工具前缀 Token 序列绝对恒定
func (a *App) buildLLMToolsFromRegistry(ctx context.Context) []llm.ToolDef {
	tools := make([]llm.ToolDef, 0)

	if a.registry != nil {
		defs := a.registry.ListTools()
		for _, d := range defs {
			var params map[string]any
			if len(d.Parameters) > 0 {
				_ = json.Unmarshal(d.Parameters, &params)
			}
			if params == nil {
				params = map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}
			}

			tools = append(tools, llm.ToolDef{
				Type: "function",
				Function: llm.ToolFunctionDef{
					Name:        d.Name,
					Description: d.Description,
					Parameters:  params,
				},
				Mutating: d.Mutating,
			})
		}
	}

	// 合并外部激活的 MCP 协议算子
	if a.mcpManager != nil {
		if mcpTools, err := a.mcpManager.GetAllTools(ctx); err == nil && len(mcpTools) > 0 {
			tools = append(tools, mcpTools...)
		}
	}

	// 严格按工具名称字母升序排序，保证工具列表声明在 Prompt 序列中绝对恒定
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Function.Name < tools[j].Function.Name
	})

	return tools
}

func normalizeWindowsPath(p string) string {
	vol := filepath.VolumeName(p)
	if len(vol) > 0 {
		return strings.ToUpper(vol) + p[len(vol):]
	}
	return p
}

// FileNode 文件树节点
type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"is_dir"`
	Children []FileNode `json:"children,omitempty"`
}

/*
 * ARCHITECTURE CONTRACT (AI-READABLE)
 * =====================================
 * App 是 Wails 宿主层，不是工具执行层。严格遵守以下约定：
 *
 * ✅ 合法：a.registry.GetTool(name).Execute(ctx, args)
 * ❌ 非法：直接持有 *gittool.Tool / *fstool.Tool / *terminaltool.Tool 字段并调用
 *
 * 新增工具唯一合法流程：
 *   1. 在 plugins/tool/<name>/ 实现 pkg/plugin/v1.ToolPlugin 接口
 *   2. 在 NewApp() 中 reg.Register(newtool.NewTool(...))
 *   3. 不需要也不允许修改 SendMessage 或任何业务代码
 *
 * Rail 约定：工具执行前必须调用 Rail.OnBeforeAct()，执行后调用 Rail.OnAfterAct()
 * 违反以上约定将被 scripts/arch_check.sh 阻断提交。
 */

// App Wails Go 原生桌面宿主结构体
type App struct {
	ctx              context.Context
	workspace        string
	sandbox          *sandbox.Sandbox
	snapshotMgr      *sandbox.SnapshotManager
	registry         *host.Registry // 唯一的工具访问入口，禁止绕过
	engine           *loop.ExecutionEngine
	channelStore     *config.ChannelStore
	extraStore       *config.ExtraStore
	sessionStore     *session.Store
	projectStore     *config.ProjectStore
	mcpManager       *mcp.Manager
	adrStore         *config.ADRStore
	terminalCancel   context.CancelFunc
	terminalMu       sync.Mutex
	termTaskID       int64
	agentCancel      context.CancelFunc
	agentMu          sync.Mutex
	agentTaskID      int64
	gateway          *WailsInteractionGateway
	currentSessionID string
}

// NewApp 构造生产级 Wails 宿主
// 注意：工具实例注册进 registry 后不保留字段引用，所有调用必须通过 registry.GetTool()
func NewApp() *App {
	wd, _ := os.Getwd()
	sb, _ := sandbox.NewSandbox(wd)
	sm := sandbox.NewSnapshotManager(wd)
	reg := host.NewRegistry()

	_ = reg.Register(openai.NewProvider())
	_ = reg.Register(anthropic.NewProvider())
	_ = reg.Register(gemini.NewProvider())
	_ = reg.Register(grok.NewProvider())
	_ = reg.Register(ollama.NewProvider())
	_ = reg.Register(azure.NewProvider())
	_ = reg.Register(gittool.NewTool(wd))
	_ = reg.Register(fstool.NewTool(sb, sm))
	_ = reg.Register(searchtool.NewTool(sb))
	_ = reg.Register(terminaltool.NewTool(wd))
	_ = reg.Register(archtool.NewTool(wd))
	_ = reg.Register(ask_user.NewTool())
	_ = reg.Register(safetyrail.New())

	chStore, _ := config.NewChannelStore()
	exStore, _ := config.NewExtraStore()
	sessStore, _ := session.NewStore()
	projStore, _ := config.NewProjectStore()
	if projStore != nil && wd != "" {
		_ = projStore.Add(wd)
	}

	mcpMgr := mcp.NewManager(wd)
	eng := loop.NewExecutionEngine(reg, nil)
	eng.MCPCall = func(ctx context.Context, name string, args map[string]any) (string, error) {
		return mcpMgr.CallTool(ctx, name, args)
	}

	app := &App{
		workspace:    wd,
		sandbox:      sb,
		snapshotMgr:  sm,
		registry:     reg,
		engine:       eng,
		channelStore: chStore,
		extraStore:   exStore,
		sessionStore: sessStore,
		projectStore: projStore,
		mcpManager:   mcpMgr,
		adrStore:     config.DefaultADRStore(),
	}

	eng.Verify = func(writtenFile string) (string, bool) {
		ws := app.workspace
		if ws == "" {
			ws = wd
		}
		report, err := agent.RunTDDValidation(ws)
		if err != nil {
			return err.Error(), false
		}
		out := report.Output
		if writtenFile != "" {
			out = writtenFile + "\n" + out
		}
		return out, report.Status == "PASS"
	}

	app.gateway = NewWailsInteractionGateway(app)
	app.engine = loop.NewExecutionEngine(reg, app.gateway)
	app.engine.MCPCall = eng.MCPCall
	app.engine.Verify = eng.Verify

	return app
}

// startup 窗口初始化生命周期
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	runtime.LogInfo(ctx, "[tiancode] Wails Native App initialized successfully")
	// 异步预热并拉起已启用的外部 MCP 协议服务
	if a.mcpManager != nil && a.extraStore != nil {
		go func() {
			a.mcpManager.SyncFromConfig(context.Background(), a.extraStore.ListMCPs())
		}()
	}
}

// shutdown 窗口退出清理生命周期
func (a *App) shutdown(ctx context.Context) {
	if a.mcpManager != nil {
		a.mcpManager.StopAll()
	}
}

// MinimizeWindow 最小化无边框窗口
func (a *App) MinimizeWindow() {
	if a.ctx != nil {
		runtime.WindowMinimise(a.ctx)
	}
}

// ToggleMaximizeWindow 切换无边框窗口最大化/还原
func (a *App) ToggleMaximizeWindow() {
	if a.ctx != nil {
		runtime.WindowToggleMaximise(a.ctx)
	}
}

// CloseWindow 安全关闭原生桌面程序
func (a *App) CloseWindow() {
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

// OpenFileDialog 弹出真实 Windows 原生多文件选择窗口
func (a *App) OpenFileDialog() ([]string, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("app context not initialized")
	}
	files, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "选择要上传给 Agent 的代码或文档",
		DefaultDirectory: a.workspace,
	})
	if err != nil {
		return nil, err
	}
	relFiles := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(a.workspace, f)
		if err == nil && !filepath.IsAbs(rel) {
			relFiles = append(relFiles, filepath.ToSlash(rel))
		} else {
			relFiles = append(relFiles, filepath.ToSlash(f))
		}
	}
	return relFiles, nil
}

// OpenDirectoryDialog 弹出系统原生选择文件夹窗口 (符合铁律 5)
func (a *App) OpenDirectoryDialog() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app context not initialized")
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "选择项目工作区文件夹",
		DefaultDirectory: a.workspace,
	})
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(dir), nil
}

// GetWorkspace 获取当前活动工作区绝对路径
func (a *App) GetWorkspace() string {
	return filepath.ToSlash(a.workspace)
}

// SetWorkspace 动态切换项目工作区，热更新沙箱、Git 工具与插件执行链
func (a *App) SetWorkspace(dir string) error {
	if dir == "" {
		return fmt.Errorf("workspace directory cannot be empty")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid workspace directory: %w", err)
	}
	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("workspace directory does not exist or is not a directory: %s", absDir)
	}

	// 切换工作区前，主动取消前序可能仍在运行的智能体流式推理或终端命令
	a.agentMu.Lock()
	if a.agentCancel != nil {
		a.agentCancel()
		a.agentCancel = nil
	}
	a.agentMu.Unlock()

	a.terminalMu.Lock()
	if a.terminalCancel != nil {
		a.terminalCancel()
		a.terminalCancel = nil
	}
	a.terminalMu.Unlock()

	a.workspace = absDir
	sb, _ := sandbox.NewSandbox(absDir)
	a.sandbox = sb
	a.snapshotMgr = sandbox.NewSnapshotManager(absDir)

	if a.registry != nil {
		_ = a.registry.RegisterOrReplace(gittool.NewTool(absDir))
		_ = a.registry.RegisterOrReplace(fstool.NewTool(sb, a.snapshotMgr))
		_ = a.registry.RegisterOrReplace(terminaltool.NewTool(absDir))
		_ = a.registry.RegisterOrReplace(searchtool.NewTool(sb))
		_ = a.registry.RegisterOrReplace(archtool.NewTool(absDir))
	}

	// 重新初始化智能体自主执行引擎，绑定新工作区的插件执行链
	if a.mcpManager != nil {
		a.mcpManager.StopAll()
	}
	a.mcpManager = mcp.NewManager(absDir)
	if a.registry != nil {
		a.engine = loop.NewExecutionEngine(a.registry, a.gateway)
		a.engine.MCPCall = func(ctx context.Context, name string, args map[string]any) (string, error) {
			return a.mcpManager.CallTool(ctx, name, args)
		}
		ws := absDir
		a.engine.Verify = func(writtenFile string) (string, bool) {
			report, err := agent.RunTDDValidation(ws)
			if err != nil {
				return err.Error(), false
			}
			out := report.Output
			if writtenFile != "" {
				out = writtenFile + "\n" + out
			}
			return out, report.Status == "PASS"
		}
	}
	if a.extraStore != nil {
		go func() {
			a.mcpManager.SyncFromConfig(context.Background(), a.extraStore.ListMCPs())
		}()
	}

	if a.projectStore != nil {
		_ = a.projectStore.Add(absDir)
	}
	return nil
}

func (a *App) ListProjects() []config.Project {
	if a.projectStore == nil {
		return nil
	}
	return a.projectStore.List()
}

func (a *App) AddProject(path string) error {
	if a.projectStore == nil {
		return fmt.Errorf("project store not initialized")
	}
	return a.projectStore.Add(path)
}

func (a *App) RemoveProject(path string) error {
	if a.projectStore == nil {
		return fmt.Errorf("project store not initialized")
	}
	return a.projectStore.Remove(path)
}

func (a *App) ListAllSessions() []session.SessionMeta {
	if a.sessionStore == nil {
		return nil
	}
	return a.sessionStore.List("")
}

// --- 会话历史持久化 (Sessions) ---

func (a *App) ListSessions() []session.SessionMeta {
	if a.sessionStore == nil {
		return nil
	}
	return a.sessionStore.List(a.workspace)
}

func (a *App) GetSession(id string) (*session.ChatSession, error) {
	if a.sessionStore == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	return a.sessionStore.Get(id)
}

func (a *App) SaveSession(sess session.ChatSession) error {
	if a.sessionStore == nil {
		return fmt.Errorf("session store not initialized")
	}
	if sess.Workspace == "" {
		sess.Workspace = a.workspace
	}
	return a.sessionStore.Save(sess)
}

func (a *App) DeleteSession(id string) error {
	if a.sessionStore == nil {
		return fmt.Errorf("session store not initialized")
	}
	return a.sessionStore.Delete(id)
}

// windowsSysProcAttr 返回 Windows 平台隐藏黑框与无窗口标志
func windowsSysProcAttr() *syscall.SysProcAttr {
	if goruntime.GOOS == "windows" {
		return &syscall.SysProcAttr{
			CreationFlags: 0x08000000,
			HideWindow:    true,
		}
	}
	return nil
}
