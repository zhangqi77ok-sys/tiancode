# 湉码 / tiancode

> **热插拔插件化桌面 AI Coding 工作台**。名字来自 **湉**：水面平静、水流安稳。  
> 基于 **Wails v2 + Go 原生微内核 + Vue 3 + TypeScript**，采用 **收敛单执行回路内核**、**Rail 物理安全防线** 与 **热插拔工具注册表**。对标专业级桌面 coding agent，提供透明、可控、高可靠的智能编程交互体验。

---

## 🧭 项目导航与核心入口

* **项目仓库**：[https://github.com/zhangqi77ok-sys/tiancode](https://github.com/zhangqi77ok-sys/tiancode)
* **工程文档与 50 篇知识库总览**：[`docs/README.md`](docs/README.md)
* **52 次核心架构演变与工程迭代详实记录**：[`docs/ARCHITECTURE_EVOLUTION.md`](docs/ARCHITECTURE_EVOLUTION.md)
* **底层工程知识与疑难解决方案速查**：[`docs/knowledge/README.md`](docs/knowledge/README.md)
* **AI 交付实现唯一施工合同**：[`docs/AI_IMPLEMENTATION_CONTRACT.md`](docs/AI_IMPLEMENTATION_CONTRACT.md)
* **特性边界矩阵与交付状态**：[`docs/V1_FEATURE_BOUNDARY_MATRIX.md`](docs/V1_FEATURE_BOUNDARY_MATRIX.md)

---

## 🏛️ 一、核心架构设计 (Single Convergent Architecture)

湉码 严格遵循单一主轴架构，打破二套循环与过度封装，将微内核执行引擎、安全防线、工具注册表与表现层状态彻底解耦：

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                              湉码 Frontend (Vue 3 + TypeScript + Pinia)               │
│   [ 单一真实源 Store: workbench.ts (会话、待确认Diff池、项目宪法、终端状态统一调度) ]       │
│   [ 单焦点主工作区 (智能对话 / Monaco编辑器 聚合切换) | Diff 对比 | 终端抽屉 | 纯净空状态 ]  │
│   [ 待采纳代码变更阻断横幅 | 严格居中暖色模态窗体系 | 16:9 人机工学空间视野 ]              │
└───────────────────────────────────────────┬────────────────────────────────────────────┘
                                            │ Wails v2 原生 IPC / Typed Event Streams
                                            ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                          湉码 Go Native Microkernel (Wails v2 Engine)                 │
│                                                                                        │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │                    Session 最小任务模型 (Session Task Model)                      │  │
│  │    Goal ➔ Status ➔ ToolBudget ➔ ToolsUsed ➔ Summary ➔ TDDPassed ➔ PendingDiffs   │  │
│  │    支持「继续」指令无损接续历史任务目标，严禁推翻重勘或无序全库遍历                │  │
│  └────────────────────────────────────────┬─────────────────────────────────────────┘  │
│                                           │                                            │
│  ┌────────────────────────────────────────▼─────────────────────────────────────────┐  │
│  │                 Single Execution Engine (单一收敛直连执行内核)                    │  │
│  │    • 生产级统一直接调用，根除二套循环，消除「两套上限、两套收尾」隐患              │  │
│  │    • AI 自主交付判定 (Zero Tool Calls 即完成) + 20 轮防爆兜底 + 连续 3 次熔断     │  │
│  │    • 四维人话结尾保障：触顶总结、用户中止、上游 4xx/5xx 转人话、空输出保全        │  │
│  │    • 审查任务铁律：先看地图（顶层结构+关键入口），再定靶向下钻，禁止盲目全库扫描 │  │
│  │    • TDD 策略刚性约束：测试未全绿则任务状态绝对不是完成，阻断虚假宣称               │  │
│  └────────────────────────────────────────┬─────────────────────────────────────────┘  │
│                                           │ 拦截链 (Rail.OnBeforeAct / OnAfterAct)     │
│  ┌────────────────────────────────────────▼─────────────────────────────────────────┐  │
│  │                       Safety Rail (生产挂钩的物理安全防线)                        │  │
│  │    • OnBeforeAct: 越界高危命令拦截、只读策略写入阻断、敏感凭据脱敏                │  │
│  │    • OnAfterAct: 物理执行审计与写后影子 Git 快照记录                              │  │
│  └────────────────────────────────────────┬─────────────────────────────────────────┘  │
│                                           │ 通过 host.Registry 统一定位与分发          │
│  ┌────────────────────────────────────────▼─────────────────────────────────────────┐  │
│  │               Hotplug Plugin & Tool Registry (微内核热插拔算子体系)               │  │
│  │    • tool.fs (沙箱读写/列表/原子写)  • tool.search (工作区 grep & find 检索)      │  │
│  │    • tool.git (版本控制与子仓库保护) • tool.terminal (CREATE_NO_WINDOW 静默执行)  │  │
│  │    • MCP Manager (标准 JSON-RPC 2.0 stdio / sse 协议)                              │  │
│  │    • provider (OpenAI / Claude / Gemini / Grok / Azure / Ollama 6 大驱动)         │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

### 依赖倒置架构铁律
微内核严格遵循单向依赖，禁止业务层直接持有具体 Tool 实例，新增算子无需修改任何路由代码：
```text
plugins/ → pkg/plugin/v1 ← internal/ ← app.go
```

---

## ⚡ 二、核心功能与技术亮点

### 1. 单一执行内核收敛与双重智能熔断器
* **AI 自主完成判定**：在 ReAct 回路中，当大模型判断目标达成（未再发起工具调用）并向用户输出文本时，系统判定任务自主圆满交付；
* **防脱缰双重主动熔断**：
  * **20 轮极端安全保险丝**：作为底层死循环安全防线；
  * **连续 3 次重复调用主动熔断**：追踪工具指纹，防止模型陷入原地死循环空耗 Token；
  * **连续 3 次报错主动熔断**：工具连续执行失败立即终止并引导用户排查；
* **四维人话收尾机制**：在触顶熔断、手动中止、上游 4xx/5xx 报错及空包等任何异常退出场景下，均以清晰的人话汇报成果与下一步建议，杜绝“突然没了”。

### 2. 多协议模型服务网关与极简鉴权 (对齐 new-api 体验)
* **6 大原生协议驱动**：内置 OpenAI（含中转及第三方端点）、Anthropic Claude（Messages API / Thinking 思考流）、Google Gemini（AI Studio 及 OpenAI 兼容）、xAI Grok（含 4.6 深度推理）、Azure OpenAI 与 Ollama 本地驱动；
* **统一凭据输入框 (对齐 new-api 体验)**：
  * **单 Key / 多 Key 模式**：直接粘贴 `sk-...` 或 `AIzaSy...`，支持换行多 Key 自动轮询调度；
  * **Google 凭据直贴**：直接粘贴 GCP 凭据 JSON（`application_default_credentials.json` 或 Service Account JSON），内核自动解构 `client_id`、`client_secret`、`refresh_token` 并自动换票保活；
  * **Codex / ChatGPT OAuth**：支持直接粘贴 OAuth JSON 或单行 Refresh Token；
  * **本地直连**：支持 Ollama 及内网服务免鉴权直连；
* **毫秒级真实探活与健康指示灯**：顶栏指示灯严格绑定主渠道真实探活状态（在线绿灯带延时、待测琥珀、离线红灯、未配灰灯、推理橙色脉冲），杜绝虚假在线。

### 3. 代码修改 Diff 审查、Monaco 多页签与 Hunk Cherry-Pick
* **改动带出 Diff 审查**：智能体写入代码时自动唤起 Monaco Diff 对比视窗，红绿对照展示前后变更；
* **行级 Hunk 分块与 Cherry-Pick**：右侧审查区将 Diff 结构化切分为独立变更块，每个块独立支持 `[✓ 采纳块]`（`git apply --cached` 暂存入 Index）与 `[✕ 丢弃块]`（`git apply --reverse` 局部无损反向还原）；
* **发送前强拦截**：存在未确认的 `PendingDiffFiles` 时，发送下一句指令自动呼出居中拦截弹窗，防止盲目覆盖代码；
* **嵌套子仓库防崩溃**：对包含未提交嵌套 Git 仓库执行暂存时自动前置探活，杜绝 Exit 128 异常，并清洗尾部斜杠。

### 4. 真实双栈 TDD 自动化验证与结构化失败清单
* **双栈级联实测**：打破单语言互斥偏见，同时级联执行 Go 原生测试 (`go test -v ./...`) 与 Node 前端测试 (`npm test`)；
* **结构化失败清单提取**：自动提取 `FAIL: TestX` 或 Jest/Vitest 失败用例名，置顶呈现 `FailedTests` 列表；测试未全绿则任务绝对不算完成，杜绝假成功。

### 5. 一等工作区检索算子与受控集成终端
* **一等检索算子 (`tool.search`)**：原生 Go 算子提供 `grep`（文本/代码正则匹配）与 `find`（文件名通配），强制 AI 先检索再下钻；
* **开发者全局检索视窗**：侧边栏支持 **📁 目录** 与 **🔍 检索** 双模切换，检索结果一键直跳 Monaco 编辑器行号；
* **Windows 零黑框终端 (`CREATE_NO_WINDOW`)**：以 `0x08000000` 静默运行外部进程，杜绝黑色控制台闪烁弹窗；终端抽屉快捷键 **`Ctrl + \``** 极速唤起；支持进程树级联销毁（`taskkill /F /T`）防孤儿挂死。

### 6. GitOps 源码控制中枢与纯净零数据
* **双层暂存分离**：`Staged Changes` 与 `Working Changes` 双层清晰呈现，与底层 Git 状态实时同构；
* **微内核影子快照**：Agent 写代码前 $\le 5\text{ms}$ 自动生成轻量 Git 影子快照，支持秒级无损回退；
* **纯净零假数据 (Zero Demo Policy)**：空项目、初次启动绝无任何硬编码伪造数据与假消息。

---

## 🎨 三、视觉与人机工程学规范

湉码 严格执行 Warm Minimalist 暖色极简视觉系统：
* **主背景色**：`#FAF8F5` (Warm Cream 柔和暖米白)
* **工作台底色**：`#F4EFEA` (Workspace Muted 米灰)
* **品牌强调色**：`#D96B27` (Terracotta Orange 陶土暖橙)
* **代码暖黑**：`#1E1C1A` (Code Dark 暖炭黑)
* **16:9 人机工学**：单焦点聚合视图自由切换：
  * `[💬 智能对话]`：全宽长句交互与思维链阅读；
  * `[◫ 双栏协同]`：左侧对话流 + 右侧代码/Diff 比对；
  * `[📝 文件与编辑器]`：Monaco 编辑器、Diff 视窗与文件树；
* **弹窗三维铁律**：全系统模态窗严格居中吸附、统一具备右上角显式 `[X]` 关闭、支持全局 `Esc` 快捷退出与悬停 Tooltip，**严禁使用浏览器原生 `alert()` / `confirm()`**。

---

## 📖 四、快速上手与使用指南

### 1. 安装与启动
* **Windows 单文件安装**：直接运行 `湉码Studio_Setup_v2.0.0.exe`，支持图形向导与静默安装参数（`-silent`，`-dir "D:\MyStudio"`）；
* **免安装绿色运行**：直接双击 `bin/tiancode.exe` 启动。

### 2. 配置模型渠道
1. 点击左侧活动栏底部的 **`⚙️ 设置`** 图标（或快捷键 `Ctrl + ,`）；
2. 切换至 **`🤖 模型与凭据`** 选项卡；
3. 点击 **`+ 添加渠道`**，选择驱动提供商（OpenAI / Claude / Gemini / Grok / Azure / Ollama）；
4. 在 **API 密钥 / OAuth JSON 凭据** 框中直接输入：
   - 粘贴单个 `sk-...` 或换行输入多个 Key 轮询；
   - 粘贴 Google ADC / Service Account JSON；
   - 粘贴 new-api OAuth 凭据 JSON 或 Refresh Token；
5. 点击 **`⚡ 测试连通性`**，确认延迟并点亮探活绿灯后保存为默认主渠道。

### 3. 打开项目工作区
1. 点击顶栏活动目录名称，调用系统原生资源管理器拾取工作区文件夹；
2. 左侧侧边栏自动加载文件树（支持 1 层受控异步懒加载，防超大项目卡顿）与 Git 变更状态。

### 4. 智能对话与任务接续
1. **选择执行策略**：
   - **`只读审查 (analyze)`**（默认）：只读探测，强制拦截代码写盘与深层目录漫游；
   - **`编码实现 (implement)`**：放行写盘与工具调用，先检索后改写；
   - **`测试驱动 (tdd)`**：实施红绿重构自愈循环，测试未通过阻断交付；
2. **长任务无损接续**：输入「继续」、「continue」或「接着做」，内核直接复用历史既定目标与未完成清单接续执行，不推翻重来；
3. **查看执行过程**：AI 推理过程中的心智思维链（ThinkingBlock）与工具调用（ToolCard）均支持折叠展开，输入区支持 `■ 终止` 即时中断。

### 5. 代码审查与一键采纳
1. AI 修改文件后，工作区自动弹出 Monaco Diff 对比视窗；
2. 审查变更内容：
   - 点击 Hunk 卡片上的 `[✓ 采纳块]` 或 `[✕ 丢弃块]` 进行行级细粒度控制；
   - 点击顶栏 **`全部采纳`** 批量将改动加入 Git 暂存区；
3. 在左侧 Git 抽屉输入 Commit 消息并点击提交。

### 6. 常用快捷键速查

| 快捷键 | 功能描述 |
| :--- | :--- |
| **`Ctrl + \``** | 快速唤起 / 隐藏底部集成终端抽屉 |
| **`Ctrl + ,`** | 快速打开系统设置中枢模态窗 |
| **`Ctrl + K`** | 全局快速启动与功能跳转面板 |
| **`Esc`** | 关闭当前处于顶层的模态弹窗或抽屉 |

---

## 🛠️ 五、本地开发与构建指南

### 前置依赖
* **Go**：1.22+
* **Node.js**：20+ (带 npm / pnpm)
* **Wails CLI**：v2.9+ (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
* **Git**：2.30+

### 1. 运行微内核测试与架构守卫
```bash
# 运行全部 Go 单元测试
go test ./...

# 运行架构合规守卫脚本 (检查分段锁、Rail 防线与工具注册规范)
go run ./tools/archcheck
```

### 2. 编译前端静态资源
```bash
cd frontend
npm install
npm run build
cd ..
```

### 3. 一键编译桌面程序与打包 Windows 安装向导
```powershell
# 执行 Windows 生产级构建打包脚本
powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1
```
打包脚本将自动执行架构合规检查、前端 Vite 生产编译、Go 微内核编译并内嵌输出至 `release/` 目录。

---

## 📚 六、核心工程知识沉淀 (docs/knowledge/)

本项目严格遵守知识点沉淀规约，在 `docs/knowledge/` 下归档了 50 篇底层核心机制剖析与实战解决方案：

* [01 - Windows 环境下 Tauri 2.0 (Rust) 编译与安装包打包全解析](docs/knowledge/01-windows-tauri2-msvc-packaging.md)
* [06 - AgentRouter 多模型网关对接、真实测速拉取与模型选择器设计](docs/knowledge/06-agentrouter-gateway-models-and-channel-sync.md)
* [13 - ReAct 自主智能体多轮自愈循环、物理算子沙箱与 Windows 静默 Shell 规范](docs/knowledge/13-react-autonomous-loop-and-silent-sandbox-execution.md)
* [16 - Git 行级 Unified Diff 结构化解析、Hunk 分块与单块 Cherry-Pick 采纳/逆向丢弃实现机制](docs/knowledge/16-monaco-unified-diff-and-hunk-cherry-pick.md)
* [18 - MCP 跨进程 Stdio 协议传输、生命周期管理与 ReAct 算子动态调度机制](docs/knowledge/18-mcp-protocol-stdio-lifecycle-and-react-dispatch.md)
* [20 - 跨语言工作区技术栈自适应探测与多轮自主 ReAct 自然收敛自愈状态机](docs/knowledge/20-language-agnostic-stack-detection-and-natural-react-loop.md)
* [24 - 核心系统前十大关键缺陷全域歼灭与桌面微内核工程加固指南](docs/knowledge/24-top-10-critical-bugs-eradication-and-architecture-hardening.md)
* [48 - 控件错名治理、防脱缰智能熔断器与 TDD 结构化失败提取](docs/knowledge/48-control-naming-honesty-and-runaway-circuit-breakers.md)
* [49 - 多协议网关、OAuth 2.0 刷新机制与 new-api 极简单输入框鉴权体验对齐](docs/knowledge/49-multi-protocol-gateway-oauth-refresh-and-newapi-alignment.md)
* [50 - 嵌套子仓库暂存防崩溃、Git Porcelain 路径清洗、工作区列表去重与采纳健壮性闭环](docs/knowledge/50-submodule-diff-defense-empty-repo-stage-guard-and-git-porcelain-hygiene.md)

完整 50 篇文档目录索引请参阅 [`docs/README.md`](docs/README.md) 与 [`docs/knowledge/README.md`](docs/knowledge/README.md)。

---

## ⚖️ 开源协议

本项目采用 MIT 协议开源。
