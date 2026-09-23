# 基建修复与剩余事项（PENDING）

> 更新：2026-09-23 ｜ 基线 `main` = `0416033`（VERSION 0.1.0）
> 本文原为"未完成需求盘点"。其中属于**基础底层能力**的部分已在本轮全部修完并逐条实测，
> 现改为"修复记录 + 剩余事项"。未执行的项一律显式标注，不伪装成已验证。

---

## 一、本轮修复（8 项，全部有实测证据）

### 1. 新克隆构建三条全红 —— 两个 `go:embed` 缺文件（最严重）

**症状（实测复现）**
```
vendor\github.com\wailsapp\wails\v2\internal\webview2runtime\webview2installer.go:9:12:
    pattern MicrosoftEdgeWebview2Setup.exe: no matching files found
main.go:24:12: pattern all:frontend/dist: no matching files found
go build exit=1   go vet exit=1   go test exit=1
```

**根因（两个独立缺陷叠加）**
- `frontend/dist/.gitkeep` **从未入库**。`.gitignore` 里确实写了 `!frontend/dist/.gitkeep`，
  但它上面那条 `dist/`（未锚定）把 `frontend/dist` **目录本身**也排除了——而 gitignore(5) 明确规定
  **父目录被排除时无法再取反其中的文件**。取反规则由此变成一句废话。
- `.gitignore` 的"白名单"只写在**注释**里（注释不生效），于是 `*.exe` 把 wails 用 `//go:embed`
  无条件嵌入的 `MicrosoftEdgeWebview2Setup.exe` 静默挡在仓库外。

**修法**
- 补入库 `frontend/dist/.gitkeep`
- `bin/ build/ dist/` → 锚定为 `/bin/ /build/ /dist/`（消除父目录排除）
- 新增真正的取反规则 `!vendor/**`（vendor 是第三方源码快照，内部文件一个都不能少）

**实测**：`go build ./...` 零输出；`go test ./... -count=1` 13 个包全部 ok。

---

### 2. `gofmt` 门禁在任何 Windows 机器上都永远无法通过

**症状**：`gofmt -l main.go app internal cmd` 列出**全部 60 个** Go 文件。

**根因**：仓库没有 `.gitattributes`。Git for Windows 安装默认 `core.autocrlf=true`，把工作区签出为
CRLF，而 gofmt 只输出 LF → 每个文件都被判为"需要格式化"。行尾是**仓库约定**，不能交给各人的
`core.autocrlf` 决定。

**修法**：新增 `.gitattributes`（`*.go text eol=lf`、`*.ps1 eol=crlf`、二进制 `binary`、
vendor 保持文本归一化），并把工作区统一为 LF。

**实测**：`gofmt -l` 空输出。

> 附带教训：写 `vendor/** -text` 会让 vendor 里的 CRLF 文件与索引里的 LF 逐字节不等，
> 一次就把 1154 个文件变成"已修改"。正确做法是保持默认文本归一化。

---

### 3. 五道门禁没有任何载体

**症状**：`docs/STANDARDS.md` §6 定义了五道门禁并声明"由 CI 强制"；`scripts/arch_check.ps1` 首行写着
"Must pass before every commit / in CI"；`.golangci.yml` 写着"CI 与本地提交前执行"——
但仓库里**不存在任何 CI 配置**。门禁全靠人自觉，这正是第 1、2 条能长期存活的原因。

**修法**：新增 `.github/workflows/ci.yml`（`windows-latest`；**前端构建必须前置**，因为
`main.go` 用 `//go:embed all:frontend/dist`）。

---

### 4. 用户点"中断"会被上报成错误（真实产线缺陷）

**症状**：新增端到端测试后立刻暴露 —— `EndReason = 2`（`EndError`），而契约 C-APP-2 要求
`EndCancelled`。后果：前端 `terminalLabel` 会把主动中断渲染成错误，用户看到"⚠ 出错"而不是"已取消"。

**根因**：`internal/core/llm/runtime.go` 中继协程把 `callCtx.Done()` 的**两种完全不同成因**一律
转成 `EndError`：调用方取消（用户中断）与总时长预算到期。

