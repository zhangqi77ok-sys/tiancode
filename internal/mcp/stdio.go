package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// StdioClient 基于标准输入输出进程通信的 MCP 客户端
type StdioClient struct {
	command   string
	args      []string
	workspace string
	env       map[string]string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	pending  sync.Map // map[int64]chan *JSONRPCMessage
	nextID   atomic.Int64
	stopChan chan struct{}
	mu       sync.Mutex
	started  bool

	lastStderr strings.Builder
}

// NewStdioClient 创建 Stdio 客户端实例
func NewStdioClient(command string, args []string, workspace string, env map[string]string) *StdioClient {
	return &StdioClient{
		command:   command,
		args:      args,
		workspace: workspace,
		env:       env,
		stopChan:  make(chan struct{}),
	}
}

// LastStderr 返回最近捕获的 stderr 输出
func (c *StdioClient) LastStderr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastStderr.String()
}


// Start 拉起外部进程并完成 MCP Initialize 协议握手
func (c *StdioClient) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}

	// 重新初始化 stopChan 保证客户端可安全重启
	select {
	case <-c.stopChan:
		c.stopChan = make(chan struct{})
	default:
	}

	cmd := exec.Command(c.command, c.args...)
	cmd.Dir = c.workspace

	if len(c.env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range c.env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("stdin pipe error: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("stdout pipe error: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("stderr pipe error: %w", err)
	}

	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000,
			HideWindow:    true,
		}
	}

	if err := cmd.Start(); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("process start error (%s): %w", c.command, err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr
	c.started = true
	c.mu.Unlock()

	// 启动后台扫描协程监听服务端按行输出
	go c.readLoop()

	// 启动后台排水协程消费 stderr，杜绝外部进程日志占满管道缓冲区引发死锁
	go func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				c.mu.Lock()
				if c.lastStderr.Len() < 8192 {
					c.lastStderr.Write(buf[:n])
				}
				c.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}(stderr)

	// 1. 发起 initialize 握手
	initParams := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
		ClientInfo: ClientInfo{
			Name:    "tcode-studio",
			Version: "0.0.1",
		},
	}

	initBytes, _ := json.Marshal(initParams)
	resp, err := c.sendRequest(ctx, "initialize", initBytes)
	if err != nil {
		_ = c.Stop()
		time.Sleep(50 * time.Millisecond) // 等待 stderr 排水
		return fmt.Errorf("mcp initialize failed: %w. stderr: %s", err, strings.TrimSpace(c.LastStderr()))
	}

	if resp == nil {
		_ = c.Stop()
		time.Sleep(50 * time.Millisecond)
		return fmt.Errorf("mcp initialize failed: received nil response from server. stderr: %s", strings.TrimSpace(c.LastStderr()))
	}

	if resp.Error != nil {
		_ = c.Stop()
		time.Sleep(50 * time.Millisecond)
		return fmt.Errorf("mcp initialize error from server: %s. stderr: %s", resp.Error.Message, strings.TrimSpace(c.LastStderr()))
	}

	// 2. 发送 notifications/initialized
	_ = c.sendNotification("notifications/initialized", nil)

	return nil
}

// Stop 优雅停止子进程
func (c *StdioClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.stopChan:
	default:
		close(c.stopChan)
	}

	if c.stdin != nil {
		_ = c.stdin.Close()
		c.stdin = nil
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
		c.stdout = nil
	}
	if c.stderr != nil {
		_ = c.stderr.Close()
		c.stderr = nil
	}

	// 唤醒并清空所有悬挂的等待请求
	c.pending.Range(func(key, value any) bool {
		c.pending.Delete(key)
		if ch, ok := value.(chan *JSONRPCMessage); ok {
			select {
			case ch <- nil:
			default:
			}
		}
		return true
	})

	if !c.started {
		return nil
	}

	done := make(chan error, 1)
	go func() {
		if c.cmd != nil {
			done <- c.cmd.Wait()
		} else {
			done <- nil
		}
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		if c.cmd != nil && c.cmd.Process != nil {
			if runtime.GOOS == "windows" {
				killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", c.cmd.Process.Pid))
				killCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
				_ = killCmd.Run()
			} else {
				_ = c.cmd.Process.Kill()
			}
		}
	}

	c.started = false
	return nil
}

