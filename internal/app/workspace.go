// 工作区用例：会话级受控工具集（fs/shell/git/search 各持一份，根固定）。
package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// ResolveSessionPath 把会话内的路径解析成绝对路径并校验（0.0.11）：
//   - 相对路径按**这场对话**的工作区根解析（sessionWorkspace），绝不用顶栏里
//     "下一场新对话"的默认根——切换工作区后，旧会话的"在资源管理器显示 /
//     用默认程序打开"曾指到别的目录（同名文件还会被判成"存在"）；
//   - 绝对路径同样必须落在这场对话的工作区内（越界一律拒绝）；
//   - 无工作区 / 路径为空 / 越界 / 不存在都是显式错误，绝不猜、不静默。
//
// isDir 供调用方选择"直接打开目录"或"选中文件"。
func (s *ChatService) ResolveSessionPath(sessionID, path string) (abs string, isDir bool, err error) {
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return "", false, fmt.Errorf("这场对话没有工作区，无法定位文件")
	}
	clean := strings.TrimSpace(path)
	if clean == "" {
		return "", false, fmt.Errorf("路径为空")
	}
	abs = clean
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, clean)
	}
	abs = filepath.Clean(abs)
	if !withinDir(root, abs) {
		return "", false, fmt.Errorf("路径不在该对话的工作区内：%s", clean)
	}
	info, statErr := os.Stat(abs)
	if statErr != nil {
		return "", false, fmt.Errorf("文件不存在：%s", abs)
	}
	return abs, info.IsDir(), nil
}

// FileBody 是「只读浏览」返回的正文（第 8 批：文件详情面板看文件当前内容）。
type FileBody struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"` // 超过单次读取上限：只给了前半
	Limit     int    `json:"limit"`     // 上限字节数（界面据此说明截断，不另编数字）
}

// ReadSessionFile 读取这场对话工作区内某个文件的正文（第 8 批）：文件详情面板的
// 只读浏览。路径走 ResolveSessionPath（会话自己的工作区，越界/缺失显式拒绝）。
//
// 上限复用 fs 的单次读取上限（fstool.MaxReadBytes）：超了只给前半并标 Truncated，
// 界面照实说明——绝不悄悄截断还装作读全了。二进制（含 NUL）与目录按显式错误返回，
// 不用空白正文冒充已读。
//
// 只读语义：不写盘、不落账本、不参与"整读过"记账（那是 write 门卫的凭据，
// 面板浏览不该替模型作证）。
func (s *ChatService) ReadSessionFile(sessionID, path string) (FileBody, error) {
	abs, isDir, err := s.ResolveSessionPath(sessionID, path)
	if err != nil {
		return FileBody{}, err
	}
	shown := strings.TrimSpace(path)
	if isDir {
		return FileBody{}, fmt.Errorf("这是一个目录，不是文件：%s", shown)
	}
	f, err := os.Open(abs)
	if err != nil {
		return FileBody{}, fmt.Errorf("读取失败：%w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return FileBody{}, fmt.Errorf("读取失败：%w", err)
	}
	head := make([]byte, 8*1024)
	n, _ := io.ReadFull(f, head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return FileBody{}, fmt.Errorf("二进制文件，不显示正文：%s", shown)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return FileBody{}, fmt.Errorf("读取失败：%w", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, fstool.MaxReadBytes))
	if err != nil {
		return FileBody{}, fmt.Errorf("读取失败：%w", err)
	}
	return FileBody{
		Path:      shown,
		Content:   string(data),
		Truncated: info.Size() > int64(fstool.MaxReadBytes),
		Limit:     fstool.MaxReadBytes,
	}, nil
}

// withinDir 报告 abs 是否位于 root 之内（"." 即 root 本身算在内）。
// filepath.Rel 在 Windows 上按盘符大小写不敏感比较（同一目录的两种写法不成两个空间）。
func withinDir(root, abs string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
	fs     *fstool.Tool // 以下四个仅在 root 非空时构造（fs 需要具体类型：「应用到文件」走 ProposeWrite）
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
