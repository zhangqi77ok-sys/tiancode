package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"tiancode/internal/config"
	"tiancode/internal/llm"
	"time"
)

// Manager MCP 插件全局管理器
type Manager struct {
	workspace   string
	clients     map[string]Client // key: server ID
	toolRouting map[string]string // key: tool name -> server ID
	mu          sync.RWMutex
}

func NewManager(workspace string) *Manager {
	return &Manager{
		workspace:   workspace,
		clients:     make(map[string]Client),
		toolRouting: make(map[string]string),
	}
}

// TestServer 对指定服务执行物理拉起与工具探活 (JSON-RPC)
func (m *Manager) TestServer(ctx context.Context, cfg config.MCPServerConfig) (MCPTestResult, error) {
	if m == nil {
		return MCPTestResult{
			ID:      cfg.ID,
			Name:    cfg.Name,
			Status:  "ERROR",
			Error:   "mcp manager not initialized",
			Latency: "0ms",
		}, nil
	}

	var client Client
	if cfg.Type == "stdio" || cfg.Type == "" {
		client = NewStdioClient(cfg.Command, cfg.Args, m.workspace, cfg.Env)
	} else {
		return MCPTestResult{
			ID:      cfg.ID,
			Name:    cfg.Name,
			Status:  "ERROR",
			Error:   "Unsupported MCP transport: " + cfg.Type,
			Latency: "0ms",
		}, nil
	}

	// 设置 5 秒握手超时
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Start(timeoutCtx); err != nil {
		return MCPTestResult{
			ID:      cfg.ID,
			Name:    cfg.Name,
			Status:  "ERROR",
			Error:   err.Error(),
			Latency: "超时",
		}, nil
	}
	defer client.Stop()

	duration, count, err := client.Ping(timeoutCtx)
	if err != nil {
		return MCPTestResult{
			ID:      cfg.ID,
			Name:    cfg.Name,
			Status:  "ERROR",
			Error:   err.Error(),
			Latency: "超时",
		}, nil
	}

	tools, _ := client.ListTools(timeoutCtx)
	toolNames := make([]string, 0, len(tools))
	for _, t := range tools {
		toolNames = append(toolNames, t.Name)
	}

	return MCPTestResult{
		ID:        cfg.ID,
		Name:      cfg.Name,
		Status:    "ONLINE",
		Latency:   fmt.Sprintf("%dms", duration.Milliseconds()),
		ToolCount: count,
		Tools:     toolNames,
	}, nil
}

// StartServer 启动单个 MCP 服务，完成握手并注册算子路由 (优化锁粒度)
func (m *Manager) StartServer(ctx context.Context, cfg config.MCPServerConfig) error {
	var client Client
	if cfg.Type == "stdio" || cfg.Type == "" {
		client = NewStdioClient(cfg.Command, cfg.Args, m.workspace, cfg.Env)
	} else {
		return fmt.Errorf("unsupported transport: %s", cfg.Type)
	}

	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// 锁外执行耗时的外部子进程启动与工具探测，避免阻塞全局并发查询
	if err := client.Start(startCtx); err != nil {
		return fmt.Errorf("failed to start mcp server [%s]: %w", cfg.Name, err)
	}

	tools, err := client.ListTools(startCtx)
	if err != nil {
		_ = client.Stop()
		return fmt.Errorf("failed to list tools for [%s]: %w", cfg.Name, err)
	}

	m.mu.Lock()
	var oldClient Client
	if old, ok := m.clients[cfg.ID]; ok {
		oldClient = old
		delete(m.clients, cfg.ID)
		for tName, sID := range m.toolRouting {
			if sID == cfg.ID {
				delete(m.toolRouting, tName)
			}
		}
	}

	for _, t := range tools {
		m.toolRouting[t.Name] = cfg.ID
	}
	m.clients[cfg.ID] = client
	m.mu.Unlock()

	if oldClient != nil {
		_ = oldClient.Stop()
	}
	return nil
}

