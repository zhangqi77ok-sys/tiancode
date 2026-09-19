package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// HotplugItemInfo 单个热插拔插件/算子节点元数据
type HotplugItemInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Type        string            `json:"type"`     // "tool", "mcp_tool", "mcp", "rail", "provider"
	Category    string            `json:"category"` // "微内核算子", "MCP协议算子", "MCP动态服务", "SafetyRail防线", "协议驱动"
	Description string            `json:"description"`
	Healthy     bool              `json:"healthy"`
	LatencyMs   int64             `json:"latency_ms"`
	Message     string            `json:"message"`
	Parameters  map[string]any    `json:"parameters,omitempty"` // JSON Schema (工具参数)
	Mutating    bool              `json:"mutating,omitempty"`
	Priority    int               `json:"priority,omitempty"` // 治理拦截优先级 (Rail)
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// HotplugDashboardReport 热插拔插件中心与 DSH 算子大盘全景报告
type HotplugDashboardReport struct {
	Summary struct {
		TotalTools      int `json:"total_tools"`
		ActiveMCPs      int `json:"active_mcps"`
		ActiveRails     int `json:"active_rails"`
		ActiveProviders int `json:"active_providers"`
		HealthyCount    int `json:"healthy_count"`
	} `json:"summary"`
	Tools     []HotplugItemInfo `json:"tools"`
	MCPs      []HotplugItemInfo `json:"mcps"`
	Rails     []HotplugItemInfo `json:"rails"`
	Providers []HotplugItemInfo `json:"providers"`
	UpdatedAt int64             `json:"updated_at"`
}

