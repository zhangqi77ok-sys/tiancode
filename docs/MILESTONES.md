# 里程碑（MILESTONES）

> 每个里程碑有明确出口标准；全部满足才算完成。状态更新随提交进行。

## M0 脚手架 + 方案文档体系 + 守卫 —— 已完成

**范围**：legacy 归档；.gitignore 提交纪律；分层骨架与端口契约（包级文档）；arch_check R1-R4；golangci 门禁；前端骨架；docs 体系 + ADR。

**出口标准**：
- [x] 离线 `go build ./... && go vet ./... && go test ./...` 全绿
- [x] `scripts/arch_check.ps1` PASS
- [x] docs 体系入库（`ARCHITECTURE` / `CONTRACTS` / `TESTING` / `MILESTONES` / `STANDARDS` + `adr/0001~0006`）
- [x] legacy + 新 main 推送远端（远端 `main`、`legacy`、`backup-legacy-v0.10`、`backup-pre-clean-v2` 均已存在）

> 口径更正：原文写作"docs 六件套 + ADR×4"（出口标准处又写"九件"），与实际不符——
> 实际是 5 篇主文档 + 6 篇 ADR（见 `STANDARDS.md` §4 的文档目录定义）。已按实物统一。

### 补记：基建断链修复（2026-09-23）

同日排查"新环境能否真的跑通"时，发现四处**构建与门禁断链**。它们此前未被发现，是因为门禁没有载体
（见下一节 CI）。修复后补登为 M0 的出口标准：

- [x] **`frontend/dist/.gitkeep` 入库** —— 否则全新克隆缺 `frontend/dist` 目录，`go:embed all:frontend/dist`
      直接报 `pattern ... no matching files found`，`go build` / `go vet` / `go test` 三条全红（已实测复现）
- [x] **`.gitignore` 白名单改为真正的取反规则**（`!vendor/**`）—— 原先是"注释里列一份清单"，注释不生效；
      于是 `*.exe` 把 wails 必需的 `vendor/.../webview2runtime/MicrosoftEdgeWebview2Setup.exe` 挡在仓库外，
      而 `webview2installer.go` 用 `//go:embed` 无条件嵌入它，导致同样的三条命令全红
- [x] **`bin/ build/ dist/` 锚定到仓库根**（`/bin/` `/build/` `/dist/`）—— 不锚定的 `dist/` 会连
      `frontend/dist` **目录本身**一起排除，而 git 的规则是"父目录被排除时无法再取反其中的文件"，
      使 `!frontend/dist/.gitkeep` 永久失效。这正是占位文件从未入库的**真实机制**
- [x] **新增 `.gitattributes` 统一行尾** —— Git for Windows 默认 `core.autocrlf=true` 会把工作区签出为 CRLF，
      而 gofmt 只输出 LF，于是 `gofmt -l` 列出**全部 60 个** Go 文件，`STANDARDS.md` §6 的格式门禁
      在任何 Windows 开发机上都永远无法通过
- [x] **`.github/workflows/ci.yml` 落地** —— `STANDARDS.md` §6 定义的五道门禁此前**没有任何载体**：
      仓库里不存在任何 CI 配置，但 STANDARDS / arch_check.ps1 / .golangci.yml 三处都声明"由 CI 强制"

## M1 事件账本会话内核 —— 已完成

**范围**：`internal/core/session` JSONL 账本：追加（fsync）/ 重放（含断尾截断）；`internal/platform/atomicfile`。

> 范围更正：原文含"Windows 备份式替换回退"。账本自 ADR-0002 起为追加式（`O_APPEND` + fsync），
> **不再有全量改写 + rename**，该回退的实现移至 `internal/platform/atomicfile`，保护对象是渠道配置与文件写入。

**出口标准**：
- [x] C-SES-1 ~ C-SES-6 全部测试绿（TDD：先红后绿）
      —— C-SES-5 的锁定测试名更正为 `TestWriteFileAtomic_RenameConflictFallback`（见 `CONTRACTS.md` 契约变更记录）
- [x] arch_check PASS；`go test ./...` 离线全绿

## M2 流式对话环 —— 已完成（剩桌面人工验收）

