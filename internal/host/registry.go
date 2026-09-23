// Package host 是"插件宿主（plugin host）"的注册中心契约，属于核心层（core）。
// 切勿与"应用宿主层"混淆：本包只描述插件的注册/分发契约，仅依赖 pkg/plugin/v1；
// app.go / main 包才是 Wails/IPC 的应用宿主（组合根），它依赖 host.Registry，
// 但 host 本身不反向依赖 app。即依赖方向永远是 app -> host -> pkg/plugin/v1。
package host

import (
	"fmt"
	"slices"
	"sync"
	v1 "tiancode/pkg/plugin/v1"
)

// Registry 工业级并发安全分段锁插件注册中心
type Registry struct {
	providerMu sync.RWMutex
	providers  map[string]v1.ProviderPlugin

	toolMu sync.RWMutex
	tools  map[string]v1.ToolPlugin

	railMu sync.RWMutex
	rails  []v1.RailPlugin
}

// NewRegistry 初始化注册中心实例
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]v1.ProviderPlugin),
		tools:     make(map[string]v1.ToolPlugin),
		rails:     make([]v1.RailPlugin, 0),
	}
}

// Register 统一注册插件
func (r *Registry) Register(p v1.Plugin) error {
	if p == nil {
		return fmt.Errorf("cannot register nil plugin")
	}

	switch p.Type() {
	case v1.TypeProvider:
		prov, ok := p.(v1.ProviderPlugin)
		if !ok {
			return fmt.Errorf("plugin %s declared as provider but does not implement ProviderPlugin", p.ID())
		}
		r.providerMu.Lock()
		defer r.providerMu.Unlock()
		if _, exists := r.providers[p.ID()]; exists {
			return fmt.Errorf("provider plugin already registered: %s", p.ID())
		}
		r.providers[p.ID()] = prov

	case v1.TypeTool:
		t, ok := p.(v1.ToolPlugin)
		if !ok {
			return fmt.Errorf("plugin %s declared as tool but does not implement ToolPlugin", p.ID())
		}
		r.toolMu.Lock()
		defer r.toolMu.Unlock()
		if _, exists := r.tools[p.ID()]; exists {
			return fmt.Errorf("tool plugin already registered: %s", p.ID())
		}
		r.tools[p.ID()] = t

	case v1.TypeRail:
		rl, ok := p.(v1.RailPlugin)
		if !ok {
			return fmt.Errorf("plugin %s declared as rail but does not implement RailPlugin", p.ID())
		}
		r.railMu.Lock()
		defer r.railMu.Unlock()
		for _, existing := range r.rails {
			if existing.ID() == p.ID() {
				return fmt.Errorf("rail plugin already registered: %s", p.ID())
			}
		}
		r.rails = append(r.rails, rl)
		// 按照 Priority 降序排序 (数值最高优先执行)
		slices.SortFunc(r.rails, func(a, b v1.RailPlugin) int {
			return b.Priority() - a.Priority()
		})

	default:
		return fmt.Errorf("unsupported plugin type: %s", p.Type())
	}

	return nil
}

// RegisterOrReplace 注册或替换已有插件 (用于工作区动态切换热重载)
func (r *Registry) RegisterOrReplace(p v1.Plugin) error {
	if p == nil {
		return fmt.Errorf("cannot register nil plugin")
	}

	switch p.Type() {
	case v1.TypeProvider:
		prov, ok := p.(v1.ProviderPlugin)
		if !ok {
			return fmt.Errorf("plugin %s declared as provider but does not implement ProviderPlugin", p.ID())
		}
		r.providerMu.Lock()
		r.providers[p.ID()] = prov
		r.providerMu.Unlock()

	case v1.TypeTool:
		t, ok := p.(v1.ToolPlugin)
		if !ok {
			return fmt.Errorf("plugin %s declared as tool but does not implement ToolPlugin", p.ID())
		}
		r.toolMu.Lock()
		r.tools[p.ID()] = t
		r.toolMu.Unlock()

	case v1.TypeRail:
		rl, ok := p.(v1.RailPlugin)
		if !ok {
			return fmt.Errorf("plugin %s declared as rail but does not implement RailPlugin", p.ID())
		}
		r.railMu.Lock()
		found := false
		for i, existing := range r.rails {
			if existing.ID() == p.ID() {
				r.rails[i] = rl
				found = true
				break
			}
		}
		if !found {
			r.rails = append(r.rails, rl)
		}
		slices.SortFunc(r.rails, func(a, b v1.RailPlugin) int {
			return b.Priority() - a.Priority()
		})
		r.railMu.Unlock()

	default:
		return fmt.Errorf("unsupported plugin type: %s", p.Type())
	}

	return nil
}