// GetHotplugDashboard 获取当前微内核已装载的算子、协议驱动、安全轨道与 MCP 拓扑大盘
func (a *App) GetHotplugDashboard() (*HotplugDashboardReport, error) {
	report := &HotplugDashboardReport{
		Tools:     make([]HotplugItemInfo, 0),
		MCPs:      make([]HotplugItemInfo, 0),
		Rails:     make([]HotplugItemInfo, 0),
		Providers: make([]HotplugItemInfo, 0),
		UpdatedAt: time.Now().UnixMilli(),
	}

	healthyCount := 0

	// 1. 微内核注册算子 (host.Registry)
	if a.registry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		tools := a.registry.GetTools()
		for _, t := range tools {
			def := t.Definition()
			var params map[string]any
			if len(def.Parameters) > 0 {
				_ = json.Unmarshal(def.Parameters, &params)
			}

			start := time.Now()
			h := t.Health(ctx)
			latency := time.Since(start).Milliseconds()
			if h.LatencyMs > 0 {
				latency = h.LatencyMs
			}

			if h.Healthy {
				healthyCount++
			}

			info := HotplugItemInfo{
				ID:          t.ID(),
				Name:        t.Name(),
				Version:     t.Version(),
				Type:        "tool",
				Category:    "微内核算子",
				Description: def.Description,
				Healthy:     h.Healthy,
				LatencyMs:   latency,
				Message:     h.Message,
				Parameters:  params,
				Mutating:    def.Mutating,
				Metadata:    h.Metadata,
			}
			if info.Metadata == nil {
				info.Metadata = make(map[string]string)
			}
			info.Metadata["definition_name"] = def.Name
			report.Tools = append(report.Tools, info)
		}
	}

	// 2. 外部动态 MCP 算子 (如有)
	if a.mcpManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if mcpTools, err := a.mcpManager.GetAllTools(ctx); err == nil {
			for _, mt := range mcpTools {
				info := HotplugItemInfo{
					ID:          "mcp." + mt.Function.Name,
					Name:        mt.Function.Name,
					Version:     "mcp-rpc",
					Type:        "mcp_tool",
					Category:    "MCP协议算子",
					Description: mt.Function.Description,
					Healthy:     true,
					LatencyMs:   1,
					Message:     "MCP 动态挂载",
					Parameters:  mt.Function.Parameters,
					Mutating:    mt.Mutating,
					Metadata: map[string]string{
						"source": "mcp_rpc",
					},
				}
				healthyCount++
				report.Tools = append(report.Tools, info)
			}
		}
	}

	// 稳定排序 Tools
	sort.Slice(report.Tools, func(i, j int) bool {
		return report.Tools[i].ID < report.Tools[j].ID
	})

	// 3. MCP 动态服务
	if a.extraStore != nil {
		mcps := a.extraStore.ListMCPs()
		for _, srv := range mcps {
			running := false
			if a.mcpManager != nil {
				running = a.mcpManager.IsRunning(srv.ID)
			}

			statusMsg := "已停止"
			if running {
				statusMsg = "活跃连接中"
				healthyCount++
			} else if !srv.Enabled {
				statusMsg = "已禁用"
			}

			info := HotplugItemInfo{
				ID:          srv.ID,
				Name:        srv.Name,
				Version:     "mcp-v1",
				Type:        "mcp",
				Category:    "MCP动态服务",
				Description: fmt.Sprintf("传输协议: %s | 命令: %s %s", srv.Type, srv.Command, strings.Join(srv.Args, " ")),
				Healthy:     running,
				LatencyMs:   0,
				Message:     statusMsg,
				Metadata: map[string]string{
					"type":    srv.Type,
					"command": srv.Command,
					"args":    strings.Join(srv.Args, " "),
					"enabled": fmt.Sprintf("%v", srv.Enabled),
					"running": fmt.Sprintf("%v", running),
				},
			}
			report.MCPs = append(report.MCPs, info)
		}
	}

	// 4. SafetyRail 安全轨道防线
	if a.registry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		rails := a.registry.ListRails()
		for _, r := range rails {
			start := time.Now()
			h := r.Health(ctx)
			latency := time.Since(start).Milliseconds()

			if h.Healthy {
				healthyCount++
			}

			info := HotplugItemInfo{
				ID:          r.ID(),
				Name:        r.Name(),
				Version:     r.Version(),
				Type:        "rail",
				Category:    "SafetyRail防线",
				Description: fmt.Sprintf("核心执行回路治理轨道 (Priority: P-%d 阻断权)", r.Priority()),
				Healthy:     h.Healthy,
				LatencyMs:   latency,
				Message:     h.Message,
				Priority:    r.Priority(),
				Metadata: map[string]string{
					"priority": fmt.Sprintf("%d", r.Priority()),
				},
			}
			report.Rails = append(report.Rails, info)
		}
	}

	// 5. 模型协议驱动
	if a.registry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		provs := a.registry.GetProviders()
		for _, p := range provs {
			start := time.Now()
			h := p.Health(ctx)
			latency := time.Since(start).Milliseconds()
			if h.LatencyMs > 0 {
				latency = h.LatencyMs
			}

			if h.Healthy {
				healthyCount++
			}

			models, _ := p.ListModels(ctx)
			modelNames := make([]string, 0, len(models))
			for _, m := range models {
				modelNames = append(modelNames, m.ID)
			}

			desc := fmt.Sprintf("协议驱动支持模型数: %d", len(models))
			if len(modelNames) > 0 {
				desc = fmt.Sprintf("支持模型数: %d (%s)", len(models), strings.Join(modelNames, ", "))
			}

			info := HotplugItemInfo{
				ID:          p.ID(),
				Name:        p.Name(),
				Version:     p.Version(),
				Type:        "provider",
				Category:    "协议驱动",
				Description: desc,
				Healthy:     h.Healthy,
				LatencyMs:   latency,
				Message:     h.Message,
				Metadata: map[string]string{
					"model_count": fmt.Sprintf("%d", len(models)),
					"models":      strings.Join(modelNames, ","),
				},
			}
			report.Providers = append(report.Providers, info)
		}
	}

	// 稳定排序 Providers
	sort.Slice(report.Providers, func(i, j int) bool {
		return report.Providers[i].ID < report.Providers[j].ID
	})

	// 汇总统计
	report.Summary.TotalTools = len(report.Tools)
	report.Summary.ActiveMCPs = len(report.MCPs)
	report.Summary.ActiveRails = len(report.Rails)
	report.Summary.ActiveProviders = len(report.Providers)
	report.Summary.HealthyCount = healthyCount

	return report, nil
}

// ReloadHotplugRegistry 触发热插拔插件中心热重载 (同步 MCP 进程状态并刷新全景大盘)
func (a *App) ReloadHotplugRegistry() (*HotplugDashboardReport, error) {
	if a.mcpManager != nil && a.extraStore != nil {
		mcps := a.extraStore.ListMCPs()
		a.mcpManager.SyncFromConfig(context.Background(), mcps)
	}
	return a.GetHotplugDashboard()
}

