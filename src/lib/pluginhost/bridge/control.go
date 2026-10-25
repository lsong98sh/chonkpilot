package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

func handlePing(_ context.Context, _ *Session, _ json.RawMessage) (any, error) {
	return map[string]any{"pong": true, "ts": time.Now().UnixMilli()}, nil
}

func handleHello(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	var in struct {
		ID       string         `json:"id"`
		Name     string         `json:"name"`
		Version  string         `json:"version"`
		Features map[string]any `json:"features"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if sanitizePluginID(in.ID) == "" {
		return nil, errf(CodeArgsInvalid, "plugin.hello 需要非空 id")
	}
	s.mu.Lock()
	if s.pluginID != "" && s.pluginID != in.ID {
		s.mu.Unlock()
		return nil, errf(CodePluginUnauthorized, "plugin id 不可变更（已注册 %s）", s.pluginID)
	}
	s.pluginID = in.ID
	if s.instanceID == "" {
		s.instanceID = newPluginInstanceID(in.ID)
	}
	if in.Features != nil {
		s.features = in.Features
	}
	instanceID := s.instanceID
	s.mu.Unlock()

	// 晚连接插件：宿主已就绪 → 立即补发 server-ready，保证索引可启动。
	if ready, payload := s.srv.Ready(); ready {
		s.Emit("server-ready", payload)
	}

	return map[string]any{
		"plugin_id":   in.ID,
		"instance_id": instanceID,
		"version":     s.srv.opts.Version,
		"methods":     s.srv.methods(),
	}, nil
}

func (s *Server) methods() []string {
	out := make([]string, 0, len(controlNames)+8)
	out = append(out, controlNames...)
	out = append(out, s.reg.Methods()...)
	sort.Strings(out)
	return out
}

func handleInfo(_ context.Context, s *Session, _ json.RawMessage) (any, error) {
	return map[string]any{
		"version": s.srv.opts.Version,
		"form":    s.srv.opts.Form,
		"methods": s.srv.methods(),
	}, nil
}

func handleRegister(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		Features      map[string]any `json:"features"`
		Subscriptions []Subscription `json:"subscriptions"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	subs, err := normalizeSubscriptions(in.Subscriptions)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if in.Features != nil {
		s.features = in.Features
	}
	s.subs = subs
	s.mu.Unlock()
	return map[string]any{"ok": true, "plugin_id": s.PluginID(), "instance_id": s.InstanceID(), "subscriptions": len(subs)}, nil
}

func handleReady(_ context.Context, s *Session, _ json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	return map[string]any{"ok": true}, nil
}

func handleExit(_ context.Context, s *Session, _ json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	return map[string]any{"ok": true}, nil
}

func handleSubscribe(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		Subscriptions []Subscription `json:"subscriptions"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	subs, err := normalizeSubscriptions(in.Subscriptions)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.subs = append(s.subs, subs...)
	n := len(s.subs)
	s.mu.Unlock()
	return map[string]any{"ok": true, "count": n}, nil
}

func handleUnsubscribe(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	drop := make(map[string]struct{}, len(in.IDs))
	for _, id := range in.IDs {
		drop[id] = struct{}{}
	}
	s.mu.Lock()
	kept := s.subs[:0]
	for _, sub := range s.subs {
		if _, ok := drop[sub.ID]; ok {
			continue
		}
		kept = append(kept, sub)
	}
	s.subs = kept
	n := len(s.subs)
	s.mu.Unlock()
	return map[string]any{"ok": true, "count": n}, nil
}

func handleFilterRegister(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in FilterRegistration
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.Stage == "" {
		return nil, errf(CodeArgsInvalid, "filter.register 需要 stage")
	}
	s.mu.Lock()
	if in.ID == "" {
		in.ID = fmt.Sprintf("filter-%d", len(s.filters)+1)
	}
	s.filters = append(s.filters, in)
	n := len(s.filters)
	s.mu.Unlock()
	return map[string]any{"ok": true, "stage": in.Stage, "count": n}, nil
}

func handleInstanceClaim(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		InstanceID string `json:"instance_id"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	own := s.InstanceID()
	if in.InstanceID != "" && in.InstanceID != own {
		return nil, errf(CodeInstanceForbidden, "插件只能使用服务端分配的 instance_id")
	}
	return map[string]any{"instance_id": own, "client_type": "plugin"}, nil
}

func handleInstanceList(_ context.Context, s *Session, _ json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	ids := make([]string, 0, 8)
	seen := make(map[string]struct{})
	s.srv.existingMu.RLock()
	existing := s.srv.existing
	s.srv.existingMu.RUnlock()
	if existing != nil {
		for _, id := range existing() {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if own := s.InstanceID(); own != "" {
		if _, ok := seen[own]; !ok {
			ids = append(ids, own)
		}
	}
	sort.Strings(ids)
	return map[string]any{"instances": ids}, nil
}

// normalizeSubscriptions 校验并补全订阅 id。
func normalizeSubscriptions(in []Subscription) ([]Subscription, error) {
	out := make([]Subscription, 0, len(in))
	for i, sub := range in {
		if sub.Pattern == "" {
			return nil, errf(CodeArgsInvalid, "subscription.pattern 不能为空")
		}
		if sub.Mode != "" && sub.Mode != "async" {
			return nil, errf(CodeSubscriptionDenied, "v1 订阅只有 async（收到 %s）", sub.Mode)
		}
		if sub.ID == "" {
			sub.ID = fmt.Sprintf("sub-%d", i+1)
		}
		out = append(out, sub)
	}
	return out, nil
}
