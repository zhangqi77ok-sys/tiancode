// Package agent 是 ReAct 执行内核：LLM 生成 → 工具调用 → 结果回填 → 续步。
//
// 做什么：无状态的对话执行循环。会话状态由 core/session 账本持有，
// 模型访问经 core/llm 端口，工具执行经 core/tools 端口——本包自身不做任何 IO。
// 被谁依赖：internal/app（编排）。
// 依赖谁：core/llm、core/tools、core/session（端口与类型）。
//
// 边界铁律：Loop 不持有会话文件句柄、不发起网络请求、不直接执行工具；
// 它只编排端口并遵守步数上限。无状态使本包可以脱离 UI/磁盘独立测试（TDD 核心）。
package agent

// MaxStepsPerTurn 单轮（一次用户消息触发的连续推理）最大步数。
// 为什么 25：足够覆盖常见多步编码任务，同时封顶失控循环的 token 消耗；
// 旧实现 50 步上限对个人工具过宽，实测从未用满且拉长失控恢复时间。
const MaxStepsPerTurn = 25

// Loop 是 ReAct 循环。M2 完整实现；当前仅固化包边界与上限常量。
type Loop struct{}

// NewLoop 构造循环。M2 将注入 ProviderPort 与工具注册表（构造期注入，禁止导出可变字段）。
func NewLoop() *Loop { return &Loop{} }
