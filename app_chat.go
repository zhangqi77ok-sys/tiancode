package main

import (
	"tiancode/internal/lsp"
	"tiancode/internal/telemetry"

	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"tiancode/internal/config"
	"tiancode/internal/core/loop"
	"tiancode/internal/core/sandbox"
	"tiancode/internal/session"
	v1 "tiancode/pkg/plugin/v1"
	"tiancode/pkg/protocol"
	safetyrail "tiancode/plugins/rail/safety"

	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type ChatRequest struct {
	SessionID    string `json:"session_id"`
	Prompt       string `json:"prompt"`
	Model        string `json:"model"`
	IsFullAuto   bool   `json:"is_full_auto"`
	Strategy     string `json:"strategy"`
	StrategyNote string `json:"strategy_note"`
}

type ChannelResolver interface {
	GetChannelForModel(reqModel string) *config.ChannelConfig
}

func resolveChatCredentials(store ChannelResolver, reqModel string) (endpoint, apiKey, model, authType string, err error) {
	if store == nil {
		return "", "", "", "", fmt.Errorf("渠道存储器未初始化")
	}
	ch := store.GetChannelForModel(reqModel)
	if ch == nil {
		return "", "", "", "", fmt.Errorf("未配置任何模型渠道。请在设置中添加真实 endpoint 与 API Key，禁止使用内置假地址")
	}
	model = strings.TrimSpace(reqModel)
	if model == "" {
		model = strings.TrimSpace(ch.Model)
	}
	if model == "" {
		return "", "", "", "", fmt.Errorf("未指定模型：请在渠道中填写 model")
	}

	var cs *config.ChannelStore
	if concrete, ok := store.(*config.ChannelStore); ok {
		cs = concrete
	}

	key, ep, prov, err := config.ResolveChannelCredentials(context.Background(), cs, ch)
	if err != nil {
		return "", "", "", "", err
	}

	return ep, key, model, prov, nil
}

func appendEnabledPolicies(base string, skills []config.SkillConfig, rules []config.RuleConfig) string {
	sortedSkills := make([]config.SkillConfig, len(skills))
	copy(sortedSkills, skills)
	sort.Slice(sortedSkills, func(i, j int) bool {
		return sortedSkills[i].Name < sortedSkills[j].Name
	})

	sortedRules := make([]config.RuleConfig, len(rules))
	copy(sortedRules, rules)
	sort.Slice(sortedRules, func(i, j int) bool {
		return sortedRules[i].Title < sortedRules[j].Title
	})

	for _, sk := range sortedSkills {
		if sk.Enabled && strings.TrimSpace(sk.Prompt) != "" {
			base += "\n[技能 " + sk.Name + "] " + sk.Prompt
		}
	}
	for _, r := range sortedRules {
		if r.Enabled && strings.TrimSpace(r.Content) != "" {
			base += "\n[规则规约] " + r.Content
		}
	}
	return protocol.NormalizeNewlines(base)
}

// expandMentionedFiles 扫描用户 Prompt 中的 @path 标记，若工作区存在对应文件则将其内容安全附入用户消息（最多 8KB/文件）
func expandMentionedFiles(workspace string, sb *sandbox.Sandbox, prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return prompt
	}

	words := strings.Fields(prompt)
	var appendedFiles []string
	seen := make(map[string]bool)

	for _, w := range words {
		if !strings.HasPrefix(w, "@") {
			continue
		}
		rawPath := strings.TrimPrefix(w, "@")
		rawPath = strings.TrimRight(rawPath, ",.?!;:'\"，。？！；：")
		if rawPath == "" || seen[rawPath] {
			continue
		}

		var fileData []byte
		var readErr error
		if sb != nil {
			fileData, readErr = sb.SafeReadFile(rawPath)
		} else if workspace != "" {
			fileData, readErr = os.ReadFile(filepath.Join(workspace, rawPath))
		} else {
			continue
		}

		if readErr != nil {
			continue
		}

		seen[rawPath] = true
		content := string(fileData)
		if len(content) > 8192 {
			content = content[:8192] + "\n...[文件内容超长已截断，保留前 8KB]..."
		}
		appendedFiles = append(appendedFiles, fmt.Sprintf("\n\n--- [引用文件内容: %s] ---\n%s\n--- [引用文件结束: %s] ---", rawPath, content, rawPath))
	}

	if len(appendedFiles) > 0 {
		return prompt + strings.Join(appendedFiles, "")
	}
	return prompt
}

