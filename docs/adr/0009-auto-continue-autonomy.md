# ADR-0009：自主续跑（额度内把"是否继续"的决策权交给模型）

- 状态：已采纳（默认关闭；**修订 ADR-0005 派生的"步数分段决策权一律在用户"惯例**）
- 日期：2026-10-04
- 关联：ADR-0005（设计模式取舍）、ADR-0007（审批闸门）、`docs/CONTRACTS.md`（C-AGT-10~14）、
  设计文档 `docs/superpowers/specs/2026-10-04-task-plan-closure-design.md`（档位 2）

## 背景

步数分段（第 3 批）规定：单轮 25 步用尽一律经 `ask_user` 通道问用户是否续跑。
大工程要人工点 N 次"继续执行"，节奏断裂——四项症状实测反馈中的"要人工续跑"。

前置事实（档位 1 交付）：任务清单已注入模型上下文（`DeriveInfo.LatestTodo`），
模型每轮都看得到计划——"对照清单自评该不该继续"因此才成立。

## 决策

步数用尽时，若用户配置了自主续跑额度且未触顶，先做一次**无工具的自评调用**，
让模型对着清单三选一：

| 裁决 | 条件 | 行为 |
| --- | --- | --- |
| `done` | 清单全部完成（结构化判定） | 直接收尾，不打扰用户 |
| `continue` | 有未完成项 + 模型知道下一步 + 额度未耗尽 | 自主续一段，落账留痕 |
| `blocked` | 无明确下一步 / 反复失败 / 无清单 / 额度耗尽 / 解析失败 | 问用户（保持旧行为） |

### 五条硬约束（每条都是"决策权交给模型"的刹车）

1. **额度硬封顶**（`autorun.MaxSegments = 3`）：配置手改成天文数字也不放大失控面。
2. **默认 0 = 关闭**：未配置时行为与旧版逐条等价，功能必须用户显式开启
   （与 ADR-0007 同一哲学——opt-in，绝不"默认开 + 猜"）。
3. **自评调用不带工具**（C-AGT-10）：否则模型会"边自评边继续干活"，判断失去意义。
4. **无清单强制 blocked**（C-AGT-13）：没有清单就谈不上"清单完成"，
   模型说 done 也不采纳——防自欺靠结构化判定，不靠模型自觉。
5. **解析失败一律 blocked**（C-AGT-14）：fail-closed——解析不了就问用户，绝不猜 continue。

### 与 ADR-0007 的关系

ADR-0007 的决策权在用户（执行前确认）。本 ADR 把**是否继续**这一个维度的决策权
在额度内交给模型，其余维度（工具审批、危险操作）仍归用户。额度是用户显式授予的
信任预算——授予多少、何时收回，都在用户手里（`autorun.json`）。

## 被否方案

- **直接调大 `MaxStepsPerTurn`**：失控循环的 token 风险完全不设防；且对所有用户一刀切。
- **引入规划子 Agent**：跨子系统改动，需独立 spec 与设计流程，不在本批范围。
- **每段续跑前问模型"要不要问用户"**：两段式询问只是把打扰换个位置，没有减少打扰。

## 代价与验证

- 代价：自评多一次模型调用（额度内每次分段一次）；新配置文件 `autorun.json`。
- 验证：`TestParseAutoVerdict`（解析 fail-closed）、`TestLoop_SelfAssessCarriesNoTools`
  （无工具）、`TestLoop_SelfAssessRefusesContinueWhenAllDone` / `TestLoop_SelfAssessWithoutTodoIsBlocked`
  （结构化防自欺）、`TestLoop_AutoAssessDoneEndsWithoutAsking`（done 不打扰）、
  `TestLoop_AutoQuotaThenAskUser`（触顶回落问用户）、`TestLoop_AutoContinueBudgetDefaultsToZero`
  （默认等价旧行为）。