**范围**：`internal/platform/openaiprovider`（流式纪律：空闲看门狗/发送逃生/EndReason）；`core/llm.ChatRuntime` 运行时抽象（ADR-0005：流前重试/流中不换渠道/超时预算）；`core/agent` ReAct 循环 + Phase 状态机；`internal/app` ChatService 四节点 Pipeline；`app/` 绑定层 + `main.go`（首次引入 wails 依赖 + vendor）；最小对话 UI（会话列表/流式气泡/中断按钮）。

**出口标准**：
- [x] C-LLM-1 ~ C-LLM-7、C-RT-1 ~ C-RT-4、C-APP-1、C-APP-2 测试绿
      —— C-RT-4 与 C-APP-2 的锁定测试此前缺失，2026-09-23 补齐（见 `CONTRACTS.md` 契约变更记录）。
      补测时发现并修复：`core/llm/runtime.go` 的中继把 ctx 取消误判为 `EndError`，
      导致用户点"中断"会看到错误而非"已取消"
- [x] httptest 模拟上游的六种故障时序全部按契约收束
- [x] 设计模式按 ADR-0005 落位（Runtime/State/Pipeline），无越界仪式
- [ ] **桌面端到端人工验收**：启动 → 发一条消息 → 流式渲染 + 终态标签正确（需人工 GUI，见 M5）
- [x] `go mod vendor` 入库，离线全量构建测试通过（依赖 `frontend/dist/.gitkeep`，见 M0 补记）

## M3 文件编辑环 —— 已完成

**范围**：fs 工具（read/write 原子写/replace 唯一性校验）；工具卡片 UI。

**出口标准**：
- [x] C-FS-1 ~ C-FS-4 测试绿
- [x] agent 可完成"读取→修改→写回"闭环（多步 ReAct + role=tool 回填），UI 显示工具卡片

## M4 命令+git 环 —— 已完成

**范围**：shell 工具（可配超时默认 120s/部分输出/TIMEOUT 标记/后台日志有界/进程树终止）；git 状态与基础操作。

**出口标准**：
- [x] C-TOOL-1 ~ C-TOOL-5 测试绿（shell：超时可配/部分输出+TIMEOUT/业务失败/后台日志有界/取消杀进程树）
- [x] agent 可完成"跑测试→读输出→修文件"闭环（shell + 只读 git 工具已装配）

## M5 打包与四环验收 —— 进行中（仅剩人工 GUI 验收）

**范围**：Windows 单 exe 打包（`release.ps1` 以 `go build -tags desktop,production` 直出，不依赖 wails CLI）；全量回归。

**出口标准**：
- [x] 全部契约测试绿（`-count=1` 新鲜执行，15 个含测试的包）；arch_check PASS；**golangci-lint 0 告警**
- [x] 新环境按 README 三命令跑通；离线构建验证（`GOPROXY=off` + vendor）
- [x] 真实上游冒烟通过（grok-4.6 流式 EndDone 收束）
- [x] **安装包流水线**（`scripts/release.ps1`：原生 Go 安装器，无需 NSIS/Inno）
- [x] **安装版可用性修复**（配置改用户级文件 + env 覆盖；数据目录固定用户级；启动失败弹框可见——ADR-0006）
- [x] **安装版启动实证**（2026-09-23 实机复验，`scripts/install-smoke.ps1`，**failures=0**；安装到隔离目录以保护既有安装）：
      - 安装（`-quiet`）：应用 exe / 卸载入口 / **开始菜单 + 桌面快捷方式** / HKCU 卸载项 **五项齐全**（C-INS-1）
      - `-no-desktop-shortcut`：只建开始菜单，桌面保持干净（C-INS-3）
      - 有配置启动：**主窗口就绪**——判据取应用**自身生命周期日志**出现 `started v0.1.0 ...`（该行由 `OnStartup` 写，
        `main.go` 注释即称其为"窗口就绪的可断言证据"）。**不再抓 `MainWindowTitle`**：非交互会话下标题可能一直为空，
        会把能用的构建判成失败（实测踩过）
      - 无配置启动：`%APPDATA%\tiancode\config.json` 模板**已生成** ✓
        （**弹框可见性未证实**：非交互启动下进程未阻塞在对话框而是即刻退出，需人工双击目视确认一次）
      - 卸载：上述五项**全部清除**，无孤儿 `.lnk`（C-INS-2）
      - **归属保护**：用无关 `-dir` 执行卸载 → 注册项**未被动**，且在 `setup.log` 留下可见警告（C-INS-4）
      - 测前状态全部还原：注册表键（值 + 类型）、桌面同名 `.lnk`、`config.json` ✓
