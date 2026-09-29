// 工作区用例：切换工具受控根。
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

// workspaceOfLedger 返回账本里最后一次记录的工作区（0.2.35 审计#1 的读侧）。
// 尽力而为语义：无记录或解析失败都返回空，保持现状——归属缺失不阻断发送。
func workspaceOfLedger(l *session.Ledger) string {
	out := ""
	_ = l.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventWorkspace {
			return nil
		}
		var p struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err == nil {
			out = p.Path
		}
		return nil
	})
	return out
}

// newRegistry 按工作区构造工具集（fs/shell/git/search 的受控根）。
// 抽成函数是为了让"启动装配"与"运行期切换工作区"共用同一段装配逻辑，
// 避免两处漂移（切换后工具集与启动时不一致是隐蔽 bug）。
//
// workDir 为空 = 纯对话模式：只注册交互类工具，本地文件工具全部下线——
// 空根会退化成进程 cwd（等于把安装目录暴露给模型），这是安全红线，
// 宁可不给工具也不越界。
func newRegistry(workDir string) (*tools.Registry, error) {
	registry := tools.NewRegistry()
	regs := []func() error{
		// todo / ask_user：交互类工具。定义进模型工具集，执行由 Loop 按名拦截
		//（todo → agent.runTodo；ask_user → Loop.runAsk 阻塞等 UI 答复）
		func() error { return registry.Register(agent.NewTodoTool()) },
		func() error { return registry.Register(agent.NewAskUserTool()) },
	}
	if strings.TrimSpace(workDir) != "" {
		regs = append(regs,
			func() error { return registry.Register(fstool.New(workDir)) },
			func() error { return registry.Register(shelltool.New(shelltool.Options{Root: workDir})) },
			func() error { return registry.Register(gittool.New(workDir)) },
			func() error { return registry.Register(searchtool.New(workDir)) },
		)
	}
	for _, reg := range regs {
		if err := reg(); err != nil {
			return nil, fmt.Errorf("register tool: %w", err)
		}
	}
	return registry, nil
}

// Workspace 返回当前工作区。
func (s *ChatService) Workspace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.WorkDir
}

// SetWorkspace 切换工作区：校验目录 → 重建工具集 → 按当前激活渠道重建 agent。
// dir 为空串 = 退出工作区（纯对话模式）：本地文件工具下线，用于"不选择工作区新建会话"。
// 为什么必须重建 agent：工具持有工作区根（受控范围），只改 cfg 不改工具集，
// 会出现"界面显示新目录、读写仍打到旧目录"的最坏情况。
func (s *ChatService) SetWorkspace(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		// 规范化（0.2.35 审计#5）：尾部反斜杠会让 fs/search 的前缀比较全部
		// 误判越界（root "D:\proj\" vs Join 结果 "D:\proj\a.go"）；`..` 与符号
		// 链接一并解析成真实路径。盘符根（D:\）的尾分隔符合法，保留。
		if abs, err := filepath.Abs(dir); err == nil {
			dir = filepath.Clean(abs)
		} else {
			dir = filepath.Clean(dir)
		}
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = filepath.Clean(real)
		}
		if len(dir) > 3 && (strings.HasSuffix(dir, `\`) || strings.HasSuffix(dir, `/`)) {
			dir = strings.TrimRight(dir, `\/`)
		}
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
	registry, err := newRegistry(dir)
	if err != nil {
		return err
	}
	if err := s.attachExtensions(registry); err != nil {
		return err
	}
	// 旧 shell 工具的后台任务随实例一起丢弃（0.2.35 审计#2）：显式收掉进程树，
	// 否则任务表丢失，bg_status/bg_kill 再也够不着，进程成为孤儿。
	if s.registry != nil {
		if sh, ok := s.registry.Get("shell"); ok {
			if closer, ok := sh.(interface{ Close() error }); ok {
				_ = closer.Close() // 终止失败无从上报（多半已退出）；cancel 幂等
			}
		}
	}
	s.registry = registry
	s.cfg.WorkDir = dir
	if s.defaultModel == "" {
		return nil // 尚无激活渠道：配置好渠道后自然使用新工作区
	}
	return s.activate(s.defaultModel)
}
