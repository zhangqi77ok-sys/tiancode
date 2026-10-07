// 历史压缩（0.0.41）：折叠到底仍超预算时的最后一级。此前该形态直接硬报错
// （"本轮上下文超过渠道上限"），用户只能新开会话；现在把旧轮次的**对话叙事**
// 摘要成一段续传上下文再继续。
//
// 纪律边界（0.0.09 用户裁决不变）：LLM 摘要只覆盖对话叙事（用户原话、助手结论、
// 工具调用的事实骨架）；工具输出本身仍走确定性单行（fold*），摘要模型绝不经手
// 行号与报错原文——它只决定"哪些事值得记住"，不负责转述技术细节。
package agent

import (
	"context"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// compactionKeepTurns 是压缩时保留全文的最近轮数（与 keepFullToolTurns 同口径的
// "最近"定义）。保留 3 轮：当前轮 + 前两轮的来龙去脉；更早的进摘要。
const compactionKeepTurns = 3

// compactionTranscriptLimit 是给摘要模型的转录文本字节上限（头尾保留）：
// 超过它说明叙事本身极长，头尾保留已足够摘要模型抓住主线。
const compactionTranscriptLimit = 96 << 10

// compactionSystemPrompt 是摘要调用的系统说明。要求：只压缩不发明；保留目标、
// 决定、文件路径与状态、未完成事项；短。
const compactionSystemPrompt = "你是会话历史压缩器。把给出的对话历史压缩成一段续传摘要，供同一个助手稍后继续这项工作使用。" +
	"必须保留：用户的原始目标与明确要求；过程中做出的关键决定及其理由；提到过的文件路径与它们的改动状态；尚未完成的事项与下一步。" +
	"严禁发明不存在的内容；严禁添加评论或建议；直接输出摘要正文（中文，不超过 500 字），不要任何前后缀。"

// tryCompact 在"折叠到底仍超预算"时尝试压缩一轮历史。返回 ok=false 表示放弃
// （不可压缩 / 摘要失败 / 压缩后仍超）——调用方走原有硬报错路径，行为与旧版一致。
// 压缩成功时把 EventCompaction 落账（write-ahead，账本即事实源）并按预算重新派生。
func (l *Loop) tryCompact(ctx context.Context, ledger *session.Ledger, ctxInfo DeriveInfo) ([]llm.Message, DeriveInfo, bool) {
	// 已压缩过仍超：说明截点之后的内容本身就装不下，再压只会丢最近上下文——
	// 放弃（也杜绝连续压缩循环）。
	if ctxInfo.CompactionUpTo > 0 {
		return nil, DeriveInfo{}, false
	}
	// 截点选择：保留最近 compactionKeepTurns 个用户轮全文，更早的进摘要。
	// 截点必须落在用户消息上——轮与轮的边界，tool_call/result 配对绝不会跨截点断裂。
	userSeqs, err := l.userMessageSeqs(ledger, ctxInfo.CompactionUpTo)
	if err != nil || len(userSeqs) <= compactionKeepTurns {
		return nil, DeriveInfo{}, false
	}
	cutoff := userSeqs[len(userSeqs)-compactionKeepTurns]
	summary, ok := l.summarizeBefore(ctx, ledger, ctxInfo.CompactionUpTo, cutoff)
	if !ok {
		return nil, DeriveInfo{}, false
	}
	if _, err := ledger.Append(session.EventCompaction, map[string]any{
		"up_to_seq": cutoff, "summary": summary,
	}); err != nil {
		return nil, DeriveInfo{}, false
	}
	refreshed, info, err := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})
	if err != nil || info.Dropped {
		// 压缩后仍超预算：不再追加压缩（上面已落账，下轮直接走"已压缩"放弃路径）
		return nil, DeriveInfo{}, false
	}
	l.latestTodo = info.LatestTodo
	return refreshed, info, true
}

