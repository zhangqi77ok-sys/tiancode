package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"tiancode/internal/core/loop"
	"tiancode/internal/core/sandbox"
	"tiancode/internal/host"
	gitTool "tiancode/plugins/tool/git"
)

// Server 本地开发网关服务端
// ARCHITECTURE CONTRACT: Server 通过 registry.GetTool() 访问工具，禁止直接持有具体 Tool 字段
type Server struct {
	addr        string
	registry    *host.Registry
	httpSrv     *http.Server
	snapshotMgr *sandbox.SnapshotManager
	sandbox     *sandbox.Sandbox
	engine      *loop.ExecutionEngine
}

// gitTool 通过 registry 获取 git 工具实例（带类型断言）
func (s *Server) getGitTool() (*gitTool.Tool, bool) {
	if s == nil || s.registry == nil {
		return nil, false
	}
	tool, ok := s.registry.GetTool("tool.git")
	if !ok {
		return nil, false
	}
	gt, ok := tool.(*gitTool.Tool)
	return gt, ok
}

// NewServer 构造本地服务端
// 注意：不接受具体 Tool 实例参数，工具通过 registry.GetTool() 访问
func NewServer(addr string, reg *host.Registry, sm *sandbox.SnapshotManager, sb *sandbox.Sandbox) *Server {
	if addr == "" {
		addr = "127.0.0.1:8765"
	}
	s := &Server{
		addr:        addr,
		registry:    reg,
		snapshotMgr: sm,
		sandbox:     sb,
		engine:      loop.NewExecutionEngine(reg, nil, nil, nil),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/chat/stream", s.handleChatStream)
	mux.HandleFunc("/api/git/status", s.handleGitStatus)
	mux.HandleFunc("/api/git/stage", s.handleGitStage)
	mux.HandleFunc("/api/git/unstage", s.handleGitUnstage)
	mux.HandleFunc("/api/git/restore", s.handleGitRestore)
	mux.HandleFunc("/api/snapshots", s.handleListSnapshots)
	mux.HandleFunc("/api/snapshots/rollback", s.handleRollbackSnapshot)
	mux.HandleFunc("/api/fs/tree", s.handleFsTree)
	mux.HandleFunc("/api/fs/read", s.handleFsRead)
	mux.HandleFunc("/api/fs/original", s.handleFsOriginal)
	mux.HandleFunc("/api/fs/write", s.handleFsWrite)

	s.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      s.corsMiddleware(mux),
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 0, // SSE 流式长连接禁止全局写入超时
	}

	return s
}

// Start 启动监听
func (s *Server) Start() error {
	return s.httpSrv.ListenAndServe()
}

// Stop 优雅停机
func (s *Server) Stop(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleHealth 探活端点
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	res := map[string]any{
		"status":    "ok",
		"version":   "0.0.1-DEV",
		"timestamp": time.Now().Unix(),
	}
	_ = json.NewEncoder(w).Encode(res)
}

// handleChatStream SSE 流式对话端点
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model    string          `json:"model"`
		Prompt   string          `json:"prompt"`
		Messages json.RawMessage `json:"messages"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
		return
	}

	if req.Model == "" {
		req.Model = "gpt-4o"
	}

	// 配置 SSE 头部
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用 Nginx 缓冲

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	if s.engine == nil {
		http.Error(w, `{"error":"execution engine not configured"}`, http.StatusInternalServerError)
		return
	}

	// 委托给 ReAct 自主执行引擎驱动 Inner Loop 循环
	engineReq := &loop.EngineRequest{
		Model:  req.Model,
		Prompt: req.Prompt,
	}

	eventChan := make(chan loop.EngineEvent, 64)
	go func() {
		_ = s.engine.Execute(r.Context(), engineReq, eventChan)
	}()

	for event := range eventChan {
		switch event.Type {
		case loop.EventChunk:
			payload, _ := json.Marshal(map[string]string{
				"delta":    event.DeltaContent,
				"thinking": event.Thinking,
			})
			fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", string(payload))
			flusher.Flush()

		case loop.EventToolStart:
			payload, _ := json.Marshal(map[string]any{
				"id":   event.ToolCallID,
				"name": event.ToolName,
				"args": event.ToolArgs,
			})
			fmt.Fprintf(w, "event: tool_start\ndata: %s\n\n", string(payload))
			flusher.Flush()

		case loop.EventToolEnd:
			payload, _ := json.Marshal(map[string]any{
				"id":       event.ToolCallID,
				"name":     event.ToolName,
				"output":   event.ToolOutput,
				"is_error": event.IsError,
			})
			fmt.Fprintf(w, "event: tool_end\ndata: %s\n\n", string(payload))
			flusher.Flush()

		case loop.EventError:
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", event.ErrorMessage)
			flusher.Flush()

		case loop.EventDone:
			fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
			flusher.Flush()
		}
	}
}

// handleGitStatus 获取物理 Git 状态
func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	gt, ok := s.getGitTool()
	if !ok {
		http.Error(w, `{"error":"git tool not configured"}`, http.StatusInternalServerError)
		return
	}

	report, err := gt.GetStatus()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// handleGitStage 暂存文件
func (s *Server) handleGitStage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gt, ok := s.getGitTool()
	if !ok {
		http.Error(w, `{"error":"git tool not configured"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, `{"error":"path required"}`, http.StatusBadRequest)
		return
	}

	if err := gt.StageFile(req.Path); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// handleGitUnstage 取消暂存
