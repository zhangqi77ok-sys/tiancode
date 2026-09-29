// Package mcpclient 是最小的 MCP 客户端（stdio 帧 + JSON-RPC）。
//
// 做什么：initialize、tools/list、tools/call。远程 URL 用一次 HTTP POST。
// 被谁依赖：internal/platform/exttools。
// 依赖谁：stdlib。
package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const protocolVersion = "2024-11-05"

// Tool 是上游公布的一个工具。
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolError 是 MCP 工具自身的业务错误（服务器正常响应 isError=true）。
// 与连接层错误区分：连接层失败要摘除会话重连（0.2.27：一次中断后坏连接
// 永久 "closed pipe"），业务错误不需要。
type ToolError struct{ Message string }

func (e *ToolError) Error() string { return e.Message }

// Client 在一条双向流上说 MCP。
type Client struct {
	rw  io.ReadWriteCloser
	r   *bufio.Reader
	mu  sync.Mutex
	seq int

	closeOnce sync.Once
	closeErr  error
	// stderr 是子进程 stderr 的有界尾部（诊断用：启动/握手失败时只看到 "EOF"
	// 无从排查——0.2.26 实机反馈）
	stderr *tailBuffer
}

// tailBuffer 保留最近 max 字节（并发安全）。
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// StderrTail 返回子进程 stderr 的最近输出（≤4KB）；无输出返回空串。
func (c *Client) StderrTail() string {
	if c.stderr == nil {
		return ""
	}
	return c.stderr.String()
}

// splitArgs 切分命令行参数：支持引号包裹（`"C:\Program Files\x"` 不再被空白切碎）。
// 不做反斜杠转义——Windows 路径里的 \ 必须原样保留。未闭合引号按"到结尾"处理
// （宽松：配置总要能被尝试）。
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	quoteChar := rune(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"' || r == '\'':
			if inQuote && r != quoteChar {
				cur.WriteRune(r)
				continue
			}
			inQuote = !inQuote
			if inQuote {
				quoteChar = r
			}
		case (r == ' ' || r == '\t' || r == '\n') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// New 包装已有的双向连接（测试注入）。
func New(rw io.ReadWriteCloser) *Client {
	return &Client{rw: rw, r: bufio.NewReader(rw)}
}

// DialStdio 启动本地进程并用 stdin/stdout 通信。
func DialStdio(command, args, env string) (*Client, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("mcp: 缺少启动命令")
	}
	if runtime.GOOS == "windows" && !strings.Contains(command, ".") {
		if _, err := exec.LookPath(command); err != nil {
			if _, err2 := exec.LookPath(command + ".cmd"); err2 == nil {
				command += ".cmd"
			}
		}
	}
	cmd := exec.Command(command, splitArgs(args)...)
	hideConsole(cmd)
	cmd.Env = os.Environ()
	for _, line := range strings.Split(env, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		cmd.Env = append(cmd.Env, line)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// stderr 收进有界尾部：启动/握手失败时把子进程的真实报错带给用户
	// （此前 io.Discard 全丢——用户只看到 "EOF"，无从排查）
	tail := &tailBuffer{max: 4096}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		// 启动失败：显式关闭已创建的管道（否则泄漏句柄）
		closeErrs := []error{}
		if e := stdin.Close(); e != nil {
			closeErrs = append(closeErrs, e)
		}
		if e := stdout.Close(); e != nil {
			closeErrs = append(closeErrs, e)
		}
		if len(closeErrs) > 0 {
			return nil, fmt.Errorf("mcp: 启动 %s 失败：%w（关闭管道：%v）", command, err, errors.Join(closeErrs...))
		}
		return nil, fmt.Errorf("mcp: 启动 %s 失败：%w", command, err)
	}
	pr, pw := io.Pipe()
	go func() {
		// stdout → pipe：结束语义由 CloseWithError 传递（nil 等价 Close），
		// 读端据此感知输出结束；读端已关闭时的返回值无需检查
		_, copyErr := io.Copy(pw, stdout)
		pw.CloseWithError(copyErr)
	}()
	// 回收进程资源（避免僵尸）；退出状态经 stdout EOF 传给读端，不参与控制流
	go func() { cmd.Wait() }()
	cl := New(stdioRW{Reader: pr, WriteCloser: stdin, cmd: cmd})
	cl.stderr = tail
	return cl, nil
}

type stdioRW struct {
	io.Reader
	io.WriteCloser
	cmd *exec.Cmd
}

