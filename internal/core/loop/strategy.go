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
func ApplyStrategy(strategy, note string, tools []llm.ToolDef, system string) ([]llm.ToolDef, string) {
	s := NormalizeStrategy(strategy)
	note = strings.TrimSpace(note)
	switch s {
	case StrategyAnalyze:
		system += "\n[执行策略 analyze (审查与分析)] 只允许读取与解释。禁止写入文件、禁止执行会改动工作区的命令、禁止 Git 写操作。"
		system += "\n【审查与探索铁律（先检索/先地图再下钻）】禁止盲目全库扫描或无节制大面积递归列举文件。必须严格执行："
		system += "\n 1. 首选检索：优先使用 search_workspace 算子 (grep/find) 快速定位关键词、函数定义与关键文件，杜绝盲扫；"
		system += "\n 2. 先看地图：首轮仅观察顶层目录结构与关键清单（如 go.mod, package.json, Cargo.toml, README 等）；"
		system += "\n 3. 精准下钻：仅深入读取靶向文件并分析，严禁读取无关目录或第三方依赖（如 node_modules/vendor/bin/dist）。"
		tools = filterTools(tools, func(t llm.ToolDef) bool {
			// 如果是 fs_control，在只读策略下修改其描述与 enum
			if strings.ToLower(t.Function.Name) == "fs_control" {
				return true // 保留，但靠 DenyByStrategy 和提示词拦写
			}
			return !t.Mutating
		})
	case StrategyTDD:
		system += "\n[执行策略 tdd (测试驱动开发)] 先运行或补齐前置测试，再改最小实现，直到测试全绿通过。"
		system += "\n【TDD 完成判定铁律】测试失败则任务状态绝对不是完成，严禁在测试未通过时宣称任务完成；必须继续分析失败原因并修复代码直至测试全部通过。"
	default:
		system += "\n[全自主统一 Coding Agent] 具备读取检索、代码编写、终端运行与测试验证的完整能力。意图自适应原则：\n 1. 当用户仅要求解释、答疑、代码审查或架构分析时，通过 search_workspace 与 read_file 只读分析并给出详尽解答，不修改工作区文件；\n 2. 当用户要求修复 Bug、实现功能、新增接口或重构代码时，先定位后精准改写，修改后自动产生 Monaco Diff 待用户审核；\n 3. 当用户要求测试驱动或验证质量时，自主调用测试算子进行验证并修复；\n 4. 探索代码时遵循「先检索/看地图再精准下钻」原则，严禁盲目全库递归遍历。"
	}
	if note != "" {
		system += "\n[用户附加约束] " + note
	}

	system += "\n【任务自主完成与结束铁律】"
	system += "\n1. 执行由你完全自主驱动，不设置人为固定轮次强行中断：当你自主判定当前任务已达成目标、或已得出明确结论向用户汇报时，请直接向用户输出答复内容，不要再发起任何工具调用。系统检测到你未发起工具调用时，即确认本次任务由你自主圆满交付。"
	system += "\n2. 若任务尚未达成（如需要修改更多关联文件、继续运行测试排错、定位或修复代码等），请自主继续调用必要工具推进，直到任务完成。"

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
