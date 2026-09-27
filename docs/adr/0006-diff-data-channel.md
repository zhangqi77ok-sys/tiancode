# ADR-0006：编辑 diff 的数据通路（结构化字段，而非解析工具结果文本）

- 状态：已采纳
- 日期：2026-09-23
- 关联：`docs/CONTRACTS.md`（C-FS 扩展）、ADR-0005（变化点收敛）

## 背景

用户反馈过"文件编辑 diff 不对"——旧实现在前端从工具结果文本里猜变更，
既脆（摘要一截断就丢信息）又不可靠（截断位置正好切在关键行）。

需要决定：diff 到底走"工具结果文本里塞 diff、UI 解析"，还是"内核结构化字段透传"。

## 决策

**新增结构化字段，单向透传**：

```
platform/fstool（生成，仅编辑类工具）
  → tools.ToolResult.Diff
  → core/agent 透传
  → llm.ToolEvent.Diff
  → 壳层事件 chat:tool.diff
  → UI 逐行着色渲染
```

三条硬约束：

1. **模型上下文不含 Diff**：发给模型的消息仍只用 `Content`。
   理由：diff 属于"给人看的确认信息"，进上下文只会白烧 token，并让工具语义在模型侧变模糊。
2. **diff 由工具实现生成**（`fstool.diffText`），形态为简化逐行 diff：
   公共前缀/后缀裁剪 + 中段整体替换，附 3 行上下文，上限 200 行（超出显式标注"已截断"）。
   不引入 Myers/LCS 完整算法——编辑的实际形态是"改几行、插几行"，够用且无组合爆炸风险。
3. **无变更时为空串**，调用方不得推事件（避免 UI 出现空 diff 卡片）。

## 被否方案

- **UI 解析工具结果文本**：截断即失真，格式一变就全错（旧实现的坑）。
- **引入第三方 diff 库**：为"给人看的确认信息"增加依赖不划算。
- **完整 Myers diff**：对大文件有性能与内存代价，而工具卡片只是辅助确认。

## 代价

- `ToolResult`/`ToolEvent` 各多一个字段（可忽略）。
- 简化 diff 在"多处不连续修改"时会显示为一个较大的替换块（不精确但诚实，且截断提示明确）。
- 完整审计仍以账本（`tool_result` 事件）为准，diff 只是视图。

## 验证

- `internal/platform/fstool/diff_test.go`：逐行变更 / 无变化为空 / 新建文件 / 超长截断
- `internal/platform/fstool/diff_test.go:TestTool_WriteAndReplace_IncludeDiff`：端到端接线（write/replace 均带 diff，重复写入无 diff）
- 前端 `stores/chat.test.ts:工具事件携带 diff`
