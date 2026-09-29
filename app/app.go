// Package app 是 Wails 绑定适配层（壳）：把前端 IPC 调用转发给 internal/app 用例层。
// 只做参数转发、类型适配与事件桥接；禁止业务规则（docs/STANDARDS.md 分层表）。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"tiancode/internal/app"
	"tiancode/internal/core/llm"
)

// Bind 是暴露给前端（Wails Bind）的入口对象。
// 公开方法 = IPC 端点：一进一出、错误上抛；流式内容经事件桥推送（Observer，ADR-0005）。
//
// 铁律：绑定方法**不得**声明 context.Context 参数。
// 本版本 Wails 的 boundMethod.ParseArgs 要求 JS 实参个数严格等于 Go 声明参数个数，
// 且 Call 直接反射调用（不做任何 ctx 注入）。曾把 ctx 写成首参，
// 导致每次发送都报 "received 2 arguments to method 'app.Bind.Send', expected 3"（实机事故）。
// 应用上下文改由 OnStartup 写入 AppCtx 字段（字段不参与绑定校验）。
type Bind struct {
	chat    *app.ChatService
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	// AppCtx 是 Wails 应用上下文，由 main 的 OnStartup 注入（事件推送与取消传播都依赖它）。
	AppCtx context.Context
}

// New 装配壳层。
func New(chat *app.ChatService) *Bind {
	b := &Bind{chat: chat, cancels: make(map[string]context.CancelFunc)}
	// 审批事件桥（ADR-0007）：内核要"问"时推给前端，前端答复经 ResolveApproval 回流。
	// sessionID 随载荷下发（0.2.25 多会话）：后台会话的审批卡要归位到它自己的会话。
	chat.SetApprovalHandler(func(e app.ApprovalEvent) {
		wruntime.EventsEmit(b.appCtx(), "chat:approval", map[string]string{
			"id":           e.ID,
			"sessionID":    e.SessionID,
			"sessionTitle": e.SessionTitle, // 确认卡显示会话名（0.2.36 审计 R3）
			"toolName":     e.ToolName,
			"arguments":    e.Arguments,
		})
	})
	// 问答事件桥（0.2.15）：ask_user 的选项卡推给前端，答复经 ResolveAsk 回流
	chat.SetAskHandler(func(e app.AskEvent) {
		wruntime.EventsEmit(b.appCtx(), "chat:ask", map[string]any{
			"id":        e.ID,
			"sessionID": e.SessionID,
			"question":  e.Question,
			"options":   e.Options,
		})
	})
	// 文件变更确认桥（0.0.10）：write/replace 落盘前推确认卡，答复经 ResolveEdit 回流
	chat.SetEditHandler(func(e app.EditEvent) {
		wruntime.EventsEmit(b.appCtx(), "chat:edit", map[string]any{
			"id":           e.ID,
			"sessionID":    e.SessionID,
			"sessionTitle": e.SessionTitle,
			"callId":       e.CallID,
			"path":         e.Path,
			"diff":         e.Diff,
			"isNew":        e.IsNew,
		})
	})
	return b
}

// appCtx 返回应用上下文；未注入时退化为 Background——
// 宁可事件桥拿到一个无价值的 ctx，也不要 nil panic 打断已建立的数据流。
func (b *Bind) appCtx() context.Context {
	if b.AppCtx == nil {
		return context.Background()
	}
	return b.AppCtx
}

// ListSessions 返回全部会话 ID。
func (b *Bind) ListSessions() ([]string, error) {
	return b.chat.ListSessions()
}

// ExportSessionMarkdown 返回会话的 Markdown 文本（前端负责复制/保存）。
func (b *Bind) ExportSessionMarkdown(sessionID string) (string, error) {
	return b.chat.ExportSessionMarkdown(sessionID)
}

// GetWorkspace 返回当前工作区路径（工具受控根）。
func (b *Bind) GetWorkspace() string { return b.chat.Workspace() }

// SetWorkspace 切换工作区；非法路径（不存在/非目录/空白）返回错误供 UI 展示。
func (b *Bind) SetWorkspace(dir string) error { return b.chat.SetWorkspace(dir) }

// PinSession 置顶/取消置顶会话（侧栏置顶分区）。
func (b *Bind) PinSession(sessionID string, pinned bool) error {
	return b.chat.PinSession(sessionID, pinned)
}

