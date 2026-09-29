// Package exttools 把已启用的 Skill 与 MCP 暴露成模型可调用的工具。
//
// 做什么：skill 按名称返回技能正文；mcp 按服务器名和工具名转发调用。
// 被谁依赖：internal/app（装配进工具注册表，并在每轮对话前生成前言）。
// 依赖谁：core/tools、platform/catalog、platform/mcpclient。
package exttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/catalog"
	"tiancode/internal/platform/mcpclient"
)

// Preface 告诉模型有哪些可选技能和 MCP，以及要用时怎么调用。
// 不连接任何服务器：用不用由模型按当前步骤决定，发消息时不启动进程。
func Preface(f catalog.File) string {
	var skills []catalog.Skill
	for _, s := range f.Skills {
		if s.Enabled && strings.TrimSpace(s.Name) != "" {
			skills = append(skills, s)
		}
	}
	var servers []catalog.Server
	for _, s := range f.MCP {
		if s.Enabled && strings.TrimSpace(s.Name) != "" {
			servers = append(servers, s)
		}
	}
	if len(skills) == 0 && len(servers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("技能和 MCP 是可选能力，不是每轮都必须调用。按 ReAct 先判断当前步骤要不要用；不需要就直接回答，或只用文件、搜索、命令这些原有工具。不要因为它们出现在下面就去调用。\n")
	b.WriteString("\n要用的时候才这样调用：\n")
	b.WriteString("- 技能：调用 skill，参数 name 为技能名。返回的是做法说明，读完再决定怎么做。\n")
	b.WriteString("- MCP：调用 mcp，参数 server 为服务器名，tool 为该服务器上的工具名，arguments 为参数对象。还不知道工具名时，把 tool 设为 list，只取清单，不要接着执行。服务器在你真正调用时才启动。\n")
	b.WriteString("- 用户让你添加/删除 MCP 服务器或技能时：调用 ext_manage（mcp_add/mcp_remove/skill_add/skill_remove，先 mcp_list/skill_list 确认名称），加完把验证结果转告用户。用户没要求时不要擅自增删。\n")
	if len(skills) > 0 {
		b.WriteString("\n技能（名称：何时考虑用它）：\n")
		for _, s := range skills {
			fmt.Fprintf(&b, "- %s：%s\n", s.Name, strings.TrimSpace(s.Description))
		}
	}
	if len(servers) > 0 {
		b.WriteString("\nMCP 服务器（名称：它能做什么）：\n")
		for _, s := range servers {
			fmt.Fprintf(&b, "- %s：%s\n", s.Name, serverHint(s))
		}
	}
	return strings.TrimSpace(b.String())
}

func serverHint(s catalog.Server) string {
	if s.Transport == "http" || (strings.TrimSpace(s.Command) == "" && strings.TrimSpace(s.URL) != "") {
		return "远程 " + strings.TrimSpace(s.URL)
	}
	line := strings.TrimSpace(strings.TrimSpace(s.Command) + " " + strings.TrimSpace(s.Args))
	if line == "" {
		return "本地命令"
	}
	if len(line) > 160 {
		line = line[:160] + "…"
	}
	return line
}

// SkillTool 按名称返回技能正文。
type SkillTool struct {
	Load func() catalog.File
}

// NewSkill 构造技能工具。
func NewSkill(load func() catalog.File) *SkillTool { return &SkillTool{Load: load} }

func (t *SkillTool) Name() string { return "skill" }
func (t *SkillTool) Description() string {
	return "可选。只有当前步骤需要某条技能的做法时才调用。参数 name 是系统说明里的技能名，返回该技能全文。"
}
func (t *SkillTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
}
func (t *SkillTool) Execute(_ context.Context, args json.RawMessage) (tools.ToolResult, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tools.ToolResult{Content: "参数不是合法 JSON", IsError: true, Op: "skill"}, nil
	}
	name := strings.TrimSpace(p.Name)
	if t.Load == nil {
		return tools.ToolResult{Content: "没有技能清单", IsError: true}, nil
	}
	for _, s := range t.Load().Skills {
		if s.Enabled && s.Name == name {
			body := strings.TrimSpace(s.Body)
			if body == "" {
				body = s.Description
			}
			return tools.ToolResult{Content: body, Title: s.Name, Op: "skill"}, nil
		}
	}
	return tools.ToolResult{Content: "没有名为 " + name + " 的已启用技能", IsError: true, Title: name, Op: "skill"}, nil
}

// MCPTool 把调用转到已启用的 MCP 服务器。
type MCPTool struct {
	Load  func() catalog.File
	hub   sync.Map   // name -> *mcpclient.Client
	hubMu sync.Mutex // 保护 hub 的读-改-删序列（0.2.35 审计#9：LoadAndDelete+Store 有盖掉新实例的窗口）
}

// NewMCP 构造 MCP 工具。
func NewMCP(load func() catalog.File) *MCPTool { return &MCPTool{Load: load} }

