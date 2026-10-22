package pluginbridge

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// Handler 是一个能力 handler；对内自行翻译到 MQ / 内部调用。
type Handler func(ctx context.Context, s *Session, args json.RawMessage) (any, error)

// Registry 是 method → Handler 注册表（唯一的能力叠加点）。
type Registry struct {
	mu sync.RWMutex
	m  map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[string]Handler)}
}

// Register 注册一个方法；重复注册覆盖（用于测试 / 热替换）。
func (r *Registry) Register(name string, h Handler) {
	r.mu.Lock()
	r.m[name] = h
	r.mu.Unlock()
}

// Lookup 查找方法。
func (r *Registry) Lookup(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.m[name]
	return h, ok
}

// Methods 返回已注册方法名（排序）。
func (r *Registry) Methods() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