// ListTools 请求服务端暴露的全部工具清单
func (c *StdioClient) ListTools(ctx context.Context) ([]Tool, error) {
	resp, err := c.sendRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("received nil response for tools/list")
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("server error: %s", resp.Error.Message)
	}

	var res ToolsListResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return nil, fmt.Errorf("parse tools/list result error: %w", err)
	}

	return res.Tools, nil
}

// CallTool 运行具体算子
func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	params := ToolCallParams{
		Name:      name,
		Arguments: args,
	}
	pBytes, _ := json.Marshal(params)

	resp, err := c.sendRequest(ctx, "tools/call", pBytes)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("received nil response for tools/call")
	}
	if resp.Error != nil {
		return "", fmt.Errorf("tool call error: %s", resp.Error.Message)
	}

	var res ToolCallResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return "", fmt.Errorf("parse tool/call result error: %w", err)
	}

	output := ""
	for _, item := range res.Content {
		if item.Text != "" {
			if output != "" {
				output += "\n"
			}
			output += item.Text
		}
	}

	return output, nil
}

// Ping 测试连通性与测量延迟
func (c *StdioClient) Ping(ctx context.Context) (time.Duration, int, error) {
	start := time.Now()
	tools, err := c.ListTools(ctx)
	duration := time.Since(start)
	if err != nil {
		return 0, 0, err
	}
	return duration, len(tools), nil
}

// sendRequest 发送带 ID 的同步请求并等待响应
func (c *StdioClient) sendRequest(ctx context.Context, method string, params json.RawMessage) (*JSONRPCMessage, error) {
	id := c.nextID.Add(1)

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	ch := make(chan *JSONRPCMessage, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)

	c.mu.Lock()
	if c.stdin == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("stdin is nil")
	}
	_, err = c.stdin.Write(append(data, '\n'))
	c.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("write error: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.stopChan:
		return nil, fmt.Errorf("client stopped")
	case res := <-ch:
		if res == nil {
			return nil, fmt.Errorf("request canceled or client stopped")
		}
		return res, nil
	}
}

// sendNotification 发送单向通知
func (c *StdioClient) sendNotification(method string, params json.RawMessage) error {
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(msg)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin != nil {
		_, _ = c.stdin.Write(append(data, '\n'))
	}
	return nil
}

// readLoop 持续按行读取外部进程输出并派发给对应 Request ID
func (c *StdioClient) readLoop() {
	if c.stdout == nil {
		return
	}
	defer func() {
		// 进程输出流关闭或异常退出时，立即唤醒所有挂起的等待请求，杜绝死锁挂死
		c.pending.Range(func(key, value any) bool {
			c.pending.Delete(key)
			if ch, ok := value.(chan *JSONRPCMessage); ok {
				select {
				case ch <- nil:
				default:
				}
			}
			return true
		})
	}()

	scanner := bufio.NewScanner(c.stdout)
	// 允许单行大报文 (最高 4MB)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg JSONRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		// 检查是否有匹配的 Request ID
		if msg.ID != nil {
			var reqID int64
			switch v := msg.ID.(type) {
			case float64:
				reqID = int64(v)
			case int64:
				reqID = v
			case int:
				reqID = int64(v)
			case json.Number:
				if n, err := v.Int64(); err == nil {
					reqID = n
				}
			case string:
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					reqID = n
				}
			}

			if chVal, ok := c.pending.Load(reqID); ok {
				if ch, ok := chVal.(chan *JSONRPCMessage); ok {
					select {
					case ch <- &msg:
					default:
					}
				}
			}
		}
	}
}
