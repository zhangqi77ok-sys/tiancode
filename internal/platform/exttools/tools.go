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
	hub   sync.Map   // name -> *mcpclient.Client（写入只走原子 LoadOrStore，0.0.05）
	hubMu sync.Mutex // 仅保护复合序列（dropClient 的读-比-删、Close 的 Range+Delete）；单次写入不拿它——原子原语自身互斥，锁内 Load→Store 两步反而制造竞窗
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
	// 摘除 + 关掉失效实例（关闭失败并入错误消息，0.2.36 审计 R7）
	if closeErr := t.dropClient(spec.Name, c); closeErr != nil {
		return "", fmt.Errorf("%s（连接已失效，已重置：下次调用将重新拉起；%v）", detail, closeErr)
	}
	return "", fmt.Errorf("%s（连接已失效，已重置：下次调用将重新拉起）", detail)
}

func (t *MCPTool) stdioClient(ctx context.Context, spec catalog.Server) (*mcpclient.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 读取复用走原子 Load（快路径）；拨号在锁外（0.2.36 审计 R7）：卡住的服务器
	// 只拖住本次调用，不堵全进程的 MCP 调用。并发重复拉起由 LoadOrStore 裁决
	// （抢占失败者立刻关闭，见下）——不能把锁一路持到 ListTools 返回，
	// 否则设置页的一次探测会卡住所有会话。
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

// dropClient 摘除并关闭指定客户端（0.2.36 审计 R7 语义）。
// hub 中若仍是这个实例才删除（只持锁一瞬间，绝不包住连接过程）；
// **无论是否在 hub 都要关掉自己**——被替换的实例不在 hub 里，不关就是
// 无主进程。关闭失败必须返回（设置页/mcp_add 结果要能看到）。
//
// 并发安全依据（0.0.05）：写入方（stdioClient/storeIfAbsent）走无锁的原子
// LoadOrStore，本函数的删除有 hubMu 包裹——两侧单操作各自原子，且删除有
// **指针比较守卫**：另一路并发放入新实例时，这里 Load 到的要么是旧值（与
// c 同指针则删，语义正确）、要么是新值（不同指针则不删）。唯一需要 hubMu
// 真正互斥的是 Close() 的 Range+Delete 复合序列。
func (t *MCPTool) dropClient(name string, c *mcpclient.Client) error {
	t.hubMu.Lock()
	v, ok := t.hub.Load(name)
	if cur, isClient := v.(*mcpclient.Client); ok && isClient && cur == c {
		t.hub.Delete(name)
	}
	t.hubMu.Unlock()
	if err := c.Close(); err != nil {
		return fmt.Errorf("关闭 MCP 客户端失败：%w", err)
	}
	return nil
}

// storeIfAbsent 条件写入（0.2.37 审计引入，0.0.05 修竞窗）：hub 里已有实例时**不覆盖**
// ——Probe 与正在进行的 mcp 调用交错时，无条件 Store 会把别人刚换上的新客户端盖掉且不关
// 旧进程。实现与 stdioClient **同源**：只做一次原子 LoadOrStore——0.0.04 版曾是
// "hubMu 锁内 Load → Store" 两步，而 stdioClient 的 LoadOrStore 不拿 hubMu：两步之间
// 另一路可先放入客户端 A，随后这次 Store 用 B 把 A 盖掉（A 不在表里也没人 Close）。
// hubMu 此路径**不参与**：原子原语自身就是互斥，拿锁只会制造"已经互斥"的错觉。
// 返回最终留在 hub 里的客户端（自己或赢家）与是否是自己的实例。
func (t *MCPTool) storeIfAbsent(name string, c *mcpclient.Client) (*mcpclient.Client, bool, error) {
	actual, loaded := t.hub.LoadOrStore(name, c)
	if !loaded {
		return c, true, nil
	}
	winner, ok := actual.(*mcpclient.Client)
	if !ok {
		// 理论不可达（hub 只存 *mcpclient.Client）；防御：不留无主进程
		if err := c.Close(); err != nil {
			return nil, false, fmt.Errorf("并发已有新实例，关闭落败实例失败：%w", err)
		}
		return nil, false, nil
	}
	if err := c.Close(); err != nil {
		return winner, false, fmt.Errorf("并发已有新实例，关闭落败实例失败：%w", err)
	}
	return winner, false, nil
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
		// 连接失败：摘掉刚拉起的实例并关掉它（hub 里若已被并发替换则只关自己，
		// 不误伤新实例）；关闭失败要返回给设置页 / mcp_add 的结果（0.2.36 审计 R7）
		if closeErr := t.dropClient(spec.Name, c); closeErr != nil {
			return nil, fmt.Errorf("%v（%v）", err, closeErr)
		}
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
				// 摘除必须走 dropClient（0.2.37 审计）：hub 里若已被并发调用换上
				// 新实例，只关自己这个坏实例，绝不误删新实例
				if closeErr := t.dropClient(s.Name, existing); closeErr != nil {
					errs[s.Name] = closeErr.Error()
				}
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
		// 写入必须走 storeIfAbsent（0.2.37 审计）：探测期间并发调用可能已为该
		// 服务器放入新客户端——落败方关闭自己刚拉起的进程，不覆盖赢家
		if _, _, err := t.storeIfAbsent(s.Name, c); err != nil {
			errs[s.Name] = err.Error()
		}
	}
	return names, errs
}

// Close 关掉已拉起的 MCP 进程。无论关闭成败都从 hub 摘除——留着一个关闭失败的
// 客户端只会让后续调用持续报错（坏连接必须能被替换，0.2.26 实机）。
// 返回首个关闭错误（由调用方决定可见方式，不静默）。
func (t *MCPTool) Close() error {
	// 锁内只做 map 操作（收集 + 摘除）；关闭在锁外执行（0.2.36 审计 R7：
	// 单个服务器慢关不该堵住其他清理路径）
	type entry struct {
		name string
		c    *mcpclient.Client
	}
	var clients []entry
	t.hubMu.Lock()
	t.hub.Range(func(k, v any) bool {
		if c, ok := v.(*mcpclient.Client); ok {
			if name, ok := k.(string); ok {
				clients = append(clients, entry{name: name, c: c})
			}
		}
		t.hub.Delete(k)
		return true
	})
	t.hubMu.Unlock()

	var firstErr error
	for _, e := range clients {
		if err := e.c.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("关闭 MCP %v 失败：%w", e.name, err)
		}
	}
	return firstErr
}