func (a *App) ResumeAgentChoice(sessionID string, requestID string, optionID string, customNote string) bool {
	return a.gateway.DeliverReply(requestID, loop.HumanReply{
		OptionID:   optionID,
		CustomNote: customNote,
		Allow:      true,
	})
}

func (a *App) ResumeAgentConfirm(sessionID string, requestID string, allow bool) bool {
	return a.gateway.DeliverReply(requestID, loop.HumanReply{
		OptionID:   "",
		CustomNote: "",
		Allow:      allow,
	})
}

func (a *App) SendMessage(req ChatRequest) error {
	if a.ctx == nil {
		return fmt.Errorf("context not initialized")
	}

	a.agentMu.Lock()
	if a.agentCancel != nil {
		a.agentCancel()
	}
	agentCtx, cancel := context.WithCancel(context.Background())
	a.agentTaskID++
	currentTaskID := a.agentTaskID
	a.agentCancel = cancel
	a.currentSessionID = req.SessionID
	a.agentMu.Unlock()

	go func() {
		defer func() {
			a.agentMu.Lock()
			if a.agentTaskID == currentTaskID {
				a.agentCancel = nil
			}
			a.agentMu.Unlock()
		}()

		endpoint, apiKey, model, authType, credErr := resolveChatCredentials(a.channelStore, req.Model)
		if credErr != nil {
			errMsg := "\n\n[配置错误] " + credErr.Error()
			runtime.EventsEmit(a.ctx, "agent:start", map[string]any{
				"session_id": req.SessionID,
				"model":      model,
			})
			runtime.EventsEmit(a.ctx, "agent:chunk", map[string]any{
				"session_id": req.SessionID,
				"delta":      errMsg,
				"turn":       1,
			})
			runtime.EventsEmit(a.ctx, "agent:complete", map[string]any{
				"session_id": req.SessionID,
				"error":      "missing_api_key",
			})
			return
		}

		// 2. 加载已有会话历史，若不存在则新建
		var currentSession session.ChatSession
		existing, err := a.sessionStore.Get(req.SessionID)
		if err == nil && existing != nil {
			currentSession = *existing
		} else {
			currentSession = session.ChatSession{
				ID:        req.SessionID,
				Title:     "",
				Model:        model,
				Workspace: a.workspace,
				CreatedAt: time.Now().Unix(),
				UpdatedAt: time.Now().Unix(),
				Messages:  make([]session.SessionMessage, 0),
			}
		}
		currentSession.Workspace = a.workspace
		currentSession.Model = model

		// 追加用户消息
		req.Prompt = safetyrail.StripSecretsFromPrompt(req.Prompt)
		expandedContent := expandMentionedFiles(a.workspace, a.sandbox, req.Prompt)

		// 3. 构建提示词体系 (确定性前缀流水线与动态上下文物理隔离)
		// Layer 0-1: 纯静态系统提示词 (角色基座 + 按字典序排序的 Rules 与 Skills，严禁插入动态时间戳或动态任务信息)
		systemPrompt := "你是 湉码 / tiancode 纯原生桌面智能体。你有权调用工具来审查、读取、修改工程代码及运行测试命令。请优先利用工具解决问题，并在每次调用后解释原因。"
		if a.extraStore != nil {
			systemPrompt = appendEnabledPolicies(systemPrompt, a.extraStore.ListSkills(), a.extraStore.ListRules())
		}
		systemPrompt = safetyrail.StripSecretsFromPrompt(systemPrompt)
		systemPrompt = protocol.NormalizeNewlines(systemPrompt)

		// Layer 6: 动态瞬态上下文 (技术栈、任务接续、待确认文件，严格作为最新 User 消息尾部附件，不污染系统前缀)
		var dynamicTail strings.Builder
		stackInfo := sandbox.DetectProjectStack(a.workspace)
		if stackPrompt := sandbox.FormatStackPrompt(stackInfo); stackPrompt != "" {
			dynamicTail.WriteString("\n" + stackPrompt)
		}

		cleanPrompt := session.CleanGoalPrompt(req.Prompt)
		isContinuation := session.IsContinuationPrompt(req.Prompt)
		if isContinuation && currentSession.Task != nil && currentSession.Task.Goal != "" {
			currentSession.Task.Status = session.TaskStatusRunning
			continuationCtx := session.BuildContinuationContext(currentSession.Task, cleanPrompt)
			dynamicTail.WriteString("\n" + continuationCtx)
		} else {
			currentSession.Task = &session.TaskModel{
				Goal:             cleanPrompt,
				Status:           session.TaskStatusRunning,
				ToolBudget:       0, // 0 代表 AI 自主决策结束模式（不设人为固定轮次扣减）
				ToolsUsed:        0,
				PendingDiffFiles: make([]string, 0),
			}
		}

		if currentSession.Task != nil && len(currentSession.Task.PendingDiffFiles) > 0 {
			dynamicTail.WriteString(fmt.Sprintf("\n[待确认修改文件] %s", strings.Join(currentSession.Task.PendingDiffFiles, ", ")))
		}

		userContentWithDynamic := expandedContent
		if dynamicTail.Len() > 0 {
			userContentWithDynamic += fmt.Sprintf("\n\n<dynamic_context>%s\n</dynamic_context>", dynamicTail.String())
		}

		userMsg := session.SessionMessage{
			ID:      fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			Role:    "user",
			Content: userContentWithDynamic,
			Time:    time.Now().Format("15:04"),
		}
		currentSession.Messages = append(currentSession.Messages, userMsg)
		_ = a.sessionStore.AppendMessage(req.SessionID, userMsg)

		// 发送开始事件
		runtime.EventsEmit(a.ctx, "agent:start", map[string]any{
			"session_id": req.SessionID,
			"model":      model,
		})

		var assistantThinking strings.Builder
		var assistantContent strings.Builder
		var lastToolExec *session.ToolExecution
		allToolExecs := make([]session.ToolExecution, 0)

		workspaceTools := a.buildLLMToolsFromRegistry(agentCtx)
		workspaceTools, systemPrompt = loop.ApplyStrategy(req.Strategy, req.StrategyNote, workspaceTools, systemPrompt)
		conversation := buildConversationWindow(systemPrompt, currentSession.Messages, 32000)
		roundStart := time.Now()
		var hasHitCap bool
		var hasError bool
		var lastTDDPassed *bool
		var lastUsage *v1.TokenUsage
		eventChan := make(chan loop.EngineEvent, 64)
		go func() {
			_ = a.engine.Execute(agentCtx, &loop.EngineRequest{
				Model:        model,
				Provider:     authType,
				Prompt:       req.Prompt,
				SessionID:    req.SessionID,
				Endpoint:     endpoint,
				APIKey:       apiKey,
				SystemPrompt: systemPrompt,
				Messages:     conversation,
				LLMTools:     workspaceTools,
				Strategy:     loop.NormalizeStrategy(req.Strategy),
				StrategyNote: req.StrategyNote,
			}, eventChan)
		}()

		for ev := range eventChan {
			switch ev.Type {
			case loop.EventChunk:
				if ev.Thinking != "" {
					assistantThinking.WriteString(ev.Thinking)
					runtime.EventsEmit(a.ctx, "agent:thinking", map[string]any{
						"session_id": req.SessionID,
						"thinking":   ev.Thinking,
					})
				}
				if ev.DeltaContent != "" {
					assistantContent.WriteString(ev.DeltaContent)
					runtime.EventsEmit(a.ctx, "agent:chunk", map[string]any{
						"session_id": req.SessionID,
						"delta":      ev.DeltaContent,
					})
				}
			case loop.EventChoice:
				if ev.Choice != nil {
					runtime.EventsEmit(a.ctx, "agent:choice", ev.Choice)
				}
			case loop.EventConfirm:
				if ev.Confirm != nil {
					runtime.EventsEmit(a.ctx, "agent:confirm", ev.Confirm)
				}
			case loop.EventToolStart:
				runtime.EventsEmit(a.ctx, "agent:tool_start", map[string]any{
					"session_id": req.SessionID,
					"id":         ev.ToolCallID,
					"tool":       ev.ToolName,
					"args":       string(ev.ToolArgs),
				})
			case loop.EventToolEnd:
				if currentSession.Task != nil {
					currentSession.Task.ToolsUsed++
				}
				tExec := session.ToolExecution{Name: ev.ToolName, Args: string(ev.ToolArgs), Output: ev.ToolOutput}
				allToolExecs = append(allToolExecs, tExec)
				cp := tExec
				lastToolExec = &cp
				runtime.EventsEmit(a.ctx, "agent:tool_end", map[string]any{
					"session_id": req.SessionID,
					"id":         ev.ToolCallID,
					"tool":       ev.ToolName,
					"output":     ev.ToolOutput,
				})
			case loop.EventFilesChanged:
				if currentSession.Task != nil {
					already := false
					for _, f := range currentSession.Task.PendingDiffFiles {
						if f == ev.ToolName {
							already = true
							break
						}
					}
					if !already {
						currentSession.Task.PendingDiffFiles = append(currentSession.Task.PendingDiffFiles, ev.ToolName)
					}
				}
				runtime.EventsEmit(a.ctx, "agent:files_changed", map[string]any{
					"session_id": req.SessionID,
					"file":       ev.ToolName,
				})
				if diagReport, diagErr := lsp.DiagnoseFile(a.workspace, ev.ToolName); diagErr == nil && diagReport != nil && diagReport.HasErrors {
					runtime.EventsEmit(a.ctx, "lsp:diagnostic", map[string]any{
						"session_id": req.SessionID,
						"file":       ev.ToolName,
						"has_errors": true,
						"errors":     diagReport.Errors,
					})
				}
			case loop.EventUsage:
				if ev.Usage != nil {
					lastUsage = ev.Usage
				}
			case loop.EventHitCap:
				hasHitCap = true
			case loop.EventTDDResult:
				if ev.TDDPassed != nil {
					lastTDDPassed = ev.TDDPassed
					if currentSession.Task != nil {
						currentSession.Task.TDDPassed = lastTDDPassed
					}
				}
			case loop.EventError:
				hasError = true
				errMsg := ev.ErrorMessage
				if !strings.HasPrefix(strings.TrimSpace(errMsg), "⚠️") && !strings.HasPrefix(strings.TrimSpace(errMsg), "[") {
					errMsg = fmt.Sprintf("\n\n⚠️ %s", errMsg)
				} else if !strings.HasPrefix(errMsg, "\n") {
					errMsg = "\n\n" + errMsg
				}
				assistantContent.WriteString(errMsg)
				runtime.EventsEmit(a.ctx, "agent:chunk", map[string]any{
					"session_id": req.SessionID,
					"delta":      errMsg,
				})
			}
		}

		roundDuration := time.Since(roundStart).Milliseconds()
		promptTok := (len(req.Prompt) + 300) / 3
		compTok := (assistantContent.Len() + assistantThinking.Len()) / 3
		var cacheReadTok, cacheCreateTok int
		if lastUsage != nil {
			if lastUsage.PromptTokens > 0 {
				promptTok = int(lastUsage.PromptTokens)
			}
			if lastUsage.CompletionTokens > 0 {
				compTok = int(lastUsage.CompletionTokens)
			}
			cacheReadTok = int(lastUsage.CacheReadTokens)
			cacheCreateTok = int(lastUsage.CacheCreationTokens)
		}
		if compTok < 1 && assistantContent.Len() > 0 {
			compTok = 1
		}
		telemetry.GetTracker().RecordWithCache(model, promptTok, compTok, cacheReadTok, cacheCreateTok, roundDuration)

		// 派发最新遥测指标，更新微型缓存指示胶囊
		runtime.EventsEmit(a.ctx, "telemetry:usage", a.GetUsageMetrics())

		// 7. 持久化 Assistant 回复至磁盘与终态流转
		if agentCtx.Err() != nil {
			if currentSession.Task != nil {
				currentSession.Task.Status = session.TaskStatusInterrupted
			}
			if assistantContent.Len() == 0 {
				notice := loop.FormatInterruptedNotice()
				assistantContent.WriteString(notice)
				runtime.EventsEmit(a.ctx, "agent:chunk", map[string]any{
					"session_id": req.SessionID,
					"delta":      notice,
				})
			}
		} else {
			if assistantContent.Len() == 0 && assistantThinking.Len() == 0 && len(allToolExecs) == 0 {
				emptyNotice := loop.FormatEmptyOutputNotice()
				assistantContent.WriteString(emptyNotice)
				runtime.EventsEmit(a.ctx, "agent:chunk", map[string]any{
					"session_id": req.SessionID,
					"delta":      emptyNotice,
				})
			}

			// 依据结构化指标与执行事实流转 Task 终态，杜绝扫描正文猜词
			if currentSession.Task != nil {
				if lastTDDPassed != nil {
					currentSession.Task.TDDPassed = lastTDDPassed
				}
				if hasHitCap {
					currentSession.Task.Status = session.TaskStatusCapped
				} else if currentSession.Task.TDDPassed != nil && !*currentSession.Task.TDDPassed {
					currentSession.Task.Status = session.TaskStatusTDDFailed
				} else if len(currentSession.Task.PendingDiffFiles) > 0 {
					currentSession.Task.Status = session.TaskStatusPendingDiff
				} else if hasError {
					currentSession.Task.Status = session.TaskStatusFailed
				} else {
					currentSession.Task.Status = session.TaskStatusCompleted
				}

				asstStr := assistantContent.String()
				runes := []rune(strings.TrimSpace(asstStr))
				if len(runes) > 120 {
					currentSession.Task.Summary = string(runes[len(runes)-120:])
				} else {
					currentSession.Task.Summary = string(runes)
				}
			}
		}

		asstMsg := session.SessionMessage{
			ID:       fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			Role:     "assistant",
			Content:  assistantContent.String(),
			Thinking: assistantThinking.String(),
			Tool:     lastToolExec,
			Tools:    allToolExecs,
			Time:     time.Now().Format("15:04"),
		}
		currentSession.Messages = append(currentSession.Messages, asstMsg)
		currentSession.UpdatedAt = time.Now().Unix()
		_ = a.sessionStore.AppendMessage(req.SessionID, asstMsg)
		if currentSession.Task != nil {
			_ = a.sessionStore.UpdateTask(req.SessionID, func(t *session.TaskModel) {
				*t = *currentSession.Task
			})
		}

		finalStatus := ""
		if currentSession.Task != nil {
			finalStatus = string(currentSession.Task.Status)
		}
		runtime.EventsEmit(a.ctx, "agent:done", map[string]any{
			"session_id":  req.SessionID,
			"task_status": finalStatus,
		})
	}()

	return nil
}

