package provider

import (
	"fmt"
	"sort"
	"sync"
)

// Registry 按平台名分发到对应的适配器。
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register 注册一个适配器。
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// For 取指定平台的适配器。
func (r *Registry) For(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("不支持的平台: %s（已注册: %v）", name, r.Names())
	}
	return p, nil
}

// Names 返回已注册的平台名。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
