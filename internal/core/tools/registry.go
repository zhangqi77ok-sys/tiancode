// Registry 工具注册表（Registry 模式，ADR-0005）。
// 做什么：按名称唯一注册工具、输出给模型的定义列表。
// 被谁依赖：internal/app（装配）、core/agent（执行时查找）。
// 依赖谁：仅 stdlib + core/llm（定义形态）。
package tools

import (
	"fmt"
	"sort"
	"sync"

	"tiancode/internal/core/llm"
)

// Registry 是工具注册表：名称即模型可见的唯一标识，重名注册视为装配错误。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]ToolPort
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolPort)}
}

// Register 注册工具；重名返回错误（装配期 fail-fast，不给运行时留歧义）。
func (r *Registry) Register(t ToolPort) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

// Get 按名称查找工具。
func (r *Registry) Get(name string) (ToolPort, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Definitions 输出全部工具定义，按名称排序（保证发给模型的工具列表稳定，
// 避免顺序抖动破坏 prompt cache 前缀）。
func (r *Registry) Definitions() []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}
