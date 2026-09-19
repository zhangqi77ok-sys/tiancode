# 54 - 从玩具级 Go AST 到现代代码架构与依赖治理工作板 (Code Architecture & Dependency Governance Workbench)

## ① 知识点与问题背景 (Context & Problem Statement)

### 1.1 历史遗留功能的局限性与玩具式实现
在前期原型与早期设计中，系统存在一个名为“Go AST 代码架构”的模块。经技术复盘与代码审查，该模块存在严重的形式主义与工程短板：
- **简陋的力导向图 (Crude Force Layout)**：仅仅在前端使用硬编码 24 轮迭代的简易 SVG 力导向算法，将每个文件作为一个节点，文件下悬挂若干结构体名称；
- **缺乏真实架构认知**：无法识别 Go 项目的包级别（Package-level）分层与真实的 `import` 引用拓扑，看不到调用链路；
- **无法呈现依赖倒置原则 (DIP)**：Go 语言的核心优势是隐式接口实现（Duck Typing），旧功能完全无法呈现接口定义（Interface）与具体实现（Struct）之间的多态契约矩阵；
- **无重构辅助能力**：当开发者或 AI Agent 计划修改或重构某个底层结构体或核心包时，系统无法评估改动影响面（Blast Radius），无法给出直接调用者、间接波及模块和需要回归的测试套件清单；
- **缺乏架构防腐规则校验**：无法自动化校验项目的模块依赖防腐规则（例如 `AGENTS.md`【铁律 7】：`plugins/` 严禁逆向依赖 `internal/core/` 或 `app/`）；
- **与 AI Agent 割裂**：架构图沦为一个自娱自乐的只读看板，无法将架构拓扑、分层守则与违规清单作为即时上下文双向注入 AI Agent 聊天回路。

### 1.2 重构目标
彻底淘汰旧版粗糙的玩具式 AST 查看器，打造一个高密度、现代化、具备深度工程洞察力的**代码架构与依赖治理工作板 (Code Architecture & Dependency Governance Workbench)**，服务于资深架构师与自主 Coding Agent。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 2.1 基于 Go 编译前端的深层语法分析 (`go/parser`, `go/ast`, `go/token`)
工作板摒弃任何外部重型工具依赖，直接依托 Go 官方编译器前端包对工作区所有 Go 源码进行内存级快速解析：
1. **模块基准识别**：解析根目录 `go.mod` 提取主模块名称（如 `tiancode`），以此作为切分**工作区内部包依赖**与**第三方外部依赖**的精确基准；
2. **包级符号提取**：遍历每个 package 内的所有非测试（Non-test）Go 源文件，解析 AST 语法树：
   - 提取所有导出的结构体（Structs）、接口（Interfaces）、函数（Functions）、方法（Methods）；
   - 记录接口的方法签名集（方法名、入参数量、出参数量）；
   - 记录结构体绑定的 Receiver 方法集；
3. **导入依赖图构建**：从每个 AST 文件的 `file.Imports` 中提取所有以主模块前缀开头的包导入路径，剔除自身内部包循环，构建精准的包间有向边集合（`Edges`）。

### 2.2 六层语义分层分类器 (Architectural Layering Classifier)
工作板依据微内核架构哲学，将工作区内部包自动划分为清晰的六大层次，防止架构混乱：
| 层级标识 | 分层名称 | 典型路径模式 | 架构语义与职责 |
| :--- | :--- | :--- | :--- |
| `entry` | 启动入口层 | `cmd/`, `main.go` | 进程唯一入口，装配根生命周期，仅依赖 host 与 plugins |
| `host` | 桌面宿主层 | 根目录 `app*.go` | Wails 桌面胶水层，负责跨进程 IPC 转发，禁止侵入核心业务逻辑 |
| `core` | 核心引擎层 | `internal/core/`, `internal/` | 业务状态机、主循环、沙箱防御、会话存储与代码 Diff 引擎 |
| `bus` | 事件通信层 | `internal/host/`, `internal/session/` | 统一事件分发、插件注册中心 (`host.Registry`) 与总线通信 |
| `spec` | 契约协议层 | `pkg/protocol/`, `pkg/plugin/v1/` | 纯抽象接口与通讯数据结构定义，零外部依赖，100% 依赖倒置基石 |
| `tool` | 插件扩展层 | `plugins/provider/`, `plugins/tool/`, `plugins/rail/` | 热插拔工具插件、模型 Provider 与安全 Rail，仅允许单向依赖 spec |

### 2.3 接口契约多态匹配算法 (Implicit Interface Matching)
在 Go 语言中，接口实现是隐式的。工作板后端实现了轻量级的 Duck Typing 契约检测引擎：
- 对工作区内的每一个导出的抽象接口 $I$（及其要求的方法集合 $M_I$）：
- 遍历所有导出的结构体 $S$（及其拥有的 Receiver 方法集合 $M_S$）：
- 若 $M_I \subseteq M_S$（即结构体的方法集合全量覆盖接口要求的方法签名），则判定结构体 $S$ 实现了接口 $I$；
- 聚合输出契约多态矩阵：展示接口所在包、方法契约清单、所有实现结构体及其覆盖率（100% 满足或部分实现）。

