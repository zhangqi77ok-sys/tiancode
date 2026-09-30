package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// dataURL 构造图片的 data URL（base64）。
func dataURL(mediaType string, b []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(b)
}

const toolResultModelLimit = 4096
const toolEventIPCLimit = 64 * 1024

// keepFullToolTurns：最近 N 个 user 轮次的只读工具结果保持全文，更早的收成
// 单行（0.0.09 油表治理）。为什么只折叠只读：read/list/tree/search 的旧输出
// 价值随时间衰减最快（"我读过什么"远不如"读到了什么"重要），而 write/replace
// 的回执与 shell 的失败尾部承载可执行信息，一律不动。为什么不用摘要模型：
// 会把行号和报错写错（0.0.09 用户裁决）——单行丢弃是确定性的，不引入新错误源。
const keepFullToolTurns = 2

// 上下文治理参数（第 2 批）。估算口径写死在这里、并在配置与界面同口径说明：
//   - contextFoldWatermark：估算用量达到预算的 85% 即开始分级折叠（"接近上限"）；
//   - keepFullImageTurns：最近 N 轮的图片保持 data URL 全文——更早的图片在超限时
//     替换为路径说明（图片 token 成本极高，是最先该丢的内容）；
//   - estimateImageTokens：单张图片的保守 token 估算（本地无法感知分辨率，
//     取常见截图的保守上界——宁可早丢，不可超限报错）。
const (
	contextFoldWatermark = 85
	keepFullImageTurns   = 1
	estimateImageTokens  = 1500
)

// DeriveOptions 是历史派生选项。BudgetTokens 为 0 时行为与旧版完全一致
// （只做 0.0.09 的基础只读折叠，不因预算裁剪任何内容）。
type DeriveOptions struct {
	BudgetTokens int // 渠道声明的上下文上限（token）；0 = 未配置
}

// DeriveInfo 是本轮派生的治理读数（经 llm.ContextEvent 透传 UI 油表）。
// 计数只统计**因超预算而执行**的折叠——基础只读折叠（>keepFullToolTurns 轮，
// 常态行为）不计入，否则界面每轮都显示"已折叠"形成噪声。
type DeriveInfo struct {
	EstimatedTokens int
	BudgetTokens    int
	FoldedImages    int
	FoldedTools     int
	FoldedReads     int
	Dropped         bool // 已无可再丢仍超预算（界面须标明"上下文已折叠"）
}

// imageRef 指向一条 user 消息里的图片（超限时把 data URL 换成路径说明）。
type imageRef struct {
	msgIdx int
	turn   int
	note   string // 折叠后的替代文本（含文件名与附件路径）
}

// toolRef 指向一条工具结果消息（超限时可收成一行摘要）。
type toolRef struct {
	msgIdx     int
	turn       int
	name       string
	title      string
	isError    bool
	isReadOnly bool
	folded     bool // 已被某级折叠处理过（避免重复覆盖）
}

// deriveMessages 以"未配置上下文上限"派生历史（旧签名入口，测试与旧调用点用）。
func deriveMessages(ledger *session.Ledger) ([]llm.Message, error) {
	msgs, _, err := deriveMessagesWith(ledger, DeriveOptions{})
	return msgs, err
}

