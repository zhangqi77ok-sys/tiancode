# 51. 插件热插拔中心、DSH 算子大盘与微内核动态拓扑一等入口设计

## ① 知识点与问题背景 (Context & Problem Statement)

在现代 AI Coding 桌面端研发中，系统宣称支持“微内核 + 热插拔插件化架构（Plugin Hotplug Architecture）”以及“DeepSeek Harness (`dsh`) 算子驾驭哲学”。然而在前端界面演进过程中，侧边活动栏（ActivityBar）被精简为仅包含“对话、文件、Git、终端、设置”五个基础按钮，造成以下关键断层：
1. **热插拔能力缺乏可视化一等入口**：用户无法在主工作台直观感知系统当前已挂载了哪些微内核算子（如 `tool.fs`、`tool.git`、`tool.search`、`tool.terminal`、`tool.ask_user`），更无法查看动态加载的外部 MCP（Model Context Protocol）协议算子；
2. **算子参数契约透明度缺失**：大模型调用各算子时依赖精确的 JSON Schema 参数定义，但在 UI 层面缺乏透明查阅与校验渠道，开发者无法确认算子是否包含破坏性修改标记（`mutating`）以及参数约束；
3. **缺少 DSH (DeepSeek Harness) 运行态大盘**：Harness 倡导的“Agent = Model + Harness (模型即灵魂，驾驭中枢即身躯)”以及四大模态（Act/Plan/Minimal/Creator）中的 **Creator（技能造物主模式）** 散落在底层配置中，开发者现场调试 Prompt、生成自定义 Rule、编写新 Skill 与挂载 MCP 服务缺乏统一枢纽；
4. **探活与热重载链路未贯通**：在新增 MCP 服务或调整插件后，缺乏无需重启客户端的动态热重载（Hot Reload）与单点探活校验机制。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. 微内核热插拔注册中心 (`host.Registry`) 架构
- Tcode 采用微内核设计，所有算子（`v1.ToolPlugin`）、协议驱动（`v1.ProviderPlugin`）与安全轨道（`v1.RailPlugin`）在应用启动时注册至 `host.Registry` 并通过读写锁（RWMutex）保护；
- 架构铁律（Iron Rule 7）：`App` 结构体禁止直接持有具体 Tool 实例字段，业务层严禁使用 `switch toolName` 硬编码路由，工具查询与执行必须通过 `registry.GetTool(name)` 或 `registry.GetTools()` 动态获取；
- 外部 MCP 进程算子由 `internal/mcp.Manager` 管理，通过标准 JSON-RPC 2.0 协议在 `GetAllTools()` 中动态转换为大模型函数声明格式。

### 2. DeepSeek Harness (`dsh`) 算子大盘哲学
- **Harness 驾驭底座**：模型负责认知推理，沙箱隔离、代码写盘拦截、测试自纠与工具分发由 Harness 承载；
- **全景健康状态（Health Probes）**：每个注册算子与驱动必须实现 `Health(ctx context.Context) HealthStatus` 契约，支持首字时延（Latency）、探活消息与异常透传；
- **SafetyRail 防线透明度**：P-100 终极阻断权（危险命令过滤、路径穿越防御、API Key 脱敏）不仅在内核中生效，更应在 UI 上透明呈现，让开发者建立安全信赖。