func (s stdioRW) Close() error {
	err1 := s.WriteCloser.Close()
	var err2 error
	if s.cmd != nil && s.cmd.Process != nil {
		// 按进程树杀（taskkill /T）：MCP 服务器是 cmd → node 的批处理链，
		// 只杀直接子进程会留下无主的 node.exe（见 killProcessTree 注释）。
		// 树杀失败通常意味着进程已经退出；此时再按单进程兜底一次，
		// 只有真的杀不动（进程仍在且拒绝结束）才算错误——否则上层的
		// MCPTool.Close 会拒绝摘除这条会话，留下一个死客户端在缓存里。
		if treeErr := killProcessTree(s.cmd.Process.Pid); treeErr != nil {
			if killErr := s.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				err2 = killErr
			}
		}
	}
	if err1 != nil {
		return err1
	}
	return err2
}

// Initialize 完成握手。
func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tiancode", "version": "0.2.22"},
	})
	if err != nil {
		return err
	}
	return c.notify(ctx, "notifications/initialized", map[string]any{})
}

// ListTools 返回工具名。
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Tools, nil
}

// Call 调用一个工具，返回文本（多段 content 拼在一起）。
func (c *Client) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": json.RawMessage(args)})
	if err != nil {
		return "", err
	}
	var payload struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw), nil
	}
	var b strings.Builder
	for _, part := range payload.Content {
		if part.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(part.Text)
		}
	}
	text := b.String()
	if text == "" {
		text = string(raw)
	}
	if payload.IsError {
		return "", &ToolError{Message: text}
	}
	return text, nil
}

// Close 关闭连接（幂等）：取消路径与上层清理可能重复调用，
// 重复 kill 的报错会被误判成"关闭失败"（0.2.27）。
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.rw == nil {
			return
		}
		c.closeErr = c.rw.Close()
	})
	return c.closeErr
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := c.seq
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}
	if err := c.write(body); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		msg, err := c.readCtx(ctx)
		if err != nil {
			return nil, err
		}
		var env struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(msg, &env); err != nil || env.ID == nil || *env.ID != id {
			continue
		}
		if env.Error != nil {
			return nil, fmt.Errorf("mcp: %s", env.Error.Message)
		}
		return env.Result, nil
	}
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 写入可能因对端不消费 stdin 而阻塞（如 npx 首次下载中）——goroutine + ctx 逃生，
	// 绝不无限挂住调用方（0.2.26：notify 整体忽略 ctx，一轮对话可被永久卡死）。
	// ctx 到期后 Close 连接：阻塞中的写会立即失败返回，不会与后续帧交错。
	done := make(chan error, 1)
	go func() { done <- c.write(body) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if closeErr := c.Close(); closeErr != nil {
			return fmt.Errorf("%w（关闭：%v）", ctx.Err(), closeErr)
		}
		return ctx.Err()
	}
}

func (c *Client) write(body []byte) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	_, err := c.rw.Write(buf.Bytes())
	return err
}

// readCtx 读一帧；ctx 结束时关闭连接，让卡在管道上的读立刻返回。
// 为什么不能只看截止时间：read 在进程没吐字节时会一直堵住，截止时间要等这一帧读完才检查，
// 用户点中断时这一轮就停不下来。
func (c *Client) readCtx(ctx context.Context) (json.RawMessage, error) {
	type result struct {
		msg json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := c.read()
		ch <- result{msg, err}
	}()
	select {
	case <-ctx.Done():
		closeErr := c.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("%w（关闭：%v）", ctx.Err(), closeErr)
		}
		return nil, ctx.Err()
	case r := <-ch:
		return r.msg, r.err
	}
}

func (c *Client) read() (json.RawMessage, error) {
	var length int
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n, err := strconv.Atoi(strings.TrimSpace(line[len("content-length:"):]))
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length <= 0 || length > 8<<20 {
		return nil, fmt.Errorf("mcp: 非法 Content-Length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// CallHTTP 对远程 MCP 发一次 tools/call（JSON 响应；若是 SSE 则取第一条 data）。
func CallHTTP(ctx context.Context, endpoint, headers, tool string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": json.RawMessage(args)},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for _, line := range strings.Split(headers, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("mcp http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	payload := raw
	if bytes.Contains(raw, []byte("data:")) {
		for _, line := range bytes.Split(raw, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if bytes.HasPrefix(line, []byte("data:")) {
				payload = bytes.TrimSpace(line[len("data:"):])
				break
			}
		}
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return string(payload), nil
	}
	if env.Error != nil {
		return "", fmt.Errorf("mcp: %s", env.Error.Message)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		return string(env.Result), nil
	}
	var b strings.Builder
	for _, part := range result.Content {
		if part.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(part.Text)
		}
	}
	if b.Len() == 0 {
		return string(env.Result), nil
	}
	return b.String(), nil
}
