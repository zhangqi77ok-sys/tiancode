// 工作区用例：会话级受控工具集（fs/shell/git/search 各持一份，根固定）。
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
	"tiancode/internal/platform/fstool"
	"tiancode/internal/platform/gittool"
	"tiancode/internal/platform/memory"
	"tiancode/internal/platform/searchtool"
	"tiancode/internal/platform/shelltool"
	"tiancode/internal/platform/workspace"
)

// WorkspaceIgnoredDir 报告 name 是否工作区默认忽略目录（定稿清单单一来源在
// internal/platform/workspace）：search 工具与壳层 @ 引用（SearchWorkspaceFiles）
// 共用同一份，壳层不直接 import platform（依赖规则），由编排层转出。
func WorkspaceIgnoredDir(name string) bool {
	return workspace.IgnoredDir(name)
}

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
	info, statErr := os.Stat(abs)
	if statErr != nil {
		return "", false, fmt.Errorf("文件不存在：%s", abs)
	}
	// 符号链接解析后再做越界判定（0.0.19 底层债修复）：root 已规范化（含符号链接
	// 解析），abs 不解析的话，指向根外的链接（root\link -> D:\elsewhere）会被
	// 字面前缀比较误判为"在根内"。解析失败保留原路径（文件系统不支持时退回
	// 字面判定——Stat 已确认存在，宁可保守也不能放行）。
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = filepath.Clean(resolved)
	}
	if !withinDir(root, abs) {
		return "", false, fmt.Errorf("路径不在该对话的工作区内：%s", clean)
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

// firstWorkspaceOfLedger 返回账本里记录的会话归属路径（own=true 表示已有归属，
// 含显式空串 = 纯对话）。归属规则（0.0.19 迁移语义）：首个 workspace 事件是
// 初始归属（与侧栏分组、TestSessionSummaries_CarryWorkspace 一致）；**最后一次**
// workspace_move 事件若存在则覆盖——迁移是用户事后的显式决定，展示（Meta）与
// 工具根（这里）必须同一规则，否则侧栏分组和真实写入目录会分叉。
// 尽力而为语义：读取失败按"无归属"处理（不阻断发送）。
func firstWorkspaceOfLedger(l *session.Ledger) (string, bool) {
	root := ""
	own := false
	moved := false
	if err := l.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventWorkspace:
			if own || moved {
				return nil
			}
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err == nil {
				root = p.Path
				own = true
			}
		case session.EventWorkspaceMove:
			// 最后一次 move 说了算（继续扫，后面的 move 再覆盖）
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err == nil {
				root = p.Path
				own = true
				moved = true
			}
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
	root string // 归属根（空 = 纯对话：只保留共享工具）
	// ctx/cancel 是会话级生命周期（第二轮体检 R2）：浏览器等长动作派生自它，
	// 会话删除/工具集收旧时先 cancel——在途动作立即中断，不再占着 tab 锁卡 UI。
	ctx     context.Context
	cancel  context.CancelFunc
	fs      *fstool.Tool // 以下四个仅在 root 非空时构造（fs 需要具体类型：「应用到文件」走 ProposeWrite）
	shell   tools.ToolPort
	git     tools.ToolPort
	search  tools.ToolPort
	browser tools.ToolPort // 会话级浏览器 tab（不依赖工作区根，纯对话也构造）；收成端口便于契约测试注入假 tab
}

// closeToolIfCloser 收掉工具实例的可关闭资源（shell 的后台任务表）。
// 非可关闭工具是 no-op。typed-nil 守卫：接口变量装着 nil 指针（半构造的
// sessionTools）时调用方法会在 nil 接收者上 panic——按"无可收资源"处理。
func closeToolIfCloser(t tools.ToolPort) error {
	if t == nil {
		return nil
	}
	if v := reflect.ValueOf(t); v.Kind() == reflect.Ptr && v.IsNil() {
		return nil
	}
	if closer, ok := t.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// sessionLedgerOnDisk 报告会话账本文件是否还在盘上（会话存活的权威判据）：
// DeleteSession 收尾即删文件；s.ledgers 句柄表只覆盖"开过句柄"的会话，
// 重启后首聊的会话只剩盘上文件。路径拼法与 session.OpenLedger 一致。
// 不校验 sessionID 合法性：怪 ID 只会 stat 落空返回 false，由调用方显式报错。
func (s *ChatService) sessionLedgerOnDisk(sessionID string) bool {
	_, err := os.Stat(filepath.Join(s.cfg.DataDir, sessionID+".jsonl"))
	return err == nil
}

// ensureSessionTools 取/建会话级工具集。根一旦确定不再变化（归属由账本首个
// workspace 事件固定）；同会话复用同一份 shell 实例（后台任务表跨轮存活，
// bg_status/bg_kill 始终可达）。root 变化只可能来自防御路径（会话删除后再建），
// 此时收掉旧实例避免孤儿进程；收旧失败显式返回（不静默丢进程）。
//
// 锁纪律（第二轮体检 R2）：sessTools 读写走专用锁 sessMu——Send 持 s.mu 期间
// 会进到这里，复用 s.mu 会自锁（互斥锁不可重入）。本函数内绝不取 s.mu /
// ledgerFor / sessionWorkspace：持 sessMu 再拿 s.mu 会与 DeleteSession
// （s.mu → sessMu）反序死锁，root 一律由调用方先算好传入。
// 所有调用方都必须已开过账本（文件在盘上）：文件没了 = 会话已删除，
// 显式报错绝不重建——否则点旧会话的链接会"复活"无主工具集挂到应用退出。
func (s *ChatService) ensureSessionTools(sessionID, root string) (*sessionTools, error) {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if st, ok := s.sessTools[sessionID]; ok && st.root == root {
		return st, nil
	}
	if !s.sessionLedgerOnDisk(sessionID) {
		return nil, fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	if old, ok := s.sessTools[sessionID]; ok {
		if old.cancel != nil {
			old.cancel() // 收旧先断在途动作（浏览器导航等），再关工具实例
		}
		if err := closeToolIfCloser(old.shell); err != nil {
			return nil, fmt.Errorf("终止该会话旧后台任务失败：%w", err)
		}
		_ = closeToolIfCloser(old.browser) // 旧 tab 一并关掉（关 tab 无失败路径，恒 nil）
	}
	st := &sessionTools{root: root}
	st.ctx, st.cancel = context.WithCancel(context.Background())
	// browser 不依赖工作区根：纯对话也构造——看本地页面/读报错是对话刚需，
	// 不该被"没选工作区"挡住；浏览器进程由 ChatService 持有的 Pool 共享。
	// sessionID 作截图子目录（按会话隔离，驾驶舱互不串图）。
	st.browser = s.browser.NewTab(sessionID)
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
	// webfetch：共享工具（与 skill/mcp 同类，不依赖工作区根）——读网页正文，
	// 查文档/查报错方案是硬伤能力，纯对话也必须在场。
	if err := registry.Register(s.webFetch); err != nil {
		return nil, fmt.Errorf("register tool: %w", err)
	}
	// memory：共享长期记忆工具（0.0.19）。root 只决定 workspace scope 的存储键
	//（纯对话时该 scope 显式报错，global 照常可用）；写的是应用数据目录下的
	// 记忆文件，不碰工作区，故不进审批闸门清单。
	if s.memory != nil {
		root := ""
		if st != nil {
			root = st.root
		}
		if err := registry.Register(memory.NewTool(s.memory, root)); err != nil {
			return nil, fmt.Errorf("register tool: %w", err)
		}
	}
	if st != nil {
		for _, t := range []tools.ToolPort{st.fs, st.shell, st.git, st.search, st.browser} {
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