### 3. 热重载与配置动态同步
- 用户在本地配置或动态调整外部 MCP 插件时，微内核通过 `mcpManager.SyncFromConfig(ctx, cfgs)` 完成增量启动与关闭；
- 后端暴露 `ReloadHotplugRegistry()`，先触发 MCP 动态对齐，再对全局微内核算子、Rails、Providers 执行一轮轻量健康扫描，返回结构化拓扑报告 `HotplugDashboardReport`。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 1. 后端拓扑检视与单点探活 SPI 实现 (`app_hotplug.go`)
通过合法访问 `a.registry` 与 `a.mcpManager`，暴露统一报告生成与单点探活接口，坚决不破坏依赖方向：
```go
// GetHotplugDashboard 获取当前微内核已装载的算子、协议驱动、安全轨道与 MCP 拓扑大盘
func (a *App) GetHotplugDashboard() (*HotplugDashboardReport, error) {
    report := &HotplugDashboardReport{
        Tools:     make([]HotplugItemInfo, 0),
        MCPs:      make([]HotplugItemInfo, 0),
        Rails:     make([]HotplugItemInfo, 0),
        Providers: make([]HotplugItemInfo, 0),
        UpdatedAt: time.Now().UnixMilli(),
    }
    // 1. 微内核注册算子 (host.Registry)
    for _, t := range a.registry.GetTools() {
        def := t.Definition()
        h := t.Health(ctx)
        report.Tools = append(report.Tools, HotplugItemInfo{
            ID: t.ID(), Name: t.Name(), Version: t.Version(),
            Type: "tool", Category: "微内核算子", Description: def.Description,
            Healthy: h.Healthy, LatencyMs: h.LatencyMs, Mutating: def.Mutating,
            Parameters: params,
        })
    }
    // 2. 外部 MCP 动态算子与活跃服务
    // 3. SafetyRail (P-100 阻断权)
    // 4. Provider 大模型驱动
    return report, nil
}
```

### 2. 前端暖色极简工作台入口与模态大盘 (`HotplugDashboardModal.vue`)
- **一等视觉入口**：在 `ActivityBar.vue` 侧边栏配置独立 `🧩` 按钮，并在 `ChatCockpit.vue` 顶栏部署 `🧩 算子大盘 (N)` 快捷胶囊；
- **五大象限视窗**：
  - `⚡ 微内核算子 (Tools)`：展示参数 JSON Schema 查看器、只读/写盘徽章、单点探活按钮；
  - `🔌 MCP 动态服务 (MCP)`：展示 stdio/sse 进程状态、一键握手探活与接入通道；
  - `🛡️ SafetyRail 防线 (Rails)`：展示前置后置拦截器与命令防御规则；
  - `🌐 协议驱动 (Providers)`：展示 OpenAI / Claude / Gemini / Grok / Azure / Ollama 支持模型；
  - `🛠️ DSH 技能造物 (Creator)`：集成现场创建 Skill、定义 Rule 与挂载 MCP 的直通工作台；
- **弹窗铁律合规**：严格遵循 `#FAF8F5` 暖米白与 `#D96B27` 陶土暖橙，屏幕严格居中，支持 `Esc` 退出与右上角显式 `[X]`。

### 3. 全局命令面板与快捷键联动
在 `workbench.ts` 中注册 `Ctrl+K` 全局快捷项：
```ts
{ 
  id: 'hotplug', 
  kind: '算子', 
  label: '热插拔插件中心与 DSH 算子大盘', 
  hint: '微内核算子 / MCP / SafetyRail / 协议驱动', 
  run: () => { void openHotplugDashboard() } 
}
```

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **绝对禁止持有 Tool 实例（守卫脚本自动阻断）**：
   - 严禁在 `App` 结构体增加 `*fstool.Tool` 或任何具体插件字段；
   - 必须通过 `a.registry.GetTools()` 或 `a.registry.GetTool(id)` 查询，否则会被 `tools/archcheck` 与 `scripts/arch_check.ps1` 阻断提交。
2. **动态 MCP 工具计数前置差异处理**：
   - 客户端初次启动且未触发 MCP 握手前，算子列表仅包含内核默认的 5 项算子；
   - 执行 `ReloadHotplugRegistry()` 时会调用 `mcpManager.SyncFromConfig()` 拉起外部服务，工具总数会增加（例如加入 12 个 Filesystem MCP 算子变为 17 个）；单元测试需断言 `reloaded.Summary.TotalTools >= report.Summary.TotalTools`。
3. **严禁使用浏览器原生 `alert()` / `confirm()`**：
   - 探活结果与热重载反馈统一通过桌面端全局 Toast（暖炭黑胶囊 `#18181B`）提示，并支持将清单 JSON 复制到系统剪贴板。
