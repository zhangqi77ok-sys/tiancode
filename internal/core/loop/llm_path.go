package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"tiancode/internal/llm"
	v1 "tiancode/pkg/plugin/v1"
)

func (e *ExecutionEngine) executeDirectLLM(ctx context.Context, req *EngineRequest, eventChan chan<- EngineEvent) error {
	provs := e.registry.GetProviders()
	if len(provs) == 0 {
		eventChan <- EngineEvent{Type: EventError, ErrorMessage: "no provider plugin registered"}
		return fmt.Errorf("no provider registered")
	}
	
	// Sort to ensure deterministic selection instead of random map traversal
	sort.Slice(provs, func(i, j int) bool {
		return provs[i].Name() < provs[j].Name()
	})
	
	prov := provs[0]
	// If a specific provider is requested, find it
	if req.Provider != "" {
		for _, p := range provs {
			if p.Name() == req.Provider {
				prov = p
				break
			}
		}
	}
	
	cfg, _ := json.Marshal(map[string]string{
		"api_key":  req.APIKey,
		"base_url": strings.TrimRight(req.Endpoint, "/"),
	})
	if err := prov.Init(ctx, cfg); err != nil {
		eventChan <- EngineEvent{Type: EventError, ErrorMessage: err.Error()}
		return err
	}

	conversation := req.Messages
	if len(conversation) == 0 {
		sys := req.SystemPrompt
		if sys == "" {
			sys = "你是 湉码 / tiancode 纯原生桌面智能体。你有权调用工具来审查、读取、修改工程代码及运行测试命令。"
		}
		conversation = []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: req.Prompt},
		}
	}

	toolDefs := make([]v1.ToolDefinition, 0, len(req.LLMTools))
	for _, t := range req.LLMTools {
		params, _ := json.Marshal(t.Function.Parameters)
		toolDefs = append(toolDefs, v1.ToolDefinition{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  params,
		})
	}
	if len(toolDefs) == 0 {
		for _, t := range e.registry.GetTools() {
			toolDefs = append(toolDefs, t.Definition())
		}
	}

	// maxTurns logic removed to not artificially increase LLM turns
	hitCap := false
	circuitBroken := false
	circuitBreakReason := ""
	var lastToolSig string
	consecutiveIdenticalCalls := 0
	consecutiveErrors := 0

	for turn := 1; turn <= 50; turn++ {
		if ctx.Err() != nil {
			eventChan <- EngineEvent{
				Type:         EventChunk,
				DeltaContent: FormatInterruptedNotice(),
			}
			eventChan <- EngineEvent{Type: EventError, ErrorMessage: "task canceled by client"}
			return ctx.Err()
		}

		msgsBytes, err := json.Marshal(conversation)
		if err != nil {
			humanErr := FormatUpstreamError(err)
			eventChan <- EngineEvent{Type: EventChunk, DeltaContent: humanErr}
			eventChan <- EngineEvent{Type: EventError, ErrorMessage: humanErr}
			return err
		}
		chatReq := &v1.ChatRequest{
			Model:    req.Model,
			Messages: msgsBytes,
			Tools:    toolDefs,
			Stream:   true,
		}
		chunkChan, err := prov.StreamChat(ctx, chatReq)
		if err != nil {
			humanErr := FormatUpstreamError(err)
			eventChan <- EngineEvent{Type: EventChunk, DeltaContent: humanErr}
			eventChan <- EngineEvent{Type: EventError, ErrorMessage: humanErr}
			return err
		}

		var asstContent strings.Builder
		var asstThinking strings.Builder
		toolReassembler := make(map[int]*AssembledToolCall)
		for chunk := range chunkChan {
			if chunk.Error != nil {
				humanErr := FormatUpstreamError(chunk.Error)
				eventChan <- EngineEvent{Type: EventChunk, DeltaContent: humanErr}
				eventChan <- EngineEvent{Type: EventError, ErrorMessage: humanErr}
				return chunk.Error
			}
			if chunk.DeltaContent != "" || chunk.Thinking != "" {
				asstContent.WriteString(chunk.DeltaContent)
				asstThinking.WriteString(chunk.Thinking)
				eventChan <- EngineEvent{
					Type:         EventChunk,
					DeltaContent: chunk.DeltaContent,
					Thinking:     chunk.Thinking,
				}
			}
			for _, tc := range chunk.ToolCalls {
				entry, exists := toolReassembler[tc.Index]
				if !exists {
					entry = &AssembledToolCall{ID: tc.ID, Name: tc.Name}
					toolReassembler[tc.Index] = entry
				}
				if tc.ID != "" {
					entry.ID = tc.ID
				}
				if tc.Name != "" {
					entry.Name = tc.Name
				}
				entry.Arguments.WriteString(tc.ArgumentsDelta)
			}
		}

		if len(toolReassembler) == 0 {
			if turn == 1 && asstContent.Len() == 0 && asstThinking.Len() == 0 {
				eventChan <- EngineEvent{
					Type:         EventChunk,
					DeltaContent: FormatEmptyOutputNotice(),
				}
			}
			break
		}
		if turn == 50 {
			hitCap = true
		}

		tcIndices := make([]int, 0, len(toolReassembler))
		for idx := range toolReassembler {
			tcIndices = append(tcIndices, idx)
		}
		sort.Ints(tcIndices)
		rawToolCalls := make([]llm.ToolCall, 0, len(tcIndices))
		for _, idx := range tcIndices {
			atc := toolReassembler[idx]
			if atc == nil {
				continue
			}
			if atc.ID == "" {
				atc.ID = fmt.Sprintf("call_%d_%d", turn, idx)
			}
			tc := llm.ToolCall{ID: atc.ID, Type: "function"}
			tc.Function.Name = atc.Name
			tc.Function.Arguments = atc.Arguments.String()
			rawToolCalls = append(rawToolCalls, tc)
		}
		conversation = append(conversation, llm.Message{
			Role:             "assistant",
			Content:          asstContent.String(),
			ReasoningContent: asstThinking.String(),
			ToolCalls:        rawToolCalls,
		})

		for _, tc := range rawToolCalls {
			sig := fmt.Sprintf("%s:%s", tc.Function.Name, strings.TrimSpace(tc.Function.Arguments))
			if sig == lastToolSig {
				consecutiveIdenticalCalls++
			} else {
				consecutiveIdenticalCalls = 1
				lastToolSig = sig
			}

			if consecutiveIdenticalCalls >= 3 {
				circuitBroken = true
				circuitBreakReason = fmt.Sprintf("⚠️ [防死循环熔断] 工具 [%s] 连续发起 3 次完全相同的调用，已触发防脱缰保护。", tc.Function.Name)
				eventChan <- EngineEvent{
					Type:         EventChunk,
					DeltaContent: "\n\n> " + circuitBreakReason + " 请根据已有信息给出最终分析与方案。\n\n",
				}
				conversation = append(conversation, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    circuitBreakReason + " 请不要继续重复调用此工具，根据已知信息完成作答。",
				})
				break
			}

			rawArgs := json.RawMessage(tc.Function.Arguments)
			eventChan <- EngineEvent{
				Type:       EventToolStart,
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
				ToolArgs:   rawArgs,
			}
			var output string
			var isErr bool
			var written string
			var tddPass *bool

			if tc.Function.Name == "ask_user" {
				var askPayload ChoicePayload
				if err := json.Unmarshal(rawArgs, &askPayload); err != nil {
					output = fmt.Sprintf("ask_user 参数解析失败: %v", err)
					isErr = true
				} else if len(askPayload.Options) < 2 || len(askPayload.Options) > 5 || askPayload.Question == "" {
					output = "ask_user 需要 question 以及 2~5 个 options"
					isErr = true
				} else {
					if e.gateway == nil {
						output = "缺少审批网关，自动跳过用户选择。"
						isErr = true
					} else {
						reply, err := e.gateway.RequestChoice(ctx, req.SessionID, tc.ID, askPayload.Question, askPayload.Options)
						if err != nil {
							output = fmt.Sprintf("已跳过（中断或错误）：%v", err)
						} else if reply.Timeout || !reply.Allow {
							output = "已跳过（用户跳过），请采用推荐选项，结果注明 skipped_default=true。"
						} else {
							var label string
							for _, opt := range askPayload.Options {
								if opt.ID == reply.OptionID {
									label = opt.Label
									break
								}
							}
							output = fmt.Sprintf("用户选择了 option_id=%s label=%s。补充：%s", reply.OptionID, label, reply.CustomNote)
						}
					}
				}
			} else {
				output, isErr, written, tddPass = e.runTool(ctx, req.SessionID, tc.ID, tc.Function.Name, rawArgs, req.Strategy, turn, req.LLMTools, eventChan)
			}
			eventChan <- EngineEvent{
				Type:       EventToolEnd,
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
				ToolOutput: output,
				IsError:    isErr,
			}
			if tddPass != nil {
				eventChan <- EngineEvent{
					Type:      EventTDDResult,
					TDDPassed: tddPass,
				}
			}
			if written != "" {
				eventChan <- EngineEvent{Type: EventFilesChanged, ToolName: written}
			}
			toolOutput := strings.TrimSpace(output)
			if toolOutput == "" {
				toolOutput = fmt.Sprintf("tool [%s] executed successfully with empty output", tc.Function.Name)
			}
			conversation = append(conversation, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    toolOutput,
			})

			if isErr {
				consecutiveErrors++
			} else {
				consecutiveErrors = 0
			}

			if consecutiveErrors >= 3 {
				circuitBroken = true
				circuitBreakReason = fmt.Sprintf("⚠️ [连续失败熔断] 工具调用已连续失败 %d 次，已自动中止继续盲目尝试，避免消耗过多上下文。", consecutiveErrors)
				eventChan <- EngineEvent{
					Type:         EventChunk,
					DeltaContent: "\n\n> " + circuitBreakReason + " 请检查系统环境或输入后重试。\n\n",
				}
				break
			}
		}

		if circuitBroken {
			break
		}
	}

	if hitCap || circuitBroken {
		if hitCap {
			eventChan <- EngineEvent{Type: EventHitCap}
			notice := FormatHitCapNotice(50)
			eventChan <- EngineEvent{Type: EventChunk, DeltaContent: notice}
		}
		wrapPrompt := "工具轮次已达上限。请不要再调用任何工具，用已经拿到的结果给出当前结论、未完成项和下一步建议。"
		if circuitBroken {
			wrapPrompt = fmt.Sprintf("工具执行已触发安全熔断（%s）。请不要再调用任何工具，用已经拿到的结果给出当前结论、问题定位和修复建议。", circuitBreakReason)
		}
		conversation = append(conversation, llm.Message{
			Role:    "user",
			Content: wrapPrompt,
		})
		msgsBytes, err := json.Marshal(conversation)
		if err == nil && ctx.Err() == nil {
			chatReq := &v1.ChatRequest{Model: req.Model, Messages: msgsBytes, Stream: true}
			chunkChan, err := prov.StreamChat(ctx, chatReq)
			if err == nil {
				for chunk := range chunkChan {
					if chunk.Error != nil {
						eventChan <- EngineEvent{Type: EventError, ErrorMessage: chunk.Error.Error()}
						break
					}
					if chunk.DeltaContent != "" || chunk.Thinking != "" {
						eventChan <- EngineEvent{
							Type:         EventChunk,
							DeltaContent: chunk.DeltaContent,
							Thinking:     chunk.Thinking,
						}
					}
				}
			}
		}
	}

	eventChan <- EngineEvent{Type: EventDone}
	return nil
}