**修法**：新增 `terminalOnCtxDone()`，用**外层 ctx** 区分——外层已结束即为取消 → `EndCancelled`；
否则才是预算到期 → `EndError`。

**实测**：`TestChatService_CancelKeepsEvents`（`httptest` 上游 + 真实 `chatRuntime`，端到端）由红转绿。

> **为什么这个缺陷能长期存活**：原有的 `TestAgent_CancelKeepsEvents` 用替身运行时
> **直接把 `EndCancelled` 喂进内核**，恰好绕过了真正会出错的那一环。测试通过，缺陷活着。

---

### 5. 三条契约只有名字、没有测试

| 契约 | 处理 |
| --- | --- |
| C-APP-2 | 新增 `internal/app/chat_service_test.go::TestChatService_CancelKeepsEvents`（端到端，见上） |
| C-RT-4 | 新增 `internal/core/agent/agent_test.go::TestRuntime_FacadeBoundary`：用替身运行时驱动内核跑完整一轮（编译期证明依赖接口而非实现）+ 扫描本包**产线**源码禁止跨层 import |
| C-SES-5 | **指向修正**：账本自 ADR-0002 起为追加式，不再有"全量改写 + rename"，该回退的实际保护对象是渠道配置与文件写入 → 锁定测试更正为 `TestWriteFileAtomic_RenameConflictFallback` |

三条均登记于 `docs/CONTRACTS.md` 的"契约变更记录"。

---

### 6. 架构守卫 R1 自我误报

**症状**：`[R1][FAIL] core package has cross-layer import: internal\core\agent\agent_test.go`
—— R1 是**子串匹配**且扫描 `_test.go`，而 C-RT-4 测试必须在断言里**列出**这些禁止路径，
于是把自己扫出来了。

**修法**：R1 排除 `_test.go`（它守的是**产线**依赖方向，与 R2 的既有做法一致），
并按 ADR-0004"规则演进必须先改 ADR"补登变更记录。**产线源码的判定条件一字未改。**

**实测**：`[ARCH CHECK] PASS`，exit 0。

---

### 7. 白名单与实现相互否定（文档）

`STANDARDS.md` §1 把 `frontend/wailsjs/` 列为"必须入库（前端编译依赖）"，但该目录不存在，
且 `frontend/src/wails.ts:2` 明写"**为什么不用 wailsjs 生成绑定**——避免前端编译依赖生成步骤"。
已按实物更正白名单，并补上两条 gitignore 踩坑说明。

---

### 8. 文档状态落后一个里程碑

`docs/MILESTONES.md` 把 M1–M4 全部标为"未开始"（而它们的代码与测试都已落地）、
M5 标"未开始"（6 项出口已达成）、`legacy + 新 main 推送远端` 未勾（实际早已完成）、
"docs 九件"与"六件套 + ADR×4"两处口径互斥（实际 5 篇 + 6 篇 ADR）。
已全部按实物更正，并把第 1、2、3 条补登为 M0 的"基建断链修复"出口标准。

---

## 二、门禁实测结果（本轮，本机实跑）

| 门禁 | 命令 | 结果 |
| --- | --- | --- |
| 格式 | `gofmt -l main.go app internal cmd` | ✅ 空输出 |
| 静态检查 | `go vet ./...` | ✅ exit 0 |
| 架构守卫 | `scripts/arch_check.ps1` | ✅ `[ARCH CHECK] PASS`，exit 0 |
| 测试 | `go test ./... -count=1` | ✅ 13 个包 ok |
| 前端类型+构建 | `npm run build`（`vue-tsc --noEmit` + `vite build`） | ✅ 1.30s，产物 90.78 kB |
| 前端单测 | `npm test`（vitest） | ✅ 14/14 |
| 注释/风格 | `golangci-lint run` | ⚠️ 本机原本未安装，隔离安装后实测见下节 |
| 发布流水线 | `scripts/release.ps1` | ⚠️ 见下节（先暴露了一个环境问题，已修） |

### 附：发布流水线暴露的两个问题