// Unregister 根据插件 ID 安全注销插件
func (r *Registry) Unregister(id string) bool {
	r.providerMu.Lock()
	if _, ok := r.providers[id]; ok {
		delete(r.providers, id)
		r.providerMu.Unlock()
		return true
	}
	r.providerMu.Unlock()

	r.toolMu.Lock()
	if _, ok := r.tools[id]; ok {
		delete(r.tools, id)
		r.toolMu.Unlock()
		return true
	}
	r.toolMu.Unlock()

	r.railMu.Lock()
	defer r.railMu.Unlock()
	for i, rl := range r.rails {
		if rl.ID() == id {
			r.rails = append(r.rails[:i], r.rails[i+1:]...)
			return true
		}
	}
	return false
}

// GetProvider 获取指定 ID 的模型驱动插件
func (r *Registry) GetProvider(id string) (v1.ProviderPlugin, bool) {
	r.providerMu.RLock()
	defer r.providerMu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}

// GetTool 获取指定 ID 的算子工具插件
func (r *Registry) GetTool(id string) (v1.ToolPlugin, bool) {
	r.toolMu.RLock()
	defer r.toolMu.RUnlock()
	t, ok := r.tools[id]
	return t, ok
}

// GetToolByName 根据算子 Definition.Name 查找算子插件 (支持大模型下发算子名调度)
func (r *Registry) GetToolByName(name string) (v1.ToolPlugin, bool) {
	r.toolMu.RLock()
	defer r.toolMu.RUnlock()

	// 1. 优先按 ID 匹配
	if t, ok := r.tools[name]; ok {
		return t, true
	}

	// 2. 遍历匹配 Definition().Name
	for _, t := range r.tools {
		if t.Definition().Name == name {
			return t, true
		}
	}
	return nil, false
}

// ListRails 获取已排好序的执行拦截器列表快照
func (r *Registry) ListRails() []v1.RailPlugin {
	r.railMu.RLock()
	defer r.railMu.RUnlock()
	snapshot := make([]v1.RailPlugin, len(r.rails))
	copy(snapshot, r.rails)
	return snapshot
}

// ListTools 获取当前注册的所有工具声明 (供大模型调用)
func (r *Registry) ListTools() []v1.ToolDefinition {
	r.toolMu.RLock()
	defer r.toolMu.RUnlock()
	defs := make([]v1.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

// GetProviders 返回所有已注册的 Provider 插件列表
func (r *Registry) GetProviders() []v1.ProviderPlugin {
	r.providerMu.RLock()
	defer r.providerMu.RUnlock()
	res := make([]v1.ProviderPlugin, 0, len(r.providers))
	for _, p := range r.providers {
		res = append(res, p)
	}
	return res
}

// GetTools 返回所有已注册的 Tool 插件列表
func (r *Registry) GetTools() []v1.ToolPlugin {
	r.toolMu.RLock()
	defer r.toolMu.RUnlock()
	res := make([]v1.ToolPlugin, 0, len(r.tools))
	for _, t := range r.tools {
		res = append(res, t)
	}
	return res
}