// ProbeHotplugItem 对指定算子、驱动或 MCP 服务发起单点实时探活
func (a *App) ProbeHotplugItem(itemID string, itemType string) (*HotplugItemInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch itemType {
	case "tool":
		if a.registry == nil {
			return nil, fmt.Errorf("registry not initialized")
		}
		t, ok := a.registry.GetTool(itemID)
		if !ok {
			t, ok = a.registry.GetToolByName(itemID)
		}
		if !ok {
			return nil, fmt.Errorf("tool %s not found in registry", itemID)
		}
		start := time.Now()
		h := t.Health(ctx)
		latency := time.Since(start).Milliseconds()
		def := t.Definition()
		var params map[string]any
		if len(def.Parameters) > 0 {
			_ = json.Unmarshal(def.Parameters, &params)
		}
		return &HotplugItemInfo{
			ID:          t.ID(),
			Name:        t.Name(),
			Version:     t.Version(),
			Type:        "tool",
			Category:    "微内核算子",
			Description: def.Description,
			Healthy:     h.Healthy,
			LatencyMs:   latency,
			Message:     h.Message,
			Parameters:  params,
			Mutating:    def.Mutating,
			Metadata:    h.Metadata,
		}, nil

	case "provider":
		if a.registry == nil {
			return nil, fmt.Errorf("registry not initialized")
		}
		p, ok := a.registry.GetProvider(itemID)
		if !ok {
			return nil, fmt.Errorf("provider %s not found", itemID)
		}
		start := time.Now()
		h := p.Health(ctx)
		latency := time.Since(start).Milliseconds()
		models, _ := p.ListModels(ctx)
		modelNames := make([]string, 0, len(models))
		for _, m := range models {
			modelNames = append(modelNames, m.ID)
		}
		return &HotplugItemInfo{
			ID:          p.ID(),
			Name:        p.Name(),
			Version:     p.Version(),
			Type:        "provider",
			Category:    "协议驱动",
			Description: fmt.Sprintf("支持模型数: %d", len(models)),
			Healthy:     h.Healthy,
			LatencyMs:   latency,
			Message:     h.Message,
			Metadata: map[string]string{
				"model_count": fmt.Sprintf("%d", len(models)),
				"models":      strings.Join(modelNames, ","),
			},
		}, nil

	case "mcp":
		testRes, err := a.TestMCPServer(itemID)
		if err != nil {
			return &HotplugItemInfo{
				ID:        itemID,
				Name:      itemID,
				Type:      "mcp",
				Category:  "MCP动态服务",
				Healthy:   false,
				LatencyMs: 0,
				Message:   err.Error(),
			}, nil
		}
		return &HotplugItemInfo{
			ID:          testRes.ID,
			Name:        testRes.Name,
			Type:        "mcp",
			Category:    "MCP动态服务",
			Description: fmt.Sprintf("算子数: %d", testRes.ToolCount),
			Healthy:     testRes.Status == "ONLINE",
			Message:     fmt.Sprintf("握手状态: %s | 工具数: %d", testRes.Status, testRes.ToolCount),
			Metadata: map[string]string{
				"latency": testRes.Latency,
				"status":  testRes.Status,
			},
		}, nil

	case "rail":
		if a.registry == nil {
			return nil, fmt.Errorf("registry not initialized")
		}
		for _, r := range a.registry.ListRails() {
			if r.ID() == itemID {
				start := time.Now()
				h := r.Health(ctx)
				latency := time.Since(start).Milliseconds()
				return &HotplugItemInfo{
					ID:          r.ID(),
					Name:        r.Name(),
					Version:     r.Version(),
					Type:        "rail",
					Category:    "SafetyRail防线",
					Description: fmt.Sprintf("Priority: P-%d", r.Priority()),
					Healthy:     h.Healthy,
					LatencyMs:   latency,
					Message:     h.Message,
					Priority:    r.Priority(),
				}, nil
			}
		}
		return nil, fmt.Errorf("rail %s not found", itemID)

	default:
		return nil, fmt.Errorf("unsupported hotplug item type: %s", itemType)
	}
}

// ExportHotplugManifest 导出微内核算子与驱动清单供 DSH Harness 校验
func (a *App) ExportHotplugManifest() (string, error) {
	report, err := a.GetHotplugDashboard()
	if err != nil {
		return "", err
	}
	bytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