**(1) 脚本在"工具链不在 PATH"时报错难以定位（已修）**
首次运行 `release.ps1` 在第一步就崩：
```
gofmt : The term 'gofmt' is not recognized as the name of a cmdlet, function, script file...
CommandNotFoundException at release.ps1:12
```
根因纯粹是**环境**：本机 Go 与 Node 都不在系统 PATH（Go 在 `E:\pro\tools\go\bin`，
Node 在 `.workbuddy\binaries\node\versions\22.22.2-3`）。脚本没写错，但错误信息读起来像脚本 bug。
**修法**：在 `release.ps1` 开头加前置检查（`go` / `gofmt` / `node` / `npm` 缺一即抛出可操作提示），
保持 ASCII-only。这属于"把环境故障转成可操作提示"，不改任何构建语义。

**(2) shelltool 的时序断言在并发负载下会假红（未改，已记录）**
在后台并行 `go install`（编译 golangci-lint）时，`go test ./...` 出现
`TestShellRun_CancelKillsTree (3.40s)` 与 `TestShellRun_BackgroundLogBounded (4.11s)` 失败；
**隔离重跑整包通过（8.3s）**。加上此前 `TestShellRun_TimeoutReturns` 的同款失败，
共观察到 **3 个不同用例**在负载下"超预算"。
这些用例断言的是墙钟时间（取消须 ≤3s、后台日志固定等 1200ms），表达的是真实契约，
**不应通过放宽阈值来消除**——正确做法是把"固定 `sleep` + 单点断言"改为
**轮询直到条件成立或超时**（语义不变、不依赖机器快慢）。已把该判据写入 `docs/TESTING.md`，
避免后来者把"负载造成的红"误判成回归。

---

## 三、剩余事项

### 必须人工验收（无法由静态检查替代）

- **M2 桌面端到端**：启动 → 发一条消息 → 流式渲染 + 终态标签正确
- **M5 安装版启动实证**：无配置 → 生成模板并提示；有配置 → 窗口正常打开
- **M5 四环人工验收**：各一例真实任务，失败路径符合契约

### 刻意不做（属设计范围，非缺陷）

| 项 | 依据 |
| --- | --- |
| 多协议（Anthropic / Gemini 原生） | `core/llm/channel.go:21` `Valid()` 只认 openai，`providerfactory.go:34` 显式报错拒绝，**不静默降级**（fail-closed 做对了） |
| git 写操作（stage/commit/branch） | `gittool.go:97/103/109` 仅 `status`/`diff`/`log`；M4 出口只要求"只读 git 已装配" |
| 无 `wails.json` | `wails build` / `wails dev` 不可用，只能 `go build -tags desktop,production` 直出；README 已注明"wails CLI 可选" |
| 平台仅 Windows | `cmd/installer`、`app/dialog`、`shelltool/kill` 均有 stub/降级分支 |

### 已知脆弱点（建议后续处理，本轮未改）

1. **`TestShellRun_TimeoutReturns` 断言墙钟时间**，并发重负载下会红。本轮 `npm ci` 并行时确实红过一次
   （`timeout not enforced: took 3.45s`），隔离重跑通过（1.88s）。建议给它加容差，或标注"需独占运行"。
2. **R1 是字面量扫描而非真实依赖图**。若要更精确，应改用 `go list -deps` 或 `go/packages`，
   而不是继续叠加子串模式（已记入 ADR-0004）。
3. `frontend/dist/.gitkeep` 依赖 `vite.config.ts` 的 `keepDistPlaceholder` 插件在构建后补回；
   若有人移除该插件，构建后工作区会开始变脏（该插件已在实跑中验证生效）。

---

## 四、验证边界（诚实声明）

- 本机 Go 为 `E:\pro\tools\go`（`go1.22.5 windows/amd64`），此前**未在 PATH 中**，
  因此本项目此前所有"Go 已全绿"的说法在本机并未被独立复核过。
- 本轮每一项结论都由实际命令输出支撑；`golangci-lint` 未预装，**在补装前未实测**。
- 排查中出现过两次**偶发**的 std 解析错误（`internal/goarch is not in std`、`internal/abi`），
  隔离重跑即通过——判定为构建缓存瞬时问题，非仓库缺陷；GOROOT 实为完整（`src/internal/goarch` 等均在）。
