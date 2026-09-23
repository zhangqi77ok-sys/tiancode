// 工作区用例：切换工具受控根。
package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/fstool"
	"tiancode/internal/platform/gittool"
	"tiancode/internal/platform/shelltool"
)

// newRegistry 按工作区构造工具集（fs/shell/git 的受控根）。
// 抽成函数是为了让"启动装配"与"运行期切换工作区"共用同一段装配逻辑，
// 避免两处漂移（切换后工具集与启动时不一致是隐蔽 bug）。
func newRegistry(workDir string) (*tools.Registry, error) {
	registry := tools.NewRegistry()
	for _, reg := range []func() error{
		func() error { return registry.Register(fstool.New(workDir)) },
		func() error { return registry.Register(shelltool.New(shelltool.Options{Root: workDir})) },
		func() error { return registry.Register(gittool.New(workDir)) },
	} {
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
// 为什么必须重建 agent：工具持有工作区根（受控范围），只改 cfg 不改工具集，
// 会出现"界面显示新目录、读写仍打到旧目录"的最坏情况。
func (s *ChatService) SetWorkspace(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("工作区不能为空")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("工作区不可用：%w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("工作区不是目录：%s", dir)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	registry, err := newRegistry(dir)
	if err != nil {
		return err
	}
	s.registry = registry
	s.cfg.WorkDir = dir
	if s.active.ID == "" {
		return nil // 尚无激活渠道：配置好渠道后自然使用新工作区
	}
	return s.activate(s.active)
}
