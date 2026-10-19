// 回合内上下文治理（P1 体检修复）。此前折叠只发生在回合开头与分段边界：
// 一段最多 50 步，每步工具结果全文进内存消息（单次 fs.read 可达 10MB）——
// 一个长回合在段内就能把请求撑到上游拒收，而派生折叠从来没机会介入。
//
// 两道闸：
//  1. 单条上限：工具结果进本回合内存消息前按 inTurnToolResultLimit 头尾保留
//     （账本仍存全文，与 ADR-0008 "账本全文、模型视图截断"同一纪律）；
//  2. 段内重折叠：每步工具执行完估算一次，超过预算水位就从账本重新派生
//     （历史按预算分级折叠、本回合更早步骤的结果按模型侧上限截断），声明了
//     上限的渠道仍装不下再尝试历史压缩。**本步刚产生的结果保持原样**——模型
//     马上要依据它们决策，重折叠不能把刚读到的文件截成 4KB。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// inTurnToolResultLimit 是单条工具结果进入本回合模型上下文的字节上限（约 24K token）。
// 高于跨轮的 toolResultModelLimit：同轮 ReAct 需要看到刚读的文件正文，但不能无界。
const inTurnToolResultLimit = 96 << 10

// capInTurnToolResult 按回合内上限头尾保留；超限时注明原始大小与出路。
func capInTurnToolResult(content string) string {
	if len(content) <= inTurnToolResultLimit {
		return content
	}
	return tools.HeadTail(content, inTurnToolResultLimit) +
		fmt.Sprintf("\n[本回合视图已截断：原文 %d 字节；需要中间部分请按行分段读取]", len(content))
}

// dropTruncatedCall 在输出被长度上限截断时，丢弃参数不完整的最后一笔工具调用
// （P1）。截断只会切在最后一笔上（之前的调用块已完整收尾）；参数仍是合法 JSON
// 说明恰好写完，照常执行。返回剩余调用与被丢弃的工具名（空 = 没丢）。
func dropTruncatedCall(calls []llm.ToolCall) ([]llm.ToolCall, string) {
	if len(calls) == 0 {
		return calls, ""
	}
	last := calls[len(calls)-1]
	if args := strings.TrimSpace(last.Arguments); args != "" && json.Valid([]byte(args)) {
		return calls, ""
	}
	return calls[:len(calls)-1], last.Name
}

// refoldMidTurn 在段内估算超过预算水位时重新派生上下文。返回 (新消息, true) 表示
// 已替换；预算未配置 / 未超水位 / 派生失败返回 (nil, false)，调用方沿用原消息
// （派生失败不打断回合：原消息仍是合法序列，上游真装不下会给出明确错误）。
// fresh 是本步工具结果（callID → 已按回合内上限截断的正文），重派生后原样回填。
func (l *Loop) refoldMidTurn(ctx context.Context, ledger *session.Ledger, msgs []llm.Message, fresh map[string]string) ([]llm.Message, DeriveInfo, bool) {
	if l.ctxBudgetTokens <= 0 {
		return nil, DeriveInfo{}, false
	}
	if estimateMessagesTokens(msgs) <= l.ctxBudgetTokens*contextFoldWatermark/100 {
		return nil, DeriveInfo{}, false
	}
	refreshed, info, err := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})
	if err != nil {
		return nil, DeriveInfo{}, false
	}
	// 声明了上限仍装不下：尝试历史压缩（与回合开头同一规则——兜底默认值不压缩）
	if info.Dropped && !l.ctxBudgetDefault {
		if compacted, cinfo, ok := l.tryCompact(ctx, ledger, info); ok {
			refreshed, info = compacted, cinfo
		}
	}
	for i := range refreshed {
		if refreshed[i].Role != "tool" {
			continue
		}
		if c, ok := fresh[refreshed[i].ToolCallID]; ok {
			refreshed[i].Content = c
		}
	}
	l.latestTodo = info.LatestTodo
	info.EstimatedTokens = estimateMessagesTokens(refreshed)
	return l.attachPreface(refreshed), info, true
}