### 2.4 改动影响面雷达 (Blast Radius Analysis)
当开发者或 Agent 计划修改、重构或废弃某个结构体/符号时，重构雷达以毫秒级时间完成涟漪效应分析：
1. **直接调用者 (Direct Callers)**：扫描全工作区中在字段声明、函数入参、返回值或方法体中显式引用该符号的全部文件、函数与代码行；
2. **间接影响包 (Indirect Packages)**：沿包依赖有向图进行拓扑广度优先搜索（BFS），计算所有下游传递依赖该包的模块链路；
3. **回归测试套件 (Regression Test Suites)**：关联搜寻直接影响包与间接影响包内的所有 `*_test.go` 测试套件；
4. **重构风险指数 (Risk Score)**：综合直接调用者数量、间接包深度及测试覆盖情况，给出低/中/高风险评级与架构审查建议。

### 2.5 单向依赖防腐守卫 (Architecture Rail)
依据 `AGENTS.md`【铁律 7】（插件热插拔架构强制执行），架构工作板实时监控所有包间导入关系，检测违规依赖（Violations）：
- **禁止反向依赖**：`plugins/` 下的任何插件严禁直接 import `internal/core/`、`internal/agent/` 或根目录 `app` 包；
- **依赖倒置强制约束**：插件与微内核之间必须且只能通过 `pkg/plugin/v1/` 或 `pkg/protocol/` 契约协议解耦通信；
- **红线警报与可视化阻断**：在拓扑图上将违规边用高亮危险红线渲染，提供一键“仅看违规”过滤开关，防腐合规性一目了然。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 3.1 后端领域模型与分析器实现
在 `internal/ast/` 包中分离模型定义与分析逻辑：
- `internal/ast/model.go`：定义 `PackageNode`, `SymbolItem`, `ArchitectureEdge`, `ContractItem`, `BlastRadiusReport`, `ArchitectureReport`；
- `internal/ast/analyzer.go`：
  ```go
  // AnalyzeWorkspaceArchitecture 全量解析工作区 Go 代码架构拓扑
  func AnalyzeWorkspaceArchitecture(rootDir string) (*ArchitectureReport, error) {
      // 1. 寻找 go.mod 确定 module 名称
      // 2. 扫描所有 .go 文件（排除测试、archive、dist）
      // 3. 构建 AST 树并提取符号与内部 import
      // 4. 分类架构层级与依赖关系
      // 5. 校验铁律 7 架构违规
      // 6. 隐式接口契约多态匹配
      return report, nil
  }

  // AnalyzeBlastRadius 分析指定符号的重构影响面与波及范围
  func AnalyzeBlastRadius(rootDir, targetSymbol string) (*BlastRadiusReport, error) {
      // 1. 精确查找符号定义所在包与文件
      // 2. 搜索直接引用点（Direct Callers）
      // 3. 计算依赖图传递受波及包（Indirect Packages）
      // 4. 关联回归测试套件与风险评分
      return report, nil
  }
  ```

### 3.2 Wails 宿主 IPC 接口暴露
在 `app_shell.go` 中将分析能力安全暴露至前端：
```go
// GetArchitectureReport 扫描工作区并获取代码分层与依赖治理报告
func (a *App) GetArchitectureReport() (*ast.ArchitectureReport, error) {
    workspace := a.GetWorkspace()
    return ast.AnalyzeWorkspaceArchitecture(workspace)
}

// GetBlastRadiusReport 获取指定符号的重构影响面分析报告
func (a *App) GetBlastRadiusReport(targetSymbol string) (*ast.BlastRadiusReport, error) {
    workspace := a.GetWorkspace()
    return ast.AnalyzeBlastRadius(workspace, targetSymbol)
}
```

### 3.3 前端响应式状态与暖色极简工作板
1. **统一类型定义与 IPC 封装 (`frontend/src/core/wailsBridge.ts`)**：
   - 增加 `ArchitectureReport`, `PackageNode`, `ArchitectureEdge`, `ContractItem`, `BlastRadiusReport` 接口定义；
   - 封装 `getArchitectureReport()` 与 `getBlastRadiusReport(targetSymbol)`。
2. **状态中枢 (`frontend/src/stores/workbench.ts`)**：
   - 管理 `architectureReport`、`activeArchitectureView`（分层DAG / 契约矩阵 / 影响面雷达）、`selectedArchitectureNode` 等响应式状态；
   - 提供 `scanArchitecture()`、`runBlastRadiusAnalysis(symbol)` 与 `injectArchitectureContext()` 动作。
3. **沉浸式交互工作板 (`frontend/src/components/ArchitectureModal.vue`)**：
   - 遵循 Warm Minimalist 规范（`#FAF8F5` 底色，`#F4EFEA` 面板，`#D96B27` 品牌橙，`#1E1C1A` 炭黑）；
   - 支持平移与缩放的 SVG 分层拓扑画布（自动按分层纵向排布，贝塞尔连线，违规红线脉冲）；
   - 契约多态矩阵网格（接口与结构体多态匹配状态，查看方法契约）；
   - 影响面雷达输入框与波及清单（直接调用、下游包、回归测试用例直达）；
   - 侧边抽屉实时审查选中节点的所有导出符号与依赖方向；
    - 顶栏配置“将架构注入 Agent”，一键将架构全景沉淀至用户提示词框。
