package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"tiancode/internal/core/sandbox"
	v1 "tiancode/pkg/plugin/v1"
)

// Tool 文件系统受控操作算子插件
type Tool struct {
	id          string
	name        string
	version     string
	sandbox     *sandbox.Sandbox
	snapshotMgr *sandbox.SnapshotManager
}

// NewTool 构造实例
func NewTool(sb *sandbox.Sandbox, sm *sandbox.SnapshotManager) *Tool {
	return &Tool{
		id:          "tool.fs",
		name:        "Filesystem Controlled Sandbox Tool",
		version:     "1.0.0",
		sandbox:     sb,
		snapshotMgr: sm,
	}
}

func (t *Tool) ID() string             { return t.id }
func (t *Tool) Name() string           { return t.name }
func (t *Tool) Version() string        { return t.version }
func (t *Tool) Type() v1.PluginType    { return v1.TypeTool }
func (t *Tool) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (t *Tool) Start(ctx context.Context) error { return nil }
func (t *Tool) Stop(ctx context.Context) error  { return nil }
func (t *Tool) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true, Message: "FS Sandbox ready"}
}

func (t *Tool) Definition() v1.ToolDefinition {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{"read", "write", "list", "replace"},
				"description": "文件操作类型: read (读取), write (原子写入), list (列出目录), replace (手术级精准局部替换)",
			},
			"path": map[string]any{
				"type": "string",
				"description": "相对工作区的目标文件或目录路径 (action=read/write/replace 时必填；action=list 时可留空或传 '.' 表示工作区根目录)",
			},
			"content": map[string]any{
				"type": "string",
				"description": "写入的文件内容 (action=write 时必填)",
			},
			"target_content": map[string]any{
				"type": "string",
				"description": "局部替换的目标代码块（精确字符匹配，action=replace 时必填）",
			},
			"replacement_content": map[string]any{
				"type": "string",
				"description": "替换后的新代码块 (action=replace 时必填)",
			},
			"start_line": map[string]any{
				"type": "integer",
				"description": "起始行号 (可选，1-indexed，用于限定局部替换搜索区间)",
			},
			"end_line": map[string]any{
				"type": "integer",
				"description": "结束行号 (可选，1-indexed，用于限定局部替换搜索区间)",
			},
			"allow_multiple": map[string]any{
				"type": "boolean",
				"description": "是否允许替换多处相同匹配 (默认为 false，有多处匹配时报错阻断以防误伤)",
			},
		},
		"required": []string{"action"},
	}
	schemaBytes, _ := json.Marshal(schema)

	return v1.ToolDefinition{
		Name:        "fs_control",
		Description: "在受控沙箱内安全地读写、局部精准替换或列出工作区文件 (只读策略下会禁用 write/replace 动作)",
		Parameters:  schemaBytes,
		Mutating:    false,
	}
}