func (t *MCPTool) Name() string { return "mcp" }
func (t *MCPTool) Description() string {
	return "可选。只有当前步骤需要某台 MCP 服务器时才调用。参数 server 是服务器名，tool 是工具名，arguments 是参数对象。不知道工具名时 tool 填 list，只返回工具清单。"
}
func (t *MCPTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"server":{"type":"string"},"tool":{"type":"string"},"arguments":{"type":"object"}},"required":["server","tool"]}`)
}

func (t *MCPTool) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	var p struct {
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tools.ToolResult{Content: "参数不是合法 JSON", IsError: true, Op: "mcp"}, nil
	}
	serverName := strings.TrimSpace(p.Server)
	toolName := strings.TrimSpace(p.Tool)
	if serverName == "" || toolName == "" {
		return tools.ToolResult{Content: "mcp 需要 server 和 tool；还不知道工具名时 tool 填 list", IsError: true, Op: "mcp"}, nil
	}
	var spec catalog.Server
	found := false
	if t.Load != nil {
		for _, s := range t.Load().MCP {
			if s.Enabled && s.Name == serverName {
				spec, found = s, true
				break
			}
		}
	}
	if !found {
		return tools.ToolResult{Content: "没有名为 " + serverName + " 的已启用 MCP", IsError: true, Title: serverName, Op: "mcp"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var (
		out string
		err error
	)
	if strings.EqualFold(toolName, "list") {
		out, err = t.listTools(ctx, spec)
	} else if spec.Transport == "http" || (spec.Command == "" && spec.URL != "") {
		out, err = mcpclient.CallHTTP(ctx, spec.URL, spec.Headers, toolName, p.Arguments)
	} else {
		out, err = t.callStdio(ctx, spec, toolName, p.Arguments)
	}
	if err != nil {
		return tools.ToolResult{Content: err.Error(), IsError: true, Title: serverName + "/" + toolName, Op: "mcp"}, nil
	}
	return tools.ToolResult{Content: out, Title: serverName + "/" + toolName, Op: "mcp"}, nil
}

func (t *MCPTool) listTools(ctx context.Context, spec catalog.Server) (string, error) {
	if spec.Transport == "http" || (strings.TrimSpace(spec.Command) == "" && strings.TrimSpace(spec.URL) != "") {
		return "远程服务器 " + strings.TrimSpace(spec.URL) + "。请直接指定 tool。", nil
	}
	c, err := t.stdioClient(ctx, spec)
	if err != nil {
		return "", err
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		return "", err
	}
	if len(tools) == 0 {
		return "该服务器没有公布工具", nil
	}
	var b strings.Builder
	for _, tool := range tools {
		fmt.Fprintf(&b, "- %s：%s\n", tool.Name, strings.TrimSpace(tool.Description))
	}
	return strings.TrimSpace(b.String()), nil
}

func (t *MCPTool) callStdio(ctx context.Context, spec catalog.Server, tool string, args json.RawMessage) (string, error) {
	c, err := t.stdioClient(ctx, spec)
	if err != nil {
		return "", err
	}
	out, err := c.Call(ctx, tool, args)
	if err == nil {
		return out, nil
	}
	var terr *mcpclient.ToolError
	if errors.As(err, &terr) {
		return "", err // 工具业务错误（isError）：连接仍健康，不摘除
	}
	// 连接层失败：摘除并关闭——一次用户中断会经 readCtx 关闭连接，此后该客户端
	// 永久 "closed pipe"（0.2.26 实机：不摘除只能重启应用恢复）
	detail := err.Error()
	if tail := c.StderrTail(); tail != "" {
		detail += "（服务器 stderr：" + tail + "）"
	}
	if t.dropClient(spec.Name, c) {
		return "", fmt.Errorf("%s（连接已失效，已重置：下次调用将重新拉起）", detail)
	}
	return "", errors.New(detail)
}

func (t *MCPTool) stdioClient(ctx context.Context, spec catalog.Server) (*mcpclient.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 拉起与登记整体在 hubMu 内：与 dropClient 的读-比-删互斥（0.2.35 审计#9）。
	// 拉起（npx 首次下载）可能耗时数秒——并发调用同一 server 会排队，可接受。
	t.hubMu.Lock()
	defer t.hubMu.Unlock()
	if v, ok := t.hub.Load(spec.Name); ok {
		if c, ok := v.(*mcpclient.Client); ok {
			return c, nil
		}
	}
	c, err := mcpclient.DialStdio(spec.Command, spec.Args, spec.Env)
	if err != nil {
		return nil, err
	}
	if err := c.Initialize(ctx); err != nil {
		if closeErr := c.Close(); closeErr != nil {
			return nil, fmt.Errorf("%v（关闭：%v）", err, closeErr)
		}
		return nil, err
	}
	// LoadOrStore：多会话并行下两个会话同时首次调用同一 server 时，只允许一份实例
	// 进 hub——抢占失败的实例立刻关闭，否则留下无主进程树（0.2.26 实机：
	// 任务管理器里一串无主 node.exe）
	if actual, loaded := t.hub.LoadOrStore(spec.Name, c); loaded {
		if winner, ok := actual.(*mcpclient.Client); ok {
			if closeErr := c.Close(); closeErr != nil {
				return nil, fmt.Errorf("并发连接已有实例，关闭重复实例失败：%w", closeErr)
			}
			return winner, nil
		}
	}
	return c, nil
}

// dropClient 摘除并关闭指定客户端（仅当 hub 中的当前实例就是它）。
// 返回 true 表示已摘除（下次调用会重新拉起）。
// 读-比-删整体在 hubMu 内（0.2.35 审计#9）：此前 LoadAndDelete + Store 的窗口里，
// 并发拉起的新实例会被旧值盖掉，留下无主进程。
func (t *MCPTool) dropClient(name string, c *mcpclient.Client) bool {
	t.hubMu.Lock()
	defer t.hubMu.Unlock()
	v, ok := t.hub.Load(name)
	if !ok {
		return false
	}
	cur, ok := v.(*mcpclient.Client)
	if !ok || cur != c {
		return false // 已被替换：不误伤新实例
	}
	t.hub.Delete(name)
	if err := c.Close(); err != nil {
		// 已从 hub 摘除；关闭失败无处上报（调用方的错误消息链已表明连接失效）
		return true
	}
	return true
}

// ProbeServer 连接单台 stdio 服务器并返回其公布的工具名（ext_manage 添加后的验证用）。
// 连接失败时把刚拉起的会话从 hub 摘掉：留着一个连不上的客户端只会让下次调用更难排查。
func (t *MCPTool) ProbeServer(ctx context.Context, spec catalog.Server) ([]string, error) {
	if spec.Transport == "http" || (spec.URL != "" && spec.Command == "") {
		return nil, fmt.Errorf("远程服务器不支持自动列工具（%s），请直接指定 tool 调用", strings.TrimSpace(spec.URL))
	}
	c, err := t.stdioClient(ctx, spec)
	if err != nil {
		return nil, err
	}
	list, err := c.ListTools(ctx)
	if err != nil {
		// 连接失败：从 hub 摘掉刚拉起的实例（读-比-删在 hubMu 内，0.2.35 审计#9——
		// 此前的无条件 Delete 可能删掉并发换上的新实例）
		_ = t.dropClient(spec.Name, c)
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Name)
	}
	return names, nil
}

// Probe 连接已启用的 stdio MCP，收集工具名。失败记在 errs 里，不中断其它服务器。
func (t *MCPTool) Probe(ctx context.Context) (map[string][]string, map[string]string) {
	names := map[string][]string{}
	errs := map[string]string{}
	if t.Load == nil {
		return names, errs
	}
	for _, s := range t.Load().MCP {
		if ctx.Err() != nil {
			return names, errs
		}
		if !s.Enabled || s.Name == "" || s.Transport == "http" || (s.URL != "" && s.Command == "") {
			continue
		}
		if v, ok := t.hub.Load(s.Name); ok {
			if existing, ok := v.(*mcpclient.Client); ok {
				pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
				tools, err := existing.ListTools(pctx)
				cancel()
				if err == nil {
					for _, tool := range tools {
						names[s.Name] = append(names[s.Name], tool.Name)
					}
					continue
				}
				if closeErr := existing.Close(); closeErr != nil {
					errs[s.Name] = closeErr.Error()
				}
				t.hub.Delete(s.Name)
			}
		}
		c, err := mcpclient.DialStdio(s.Command, s.Args, s.Env)
		if err != nil {
			errs[s.Name] = err.Error()
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err = c.Initialize(pctx)
		var tools []mcpclient.Tool
		if err == nil {
			tools, err = c.ListTools(pctx)
		}
		cancel()
		if err != nil {
			if closeErr := c.Close(); closeErr != nil {
				errs[s.Name] = err.Error() + "（关闭：" + closeErr.Error() + "）"
			} else {
				errs[s.Name] = err.Error()
			}
			continue
		}
		for _, tool := range tools {
			names[s.Name] = append(names[s.Name], tool.Name)
		}
		t.hub.Store(s.Name, c)
	}
	return names, errs
}

// Close 关掉已拉起的 MCP 进程。无论关闭成败都从 hub 摘除——留着一个关闭失败的
// 客户端只会让后续调用持续报错（坏连接必须能被替换，0.2.26 实机）。
// 返回首个关闭错误（由调用方决定可见方式，不静默）。
func (t *MCPTool) Close() error {
	t.hubMu.Lock()
	defer t.hubMu.Unlock()
	var firstErr error
	t.hub.Range(func(k, v any) bool {
		if c, ok := v.(*mcpclient.Client); ok {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("关闭 MCP %v 失败：%w", k, err)
			}
		}
		t.hub.Delete(k)
		return true
	})
	return firstErr
}