- [ ] 四环人工验收各一例真实任务，失败路径符合契约（需人工 + 真实模型渠道）

## M6 编程智能体可用性 —— 实现已完成（剩 GUI 人工验收）

**范围**：跨轮工具历史回放（账本带 `id`、derive 含 `tool_calls` + `role=tool`、模型侧截断）；`fs.list`；独立 `search` 工具；对话区 markdown / thinking / 可展开工具卡；Replay 投影工具与 thinking。非目标：文件树 / 编辑器 / git 面板 / git 写操作 / 语法高亮库 / .gitignore 解析 / 会话自动标题 / 四环人工验收。

**出口标准**（与 spec §2 一致）：
- [x] 第二轮 `Send` 发给模型的 `Messages` 含上一轮完整的 `assistant(tool_calls)` + `role=tool`，ID 配对正确；单条工具结果超过 4096 字节时模型侧截断并标注，账本仍是全文（C-AGT-1~4，ADR-0008）
- [x] 模型可调用 `fs` 的 `list`（非递归、有界，C-FS-5~7）和独立工具 `search`（工作区内容搜索、有界、跳过内置忽略目录，C-SEARCH-1~6）
- [x] 对话区：助手消息渲染 markdown；有 thinking 时流式展开、终态折叠；工具卡可展开看全文；刷新/切换会话后工具卡仍在（C-APP-3）
- [x] 新契约测试先红后绿；既有 `go test ./...` 与 `frontend` vitest 不回退
- [x] 文档与代码同一批提交：`CONTRACTS` / `ARCHITECTURE` / `MILESTONES` / `TESTING` / `PENDING` + ADR-0008；`VERSION`=`0.2.0`
- [ ] **四环人工验收**仍属 M5 遗留（需人工 + 真实模型渠道，见 `PENDING.md`）；本里程碑不替代该项
- [x] `scripts/release.ps1` 发布 `0.2.0`（2026-09-28 实跑：六道门禁全绿——16 包 ok 含负载下 shelltool 15.3s、arch_check PASS、前端 3.10s；产出 `dist/tiancode-setup-v0.2.0.exe` 10.82MB 与 `tiancode-v0.2.0-portable.zip` 3.63MB，便携包内容已抽验；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.1`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 61 模块 3.14s；**UI 视觉重建随包交付**——AA 达标配色/全局焦点环/对话框与通知体系/响应式抽屉/流式节流渲染；产出 `dist/tiancode-setup-v0.2.1.exe` 10.85MB 与 `tiancode-v0.2.1-portable.zip` 3.64MB；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.2`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 2.88s；随包交付两项用户反馈修复——工具卡并入助手回合块（视觉时序：思考→执行→回复）、工作区改系统目录选择框（`Bind.PickWorkspace`，端点契约测试同步）；产出 `dist/tiancode-setup-v0.2.2.exe` 10.85MB 与 `tiancode-v0.2.2-portable.zip` 3.64MB；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.3`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 2.88s；随包交付用户反馈「输出内容很奇怪」根治——markdown 管线对齐开源实践（GFM+breaks、原始 HTML 白名单渲染替代转义、流式未闭合栅栏补齐对齐 Streamdown）、caret 内联不再单独占行；**vitest 环境补 jsdom——DOMPurify 消毒首次真正受测（此前 node 环境下 isSupported=false 原样透传，消毒契约形同虚设）**；产出 `dist/tiancode-setup-v0.2.3.exe` 10.85MB（11,375,104 B）与 `tiancode-v0.2.3-portable.zip` 3.64MB（3,815,815 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.4`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 60 模块 2.79s；**首次执行 `TestChatService_CancelKeepsEvents` 假红**——PENDING 登记过的本机间歇失败，隔离重跑 PASS 后全量重跑绿，符合「判定回归前先隔离重跑」纪律。随包交付用户截图反馈逐项修复：①消息分组抽纯函数+6 项契约测试——修工具卡冒充气泡/双重渲染（账本取证定位：实流中工具卡后随是兄弟卡，被错误路由进消息气泡分支，整份工具输出糊成回复）；②首轮结束自动命名会话（首条消息截断 20 字，open-webui/lobe-chat 惯例，已命名不覆盖）；③代码块语言标签+复制按钮（marked renderer+事件委托，开源聊天标配）；④细滚动条；⑤「回到底部」不再压输入框。产出 `dist/tiancode-setup-v0.2.4.exe` 10.85MB（11,377,152 B）与 `tiancode-v0.2.4-portable.zip` 3.64MB（3,816,280 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.5`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 2.85s。随包交付用户截图反馈的 shell 乱码根治：①控制台输出按系统代码页解码——中文 Windows cmd 输出为 GBK/CP936，直接按 UTF-8 解释曾满屏替换符；策略对齐开源工具（合法 UTF-8 透传、否则 GBK 解码、解不开原样可见），x/text 进 vendor（模块缓存离线完成）；②工具描述显式声明 shell 类型——模型曾把 PowerShell 语法喂给 cmd.exe 致 exit 255，现明确 cmd.exe/禁用 PS 语法。TDD：固定 GBK 字节解码契约测试先行（红→绿，不依赖机器代码页）。安装包增至 11MB 系 GBK 码表入库，预期内。产出 `dist/tiancode-setup-v0.2.5.exe`（11,531,264 B）与 `tiancode-v0.2.5-portable.zip`（3,915,732 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.6`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 76 模块 2.98s。「做真正的 AI 工具」UI 对齐批次，对照 ChatGPT/Cursor/Cline/open-webui 惯例逐项：①**Enter 发送/Shift+Enter 换行**（替代 Ctrl+Enter，带中文输入法 isComposing 保护——选词 Enter 不发送）；②**代码块语法高亮**——highlight.js 按需注册 11 语言+常用别名（tree-shake，未注册回退转义；包体 90KB gzip，+19KB 换代码可读性）；③**回复复制按钮**（AGENT 元信息行，复制 Markdown 原文）；④**轮次耗时显示**（terminal 时落到助手消息，`· 3.2s`）；⑤**Esc 中断生成**（模态打开时由 BaseModal 捕获层让位）。前端测试 41/41（新增高亮/耗时契约；高亮拆分导致连续字符串断言失效已修断言）。新依赖：highlight.js（判断依据：语法高亮是 AI 编程工具基线，用户明确要求对齐）。产出 `dist/tiancode-setup-v0.2.6.exe` 11.06MB（11,592,192 B）与 `tiancode-v0.2.6-portable.zip` 3.75MB（3,934,067 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.7`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 78 模块 3.05s。随包交付用户反馈「新建对话应支持新建对话和打开工作区两种情况」：①账本新增 `workspace` 事件（Send 每轮快照，归属=首个，旧账本兼容为空；core/session.Workspace + 摘要 DTO 带 workspace，契约测试 3 项）；②侧栏按空间分组（当前空间排最前默认展开、其余折叠可展开、旧会话归"未分组"、新建未发送会话归当前空间最上方——分组纯函数+5 项单测）；③双动作入口（新建对话 / 打开工作区→系统目录选择框），工作区状态收敛为共享 store（顶栏与侧栏同源）。过程中抓到并修复两处自身失误：workspace.ts 漏声明 toast（vue-tsc 抓获，amend 未推送提交保持每次提交可用）、新 Go 文件 gofmt 不合规（门禁抓获）。产出 `dist/tiancode-setup-v0.2.7.exe` 11.06MB（11,596,800 B）与 `tiancode-v0.2.7-portable.zip` 3.75MB（3,936,003 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.8`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 81 模块 3.05s、前端测试 49/49。随包交付用户确认的效果图全量：①**侧栏三段式分区**——置顶 / 会话（未归属空间）/ 空间（按工作区分组，当前空间排最前默认展开）；②**相对时间**（账本文件修改时间=最后活跃事实源，摘要带 lastActiveMs；刚刚/N分钟前/昨天/N天前/日期，纯函数+单测）；③**置顶**（账本 session_pinned 事件取最后状态、Bind.PinSession 端点+表面测试、行内星标切换，TDD 红→绿）；④**查看更多**（每列表默认 5 条，折叠/展开 key 与分区折叠分离）。重构：SessionRow 行组件抽出（三分区共用）；buildSidebar 纯函数（6 项单测）。过程中修掉 2 处自身断言/方向错误（old/new 写反 ×2、置顶空组断言）。产出 `dist/tiancode-setup-v0.2.8.exe` 11.07MB（11,604,992 B）与 `tiancode-v0.2.8-portable.zip` 3.76MB（3,938,525 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.9`（2026-09-28 实跑：门禁全绿——16 包 ok、ARCH CHECK PASS、前端 81 模块 3.10s。随包交付用户反馈语义修正：**打开/切换工作区后自动新建会话**——切换空间即在该空间开新对话（归属由首条消息落账本时的快照决定，天然记到新空间名下），侧栏"打开"与顶栏工作区两处入口同语义。前端测试 49/49 不回退。产出 `dist/tiancode-setup-v0.2.9.exe` 11.07MB（11,604,992 B）与 `tiancode-v0.2.9-portable.zip` 3.76MB（3,938,534 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.10`（2026-09-28 实跑：门禁全绿、ARCH CHECK PASS。随包交付**新会话草稿模型**修复——0.2.9 的"打开工作区自动新建"每点击一次就生成一个真实格式 ID 塞进本地列表，伪会话被分组归到当前空间组，两次打开堆两个空会话（截图实证）；改为首聊发消息时才领 ID 落账本，侧栏只镜像事件账本，`buildSidebar` 伪会话分支与冗余 ids 参数一并移除。前端 49/49（含新增"草稿不堆积"回归用例）。产出 `dist/tiancode-setup-v0.2.10.exe` 11.60MB（11,604,480 B）与 `tiancode-v0.2.10-portable.zip` 3.76MB（3,938,376 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.11`（2026-09-28 实跑：门禁全绿、ARCH CHECK PASS，前端 50/50。随包交付两项用户反馈修复：① 侧栏"查看更多"误绑折叠态表（`toggle` 改 `collapsed`，可见性读 `expanded`），点击永远无效——两处按钮改绑新增 `toggleMore`；② 首聊即时入列——发消息领 ID 的瞬间往 summaries 塞本地待定摘要（归属当前工作区、lastActiveMs=now），回合结束后 loadSessions 用账本真实数据校正，长任务跑完前侧栏即可见、空间分组不再迟到。产出 `dist/tiancode-setup-v0.2.11.exe` 11.60MB（11,604,992 B）与 `tiancode-v0.2.11-portable.zip` 3.76MB（3,938,517 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.12`（2026-09-28 实跑：门禁全绿、ARCH CHECK PASS。随包交付两项用户反馈修复：① 安装器新增 `closeRunningApp`——升级/卸载前自动关闭运行中的应用（温和 taskkill 发 WM_CLOSE → 2s 轮询等待 → 强杀），失败不阻断（文件占用错误在复制/删除步骤显式暴露），全过程落 setup.log；tasklist 解析为纯函数并有中英文"无匹配"防误报回归测试；② 工具卡药丸摘要 200→60 字符（目录列举类摘要曾撑满整行），超长 MCP 工具名同步截断（max-w-40%）。产出 `dist/tiancode-setup-v0.2.12.exe` 11.62MB（11,615,744 B）与 `tiancode-v0.2.12-portable.zip` 3.76MB（3,938,487 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.13`（2026-09-28 实跑：门禁全绿、ARCH CHECK PASS。随包交付**会话流语义化重构**（用户给参考稿定方案 A 全栈）：① 内核 `ToolResult`/`ToolEvent` 增加 `Title`/`Op` 语义字段，fs/shell/search/git 四工具以"命名返回 + defer 注记"全路径产出（文件名/命令首段 120 rune/搜索词/git 子命令 + 动作类型），`tools.Headline` 统一截断；② 账本 EventToolResult 补落 `title`/`op`/`diff`——**顺手修掉历史会话丢变更预览的存量缺口**（此前 diff 只在实时事件，Replay/重启即丢），Replay 投影三字段、旧账本缺字段前端回退工具名；③ 前端 ToolCard 重做为全宽动作行：`[状态点][类型图标][主标签][动作徽章][+N -M][查看变更▾]`，diff 面板为展开主体，shell 卡显示命令本身（此前显示输出片段），新增 file/terminal/search/wrench 四内联图标，思考文案改"深度思考"。测试：ToolRoundtrip 透传/落账断言 + ReplayIncludesTools 投影断言，前端 50/50。产出 `dist/tiancode-setup-v0.2.13.exe` 11.62MB（11,624,960 B）与 `tiancode-v0.2.13-portable.zip` 3.76MB（3,942,471 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.14`（2026-09-28 实跑：门禁全绿、ARCH CHECK PASS。随包交付**批次 1 三项用户需求**（批次 2 问答交互另发 0.2.15）：① **ReAct 段落化**——思考/中间文本按轮次分段，不再全部堆进一个气泡：后端 Replay 以 tool_call 为边界 flush 助手段（此前 delta 文本不投影、thinking 跨轮累积），前端 send 不预建占位、onChunk 按需开新段、onTool 先封存当前段再插卡，纯思考段不出空气泡；② **任务清单**——内核新增 todo 工具（Loop 按名拦截，不走普通执行：落账 EventTodo + 实时 TodoEvent + 结果回填模型），Replay 只投影最新一条原位更新，前端 TodoCard（N/M 已完成 + 勾选行可折叠），onTool 对 todo 不重复出卡；③ **输入队列**——running 提交不再拒绝而是入队，队列条支持置顶（立即发送）/取回编辑/删除，终态自动逐条发出；④ 顺手修复 chat:tool 实时载荷漏 title/op（0.2.13 回归：实时工具卡一直回退显示工具名）。测试：后端 TodoRoundtrip/ReplayTodoInPlace/分段契约断言，前端 56/56（+分段/todo/队列 5 用例）。产出 `dist/tiancode-setup-v0.2.14.exe` 11.64MB（11,643,392 B）与 `tiancode-v0.2.14-portable.zip` 3.76MB（3,950,547 B）；本地发布，未打 tag/未推送）
- [x] `scripts/release.ps1` 发布 `0.2.15`（2026-09-28 实跑：门禁全绿、ARCH CHECK PASS。随包交付**批次 2：ask_user 问答交互**（四项需求收口）：① 内核新增 `Asker` 端口（与 ADR-0007 审批端口同构——内核只管"问与等"，决策权在用户），`ask_user` 工具由 Loop 按名拦截：校验 {question, options} → 编排层 uiAsker 发 `chat:ask` 事件并阻塞等答复 → 答案作为工具结果回填模型继续推理；取消/超时为模型可见失败；问答通道未启用时返回引导性结果（不诱发重试）；② 前端 AskCard：待答态问题 + 选项按钮（点选回流 ResolveAsk），已答态展示所选答案；③ Replay 按 call-id 配对 tool_call（问题/选项）与 tool_result（答案）恢复为已答卡，旧账本兼容；④ 顺手修复组件拆分期的潜伏 bug——ApprovalCard 只 import 未渲染，审批消息一直被当普通助手气泡渲染。测试：AskRoundtrip/AskWithoutChannel/ResolveAskFlow + 前端问答 3 用例与分组断言更新，Bind 表面清单 +ResolveAsk。前端 60/60、Go 全绿。产出 `dist/tiancode-setup-v0.2.15.exe` 11.66MB（11,663,872 B）与 `tiancode-v0.2.15-portable.zip` 3.77MB（3,955,888 B）；本地发布，未打 tag/未推送）

### 已闭环：golangci-lint 零告警

`STANDARDS.md` §6 与 `.golangci.yml` 要求 `golangci-lint run` 零告警，CI 把它列为**阻断性**步骤。

**2026-09-23 已实测通过**：把 `golangci-lint v1.59.1` 装到隔离目录
（`GOBIN` + 隔离 `GOMODCACHE`，不动用户级模块缓存——直接 `go install` 曾被环境拒绝：
`rename ...\go\pkg\mod\cache\download\...zip: Access is denied`），
`golangci-lint run` → **exit 0，0 告警**。

过程中它抓到 1 条真实问题并已修：`internal/core/session/title.go` 的 `SessionTitle`
触发 `revive: exported` 的 stutter 规则（调用方已是 `session.SessionTitle`）→ 更名为
`session.Title`（2 处调用点同步更新；契约登记的测试名 `TestSessionTitle_*` 保持不变）。

同批复核：`gofmt -l` 空输出、`go vet ./...` exit 0、`go test ./... -count=1` 15 包全 ok、
`scripts/arch_check.ps1` PASS。**六道门禁首次全部有本机实测证据。**