func (t *Tool) Execute(ctx context.Context, rawArgs json.RawMessage) (*v1.ToolResult, error) {
	if t.sandbox == nil {
		return &v1.ToolResult{Content: "error: filesystem sandbox not initialized", IsError: true}, nil
	}

	var args struct {
		Action             string `json:"action"`
		Path               string `json:"path"`
		RelPath            string `json:"rel_path"`
		FilePath           string `json:"file_path"`
		Content            string `json:"content"`
		TargetContent      string `json:"target_content"`
		OldString          string `json:"old_string"`
		ReplacementContent string `json:"replacement_content"`
		NewString          string `json:"new_string"`
		StartLine          int    `json:"start_line"`
		EndLine            int    `json:"end_line"`
		AllowMultiple      bool   `json:"allow_multiple"`
	}

	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return &v1.ToolResult{Content: fmt.Sprintf("invalid arguments: %v", err), IsError: true}, nil
	}

	targetPath := strings.TrimSpace(args.Path)
	if targetPath == "" {
		targetPath = strings.TrimSpace(args.RelPath)
	}
	if targetPath == "" {
		targetPath = strings.TrimSpace(args.FilePath)
	}

	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "read":
		if targetPath == "" {
			return &v1.ToolResult{Content: "read error: empty file path", IsError: true}, nil
		}
		data, err := t.sandbox.SafeReadFile(targetPath)
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
		}
		return &v1.ToolResult{Content: string(data), IsError: false}, nil

	case "write":
		if targetPath == "" {
			return &v1.ToolResult{Content: "atomic write error: empty file path", IsError: true}, nil
		}
		// 写前轻量建立影子快照
		if t.snapshotMgr != nil {
			_, _ = t.snapshotMgr.CreateSnapshot(fmt.Sprintf("before write to %s", targetPath))
		}

		if err := t.sandbox.AtomicWriteFile(targetPath, []byte(args.Content)); err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("atomic write error: %v", err), IsError: true}, nil
		}
		return &v1.ToolResult{Content: fmt.Sprintf("file [%s] written successfully (atomic sync)", targetPath), IsError: false}, nil

	case "replace":
		if targetPath == "" {
			return &v1.ToolResult{Content: "replace error: empty file path", IsError: true}, nil
		}
		targetStr := args.TargetContent
		if targetStr == "" {
			targetStr = args.OldString
		}
		if targetStr == "" {
			return &v1.ToolResult{Content: "replace error: target_content (or old_string) is required", IsError: true}, nil
		}
		replacementStr := args.ReplacementContent
		if replacementStr == "" {
			replacementStr = args.NewString
		}

		data, err := t.sandbox.SafeReadFile(targetPath)
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("replace read error: %v", err), IsError: true}, nil
		}
		contentStr := string(data)

		var newContent string
		var replaceCount int

		if args.StartLine > 0 || args.EndLine > 0 {
			lines := strings.Split(contentStr, "\n")
			startIdx := args.StartLine - 1
			if startIdx < 0 {
				startIdx = 0
			}
			endIdx := args.EndLine
			if endIdx <= 0 || endIdx > len(lines) {
				endIdx = len(lines)
			}
			if startIdx >= len(lines) || startIdx >= endIdx {
				return &v1.ToolResult{Content: fmt.Sprintf("replace error: invalid line range [%d, %d] for file with %d lines", args.StartLine, args.EndLine, len(lines)), IsError: true}, nil
			}

			subChunk := strings.Join(lines[startIdx:endIdx], "\n")
			count := strings.Count(subChunk, targetStr)
			if count == 0 {
				return &v1.ToolResult{Content: fmt.Sprintf("replace error: target_content not found within line range [%d, %d]", args.StartLine, args.EndLine), IsError: true}, nil
			}
			if count > 1 && !args.AllowMultiple {
				return &v1.ToolResult{Content: fmt.Sprintf("replace error: target_content matched %d times within line range [%d, %d]; specify allow_multiple: true or narrow down context", count, args.StartLine, args.EndLine), IsError: true}, nil
			}
			var replacedChunk string
			if args.AllowMultiple {
				replacedChunk = strings.ReplaceAll(subChunk, targetStr, replacementStr)
				replaceCount = count
			} else {
				replacedChunk = strings.Replace(subChunk, targetStr, replacementStr, 1)
				replaceCount = 1
			}

			prefix := ""
			if startIdx > 0 {
				prefix = strings.Join(lines[:startIdx], "\n") + "\n"
			}
			suffix := ""
			if endIdx < len(lines) {
				suffix = "\n" + strings.Join(lines[endIdx:], "\n")
			}
			newContent = prefix + replacedChunk + suffix
		} else {
			count := strings.Count(contentStr, targetStr)
			if count == 0 {
				return &v1.ToolResult{Content: fmt.Sprintf("replace error: target_content not found in [%s]", targetPath), IsError: true}, nil
			}
			if count > 1 && !args.AllowMultiple {
				return &v1.ToolResult{Content: fmt.Sprintf("replace error: target_content matched %d times in [%s]; specify allow_multiple: true or provide more surrounding lines", count, targetPath), IsError: true}, nil
			}
			if args.AllowMultiple {
				newContent = strings.ReplaceAll(contentStr, targetStr, replacementStr)
				replaceCount = count
			} else {
				newContent = strings.Replace(contentStr, targetStr, replacementStr, 1)
				replaceCount = 1
			}
		}

		// 写前轻量建立影子快照
		if t.snapshotMgr != nil {
			_, _ = t.snapshotMgr.CreateSnapshot(fmt.Sprintf("before replace in %s", targetPath))
		}

		if err := t.sandbox.AtomicWriteFile(targetPath, []byte(newContent)); err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("atomic write error: %v", err), IsError: true}, nil
		}
		return &v1.ToolResult{
			Content: fmt.Sprintf("file [%s] content replaced successfully (%d occurrence(s) replaced)", targetPath, replaceCount),
			IsError: false,
		}, nil

	case "list":
		if targetPath == "" {
			targetPath = "."
		}
		entries, err := t.sandbox.ListDir(targetPath)
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("list error: %v", err), IsError: true}, nil
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			tag := "[FILE]"
			if e.IsDir() {
				tag = "[DIR]"
			}
			names = append(names, fmt.Sprintf("%s %s", tag, e.Name()))
		}
		bytesOut, _ := json.Marshal(names)
		return &v1.ToolResult{Content: string(bytesOut), IsError: false}, nil

	default:
		return &v1.ToolResult{Content: fmt.Sprintf("unknown action: %s", args.Action), IsError: true}, nil
	}
}
