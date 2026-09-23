# 测试策略（TESTING）

> 契约的锁定手段。核心纪律：**先写失败测试再写实现（TDD）**，测试名即契约 ID 的实现形态。

## TDD 流程（每个行为契约）

1. **红**：为契约写失败测试（如 `TestProviderStream_IdleTimeout`），运行确认失败；
2. **绿**：写最小实现让测试通过；
3. **重构**：在测试保护下整理，不引入新契约；
4. **提交**：测试与实现同一原子提交（`test:` + `feat:` 可合并为 `feat(scope):`，提交信息注明契约 ID）。

## 测试命名与组织

- 命名：`Test<Module>_<Behavior>`，与 `CONTRACTS.md` 契约 ID 一一对应；
- 表驱动优先：同类输入输出变体用 `t.Run` 子测试，不复制测试体；
- 断言用标准库，不引入第三方断言库（YAGNI，保持 stdlib-only 内核可离线测试）。

## 上游模拟（不依赖真实 LLM 服务）

- `httptest.NewServer` 构造 OpenAI 兼容 SSE 上游，覆盖：
  正常流 / `[DONE]` / 流内 error 报文 / 挂起不响应（配合短 idleTimeout）/ 连接重置 / 慢消费；
- 故障注入模式：专用测试 server 按脚本逐行吐数据，测试可控时间线；
- 时间相关（空闲超时）通过可注入的 idleTimeout 参数（测试传毫秒级），不 sleep 长等待。

## 契约测试清单（按里程碑交付）

| 里程碑 | 契约组 | 测试数 |
| --- | --- | --- |
| M1 | C-SES-1 ~ C-SES-6 | 6 |
| M2 | C-LLM-1 ~ C-LLM-7、C-RT-1 ~ C-RT-4、C-APP-1、C-APP-2 | 13 |
| M3 | C-FS-1 ~ C-FS-4、C-TOOL-1（fs 部分） | 4+ |
| M4 | C-TOOL-1 ~ C-TOOL-5（shell/git） | 5 |
| M5 | 全量回归 + 守卫 + 打包冒烟 | — |

## 离线约束

- `internal/core/**` 仅依赖 stdlib → `go test ./...` 离线可跑（M0 已验证）；
- 涉及 wails 的壳层测试依赖 `vendor/`（首次联网生成）；
- **CI 全量门禁见 `STANDARDS.md` §6**，落地于 `.github/workflows/ci.yml`（windows-latest，
  必须先构建前端再跑 Go 门禁——`main.go` 用 `//go:embed all:frontend/dist`）。

> 反面教训（保留以示警）：本仓库曾有两条契约（C-RT-4、C-APP-2）**只有契约表里的名字、
> 没有对应测试**，长期未被察觉——因为没有任何门禁在执行"契约 ID ↔ 测试名"的核对。
> 其中 C-APP-2 的缺陷尤其隐蔽：已有的 `TestAgent_CancelKeepsEvents` 用替身运行时**直接喂入
> `EndCancelled`**，恰好绕过了真正会出错的那一环（runtime 中继把 ctx 取消误判成 `EndError`），
> 于是"测试通过"与"用户点中断看到错误"并存。
> **结论：契约的锁定测试必须走真实路径；用替身替掉待验证的那一环，等于没有测。**

## 明确不测什么（YAGNI）

- 不为占位骨架写"形式测试"（如断言 struct 非空）；
- 不 mock 内核端口做端到端剧本测试——契约测试即行为规范，UI 冒烟留给 M5 打包验收。

## 时序敏感测试：并发负载下会假红（读这一节能省一轮排查）

`internal/platform/shelltool` 的若干用例断言**墙钟时间**（如 C-TOOL-2/C-TOOL-5 的"取消须在 3s 内生效"、
后台日志用例的固定 1200ms 等待）。它们表达的是真实契约，**不要为了让它变绿而放宽阈值**——
但要知道：**机器上有其他重负载时它们会失败，而那不是回归。**

实测记录（2026-09-23）：与 `npm ci`／`go install` 并行时，
`TestShellRun_TimeoutReturns`、`TestShellRun_CancelKillsTree`、`TestShellRun_BackgroundLogBounded`
先后出现过 3.4–4.1s 的"超预算"失败；**隔离重跑全部通过**（整包 8.3s）。

因此：

- 看到 shelltool 的时序用例失败时，**先隔离重跑**（`go test ./internal/platform/shelltool/ -count=1 -v`）再判断；
- 不要在跑重任务（依赖安装、全量构建）的同时跑发布流水线或判断测试结果；
- 若要根治，正确做法是把"固定 sleep + 单点断言"改成**轮询直到条件成立或超时**
  （语义不变、不再依赖机器快慢），而不是加大 sleep 或放宽阈值。