4. **多模块切换、外部项目原生拾取与防投毒守卫**：
   - **Monorepo / 子模块自动发现**：通过 `DiscoverGoModules` 自动检索工作区内所有 `go.mod` 独立模块与 `cmd/` 下的可执行入口程序，在顶栏下拉菜单中整齐列出；
   - **外部项目原生拾取**：通过 Wails `runtime.OpenDirectoryDialog` 调起 Windows 资源管理器选择任意外部本地 Go 项目进行即时架构分析，无需切换 IDE 活动工作区；
   - **Agent 上下文防投毒硬防线 (Context Poisoning Defense)**：在外部参考模式下，顶栏与抽屉底部的“将架构注入 Agent”按钮**自动物理禁用**并显示拦截 Tooltip，严禁将外部项目的架构规范与包结构污染至当前工作区的 AI 会话中；
   - **瞬态自愈重置**：外部项目仅作为当前弹窗内的只读参考，弹窗关闭后自动无条件重置回当前主工作区，杜绝状态漂移与工作区错位。
5. **交互入口收敛与极简呼出**：
   - 活动栏（ActivityBar）配置专属 `🏛️ 代码架构与依赖治理` 图标；
   - 顶栏状态区提供一键唤醒胶囊按钮；
   - 彻底从 `App.vue` 中剥离旧版 120 行行内杂乱 SVG，提升组件独立性与单向数据流纯净度。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **工作区目录归一化与 Module 匹配**：
   - 在解析 `import` 路径时，必须准确剥离 `go.mod` 定义的 module 前缀（如 `tiancode/internal/core` -> 相对路径 `internal/core`）；
   - 必须过滤标准库包（如 `fmt`, `os`, `context`）和外部三方依赖（如 `github.com/...`），仅保留工作区内部模块，保持依赖图纯净不臃肿。
2. **严格隔离非发货目录**：
   - 解析器扫描时必须强过滤 `archive/`、`node_modules/`、`dist/`、`.git/` 等非发货目录，严禁将历史废弃代码带入当前架构图。
3. **测试文件与业务文件物理隔离**：
   - 构建依赖图时必须跳过 `*_test.go` 文件，因为 Go 测试文件允许引入测试治具或同级测试包，混入会导致依赖图出现虚假的“循环依赖”或逆向依赖假警报。
   - `*_test.go` 仅在计算“改动影响面雷达 (Blast Radius)”时用于提取回归测试套件。
4. **性能与内存友好**：
   - 解析时使用 `parser.ParseComments` 仅在需要时提取注释，利用包级缓存使得 500+ 文件规模的 Go 仓库在 30 毫秒内即可完成全量拓扑计算，保障 UI 零延迟流畅体验。
5. **防投毒与瞬态生命周期原则**：
   - 严禁允许跨项目注入架构上下文到 Agent 会话；外部参考项目必须在关闭或退出时清空瞬态状态，杜绝全局持久化混乱。
6. **铁律 0.5 实践：零假数据原则**：
   - 前端空状态必须诚实展示“未打开有效 Go 工作区或未探测到 Go 模块”，严禁展示硬编码的假架构图或假节点。
7. **符号级影响面精准雷达 (Symbol-level Call Sites)**：
   - 利用 `ast.Inspect` 结合 `ast.SelectorExpr` 与 `ast.Ident` 精准搜寻具体调用点；
   - 提取包含相对路径、函数作用域、行号与精炼源码切片（Snippet）的强类型 `CallSite` 列表，为重构提供外科手术级的精准视野；
8. **微内核 `tool.arch` 算子闭环 (Agent Autonomous Architecture Tool)**：
   - 实现符合 `pkg/plugin/v1.ToolPlugin` 标准接口的 `tool.arch` 插件；
   - 暴露 `code_architecture` 算子（动作：`inspect`、`blast_radius`、`discover_modules`），彻底赋能自主智能体在重构前自发调用评估，零 hardcode 路由，100% 铁律 7 合规；
9. **符号与契约源码穿梭 (Click-to-Source in Monaco)**：
   - 抽象接口契约、结构体实现、抽屉导出符号、影响面调用点支持一键单击直跳 Monaco 编辑器对应代码文件与行号高亮；
10. **影响面回归测试一键执行 (In-Place Regression Test Runner)**：
    - 雷达看板列出的推荐回归测试条目支持 `[▶ 运行]` 按钮，直接调度底层终端抽屉实时流式执行 `go test -v ./<pkg>/...` 并查看即时结果；
11. **SVG 画布自适应 4 列流式折行与动态层高 (Multi-row Flow Layout per Layer)**：
    - 废弃单行无限水平外延卡片，按层采用 4 列网格自适应折行；
    - 基于各层实际模块数量与行数动态计算层高与 Y 轴偏移，保证 16:9 画布内高密度展示且无交叉重叠。