// deriveMessagesWith 从账本派生发给模型的历史，并按预算分级折叠（第 2 批）。
// 折叠顺序（估算超 watermarked 预算时逐级执行，每级后重估）：
//  1. 基础（0.0.09 常态，不计入 info）：>keepFullToolTurns 轮的成功只读结果收成单行；
//  2. 旧图片（>keepFullImageTurns 轮）→ 路径说明（保留"当时给过图"的事实）；
//  3. 两轮以前的 shell/写入回执 → 一行摘要（保留工具名、路径与成败结论）；
//  4. 只读折叠窗口收紧到"最近一轮之外全部"。
//
// 仍超预算时置 Dropped=true（界面标明已折叠）；**用户原话永不删减**，
// 最近一轮全文始终保留（各级 minTurn 边界不含最近一轮）。
func deriveMessagesWith(ledger *session.Ledger, opt DeriveOptions) ([]llm.Message, DeriveInfo, error) {
	// 分叉区间（第 6 批）：「从这条用户消息重跑」标记的区间内事件不参与派生
	//（账本只追加，旧行不动）。
	drops, err := ledger.ForkDrops()
	if err != nil {
		return nil, DeriveInfo{}, err
	}
	// 先数 user 轮次总数：折叠判定需要"最近 N 轮"的边界（账本是单向流，
	// 投影前不知道后面还有没有新 turn）。
	totalTurns := 0
	if err := ledger.Replay(func(ev session.Event) error {
		if session.ForkDropped(drops, ev.Seq()) {
			return nil
		}
		if ev.Kind() == session.EventUserMessage {
			totalTurns++
		}
		return nil
	}); err != nil {
		return nil, DeriveInfo{}, err
	}

	var msgs []llm.Message
	var calls []llm.ToolCall
	results := []*llm.Message{}
	// resultRefs 与 results 平行（同索引）：flush 时填 msgIdx 并移入 toolRefs。
	resultRefs := []toolRef{}
	var toolRefs []toolRef
	var imageRefs []imageRef
	// lastAssistant 指向最近一条纯文本 assistant 锚点（0.0.06）：工具调用前的
	// 助手正文必须随 tool_calls 一起回传给模型——丢掉它，下一轮模型就看不到
	// 自己"为什么"调了工具。锚点只来自 EventAssistantMsg（回合内已确认落盘
	// 的文本）；EventAssistantDelta 的半截内容绝不投影（不把未完成的回复
	// 当成完成回复，thinking 也不进模型上下文）。
	lastAssistant := -1
	turn := -1

	complete := func() bool {
		if len(calls) == 0 {
			return false
		}
		for _, r := range results {
			if r == nil {
				return false
			}
		}
		return len(results) == len(calls)
	}
	flush := func() {
		if !complete() {
			calls, results, resultRefs = nil, nil, nil
			return
		}
		if lastAssistant >= 0 && lastAssistant < len(msgs) &&
			msgs[lastAssistant].Role == "assistant" &&
			len(msgs[lastAssistant].ToolCalls) == 0 {
			// 工具调用前的助手正文：并入同一条 assistant(text, tool_calls)
			msgs[lastAssistant].ToolCalls = append([]llm.ToolCall(nil), calls...)
		} else {
			msgs = append(msgs, llm.Message{Role: "assistant", ToolCalls: append([]llm.ToolCall(nil), calls...)})
		}
		for i, r := range results {
			msgs = append(msgs, *r)
			if i < len(resultRefs) {
				ref := resultRefs[i]
				ref.msgIdx = len(msgs) - 1
				toolRefs = append(toolRefs, ref)
			}
		}
		calls, results, resultRefs = nil, nil, nil
		lastAssistant = -1
	}

	err = ledger.Replay(func(ev session.Event) error {
		if session.ForkDropped(drops, ev.Seq()) {
			return nil
		}
		switch ev.Kind() {
		case session.EventUserEdit:
			// 用户手动写入（代码块「应用到文件」，第 6 批）：给模型一句"已应用过"——
			// 否则下一轮模型不知道自己"以为该改未改"的文件其实已被用户改过。
			// 用 assistant 角色（不需要 tool_call 配对，任意位置合法）；不增轮次计数。
			flush()
			var p struct {
				Path  string `json:"path"`
				Bytes int    `json:"bytes"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			msgs = append(msgs, llm.Message{Role: "assistant", Content: fmt.Sprintf(
				"[用户手动操作] 用户已把代码块应用到 %s（%d 字节），该文件当前内容以磁盘为准。",
				p.Path, p.Bytes)})
		case session.EventUserMessage:
			flush()
			lastAssistant = -1
			turn++
			var p struct {
				Text        string                   `json:"text"`
				Attachments []session.UserAttachment `json:"attachments"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			// 附件（0.0.10）：重建发给模型的历史时图片必须仍然带图（data URL），
			// 内联文件仍然带当时的内容——否则下一轮模型看不见截图/日志。
			// 无附件 = 纯文本字符串 content（请求形态不变）。
			if len(p.Attachments) == 0 {
				msgs = append(msgs, llm.Message{Role: "user", Content: p.Text})
				return nil
			}
			var parts []llm.ContentPart
			text := p.Text
			var notes []string
			hasImage := false
			for _, att := range p.Attachments {
				full := ledger.ResolveAttachmentPath(att.Path)
				if att.Kind == "image" {
					// 图片：data URL（上游按 image_url 读；不支持视觉的渠道展示上游错误）
					b, err := os.ReadFile(full)
					if err != nil {
						text += fmt.Sprintf("\n[图片 %s 读取失败：%v]", att.Name, err)
						continue
					}
					parts = append(parts, llm.ContentPart{Type: "text", Text: text})
					text = ""
					parts = append(parts, llm.ContentPart{Type: "image_url", ImageURL: dataURL(att.MediaType, b)})
					hasImage = true
					notes = append(notes, fmt.Sprintf("[图片 %s（%s）：旧轮次已省略图像数据，需要时请重新提供]", att.Name, att.Path))
					continue
				}
				if att.Inline == "full" {
					b, err := os.ReadFile(full)
					if err == nil {
						text += fmt.Sprintf("\n\n[附件文件 %s 内容如下]\n%s", att.Path, string(b))
						continue
					}
					text += fmt.Sprintf("\n[附件 %s 读取失败：%v]", att.Name, err)
					continue
				}
				// 只附路径
				text += fmt.Sprintf("\n[附件文件 %s（未内联，需要时用 fs 读取）]", att.Path)
			}
			if text != "" || len(parts) == 0 {
				parts = append([]llm.ContentPart{{Type: "text", Text: text}}, parts...)
			}
			msgs = append(msgs, llm.Message{Role: "user", Parts: parts})
			if hasImage {
				imageRefs = append(imageRefs, imageRef{msgIdx: len(msgs) - 1, turn: turn, note: strings.Join(notes, "\n")})
			}
		case session.EventToolCall:
			if complete() {
				flush()
			}
			var p struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			id := p.ID
			if id == "" {
				id = fmt.Sprintf("call-%d", ev.Seq())
			}
			calls = append(calls, llm.ToolCall{ID: id, Name: p.Name, Arguments: p.Arguments})
			results = append(results, nil)
			resultRefs = append(resultRefs, toolRef{})
		case session.EventToolResult:
			var p struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Content string `json:"content"`
				IsError bool   `json:"is_error"`
				Title   string `json:"title"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			idx := pairToolResult(calls, results, p.ID, p.Name)
			if idx < 0 {
				return nil
			}
			// 内容一律先按模型侧上限截断（头尾保留）；是否折叠由预算分级统一决定
			//（第 2 批：折叠从"构建时无条件"改为"构建后按预算分级"，见 deriveMessagesWith 文档）。
			results[idx] = &llm.Message{
				Role:       "tool",
				ToolCallID: calls[idx].ID,
				Content:    truncateToolResult(p.Content),
			}
			resultRefs[idx] = toolRef{
				turn:       turn,
				name:       calls[idx].Name,
				title:      p.Title,
				isError:    p.IsError,
				isReadOnly: isReadOnlyCall(llm.ToolCall{Name: calls[idx].Name, Arguments: calls[idx].Arguments}),
			}
		case session.EventAssistantMsg:
			flush()
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			msgs = append(msgs, llm.Message{Role: "assistant", Content: p.Text})
			lastAssistant = len(msgs) - 1
		}
		return nil
	})
	if err != nil {
		return nil, DeriveInfo{}, err
	}
	flush()

	info := DeriveInfo{BudgetTokens: opt.BudgetTokens}
	// 基础折叠（0.0.09 常态行为，不计入 info）：>keepFullToolTurns 轮的成功只读结果收成单行
	foldOldReads(msgs, toolRefs, totalTurns-keepFullToolTurns)

	if opt.BudgetTokens <= 0 {
		info.EstimatedTokens = estimateMessagesTokens(msgs)
		return msgs, info, nil
	}
	threshold := opt.BudgetTokens * contextFoldWatermark / 100
	est := estimateMessagesTokens(msgs)
	if est > threshold {
		// 一级：旧图片 → 路径说明（图像 token 成本最高，最先丢）
		info.FoldedImages += foldOldImages(msgs, imageRefs, totalTurns-keepFullImageTurns)
		est = estimateMessagesTokens(msgs)
	}
	if est > threshold {
		// 二级：两轮以前的 shell/写入回执 → 一行摘要（保留工具名、路径与成败）
		info.FoldedTools += foldOldTools(msgs, toolRefs, totalTurns-keepFullToolTurns)
		est = estimateMessagesTokens(msgs)
	}
	if est > threshold {
		// 三级：只读折叠窗口收紧到"最近一轮之外全部"（最近一轮全文始终保留）
		info.FoldedReads += foldOldReads(msgs, toolRefs, totalTurns-1)
		est = estimateMessagesTokens(msgs)
	}
	if est > threshold {
		info.Dropped = true
	}
	info.EstimatedTokens = est
	return msgs, info, nil
}

// foldOldImages 把旧轮次（turn < minTurn）user 消息里的图片替换为路径说明。
func foldOldImages(msgs []llm.Message, refs []imageRef, minTurn int) int {
	n := 0
	for _, ref := range refs {
		if ref.turn >= minTurn {
			continue
		}
		m := &msgs[ref.msgIdx]
		hasImage := false
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				hasImage = true
				break
			}
		}
		if !hasImage {
			continue
		}
		kept := make([]llm.ContentPart, 0, len(m.Parts))
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				n++
				continue
			}
			kept = append(kept, p)
		}
		kept = append(kept, llm.ContentPart{Type: "text", Text: "\n" + ref.note})
		m.Parts = mergeAdjacentTextParts(kept)
	}
	return n
}

// foldOldTools 把旧轮次（turn < minTurn）的非只读工具结果（shell/写入回执）收成一行摘要。
// 为什么错误结果也折：旧轮次的失败原因价值随时间衰减，但"失败过"的结论必须保留
// （摘要里写明失败 + 工具名 + 路径），模型不会对着虚构的成功继续推进。
func foldOldTools(msgs []llm.Message, refs []toolRef, minTurn int) int {
	n := 0
	for i := range refs {
		ref := &refs[i]
		if ref.isReadOnly || ref.folded || ref.turn >= minTurn {
			continue
		}
		msgs[ref.msgIdx].Content = foldToolResult(ref.name, ref.title, ref.isError)
		ref.folded = true
		n++
	}
	return n
}

// foldOldReads 把旧轮次（turn < minTurn）的成功只读结果收成单行；已被折叠的跳过。
func foldOldReads(msgs []llm.Message, refs []toolRef, minTurn int) int {
	n := 0
	for i := range refs {
		ref := &refs[i]
		if !ref.isReadOnly || ref.isError || ref.folded || ref.turn >= minTurn {
			continue
		}
		msgs[ref.msgIdx].Content = foldOldReadOnly(ref.name, ref.title)
		ref.folded = true
		n++
	}
	return n
}

// mergeAdjacentTextParts 合并相邻 text part（折叠产生的说明文本与原有 part 拼接；
// 上游对多 text part 的支持不尽相同，合并后形态最稳）。
func mergeAdjacentTextParts(parts []llm.ContentPart) []llm.ContentPart {
	out := make([]llm.ContentPart, 0, len(parts))
	for _, p := range parts {
		if n := len(out); n > 0 && p.Type == "text" && out[n-1].Type == "text" {
			out[n-1].Text += p.Text
			continue
		}
		out = append(out, p)
	}
	return out
}

// estimateTextTokens 估算文本 token（保守口径，与渠道配置说明同源）：
// ASCII 4 字符 ≈ 1 token、1 个非 ASCII 字符 ≈ 1 token——中文场景取最保守上界
// （宁可早折叠，不可超限被上游拒绝）。
func estimateTextTokens(s string) int {
	ascii, nonASCII := 0, 0
	for _, r := range s {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII
}

// estimateMessagesTokens 估算整轮上下文 token（含 tool_calls 参数与图片固定值）。
func estimateMessagesTokens(msgs []llm.Message) int {
	total := 0
	for i := range msgs {
		m := &msgs[i]
		total += 4 // 每条消息的 role/格式开销（保守常量）
		total += estimateTextTokens(m.Content)
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				total += estimateImageTokens
				continue
			}
			total += estimateTextTokens(p.Text)
		}
		for _, tc := range m.ToolCalls {
			total += 8 + estimateTextTokens(tc.Arguments)
		}
	}
	return total
}

func pairToolResult(calls []llm.ToolCall, results []*llm.Message, id, name string) int {
	if id != "" {
		for i, c := range calls {
			if c.ID == id && results[i] == nil {
				return i
			}
		}
	}
	if name != "" {
		for i, c := range calls {
			if results[i] == nil && c.Name == name {
				return i
			}
		}
	}
	for i, r := range results {
		if r == nil {
			return i
		}
	}
	return -1
}

func truncateToolResult(content string) string {
	if len(content) <= toolResultModelLimit {
		return content
	}
	// 0.0.06 头尾保留：工具结果里模型最需要的信息常在尾部（测试 FAIL 汇总、
	// 命令最终错误、diff 末尾）。只留头部会让模型对着开头猜结局。
	return tools.HeadTail(content, toolResultModelLimit)
}

// foldOldReadOnly 把旧轮次的只读结果收成确定性的单行（0.0.09）。
// 明示"已省略"：模型若需要旧内容就重新 read，而不是对被裁的尾巴猜。
func foldOldReadOnly(name, title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		t = "（无标题）"
	}
	return fmt.Sprintf("%s %s → 已读（旧轮次输出已省略，需要时请重新读取）", name, t)
}

// foldToolResult 把旧轮次的 shell/写入回执收成一行摘要（第 2 批）。
// 保留三件可执行信息：工具名、路径/标题、成败结论——模型仍知道"这个文件改过"
// "这条命令跑过"，只是不再携带全文输出。
func foldToolResult(name, title string, isError bool) string {
	t := strings.TrimSpace(title)
	if t == "" {
		t = "（无标题）"
	}
	if isError {
		return fmt.Sprintf("%s %s → 失败（旧轮次输出已省略，需要时请重新执行）", name, t)
	}
	return fmt.Sprintf("%s %s → 已完成（旧轮次输出已省略，需要时请重新执行）", name, t)
}

func truncateToBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
