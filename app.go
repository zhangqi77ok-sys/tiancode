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
	registerWorkspaceTools(reg, wd, sb, sm)
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
	app := &App{
		workspace:    wd,
		sandbox:      sb,
		snapshotMgr:  sm,
		registry:     reg,
		channelStore: chStore,
		extraStore:   exStore,
		sessionStore: sessStore,
		projectStore: projStore,
		mcpManager:   mcpMgr,
		adrStore:     config.DefaultADRStore(),
	}

	app.gateway = NewWailsInteractionGateway(app)
	app.engine = configureEngine(reg, app.gateway, mcpMgr, wd)

	return app
}

// registerWorkspaceTools 注册所有依赖工作区路径的内置工具算子。
// NewApp 与 SetWorkspace 共用此函数：新增工具只需在此注册一次，杜绝两处重复与遗漏。
func registerWorkspaceTools(reg *host.Registry, wd string, sb *sandbox.Sandbox, sm *sandbox.SnapshotManager) {
	_ = reg.RegisterOrReplace(gittool.NewTool(wd))
	_ = reg.RegisterOrReplace(fstool.NewTool(sb, sm))
	_ = reg.RegisterOrReplace(terminaltool.NewTool(wd))
	_ = reg.RegisterOrReplace(searchtool.NewTool(sb))
	_ = reg.RegisterOrReplace(archtool.NewTool(wd))
}

// configureEngine 构造并配置执行引擎（注入 MCP 调用与 TDD 校验回调），由 NewApp 与 SetWorkspace 共用，
// 确保引擎仅构建一次、且行为在构造期即确定（消除导出可变字段与先建后弃的双构建隐患）。
func configureEngine(reg *host.Registry, gw loop.InteractionGateway, mgr *mcp.Manager, workspace string) *loop.ExecutionEngine {
	return loop.NewExecutionEngine(reg, gw,
		func(ctx context.Context, name string, args map[string]any) (string, error) {
			return mgr.CallTool(ctx, name, args)
		},
		func(writtenFile string) (string, bool) {
			report, err := agent.RunTDDValidation(workspace)
			if err != nil {
				return err.Error(), false
			}
			out := report.Output
			if writtenFile != "" {
				out = writtenFile + "\n" + out
			}
			return out, report.Status == "PASS"
		},
	)
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

	registerWorkspaceTools(a.registry, absDir, sb, a.snapshotMgr)

	// 重新初始化智能体自主执行引擎，绑定新工作区的插件执行链
	a.rebindMCPManager(absDir)
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

// rebindMCPManager 停止旧 MCP 管理器并以新工作区重建，同步重建执行引擎。
// 引擎的 mcpCall 闭包捕获 mcpManager 指针，故"重赋 mcpManager"与"重建引擎"是耦合操作，必须成对出现。
// 此方法是唯一入口：禁止在别处单独重赋 a.mcpManager，否则引擎持有的仍是旧管理器，MCP 调用会静默失效（对应 hotplug P3 脆弱点）。
func (a *App) rebindMCPManager(absDir string) {
	if a.mcpManager != nil {
		a.mcpManager.StopAll()
	}
	a.mcpManager = mcp.NewManager(absDir)
	a.engine = configureEngine(a.registry, a.gateway, a.mcpManager, absDir)
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

func (a *App) RenameSession(id string, newTitle string) error {
	if a.sessionStore == nil {
		return fmt.Errorf("session store not initialized")
	}
	return a.sessionStore.Rename(id, newTitle)
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