// PickWorkspace 弹出系统目录选择框，返回选中的目录；用户取消返回空串。
// 为什么放后端：原生目录选择依赖 Wails 应用上下文（窗口句柄），前端无法自行唤起。
func (b *Bind) PickWorkspace() (string, error) {
	if b.AppCtx == nil {
		return "", errors.New("应用尚未就绪（缺少窗口上下文），无法打开目录选择框")
	}
	return wruntime.OpenDirectoryDialog(b.AppCtx, wruntime.OpenDialogOptions{
		// 文案与真实能力一致（0.2.35 审计#4）：文件/搜索工具受工作区约束，
		// 命令执行只是把工作目录设在此处，cmd 本身可访问整机（审批策略可加约束）。
		Title:                "选择工作区目录（文件与搜索工具限定在此目录内；命令执行默认可访问本机）",
		CanCreateDirectories: true,
	})
}

// ApprovalPolicy 返回当前需要执行前审批的工具清单（空 = 审批关闭，默认）。
func (b *Bind) ApprovalPolicy() []string { return b.chat.ApprovalPolicy() }

// RevealInExplorer 打开 path 所在目录的资源管理器并选中它（0.0.06：工具卡
// "在资源管理器中显示"）。path 相对当前工作区根解析（工具卡 Title 即工作区
// 相对路径）；绝对路径原样接受但必须存在。目录不存在/越界一律显式报错。
func (b *Bind) RevealInExplorer(path string) error {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return errors.New("路径为空")
	}
	if !filepath.IsAbs(clean) {
		root := b.chat.Workspace()
		if root == "" {
			return errors.New("当前没有工作区，无法定位文件位置")
		}
		clean = filepath.Join(root, clean)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("文件不存在：%s", clean)
	}
	if info.IsDir() {
		// 目录：直接打开目录本身
		return exec.Command("explorer", clean).Start()
	}
	// 文件：/select 打开所在目录并选中（explorer /select 需反斜杠路径）
	return exec.Command("explorer", "/select,", clean).Start()
}

// SetApprovalPolicy 设置需要审批的工具清单；传空数组即关闭审批（ADR-0007 默认关）。
func (b *Bind) SetApprovalPolicy(tools []string) error { return b.chat.SetApprovalPolicy(tools) }

// ResolveApproval 提交用户对某次审批请求的答复（允许/拒绝 + 原因）。
// 未知或已处理的 ID 返回错误——UI 会明确提示，绝不静默放行。
func (b *Bind) ResolveApproval(id string, approved bool, reason string) error {
	return b.chat.ResolveApproval(id, approved, reason)
}

// ResolveEdit 提交用户对某次文件变更的答复（0.0.10：应用/跳过）。
// 未确认前文件不会落盘；应用失败（外部改动）错误显式返回。
func (b *Bind) ResolveEdit(sessionID, editID string, apply bool) error {
	return b.chat.ResolveEdit(sessionID, editID, apply)
}

// ProposeFileWrite 把「应用到文件」的代码块内容变成待确认变更（0.0.10）。
// 同一条确认链路：先看 diff，确认才落盘，取消不写。
func (b *Bind) ProposeFileWrite(sessionID, path, content string) error {
	return b.chat.ProposeFileWrite(sessionID, path, content)
}

// SendWithAttachments 发送带附件的用户消息（0.0.10）。attachments 是 JSON 数组
// 字符串（图片/文件：name、kind、mediaType、dataB64、sourcePath）。
// 发送失败时错误上抛——前端保留待发送区允许重试。
func (b *Bind) SendWithAttachments(sessionID, text, attachments string) error {
	ctx := b.appCtx()
	runCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancels[sessionID] = cancel
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.cancels, sessionID)
		b.mu.Unlock()
	}()
	var atts []app.IncomingAttachment
	if strings.TrimSpace(attachments) != "" {
		if err := json.Unmarshal([]byte(attachments), &atts); err != nil {
			return fmt.Errorf("附件格式错误：%w", err)
		}
	}
	_, sendErr := b.chat.SendWithAttachments(runCtx, sessionID, text, atts)
	return sendErr
}

