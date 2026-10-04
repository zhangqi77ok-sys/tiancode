package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
)

// autoVerdict 是模型对"还要不要继续"的自主裁决（档位 2，ADR-0009）。
type autoVerdict string

const (
	// verdictDone 清单已清空，直接收尾（不再问用户）。
	verdictDone autoVerdict = "done"
	// verdictContinue 仍有明确未完成项且知道下一步，额度内可自主续跑。
	verdictContinue autoVerdict = "continue"
	// verdictBlocked 无明确下一步 / 反复失败 / 额度耗尽 / 无清单 / 解析失败——
	// 一律问用户。解析失败也归这里：解析不了就问用户，绝不猜一个 continue（fail-closed）。
	verdictBlocked autoVerdict = "blocked"
)

// autoAssessPrompt 是自评的追加提问（只存在于本次请求，不落账本、不进系统提示——
// 与 pinnedCallNote / SetTrailingNote 同一纪律：它是"内部判断"，不是会话事实）。
const autoAssessPrompt = "（本轮内部判断，不是用户原话）你已连续执行 %d 步。" +
	"对照上面的任务清单判断接下来该怎么办，用且仅用一行 JSON 回答，" +
	"不要调用任何工具、不要继续干活：" +
	`{"verdict":"done|continue|blocked","reason":"一句话理由"}。` +
	"done=清单已全部完成；continue=仍有明确未完成项且你知道下一步做什么；" +
	"blocked=没有明确下一步或反复失败。"

// parseAutoVerdict 从模型回复里取裁决：取首个 '{' 到末个 '}' 之间的 JSON 段。
// 解析失败或取值非法一律 verdictBlocked。
func parseAutoVerdict(text string) autoVerdict {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return verdictBlocked
	}
	var p struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &p); err != nil {
		return verdictBlocked
	}
	switch autoVerdict(strings.TrimSpace(p.Verdict)) {
	case verdictDone, verdictContinue, verdictBlocked:
		return autoVerdict(strings.TrimSpace(p.Verdict))
	default:
		return verdictBlocked
	}
}

// selfAssess 让模型对着任务清单自评该不该继续。
//
// 纪律：
//  1. **不带工具**（Tools: nil）——否则模型会"边自评边继续干活"，判断失去意义。
//  2. 判定用**结构化数据**（调用方传入的 items），不做文本匹配：
//     全 done → 强制 done（模型说 continue 也忽略，防自欺）；
//     无清单 → 强制 blocked（没有清单就谈不上"清单完成"，必须问用户）。
//  3. 返回的 reason 是模型的原文（截到 200 字），供落账与 UI 展示。
func (l *Loop) selfAssess(ctx context.Context, msgs []llm.Message, items []llm.TodoItem, segment int) (autoVerdict, string, error) {
	if err := ctx.Err(); err != nil {
		return verdictBlocked, "", err
	}
	ask := fmt.Sprintf(autoAssessPrompt, segment*MaxStepsPerTurn)
	req := make([]llm.Message, 0, len(msgs)+1)
	req = append(req, msgs...)
	req = append(req, llm.Message{Role: "user", Content: ask})

	ch, err := l.runtime.Chat(ctx, llm.ChatRequest{
		Model:    l.model,
		Messages: req,
		Tools:    nil, // 纪律 1：不带工具
	}, llm.DefaultRuntimePolicy())
	if err != nil {
		return verdictBlocked, "", fmt.Errorf("self assess: %w", err)
	}
	var sb strings.Builder
	for c := range ch {
		if c.Delta != "" {
			sb.WriteString(c.Delta)
		}
	}
	// 纪律 2：结构化判定优先于模型自述
	if len(items) == 0 {
		return verdictBlocked, "本轮没有登记任务清单，无法自评", nil
	}
	allDone := true
	for _, it := range items {
		if it.Status != "done" {
			allDone = false
			break
		}
	}
	if allDone {
		return verdictDone, "任务清单已全部完成", nil
	}
	raw := sb.String()
	verdict := parseAutoVerdict(raw)
	reason := strings.TrimSpace(raw)
	if len(reason) > 200 {
		reason = reason[:200] + "…"
	}
	return verdict, reason, nil
}
