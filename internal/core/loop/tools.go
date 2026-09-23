package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiancode/internal/llm"
	v1 "tiancode/pkg/plugin/v1"
)

func TrimToolOutput(output string, maxChars int) string {
	runes := []rune(output)
	if len(runes) <= maxChars {
		return output
	}
	half := maxChars / 2
	head := string(runes[:half])
	tail := string(runes[len(runes)-half:])
	return head + fmt.Sprintf("\n\n...[输出过长，中间 %d 字符已截断]...\n\n", len(runes)-maxChars) + tail
}

func (e *ExecutionEngine) runTool(ctx context.Context, sessionID, toolCallID, toolName string, rawArgs json.RawMessage, strategy string, turn int, allowedTools []llm.ToolDef, eventChan chan<- EngineEvent) (output string, isErr bool, written string, tddPass *bool) {
	if deny, reason := DenyByStrategy(strategy, toolName, rawArgs, turn); deny {
		return "[策略拦截] " + reason, true, "", nil
	}
	
	// 在 analyze 模式下，防御性阻断一切 Mutating 算子（比如外部挂载的 MCP），即使大模型出现幻觉去调用
	if NormalizeStrategy(strategy) == StrategyAnalyze {
		for _, td := range allowedTools {
			if td.Function.Name == toolName && td.Mutating {
				return "[安全拦截] 当前策略为只读分析，禁止调用该工具 (Mutating=true)", true, "", nil
			}
		}
	}
	rails := e.registry.ListRails()
	for _, rail := range rails {
		decision, railErr := rail.OnBeforeAct(ctx, sessionID, toolName, rawArgs)
		if railErr != nil || (decision != nil && !decision.Allow) {
			reason := fmt.Sprintf("%v", railErr)
			if decision != nil && decision.Reason != "" {
				reason = decision.Reason
			}

			// HITL: 危险工具拦截且标记为 NeedsConfirm，交由用户允许一次
			if decision != nil && decision.NeedsConfirm {
				if e.gateway == nil {
					return "[安全拦截] 缺少审批网关，拒绝危险操作", true, "", nil
				}
				allow, err := e.gateway.RequestConfirm(ctx, sessionID, toolCallID, toolName, string(rawArgs), reason)
				if err != nil {
					return fmt.Sprintf("[安全拦截] 审批中断: %v", err), true, "", nil
				}
				if !allow {
					return fmt.Sprintf("[安全拦截] 用户拒绝：%s", reason), true, "", nil
				}
				// 放行
				goto ALLOW_ONCE
			}
			return fmt.Sprintf("[安全拦截] 工具 [%s] 被 Rail 阻断: %s", toolName, reason), true, "", nil
		}
	}
ALLOW_ONCE:

	var result *v1.ToolResult
	if tool, ok := e.registry.GetToolByName(toolName); ok {
		res, err := tool.Execute(ctx, rawArgs)
		if err != nil {
			return fmt.Sprintf("工具 [%s] 执行失败: %v", toolName, err), true, "", nil
		}
		if res == nil {
			return fmt.Sprintf("工具 [%s] 返回空结果", toolName), true, "", nil
		}
		result = res
		output = TrimToolOutput(res.Content, 3000)
		isErr = res.IsError
	} else if e.mcpCall != nil {
			var mcpArgs map[string]any
			if len(rawArgs) > 0 {
				_ = json.Unmarshal(rawArgs, &mcpArgs)
			}
			if mcpArgs == nil {
				mcpArgs = map[string]any{}
			}
			mcpRes, err := e.mcpCall(ctx, toolName, mcpArgs)
			if err != nil {
				return fmt.Sprintf("MCP 算子 [%s] 执行失败: %v", toolName, err), true, "", nil
			}
			output = TrimToolOutput(mcpRes, 3000)
		} else {
			return fmt.Sprintf("[未知工具] %s 未在 Registry 或 MCP 中注册", toolName), true, "", nil
		}

	if result != nil {
		for _, rail := range rails {
			_ = rail.OnAfterAct(ctx, sessionID, toolName, result)
		}
		isWrite := toolName == "write_file"
		var argsObj struct {
			Action   string `json:"action"`
			RelPath  string `json:"rel_path"`
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
		}
		_ = json.Unmarshal(rawArgs, &argsObj)
		if strings.ToLower(argsObj.Action) == "write" {
			isWrite = true
		}
		if isWrite && result != nil && !result.IsError {
			written = argsObj.RelPath
			if written == "" {
				written = argsObj.Path
			}
			if written == "" {
				written = argsObj.FilePath
			}
		}
	}
	if written != "" && !isErr && ShouldVerifyAfterWrite(strategy) && e.verify != nil {
		vout, pass := e.verify(written)
		tddPass = &pass
		output = strings.TrimSpace(output) + "\n\n" + FormatVerifyFollowup(written, vout, pass)
	}
	return output, isErr, written, tddPass
}