// ResolveAsk 提交用户对某次问答的答复（答案原样回流给模型继续推理）。
func (b *Bind) ResolveAsk(id string, answer string) error {
	return b.chat.ResolveAsk(id, answer)
}

// RestoreToolWrite 恢复一次 write/replace 写入前的内容（0.0.07：工具卡"恢复写入前"）。
// 恢复前比对写入后内容哈希——文件被人改过时拒绝并说明；成功返回可显示的说明文案。
func (b *Bind) RestoreToolWrite(sessionID, callID string) (string, error) {
	return b.chat.RestoreToolWrite(sessionID, callID)
}

// SearchWorkspaceFiles 为输入框的 @ 引用列出工作区文件（0.0.09）：把路径直接
// 递给模型，省掉"模型先花一步找文件"。只读遍历、有界（跳过依赖/构建目录、
// 命中上限 20），query 为空返回常用文件前 20 个。
// 0.0.10：根 = **这场对话自己的工作区**（账本归属）；
// 0.0.11：草稿（还没发过消息的会话）用顶栏当前工作区——用户在草稿里 @ 的
// 文件，发送后这场对话就归属该工作区，搜索与发送看到的是同一个根。
func (b *Bind) SearchWorkspaceFiles(sessionID, query string) ([]string, error) {
	root := b.chat.SessionWorkspace(sessionID)
	if strings.TrimSpace(root) == "" {
		root = b.chat.Workspace() // 草稿：顶栏根就是这场对话即将归属的根
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("当前没有工作区——先在顶栏选择工作区，或在设置里开启本地文件工具")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("工作区目录不可用：%s", root)
	}
	q := strings.ToLower(strings.TrimSpace(query))
	skipped := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, "dist": true,
		"build": true, "bin": true, "obj": true, ".idea": true, ".vscode": true,
	}
	var hits []string
	visited := 0
	maxVisited, maxHits := 5000, 20
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 单个不可读条目不阻断遍历（尽力而为）
		}
		if visited >= maxVisited {
			return filepath.SkipAll
		}
		visited++
		name := d.Name()
		if d.IsDir() {
			if skipped[name] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if q == "" || strings.Contains(strings.ToLower(relSlash), q) {
			hits = append(hits, relSlash)
			if len(hits) >= maxHits {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// SaveTextFile 弹系统保存对话框并写入文本（0.0.06：导出会话"另存为文件"）。
// 为什么放后端：原生保存对话框依赖 Wails 应用上下文；WebView 内的下载行为不可控。
// 用户取消返回空串（前端据此不提示成功）；写盘失败显式报错。
func (b *Bind) SaveTextFile(defaultName, content string) (string, error) {
	if b.AppCtx == nil {
		return "", errors.New("应用尚未就绪（缺少窗口上下文），无法打开保存对话框")
	}
	target, err := wruntime.SaveFileDialog(b.AppCtx, wruntime.SaveDialogOptions{
		DefaultFilename: defaultName,
		Title:           "保存 Markdown 文件",
	})
	if err != nil {
		return "", fmt.Errorf("保存对话框失败：%w", err)
	}
	if strings.TrimSpace(target) == "" {
		return "", nil // 用户取消
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("写入文件失败：%w", err)
	}
	return target, nil
}

// ListSessionSummaries 返回会话摘要（ID + 用户标题；标题来自账本事件）。
func (b *Bind) ListSessionSummaries() ([]app.SessionSummary, error) {
	return b.chat.SessionSummaries()
}

// RenameSession 重命名会话（标题写入账本，重启后仍可恢复）。
func (b *Bind) RenameSession(sessionID, title string) error {
	return b.chat.RenameSession(sessionID, title)
}

// DeleteSession 删除会话及其账本文件；被删除的会话不再出现在列表。
func (b *Bind) DeleteSession(sessionID string) error {
	return b.chat.DeleteSession(sessionID)
}

// Replay 返回会话的已确认消息（历史恢复）。
func (b *Bind) Replay(sessionID string) ([]app.ChatMessage, error) {
	return b.chat.Replay(sessionID)
}

// Send 发送一条消息；流式内容经事件桥推送：
//   - "chat:chunk"    {sessionID, delta, thinking}
//   - "chat:terminal" {sessionID, endReason, error}
//
// endReason 取值对应 core/llm：1=EndDone 2=EndError 3=EndCancelled 4=EndIdleTimeout。
// 返回值仅在"流建立失败"（前置错误，如配置缺失/连接失败重试耗尽）时非 nil；
// 流中终态一律经 chat:terminal 事件传递。
func (b *Bind) Send(sessionID, text string) error {
	ctx := b.appCtx() // 见 Bind 注释：ctx 不能作绑定方法参数
	runCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancels[sessionID] = cancel
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.cancels, sessionID)
		b.mu.Unlock()
	}()

	// 为什么兜底合成终态：极端时序下（取消恰逢发送受阻）上游通道可能无终态关闭，
	// 前端必须始终收到 chat:terminal 才能解锁输入框（C-APP-2 的 UI 侧保证）。
	terminalSeen := false
	emitTerminal := func(reason llm.EndReason, errText string) {
		terminalSeen = true
		wruntime.EventsEmit(ctx, "chat:terminal", map[string]interface{}{
			"sessionID": sessionID,
			"endReason": int(reason),
			"error":     errText,
		})
	}

	ch, err := b.chat.Send(runCtx, sessionID, text)
	if err != nil {
		// 中断发生在流建立之前（例如还在连 MCP）：必须发终态，不能只返回错误。
		// 只返回错误时前端会当成失败，输入框要等异常路径才解锁，观感是按钮没反应。
		if runCtx.Err() != nil {
			emitTerminal(llm.EndCancelled, "cancelled")
			return nil
		}
		return err
	}

	for c := range ch {
		if c.Delta != "" || c.Thinking != "" {
			wruntime.EventsEmit(ctx, "chat:chunk", map[string]string{
				"sessionID": sessionID,
				"delta":     c.Delta,
				"thinking":  c.Thinking,
			})
		}
		if c.Usage != nil {
			// 油表（0.0.09）：上游 token 用量透传——顶栏显示本轮 prompt token
			//（上下文大小的直接读数）。没有上下文长度配置时不编百分比。
			wruntime.EventsEmit(ctx, "chat:usage", map[string]any{
				"sessionID":  sessionID,
				"prompt":     c.Usage.PromptTokens,
				"completion": c.Usage.CompletionTokens,
				"total":      c.Usage.TotalTokens,
			})
		}
		if c.ToolEvent != nil {
			// 工具卡片数据（M3）：执行动态实时推送，前端渲染独立卡片
			wruntime.EventsEmit(ctx, "chat:tool", map[string]any{
				"sessionID": sessionID,
				"name":      c.ToolEvent.Name,
				"status":    c.ToolEvent.Status,
				"summary":   c.ToolEvent.Summary,
				"content":   c.ToolEvent.Content,
				"diff":      c.ToolEvent.Diff, // 编辑类工具的结构化 diff（无变更时为空串）
				// 语义标签（0.2.13 曾漏发，实时卡只能回退工具名；此处补齐与 Replay 对齐）
				"title": c.ToolEvent.Title,
				"op":    c.ToolEvent.Op,
				// CallID（0.0.06）：running 与终态配对——前端更新同一张卡
				"callID": c.ToolEvent.CallID,
				// 撤销元数据（0.0.07）：旧全文不进前端——恢复走 RestoreToolWrite
				"hasUndo":  c.ToolEvent.HasUndo,
				"undoPath": c.ToolEvent.UndoPath,
				"undoNote": c.ToolEvent.UndoNote,
			})
		}
		if c.Todo != nil {
			// 任务清单动态：前端单卡原地更新
			items := make([]map[string]string, len(c.Todo.Items))
			for i, it := range c.Todo.Items {
				items[i] = map[string]string{"text": it.Text, "status": it.Status}
			}
			wruntime.EventsEmit(ctx, "chat:todo", map[string]any{
				"sessionID": sessionID,
				"items":     items,
			})
		}
		if c.EndReason != llm.EndNone {
			errText := ""
			if c.Err != nil {
				errText = c.Err.Error()
			}
			emitTerminal(c.EndReason, errText)
		}
	}
	if !terminalSeen {
		emitTerminal(llm.EndCancelled, "cancelled")
	}
	return nil
}

// Stop 中断指定会话的进行中轮次（幂等：无进行中轮次时为空操作）。
func (b *Bind) Stop(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cancel, ok := b.cancels[sessionID]; ok {
		cancel()
	}
}
