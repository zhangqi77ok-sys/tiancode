// 上下文预算折算（阶段 5-2，从阶段 5 提前）。
//
// 背景（实测，不是推想）：用户渠道没填 contextLimit 时，tiancode 不折叠任何历史，
// 每个回合都把整个会话重发一遍——一次「发 115KB 文本附件」的请求体到 510KB，其中
// **约 400KB 是历史**（账本统计：assistant_delta 6922 条 ≈ 495KB），上游 90 秒没吐
// 第一个字节，被看门狗收掉。
//
// 开源工具没有一个要求"用户手填窗口才不炸"：aider 从模型推断并给 repo map 独立预算
// （--map-tokens 默认 1k），Cline 用 max(窗口-40k, 窗口×0.75) 算可用上限，open-webui
// 按窗口给 chunk×topK 预算（8K 窗口 → topK 3–5）。tiancode 缺的正是这个兜底。
package app

// defaultContextLimit 是渠道未声明上下文上限时的保守默认预算（token）。
//
// 为什么保守取 32k：我们的估算口径本身保守（ASCII 4 字符≈1 token、1 个非 ASCII
// 字符≈1 token），32k 预算约等于 96KB 中文文本；而实测该上游能建流的请求体在 208KB
// 以内、510KB 会黑洞——默认值必须落在"能出去"的一侧，宁可多折一点。
//
// 为什么可以被覆盖：用户显式给渠道填了 contextLimit 就一切以用户的为准，
// 本默认只在"一条都没声明"时兜底（见 pool.MinContextLimit 的口径）。
const defaultContextLimit = 32 << 10

// resolveContextBudget 折算本轮上下文预算：渠道声明优先（取已启用渠道的最小值），
// 全部未声明时退回保守默认值。
//
// 返回 (预算, 是否来自默认值)：第二个返回值只用于读数——界面必须写明
// 「未配置，按默认值」，否则用户会以为渠道里填过这个数（折叠不是静默行为）。
func resolveContextBudget(minDeclared int) (int, bool) {
	if minDeclared > 0 {
		return minDeclared, false
	}
	return defaultContextLimit, true
}
