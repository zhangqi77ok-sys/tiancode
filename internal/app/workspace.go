// 工作区用例：会话级受控工具集（fs/shell/git/search 各持一份，根固定）。
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
	"tiancode/internal/platform/fstool"
	"tiancode/internal/platform/gittool"
	"tiancode/internal/platform/searchtool"
	"tiancode/internal/platform/shelltool"
)

// normalizeWorkspace 规范化工作区路径（0.2.36 审计 R5：启动与切换共用一套）：
// Abs + Clean + 符号链接解析（能解析就用真实路径，失败就用 Clean 后原路径——
// 网络盘/未挂载盘不让调用方拒绝启动）；盘符根与 UNC 不做额外去斜杠
// （filepath.Clean 已正确处理尾部分隔符，手工 TrimRight 会裁坏 \\server\share\）。
func normalizeWorkspace(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = filepath.Clean(abs)
	} else {
		dir = filepath.Clean(dir)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = filepath.Clean(real)
	}
	return dir
}

// firstWorkspaceOfLedger 返回账本里**第一个** workspace 事件记录的路径
// （0.2.36 审计 R1）。归属以首个为准（与侧栏分组、TestSessionSummaries_CarryWorkspace
// 一致）：旧账本里后来若又写了别的路径也不改归属——否则侧栏分组与真实
// 写入目录会再次分叉。own=true 表示该会话已有归属（含显式空串 = 纯对话）。
// 尽力而为语义：读取失败按"无归属"处理（不阻断发送）。
func firstWorkspaceOfLedger(l *session.Ledger) (string, bool) {
	root := ""
	own := false
	if err := l.Replay(func(ev session.Event) error {
		if own || ev.Kind() != session.EventWorkspace {
			return nil
		}
		var p struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err == nil {
			root = p.Path
			own = true
		}
		return nil
	}); err != nil {
		return "", false
	}
	return root, own
}

// sessionTools 是**会话级**受控工具集（0.2.36 审计 R1）：fs/shell/git/search
// 各持一份，根固定在会话的归属工作区。为什么不再用全局单例 + 发送前切根：
// 并行发送时后完成的切换会换掉先发那路已准备使用的工具集（文件写到另一个
// 项目），切会话还会 Close 掉另一路正在跑的 shell（dev server 被误杀）。
// MCP/技能/扩展管理是进程共享的清单与连接，不随会话克隆（否则两套 MCP
// 进程抢端口、一个会话添加的技能另一个会话看不到）。
type sessionTools struct {
	root   string       // 归属根（空 = 纯对话：只保留共享工具）
	fs     *fstool.Tool // 以下四个仅在 root 非空时构造（fs 需要具体类型：确认 gate/CallID 注入）
	shell  tools.ToolPort
	git    tools.ToolPort
	search tools.ToolPort
}

// closeToolIfCloser 收掉工具实例的可关闭资源（shell 的后台任务表）。
// 非可关闭工具是 no-op。
func closeToolIfCloser(t tools.ToolPort) error {
	if t == nil {
		return nil
	}
	if closer, ok := t.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// ensureSessionTools 取/建会话级工具集。根一旦确定不再变化（归属由账本首个
// workspace 事件固定）；同会话复用同一份 shell 实例（后台任务表跨轮存活，
// bg_status/bg_kill 始终可达）。root 变化只可能来自防御路径（会话删除后再建），
// 此时收掉旧实例避免孤儿进程；收旧失败显式返回（不静默丢进程）。
func (s *ChatService) ensureSessionTools(sessionID, root string) (*sessionTools, error) {
	if st, ok := s.sessTools[sessionID]; ok && st.root == root {
		return st, nil
	}
	if old, ok := s.sessTools[sessionID]; ok {
		if err := closeToolIfCloser(old.shell); err != nil {
			return nil, fmt.Errorf("终止该会话旧后台任务失败：%w", err)
		}
	}
	st := &sessionTools{root: root}
	if root != "" {
		st.fs = fstool.New(root)
		// 文件写入确认（0.0.10）：write/replace 落盘前必须经用户应用/跳过
		st.fs.SetEditGate(&chatEditGate{svc: s, sessionID: sessionID})
		st.shell = shelltool.New(shelltool.Options{Root: root})
		st.git = gittool.New(root)
		st.search = searchtool.New(root)
	}
	s.sessTools[sessionID] = st
	return st, nil
}

// assembleRegistry 组装一轮用的工具注册表：共享交互/扩展工具 + 该会话的
// 受控工具。每轮组装只是把已缓存的工具实例注册一遍（结构很轻）。
func (s *ChatService) assembleRegistry(st *sessionTools) (*tools.Registry, error) {
	registry := tools.NewRegistry()
	// todo / ask_user：交互类工具。定义进模型工具集，执行由 Loop 按名拦截
	//（todo → agent.runTodo；ask_user → Loop.runAsk 阻塞等 UI 答复）
	if err := registry.Register(agent.NewTodoTool()); err != nil {
		return nil, fmt.Errorf("register tool: %w", err)
	}
	if err := registry.Register(agent.NewAskUserTool()); err != nil {
		return nil, fmt.Errorf("register tool: %w", err)
	}
	if st != nil {
		for _, t := range []tools.ToolPort{st.fs, st.shell, st.git, st.search} {
			if t == nil {
				continue
			}
			if err := registry.Register(t); err != nil {
				return nil, fmt.Errorf("register tool: %w", err)
			}
		}
	}
	if err := s.attachExtensions(registry); err != nil {
		return nil, err
	}
	return registry, nil
}

// Workspace 返回"新会话的默认工作区"（顶栏当前显示值）。
func (s *ChatService) Workspace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.WorkDir
}

// SetWorkspace 设置"新会话的默认工作区"（0.2.36 审计 R1 语义变更）。
// dir 为空串 = 纯对话（本地文件工具下线）。
// 为什么不再替换全局工具集：那会在并行发送时把先发那路已准备使用的工具集
// 换掉（写错项目），并关闭另一路正在跑的 shell（dev server 被误杀）。
// 已有会话的根由账本归属固定，不受这里影响。
func (s *ChatService) SetWorkspace(dir string) error {
	dir = normalizeWorkspace(dir)
	if dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("工作区不可用：%w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("工作区不是目录：%s", dir)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.WorkDir = dir
	if s.defaultModel == "" {
		return nil // 尚无激活渠道：配置好渠道后自然生效
	}
	return s.activate(s.defaultModel)
}