func (s *Server) handleGitUnstage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gt, ok := s.getGitTool()
	if !ok {
		http.Error(w, `{"error":"git tool not configured"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, `{"error":"path required"}`, http.StatusBadRequest)
		return
	}

	if err := gt.UnstageFile(req.Path); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// handleGitRestore 放弃修改
func (s *Server) handleGitRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gt, ok := s.getGitTool()
	if !ok {
		http.Error(w, `{"error":"git tool not configured"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, `{"error":"path required"}`, http.StatusBadRequest)
		return
	}

	if err := gt.RestoreFile(req.Path); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// handleListSnapshots 获取历史快照
func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.snapshotMgr == nil {
		http.Error(w, `{"error":"snapshot manager not configured"}`, http.StatusInternalServerError)
		return
	}

	snapshots, err := s.snapshotMgr.ListSnapshots()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshots)
}

// handleRollbackSnapshot 回退快照
func (s *Server) handleRollbackSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CommitSHA string `json:"commit_sha"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CommitSHA == "" || req.Path == "" {
		http.Error(w, `{"error":"commit_sha and path required"}`, http.StatusBadRequest)
		return
	}

	if s.snapshotMgr == nil {
		http.Error(w, `{"error":"snapshot manager not initialized"}`, http.StatusInternalServerError)
		return
	}

	if s.sandbox != nil {
		if _, err := s.sandbox.ValidatePath(req.Path); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"security violation: %v"}`, err), http.StatusForbidden)
			return
		}
	}

	if err := s.snapshotMgr.RollbackFile(req.CommitSHA, req.Path); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// FileNode 目录树节点
type FileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"is_dir"`
	Children []*FileNode `json:"children,omitempty"`
}

// handleFsTree 遍历工作区目录树
func (s *Server) handleFsTree(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		http.Error(w, `{"error":"sandbox not initialized"}`, http.StatusInternalServerError)
		return
	}

	ignoreDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		"dist":         true,
		".gemini":      true,
		"target":       true,
		".vscode":      true,
		".idea":        true,
	}

	var buildTree func(relPath string) ([]*FileNode, error)
	buildTree = func(relPath string) ([]*FileNode, error) {
		entries, err := s.sandbox.ListDir(relPath)
		if err != nil {
			return nil, err
		}

		nodes := make([]*FileNode, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if ignoreDirs[name] || strings.HasPrefix(name, ".tcode_tmp_") {
				continue
			}

			childRel := filepath.ToSlash(filepath.Join(relPath, name))
			node := &FileNode{
				Name:  name,
				Path:  childRel,
				IsDir: e.IsDir(),
			}

			if e.IsDir() {
				children, _ := buildTree(childRel)
				node.Children = children
			}

			nodes = append(nodes, node)
		}
		return nodes, nil
	}

	tree, err := buildTree("")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tree)
}

// handleFsRead 安全读取文件内容
func (s *Server) handleFsRead(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		http.Error(w, `{"error":"sandbox not initialized"}`, http.StatusInternalServerError)
		return
	}

	targetPath := r.URL.Query().Get("path")
	if targetPath == "" {
		http.Error(w, `{"error":"path query required"}`, http.StatusBadRequest)
		return
	}

	data, err := s.sandbox.SafeReadFile(targetPath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"path":    targetPath,
		"content": string(data),
	})
}

// handleFsOriginal 获取文件的 Git 原始基准内容 (用于 Monaco Diff)
func (s *Server) handleFsOriginal(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		http.Error(w, `{"error":"sandbox not initialized"}`, http.StatusInternalServerError)
		return
	}

	targetPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if targetPath == "" {
		http.Error(w, `{"error":"path query required"}`, http.StatusBadRequest)
		return
	}

	if _, err := s.sandbox.ValidatePath(targetPath); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"security violation: %v"}`, err), http.StatusForbidden)
		return
	}

	// 转换为斜杠格式让 git show HEAD:path 正确识别
	slashPath := filepath.ToSlash(targetPath)
	cmd := exec.CommandContext(r.Context(), "git", "show", fmt.Sprintf("HEAD:%s", slashPath))
	cmd.Dir = s.sandbox.Root()
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
	} else {
		cmd.Cancel = func() error {
			if cmd.Process != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	originalContent := ""
	if err := cmd.Run(); err == nil {
		originalContent = stdout.String()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"path":     targetPath,
		"original": originalContent,
	})
}

// handleFsWrite 安全原子写文件
func (s *Server) handleFsWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.sandbox == nil {
		http.Error(w, `{"error":"sandbox not initialized"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, `{"error":"path and content required"}`, http.StatusBadRequest)
		return
	}

	if s.snapshotMgr != nil {
		_, _ = s.snapshotMgr.CreateSnapshot(fmt.Sprintf("user edit: %s", req.Path))
	}

	if err := s.sandbox.AtomicWriteFile(req.Path, []byte(req.Content)); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}
