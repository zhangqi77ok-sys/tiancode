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
| M2 | C-LLM-1 ~ C-LLM-7、C-APP-1、C-APP-2 | 9 |
| M3 | C-FS-1 ~ C-FS-4、C-TOOL-1（fs 部分） | 4+ |
| M4 | C-TOOL-1 ~ C-TOOL-5（shell/git） | 5 |
| M5 | 全量回归 + 守卫 + 打包冒烟 | — |

## 离线约束

- `internal/core/**` 仅依赖 stdlib → `go test ./...` 离线可跑（M0 已验证）；
- 涉及 wails 的壳层测试依赖 `vendor/`（首次联网生成）；
- CI 全量门禁见 `STANDARDS.md` §5。

## 明确不测什么（YAGNI）

- 不为占位骨架写"形式测试"（如断言 struct 非空）；
- 不 mock 内核端口做端到端剧本测试——契约测试即行为规范，UI 冒烟留给 M5 打包验收。
