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
// 常态行为）不计入，否则界面每轮都显示"已折叠"形成噪声。分类计数随事件下发，
// 界面写清"折了什么"（旧工具输出 / 图片 / 重复读 / 旧回复）——折叠绝不静默。
type DeriveInfo struct {
	EstimatedTokens int
	BudgetTokens    int
	FoldedImages    int
	FoldedTools     int
	FoldedReads     int
	FoldedBodies    int  // 因超预算折叠的旧轮次回复正文数（→ 一行说明）
	Dropped         bool // 已无可再丢仍超预算（界面须标明"上下文已折叠"）
}

// imageRef 指向一条 user 消息里的图片（超限时把 data URL 换成路径说明）。
type imageRef struct {
	msgIdx int
	turn   int
	note   string // 折叠后的替代文本（含文件名与附件路径）
}

// bodyRef 指向一条助手正文锚点（超预算最后一级可收成一行说明）。
type bodyRef struct {
	msgIdx int
	turn   int
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

// deriveMessagesWith 从账本派生发给模型的历史，并按预算分级折叠（第 2 批；
// 0.3 按用户裁决重排优先级）。折叠顺序（估算超 watermarked 预算时逐级执行，
// 每级后重估）：
//  1. 基础（0.0.09 常态，不计入 info）：>keepFullToolTurns 轮的成功只读结果收成单行；
//  2. 旧工具输出（>keepFullToolTurns 轮的 shell/写入回执）→ 一行摘要；
//  3. 旧图片（>keepFullImageTurns 轮）→ 路径说明（保留"当时给过图"的事实）；
//  4. 重复读到的同一文件 → 只留最近一次全文，更早的收成单行；
//  5. 只读折叠窗口收紧到"最近一轮之外全部"；
//  6. **最后**才动对话正文：旧轮次回复 → 一行说明。
//
// 仍超预算时置 Dropped=true（界面标明已折叠）；**用户原话永不删减**，
// 最近一轮全文始终保留（各级 minTurn 边界不含最近一轮）。
func deriveMessagesWith(ledger *session.Ledger, opt DeriveOptions) ([]llm.Message, DeriveInfo, error) {
	// 分叉区间（第 6 批）与主投影合并为单次遍历（性能改造）：
	// 旧实现是三次全量重放——ForkDrops 一趟、轮次计数一趟、主投影一趟，每趟
	// 都把整本账本的 JSON 完整解析一遍（实测 2 万行约 400ms/次派生），派生又
	// 在一回合内触发多次，纯重放开销线性恶化。合并方式：
	//   - 轮次计数并入主扫描：旧一趟数的正是"未丢弃的 user 消息条数"，与主扫描
	//     里 turn 的自增条件完全相同（turn 从 -1 起每条 +1，终值 + 1 即该数）；
	//   - 分叉区间随扫描发现：fork 事件极少（仅「从这条用户消息重跑」时追加），
	//     发现新区间就重启扫描——重启后该 fork 已入 drops 不会再触发，总扫描数
	//     = 分叉数 + 1，无分叉（常态）恰好一遍；
	//   - 等价性：最终完整一遍持有全量 drops，投影代码一行未动；fork 事件即使
	//     自身落在更早的丢弃区间里，它声明的区间也照旧收录（union 语义，与旧
	//     ForkDrops 全量扫描一致，否则嵌套分叉的区间头部会泄漏）。
	var drops []session.ForkDrop
	// forkSeen 记录已收录的 fork 事件 seq：嵌套分叉时避免同一 fork 在重启扫描里
	// 被反复触发重启（fork 数量极少，线性查足够）。
	var forkSeen []int64

	var msgs []llm.Message
	var calls []llm.ToolCall
	results := []*llm.Message{}
	// resultRefs 与 results 平行（同索引）：flush 时填 msgIdx 并移入 toolRefs。
	resultRefs := []toolRef{}
	var toolRefs []toolRef
	var imageRefs []imageRef
	var bodyRefs []bodyRef
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

	// projectEvent 是主投影的单事件处理（与旧实现逐行相同；分叉跳过与轮次
	// 计数见下方唯一一次全量重放）。
	projectEvent := func(ev session.Event) error {
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
			// 0.0.25 起 **Content 与 Parts 都写**（此前 Content 留空）：Anthropic 与 Codex
			// 两个适配器只读 Content，附件轮于是等于没发出去（实机反馈：上传文件或图片后
			// 发出去没有任何回复）。两者同源构造，不写两套文本：
			//   Content —— 用户原文 + 内联文件正文 / 未内联的路径说明 + 每张图只写
			//             「文件名：图像见多模态部分」；**base64 绝不进文字**（省 token，
			//             也免得文字化协议把乱码当正文）；
			//   Parts   —— 同一份文字按出现顺序切成 text 段 + 图片的 image_url data URL。
			// 图片一律内联在请求体里，不转 http(s) 链接（本工具是本地开源工具；
			// 某个中转只收 http(s) 时由适配器/上游明确报错，不空转）。
			var content strings.Builder
			content.WriteString(p.Text)
			var parts []llm.ContentPart
			flushed := 0
			flushText := func() {
				if s := content.String(); len(s) > flushed {
					parts = append(parts, llm.ContentPart{Type: "text", Text: s[flushed:]})
					flushed = len(s)
				}
			}
			var notes []string
			hasImage := false
			for _, att := range p.Attachments {
				full := ledger.ResolveAttachmentPath(att.Path)
				if att.Kind == "image" {
					// 图片：data URL（上游按 image_url 读；不支持视觉的渠道展示上游错误）
					b, err := os.ReadFile(full)
					if err != nil {
						content.WriteString(fmt.Sprintf("\n[图片 %s 读取失败：%v]", att.Name, err))
						continue
					}
					content.WriteString(fmt.Sprintf("\n[图片 %s：图像见多模态部分]", att.Name))
					flushText() // 图在消息里的位置不变：先把到这里的文字收成一个 text 段
					parts = append(parts, llm.ContentPart{
						Type: "image_url", Name: att.Name, ImageURL: dataURL(att.MediaType, b),
					})
					hasImage = true
					notes = append(notes, fmt.Sprintf("[图片 %s（%s）：旧轮次已省略图像数据，需要时请重新提供]", att.Name, att.Path))
					continue
				}
				if att.Inline == "full" {
					b, err := os.ReadFile(full)
					if err == nil {
						content.WriteString(fmt.Sprintf("\n\n[附件文件 %s 内容如下]\n%s", att.Path, string(b)))
						continue
					}
					content.WriteString(fmt.Sprintf("\n[附件 %s 读取失败：%v]", att.Name, err))
					continue
				}
				// 只附路径
				content.WriteString(fmt.Sprintf("\n[附件文件 %s（未内联，需要时用 fs 读取）]", att.Path))
			}
			flushText()
			text := content.String()
			if len(parts) == 0 {
				// 理论不可达（有附件就必有一行说明）；真出现时退回纯文本，不造空消息
				msgs = append(msgs, llm.Message{Role: "user", Content: text})
				return nil
			}
			msgs = append(msgs, llm.Message{Role: "user", Content: text, Parts: mergeAdjacentTextParts(parts)})
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
			bodyRefs = append(bodyRefs, bodyRef{msgIdx: len(msgs) - 1, turn: turn})
		}
		return nil
	}

	// 唯一一次全量重放（性能改造）：轮次计数与分叉区间并入主扫描。fork 事件
	// 极少（仅「从这条用户消息重跑」时追加）；扫描中发现新区间则重启——重启后
	// 该 fork 已入 drops 不会再触发，总扫描数 = 分叉数 + 1，无分叉（常态）恰好
	// 一遍。重启必须从零重建投影（drops 只增不减，最终完整一遍与旧三次扫描等价）。
	for {
		restart := false
		msgs, calls, toolRefs, imageRefs, bodyRefs = nil, nil, nil, nil, nil
		results, resultRefs = []*llm.Message{}, []toolRef{}
		lastAssistant, turn = -1, -1
		err := ledger.Replay(func(ev session.Event) error {
			if ev.Kind() == session.EventFork {
				// forkSeen 记录已收录的 fork 事件 seq：嵌套分叉时（fork 自身落在
				// 更早的丢弃区间里）区间也要照旧收录（union 语义，与旧 ForkDrops
				// 全量扫描一致），且不得在重启扫描里反复触发重启。
				for _, s := range forkSeen {
					if s == ev.Seq() {
						return nil
					}
				}
				forkSeen = append(forkSeen, ev.Seq())
				var p struct {
					FromSeq int64 `json:"from_seq"`
				}
				if err := json.Unmarshal(ev.Data(), &p); err != nil {
					return err
				}
				if p.FromSeq > 0 && p.FromSeq <= ev.Seq() {
					drops = append(drops, session.ForkDrop{From: p.FromSeq, To: ev.Seq()})
					restart = true
				}
				return nil
			}
			if session.ForkDropped(drops, ev.Seq()) {
				return nil
			}
			return projectEvent(ev)
		})
		if err != nil {
			return nil, DeriveInfo{}, err
		}
		if !restart {
			break
		}
	}
	flush()

	// 轮次总数 = 未丢弃的 user 消息条数：与主扫描里 turn 的自增条件完全相同
	//（turn 从 -1 起每条 +1），终值 + 1 即旧"单独一趟计数"的结果。
	totalTurns := turn + 1

	info := DeriveInfo{BudgetTokens: opt.BudgetTokens}
	// 基础折叠（0.0.09 常态行为，不计入 info）：>keepFullToolTurns 轮的成功只读结果收成单行
	foldOldReads(msgs, toolRefs, totalTurns-keepFullToolTurns)

	if opt.BudgetTokens <= 0 {
		info.EstimatedTokens = estimateMessagesTokens(msgs)
		return msgs, info, nil
	}
	threshold := opt.BudgetTokens * contextFoldWatermark / 100
	est := estimateMessagesTokens(msgs)
	// 折叠优先级（0.3 用户裁决）：旧的工具输出 → 旧图片 → 重复读到的同一文件 →
	// 收紧只读窗口 → **最后才动对话正文**。用户原话与最近一轮始终保留。
	if est > threshold {
		// 一级：旧工具输出（shell/写入回执）→ 一行摘要（保留工具名、路径与成败）
		info.FoldedTools += foldOldTools(msgs, toolRefs, totalTurns-keepFullToolTurns)
		est = estimateMessagesTokens(msgs)
	}
	if est > threshold {
		// 二级：旧图片 → 路径说明（图像 token 成本最高，紧随工具输出之后丢）
		info.FoldedImages += foldOldImages(msgs, imageRefs, totalTurns-keepFullImageTurns)
		est = estimateMessagesTokens(msgs)
	}
	if est > threshold {
		// 三级：重复读到的同一文件——同一路径只保留最近一次全文，更早的收成单行
		//（确定性去重：模型需要的"我读过 X"保留一次即可）
		info.FoldedReads += foldDuplicateReads(msgs, toolRefs, totalTurns-1)
		est = estimateMessagesTokens(msgs)
	}
	if est > threshold {
		// 四级：只读折叠窗口收紧到"最近一轮之外全部"（最近一轮全文始终保留）
		info.FoldedReads += foldOldReads(msgs, toolRefs, totalTurns-1)
		est = estimateMessagesTokens(msgs)
	}
	if est > threshold {
		// 五级（最后）：旧轮次的回复正文 → 一行说明。历史叙事仍有骨架（用户原话、
		// 工具摘要、回复说明），只是不再携带全文；最近一轮正文永远保留。
		info.FoldedBodies += foldOldBodies(msgs, bodyRefs, totalTurns-1)
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
			// 文字里的「图像见多模态部分」随图一起作废（0.0.25）：改成"已省略"，
			// 免得留下一句"图在多模态部分"而那里已经没有图
			if p.Type == "text" {
				p.Text = strings.ReplaceAll(p.Text, "：图像见多模态部分]", "：图像数据已省略]")
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

// foldDuplicateReads 折叠"重复读到的同一文件"（0.3）：同一路径（按工具名+标题）
// 的成功只读结果只保留**最近一次**全文，更早的收成单行。minTurn 之外的当前轮
// 不参与（最近一轮永远保留全文）。已被折叠的跳过。
func foldDuplicateReads(msgs []llm.Message, refs []toolRef, minTurn int) int {
	n := 0
	// lastFullOf 记录每个"工具+路径"最后一次全文出现的下标；先扫一遍定位保留者，
	// 再回头折叠更早的（确定性：无论出现多少次，只有最近一份全文存活）。
	lastFullOf := map[string]int{} // 组键 -> 该组最近一次全文的 refs 下标
	keyOf := map[int]string{}
	for i := range refs {
		keyOf[i] = refs[i].name + "\x00" + refs[i].title
	}
	for i := range refs {
		ref := &refs[i]
		if !ref.isReadOnly || ref.isError || ref.turn >= minTurn {
			continue
		}
		if prev, ok := lastFullOf[keyOf[i]]; !ok || refs[prev].folded {
			lastFullOf[keyOf[i]] = i
		} else {
			// 之前那份全文出现得更早：折叠它，保留当前这份（更近）
			msgs[refs[prev].msgIdx].Content = foldOldReadOnly(refs[prev].name, refs[prev].title)
			refs[prev].folded = true
			n++
			lastFullOf[keyOf[i]] = i
		}
	}
	return n
}

// foldOldBodies 把旧轮次（turn < minTurn）的回复正文收成一行说明（0.3，最后一级）：
// 保留"这一轮有过回复、多长"的事实。空正文（纯工具轮的锚点）没有可折内容，跳过。
func foldOldBodies(msgs []llm.Message, refs []bodyRef, minTurn int) int {
	n := 0
	for _, ref := range refs {
		if ref.turn >= minTurn {
			continue
		}
		m := &msgs[ref.msgIdx]
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		m.Content = foldOldBody(len(m.Content))
		n++
	}
	return n
}

// foldOldBody 生成旧回复的替身说明：模型仍知道"此处有过一段回复、原文多少字"，
// 需要细节时会明说"已折叠"，而不是对着被裁的尾巴猜。
func foldOldBody(chars int) string {
	return fmt.Sprintf("[旧回复已折叠，原文约 %d 字；用户原话与工具摘要仍在，如需细节可重述要点]", chars)
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
		// 附件轮（0.0.25）：Content 与 Parts 装的是同一份文字（同源构造），只算一处——
		// 否则内联文件正文被算两遍，油表虚高、可能误触发折叠
		if len(m.Parts) == 0 {
			total += estimateTextTokens(m.Content)
		}
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