// StopServer 安全停止指定 MCP 服务并清理路由 (锁外执行进程退出，避免死锁)
func (m *Manager) StopServer(ctx context.Context, srvID string) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	client, ok := m.clients[srvID]
	if !ok {
		m.mu.Unlock()
		return nil
	}

	delete(m.clients, srvID)
	for tName, sID := range m.toolRouting {
		if sID == srvID {
			delete(m.toolRouting, tName)
		}
	}
	m.mu.Unlock()

	return client.Stop()
}

// StopAll 停止所有运行中的 MCP 服务 (锁外批量停止)
func (m *Manager) StopAll() {
	if m == nil {
		return
	}

	m.mu.Lock()
	clientsToStop := make([]Client, 0, len(m.clients))
	for _, client := range m.clients {
		clientsToStop = append(clientsToStop, client)
	}
	m.clients = make(map[string]Client)
	m.toolRouting = make(map[string]string)
	m.mu.Unlock()

	for _, client := range clientsToStop {
		_ = client.Stop()
	}
}

// RegisterClient 注册指定客户端 (用于测试治具或自定义插件)
func (m *Manager) RegisterClient(srvID string, client Client, tools []Tool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clients[srvID] = client
	for _, t := range tools {
		m.toolRouting[t.Name] = srvID
	}
}

// SyncFromConfig 根据配置同步启动/关闭 MCP 服务
func (m *Manager) SyncFromConfig(ctx context.Context, cfgs []config.MCPServerConfig) {
	for _, cfg := range cfgs {
		if cfg.Enabled {
			m.mu.RLock()
			_, running := m.clients[cfg.ID]
			m.mu.RUnlock()
			if !running {
				_ = m.StartServer(ctx, cfg)
			}
		} else {
			_ = m.StopServer(ctx, cfg.ID)
		}
	}
}

// GetAllTools 获取所有已启用服务的算子定义，并转换为 OpenAI LLM 工具格式 (锁外请求各服务，避免长阻塞)
func (m *Manager) GetAllTools(ctx context.Context) ([]llm.ToolDef, error) {
	if m == nil {
		return []llm.ToolDef{}, nil
	}

	m.mu.RLock()
	clientMap := make(map[string]Client, len(m.clients))
	for srvID, client := range m.clients {
		clientMap[srvID] = client
	}
	m.mu.RUnlock()

	defs := make([]llm.ToolDef, 0)
	for srvID, client := range clientMap {
		tools, err := client.ListTools(ctx)
		if err != nil {
			continue
		}

		for _, t := range tools {
			var params map[string]any
			if len(t.InputSchema) > 0 {
				_ = json.Unmarshal(t.InputSchema, &params)
			}
			if params == nil {
				params = map[string]any{
					"type": "object",
				}
			}

			isMutating := true
			if t.Mutating != nil {
				isMutating = *t.Mutating
			}

			defs = append(defs, llm.ToolDef{
				Type: "function",
				Function: llm.ToolFunctionDef{
					Name:        t.Name,
					Description: fmt.Sprintf("[%s] %s", srvID, t.Description),
					Parameters:  params,
				},
				Mutating: isMutating,
			})
		}
	}

	return defs, nil
}

// CallTool 派发算子调用到对应的 MCP Client 进程
func (m *Manager) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if m == nil {
		return "", fmt.Errorf("mcp manager is not initialized")
	}

	m.mu.RLock()
	srvID, exists := m.toolRouting[name]
	if !exists {
		m.mu.RUnlock()
		return "", fmt.Errorf("tool %s not registered in any active MCP server", name)
	}

	client, hasClient := m.clients[srvID]
	m.mu.RUnlock()

	if !hasClient {
		return "", fmt.Errorf("mcp server %s not running", srvID)
	}

	return client.CallTool(ctx, name, args)
}

// IsRunning 检查指定 MCP 服务当前是否运行中
func (m *Manager) IsRunning(srvID string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, running := m.clients[srvID]
	return running
}

// GetRunningServerIDs 返回当前处于活跃连接状态的 MCP 服务 ID 列表
func (m *Manager) GetRunningServerIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.clients))
	for id := range m.clients {
		ids = append(ids, id)
	}
	return ids
}