// userMessageSeqs 收集（自 fromSeq 起）用户消息的 seq，供截点选择。
// 重放失败上抛（R2：账本读不出来时宁可放弃压缩，不可当作"没有历史"）。
func (l *Loop) userMessageSeqs(ledger *session.Ledger, fromSeq int64) ([]int64, error) {
	var seqs []int64
	err := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventUserMessage && ev.Seq() > fromSeq {
			seqs = append(seqs, ev.Seq())
		}
		return nil
	})
	return seqs, err
}

// summarizeBefore 把 (afterSeq, cutoff) 区间的历史转录成文本并请模型摘要。
// 转录先按预算折叠规则派生（Budget=0 不裁剪），再在派生结果里定位截点——
// 摘要模型的输入里工具输出已是确定性单行（0.0.09 纪律），叙事保持原文。
func (l *Loop) summarizeBefore(ctx context.Context, ledger *session.Ledger, afterSeq, cutoff int64) (string, bool) {
	msgs, _, err := deriveMessagesWith(ledger, DeriveOptions{})
	if err != nil {
		return "", false
	}
	// 定位截点：派生输出里的真实用户轮与 userSeqs 一一对应（摘要消息带 marker，
	// 不参与计数）。截点前的历史 = 前 k 个真实用户轮（userSeqs[0..k-1] 均在截点前），
	// 即转录到第 k+1 个真实用户轮之前。
	turnSeqs, err := l.userMessageSeqs(ledger, afterSeq)
	if err != nil {
		return "", false
	}
	k := 0
	for i, s := range turnSeqs {
		if s >= cutoff {
			k = i
			break
		}
	}
	if k <= 0 {
		return "", false
	}
	seen := 0
	cutIdx := -1
	for i := range msgs {
		if msgs[i].Role != "user" || strings.HasPrefix(msgs[i].Content, compactionMarker) {
			continue
		}
		seen++
		if seen == k+1 {
			cutIdx = i
			break
		}
	}
	if cutIdx <= 0 {
		return "", false
	}
	var sb strings.Builder
	for _, m := range msgs[:cutIdx] {
		switch m.Role {
		case "user":
			if strings.HasPrefix(m.Content, compactionMarker) {
				sb.WriteString(strings.TrimPrefix(m.Content, compactionMarker) + "\n\n")
				continue
			}
			fmt.Fprintf(&sb, "用户：%s\n\n", m.Content)
		case "assistant":
			if len(m.ToolCalls) > 0 {
				names := make([]string, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					names = append(names, tc.Name)
				}
				fmt.Fprintf(&sb, "助手（调用工具：%s）：%s\n\n", strings.Join(names, ", "), m.Content)
				continue
			}
			fmt.Fprintf(&sb, "助手：%s\n\n", m.Content)
		case "tool":
			// 工具结果只进事实骨架（截断到 400 字节，头尾保留）——摘要模型
			// 不需要全文，也绝不允许它转述行号与报错细节。
			fmt.Fprintf(&sb, "[工具结果] %s\n\n", tools.HeadTail(m.Content, 400))
		}
	}
	transcript := tools.HeadTail(sb.String(), compactionTranscriptLimit)
	if strings.TrimSpace(transcript) == "" {
		return "", false
	}
	ch, err := l.runtime.Chat(ctx, llm.ChatRequest{
		Model: l.model,
		Messages: []llm.Message{
			{Role: "system", Content: compactionSystemPrompt},
			{Role: "user", Content: transcript},
		},
	}, llm.DefaultRuntimePolicy())
	if err != nil {
		return "", false
	}
	var out strings.Builder
	for c := range ch {
		if c.EndReason != llm.EndNone {
			if c.EndReason != llm.EndDone {
				return "", false
			}
			break
		}
		out.WriteString(c.Delta)
	}
	summary := strings.TrimSpace(out.String())
	if summary == "" {
		return "", false
	}
	return summary, true
}