type WailsInteractionGateway struct {
	app     *App
	pending map[string]chan loop.HumanReply
	mu      sync.Mutex
}

func NewWailsInteractionGateway(a *App) *WailsInteractionGateway {
	return &WailsInteractionGateway{
		app:     a,
		pending: make(map[string]chan loop.HumanReply),
	}
}

func (w *WailsInteractionGateway) RequestConfirm(ctx context.Context, sessionID, requestID, toolName, argsPreview, reason string) (bool, error) {
	w.mu.Lock()
	ch := make(chan loop.HumanReply, 1)
	w.pending[requestID] = ch
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.pending, requestID)
		w.mu.Unlock()
	}()

	runtime.EventsEmit(w.app.ctx, "agent:confirm", &loop.ConfirmPayload{
		SessionID:   sessionID,
		RequestID:   requestID,
		Tool:        toolName,
		ArgsPreview: argsPreview,
		Reason:      reason,
	})

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(5 * time.Minute):
		return false, fmt.Errorf("timeout waiting for user confirmation")
	case reply := <-ch:
		if reply.Timeout {
			return false, fmt.Errorf("timeout waiting for user confirmation")
		}
		return reply.Allow, nil
	}
}

func (w *WailsInteractionGateway) RequestChoice(ctx context.Context, sessionID, requestID, question string, options []loop.ChoiceOption) (loop.HumanReply, error) {
	w.mu.Lock()
	ch := make(chan loop.HumanReply, 1)
	w.pending[requestID] = ch
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.pending, requestID)
		w.mu.Unlock()
	}()

	runtime.EventsEmit(w.app.ctx, "agent:choice", &loop.ChoicePayload{
		SessionID:   sessionID,
		RequestID:   requestID,
		Question:    question,
		Options:     options,
		AllowCustom: true,
	})

	select {
	case <-ctx.Done():
		return loop.HumanReply{}, ctx.Err()
	case <-time.After(5 * time.Minute):
		return loop.HumanReply{Timeout: true}, nil
	case reply := <-ch:
		return reply, nil
	}
}

func (w *WailsInteractionGateway) DeliverReply(requestID string, reply loop.HumanReply) bool {
	w.mu.Lock()
	ch, ok := w.pending[requestID]
	w.mu.Unlock()
	if ok {
		select {
		case ch <- reply:
			return true
		default:
		}
	}
	return false
}

