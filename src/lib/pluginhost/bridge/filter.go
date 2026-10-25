package bridge

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// 默认超时：单 filter 200ms、整链 1s（设计方案 §11.13）。
const (
	DefaultFilterTimeout = 200 * time.Millisecond
	DefaultChainTimeout  = time.Second
)

// ErrDropped 表示某个 filter 明确要求丢弃本次调用（fail-closed）。
var ErrDropped = &Error{Code: CodeFilterDropped, Msg: "filter dropped"}

// FilterChain 是 server 内部 before-hook 链（唯一允许的阻塞例外）。
type FilterChain struct {
	srv           *Server
	FilterTimeout time.Duration
	ChainTimeout  time.Duration
}

// Filters 返回插件桥的 filter 链。
func (s *Server) Filters() *FilterChain {
	return &FilterChain{
		srv:           s,
		FilterTimeout: DefaultFilterTimeout,
		ChainTimeout:  DefaultChainTimeout,
	}
}

type filterEntry struct {
	sess *Session
	reg  FilterRegistration
}

// Run 执行某个 stage 的全部 filter；返回最终 payload。
// 任一 filter 失败 / 超时 / drop → 返回错误（调用方不得继续副作用）。
func (f *FilterChain) Run(ctx context.Context, stage string, payload json.RawMessage) (json.RawMessage, error) {
	entries := f.entries(stage)
	if len(entries) == 0 {
		return payload, nil
	}
	chainCtx, cancel := context.WithTimeout(ctx, f.ChainTimeout)
	defer cancel()
	cur := payload
	for _, e := range entries {
		out, err := f.runOne(chainCtx, e, stage, cur)
		if err != nil {
			return nil, err
		}
		cur = out
	}
	return cur, nil
}

func (f *FilterChain) entries(stage string) []filterEntry {
	f.srv.mu.Lock()
	sessions := make([]*Session, 0, len(f.srv.sessions))
	for s := range f.srv.sessions {
		sessions = append(sessions, s)
	}
	f.srv.mu.Unlock()

	var out []filterEntry
	for _, s := range sessions {
		if s.PluginID() == "" {
			continue
		}
		s.mu.RLock()
		for _, reg := range s.filters {
			if reg.Stage == stage {
				out = append(out, filterEntry{sess: s, reg: reg})
			}
		}
		s.mu.RUnlock()
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].reg.Priority != out[j].reg.Priority {
			return out[i].reg.Priority < out[j].reg.Priority
		}
		return out[i].sess.PluginID() < out[j].sess.PluginID()
	})
	return out
}

func (f *FilterChain) runOne(ctx context.Context, e filterEntry, stage string, payload json.RawMessage) (json.RawMessage, error) {
	timeout := f.FilterTimeout
	if timeout <= 0 {
		timeout = DefaultFilterTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := map[string]any{
		"stage":     stage,
		"filter_id": e.reg.ID,
		"payload":   payload,
	}
	res, err := e.sess.conn.Call(callCtx, "plugin.filter.run", req)
	if err != nil {
		if callCtx.Err() != nil {
			return nil, &Error{Code: CodeFilterTimeout, Msg: "filter timeout: " + e.reg.ID}
		}
		return nil, &Error{Code: CodeFilterFailed, Msg: err.Error()}
	}
	if !res.OK {
		msg := "filter failed"
		if len(res.Errors) > 0 {
			msg = res.Errors[0]
		}
		return nil, &Error{Code: CodeFilterFailed, Msg: msg}
	}
	raw, _ := json.Marshal(res.Value)
	var out struct {
		Action  string          `json:"action"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &Error{Code: CodeFilterFailed, Msg: "invalid filter result"}
	}
	switch out.Action {
	case "pass", "":
		return payload, nil
	case "replace":
		if len(out.Payload) == 0 {
			return nil, &Error{Code: CodeFilterFailed, Msg: "replace without payload"}
		}
		return out.Payload, nil
	case "drop":
		return nil, ErrDropped
	default:
		return nil, &Error{Code: CodeFilterFailed, Msg: "unknown action: " + out.Action}
	}
}
