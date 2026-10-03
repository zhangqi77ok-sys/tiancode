// 工作区级可选项（第 8 批）：workspace-settings.json 按工作区绝对路径作 key。
//
// 为什么放工作区而不是全局：同一个"在这一行打开"命令在不同项目里可能完全不同
// （有的用 code -g，有的用别的编辑器），检查命令更是每个仓库一套。
// 文件与 extensions.json 同目录（configfile.Dir()），不进 config.json——
// 那是渠道/代理这类全局设置。
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// WorkspaceSettings 是单个工作区的可选项。两者缺省都为空 = 不启用。
type WorkspaceSettings struct {
	// OpenAtLine 是"在这一行打开"的命令模板：按空格拆成 argv，
	// {path} 与 {line} 会被替换。空 = 与 OpenInDefaultApp 行为完全一致。
	OpenAtLine string `json:"openAtLine"`
	// CheckCommand 是回合终态 / 写完文件后要跑的检查命令（同一套 argv 拆分规则）。
	// 空 = 任何时候都不跑（绝不猜 go test）。
	CheckCommand string `json:"checkCommand"`
	// ShellAllow 是 shell 审批白名单（0.0.24）：命令以这些前缀开头时，审批闸门
	// 自动放行不再弹确认卡（shell 在审批清单内才有意义）。按工作区各存一份——
	// 不同项目的安全基线不同。整条规则由用户显式配置，编排层只做前缀匹配，
	// 不做任何命令语义分析（ADR-0007 的纪律边界不变）。
	ShellAllow []string `json:"shellAllow,omitempty"`
}

const workspaceSettingsFile = "workspace-settings.json"

// workspaceSettingsPath 故意做成函数变量：测试把它指到临时目录，
// 绝不碰用户真实的 %APPDATA%\tiancode（测试污染用户配置是事故）。
var workspaceSettingsPath = func() string { return filepath.Join(configfile.Dir(), workspaceSettingsFile) }

// loadWorkspaceSettings 读某个工作区的设置。缺文件 / 解析失败 = 零值（不启用）——
// 一个可选项读不出来不该拦住对话。
func loadWorkspaceSettings(root string) WorkspaceSettings {
	root = strings.TrimSpace(root)
	if root == "" {
		return WorkspaceSettings{}
	}
	raw, err := os.ReadFile(workspaceSettingsPath())
	if err != nil {
		return WorkspaceSettings{}
	}
	var all map[string]WorkspaceSettings
	if err := json.Unmarshal(raw, &all); err != nil {
		return WorkspaceSettings{}
	}
	return all[workspaceKey(root)]
}

// saveWorkspaceSettings 写回某个工作区的设置（其余工作区的条目原样保留）。
// 两次都为空 = 删除该键，不留空壳。写盘走原子写（配置半截最难受）。
func saveWorkspaceSettings(root string, ws WorkspaceSettings) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("这场对话没有工作区，无法保存工作区设置")
	}
	all := map[string]WorkspaceSettings{}
	if raw, err := os.ReadFile(workspaceSettingsPath()); err == nil {
		if uerr := json.Unmarshal(raw, &all); uerr != nil {
			// 旧文件坏了就从空表重建：内容读不出来不该把保存也堵死
			all = map[string]WorkspaceSettings{}
		}
	}
	key := workspaceKey(root)
	// 三项全空才删键：ShellAllow（0.0.24）也是有效设置，重建结构体时必须带上
	//（漏了它会让保存悄悄丢白名单——测试抓到）。
	if strings.TrimSpace(ws.OpenAtLine) == "" && strings.TrimSpace(ws.CheckCommand) == "" && len(ws.ShellAllow) == 0 {
		delete(all, key)
	} else {
		all[key] = WorkspaceSettings{
			OpenAtLine:   strings.TrimSpace(ws.OpenAtLine),
			CheckCommand: strings.TrimSpace(ws.CheckCommand),
			ShellAllow:   ws.ShellAllow,
		}
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化工作区设置失败：%w", err)
	}
	if err := os.MkdirAll(configfile.Dir(), 0o700); err != nil {
		return fmt.Errorf("准备工作区设置目录失败：%w", err)
	}
	if err := atomicfile.WriteFileAtomic(workspaceSettingsPath(), data, 0o600); err != nil {
		return fmt.Errorf("保存工作区设置失败：%w", err)
	}
	return nil
}

// workspaceKey 统一 key 形态：Windows 上大小写不敏感，同一个目录的两种写法
// 不能当成两个键（与 normalizeWorkspace 同一套规范化）。
func workspaceKey(root string) string {
	clean := normalizeWorkspace(root)
	clean = strings.TrimRight(strings.ReplaceAll(clean, `\`, "/"), "/")
	return strings.ToLower(clean)
}

// WorkspaceSettingsFor 返回这场对话工作区的设置（无工作区 / 未配置 = 零值）。
func (s *ChatService) WorkspaceSettingsFor(sessionID string) WorkspaceSettings {
	root := s.SessionWorkspace(sessionID)
	if strings.TrimSpace(root) == "" {
		root = s.Workspace()
	}
	return loadWorkspaceSettings(root)
}

// SaveWorkspaceSettings 保存这场对话工作区的设置（key 用会话自己的工作区）。
func (s *ChatService) SaveWorkspaceSettings(sessionID string, ws WorkspaceSettings) error {
	root := s.SessionWorkspace(sessionID)
	if strings.TrimSpace(root) == "" {
		root = s.Workspace()
	}
	return saveWorkspaceSettings(root, ws)
}

// SplitArgv 把命令模板按**空格**拆成 argv（第 8 批的明确规则：不经 shell、
// 不自己加引号、不做更聪明的解析）。空模板返回 nil。
func SplitArgv(template string) []string {
	t := strings.TrimSpace(template)
	if t == "" {
		return nil
	}
	return strings.Fields(t)
}

// OpenAtLineArgv 由"在这一行打开"模板算出 argv：
// 第一个 token 是程序名，其余是参数，token 里的 {path}/{line} 会被替换。
// 返回 ok=false 表示不该走这条命令（模板为空 / 行号缺失或 0 / 模板没有程序名），
// 调用方按约定退回 OpenInDefaultApp。
func OpenAtLineArgv(template, absPath string, line int) (name string, args []string, ok bool) {
	if line <= 0 {
		return "", nil, false
	}
	tokens := SplitArgv(template)
	if len(tokens) == 0 {
		return "", nil, false
	}
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, strings.NewReplacer(
			"{path}", absPath,
			"{line}", fmt.Sprintf("%d", line),
		).Replace(t))
	}
	return out[0], out[1:], true
}

// DefaultOpenArgv 是"用系统默认关联程序打开"的 argv（与 OpenInDefaultApp 同源）：
// 目录直接开目录，文件交给 Shell 决定关联程序（不写死任何编辑器）。
func DefaultOpenArgv(abs string, isDir bool) (name string, args []string) {
	if isDir {
		return "explorer", []string{abs}
	}
	// cmd /c start "" <path>：空标题参数必须有，否则带引号的路径会被当成标题
	return "cmd", []string{"/c", "start", "", abs}
}
