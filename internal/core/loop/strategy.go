package loop

import (
	"encoding/json"
	"strings"

	"tiancode/internal/llm"
)

const (
	StrategyAnalyze   = "analyze"
	StrategyImplement = "implement"
	StrategyTDD       = "tdd"
)

// NormalizeStrategy 只接受内核认识的三种策略，其余一律当成 implement。
func NormalizeStrategy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case StrategyAnalyze, "readonly", "read_only":
		return StrategyAnalyze
	case StrategyTDD, "test":
		return StrategyTDD
	default:
		return StrategyImplement
	}
}

// ApplyStrategy 改写系统提示，并在只读策略下从模型可见工具表里拿掉会改磁盘的算子。
// 提示词模板外置于 strategy_prompts.go，按归一化策略名选择模板并注入变量（Label/Note），实现配置与逻辑分离。
func ApplyStrategy(strategy, note string, tools []llm.ToolDef, system string) ([]llm.ToolDef, string) {
	s := NormalizeStrategy(strategy)
	note = strings.TrimSpace(note)

	var label string
	switch s {
	case StrategyAnalyze:
		label = "analyze"
	case StrategyTDD:
		label = "tdd"
	default:
		label = "implement"
	}

	var body strings.Builder
	if tmpl, ok := strategyPromptTpl[s]; ok {
		_ = tmpl.Execute(&body, map[string]string{"Label": label, "Note": note})
	}
	system += "\n" + body.String()
	system += "\n" + sharedCompletionTpl

	// 只读策略下从可见工具表移除会改盘/执行的算子（fs_control 保留，由 DenyByStrategy 拦截其写动作）
	if s == StrategyAnalyze {
		tools = filterTools(tools, func(t llm.ToolDef) bool {
			if strings.ToLower(t.Function.Name) == "fs_control" {
				return true
			}
			return !t.Mutating
		})
	}
	return tools, system
}

// DenyByStrategy 在真正执行工具前再拦一层，避免模型无视提示词去写盘或在首轮盲目扫库。
func DenyByStrategy(strategy, toolName string, rawArgs json.RawMessage, turn ...int) (deny bool, reason string) {
	s := NormalizeStrategy(strategy)
	name := strings.ToLower(strings.TrimSpace(toolName))

	// implement / tdd 策略：首轮（turn == 1）硬闸，禁止盲目直接读深层业务文件，引导先检索定位或先看根地图
	if s != StrategyAnalyze {
		if len(turn) > 0 && turn[0] == 1 && name == "fs_control" {
			var args struct {
				Action   string `json:"action"`
				Path     string `json:"path"`
				RelPath  string `json:"rel_path"`
				FilePath string `json:"file_path"`
			}
			_ = json.Unmarshal(rawArgs, &args)
			action := strings.ToLower(strings.TrimSpace(args.Action))
			if action == "read" {
				target := strings.TrimSpace(args.Path)
				if target == "" {
					target = strings.TrimSpace(args.RelPath)
				}
				if target == "" {
					target = strings.TrimSpace(args.FilePath)
				}
				if !isAllowedAnalyzeFirstTurnFile(target) {
					return true, "请先 search_workspace 或 list 工作区根（先地图后下钻）：第 1 轮工具调用禁止直接读取深层代码，请先使用 search_workspace 定位或 list 根目录结构"
				}
			}
		}
		return false, ""
	}

	// 以下为 analyze 策略专有拦截逻辑
	if name == "exec_command" {
		return true, "当前策略为只读分析，已拦截 exec_command"
	}
	if name == "write_file" {
		return true, "当前策略为只读分析，已拦截写文件"
	}
	if name == "fs_control" {
		var args struct {
			Action   string `json:"action"`
			Path     string `json:"path"`
			RelPath  string `json:"rel_path"`
			FilePath string `json:"file_path"`
		}
		_ = json.Unmarshal(rawArgs, &args)
		action := strings.ToLower(strings.TrimSpace(args.Action))
		if action == "write" {
			return true, "当前策略为只读分析，已拦截 fs_control write"
		}
		// 审查任务首轮（turn == 1）：硬闸约束必须先看地图（list 根目录或读取根清单文件）
		if len(turn) > 0 && turn[0] == 1 && action == "read" {
			target := strings.TrimSpace(args.Path)
			if target == "" {
				target = strings.TrimSpace(args.RelPath)
			}
			if target == "" {
				target = strings.TrimSpace(args.FilePath)
			}
			if !isAllowedAnalyzeFirstTurnFile(target) {
				return true, "请先 search_workspace 或 list 工作区根（先地图后下钻）：第 1 轮工具调用必须先观察地图（list 根目录或读取根清单文件如 README.md, go.mod, package.json）。请先输出顶层结构地图并定靶，下一轮再精准下钻读取具体业务文件。"
			}
		}
	}
	return false, ""
}

func isAllowedAnalyzeFirstTurnFile(p string) bool {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.TrimPrefix(p, "./")
	if p == "" || p == "." {
		return true
	}
	lower := strings.ToLower(p)
	// 允许根文档目录（如 docs/ 目录下的宏观设计或 readme）
	if strings.HasPrefix(lower, "docs/") || strings.HasSuffix(lower, "/readme.md") || strings.HasSuffix(lower, "/readme") {
		return true
	}
	// 含有其他子目录的深层业务代码必须第 2 轮及以后定靶下钻
	if strings.Contains(p, "/") {
		return false
	}
	// 根目录仅允许清单配置与宏观工程说明，严禁首轮直接啃任意源文件 (如 a.go / main.go)
	switch lower {
	case "readme.md", "readme", "agents.md", "gemini.md", "architecture.md", "roadmap.md", "license":
		return true
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock":
		return true
	case "cargo.toml", "cargo.lock", "requirements.txt", "pyproject.toml", "pom.xml", "build.gradle", "build.gradle.kts":
		return true
	case "makefile", "cmakelists.txt", "tsconfig.json", "wails.json", ".gitignore", "docker-compose.yml", "dockerfile":
		return true
	default:
		return false
	}
}

// ShouldVerifyAfterWrite TDD 策略在写盘后必须跑工作区测试，其它策略不自动跑。
func ShouldVerifyAfterWrite(strategy string) bool {
	return NormalizeStrategy(strategy) == StrategyTDD
}

// FormatVerifyFollowup 把测试结果缝进工具输出，下一轮模型能看见。
func FormatVerifyFollowup(file, output string, pass bool) string {
	if pass {
		return "[TDD 验证] 写入 " + file + " 后测试通过\n" + output
	}
	return "[TDD 验证失败] 写入 " + file + " 后测试未通过，必须继续修复，不得宣称完成\n" + output
}

func filterTools(tools []llm.ToolDef, keep func(t llm.ToolDef) bool) []llm.ToolDef {
	out := make([]llm.ToolDef, 0, len(tools))
	for _, t := range tools {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}
