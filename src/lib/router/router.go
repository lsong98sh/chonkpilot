// Provider 注册表 + 协议适配器分派 + 「一次」调用入口（LR-2 骨架；协议实现见 LR-3 / LR-4）。
package router

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/adaptor/anthropic"
	"github.com/chonkpilot/chonkpilot-router/internal/adaptor/echo"
	"github.com/chonkpilot/chonkpilot-router/internal/adaptor/openai"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// builtinFallback 是**内置兜底 provider**（D-30）：协议 echo（不发 HTTP、回显最后一条真实用户消息）。
//
// 启用时机 = **没有任何可用 LLM provider 时** —— `Call` 的 `Request.Provider` 为空（llm 侧
// 「未配置可用 provider」的信号）或等于本保留名（兼容既有 usr `defaultLLM: "echo"` 记录）时回落
// 到它；**未注册的 provider 名仍报 `invalid`**（LR-2 契约不变）。它**不进入 `specs`**，故
// `Reconcile` 永不注销它（兜底恒定可用），也不出现在任何「可配置 provider 列表」中。
var builtinFallback = Spec{
	Name:         "echo",
	Protocol:     canon.ProtocolEcho,
	DefaultModel: "echo",
}

// Config 是 Router 构造参数。
type Config struct {
	// HTTPClient 出网客户端（nil = 适配器自建默认客户端；连接池由调用方决定寿命）。
	HTTPClient *http.Client
}

// Router 是多 Provider 统一入口。**无状态**：只持 provider 注册表与协议适配器表，
// 不含会话 / 轮次 / 历史（见包注释边界）。
//
// 适配器表的元素类型 = `internal/adaptor.Adapter`（协议实现契约，见该包）；本包按
// `Spec.Protocol` 归一后分派。**本包不反向 import 任何东西**：adaptor 层只依赖
// `internal/canon`，故不存在 `router → internal/adaptor → router` 的 import 环。
type Router struct {
	cfg Config

	mu       sync.RWMutex
	specs    map[string]Spec            // provider 名（Spec.Name）→ Spec
	adapters map[string]adaptor.Adapter // 归一协议名 → 适配器
}

// New 构造 Router 并装配内置协议适配器（LR-3 `openai` / LR-4 `anthropic` / D-29 `responses` /
// D-30 内置兜底 `echo`）。
func New(cfg Config) *Router {
	r := &Router{
		cfg:      cfg,
		specs:    make(map[string]Spec),
		adapters: make(map[string]adaptor.Adapter),
	}
	// 内置协议实现：协议名在适配器内为常量（非空），故注册不会失败。
	_ = r.registerAdapter(openai.New(openai.Options{HTTPClient: cfg.HTTPClient}))
	_ = r.registerAdapter(openai.NewResponsesAdapter(openai.Options{HTTPClient: cfg.HTTPClient}))
	_ = r.registerAdapter(anthropic.New(anthropic.Options{HTTPClient: cfg.HTTPClient}))
	_ = r.registerAdapter(echo.New())
	return r
}

// registerAdapter 注册协议适配器（同协议重复注册 = 覆盖；协议名归一后为空 → 报错）。
func (r *Router) registerAdapter(a adaptor.Adapter) error {
	if a == nil {
		return newError(ErrorInvalid, "adapter 为 nil")
	}
	p := normalizeProtocol(a.Protocol())
	if p == "" {
		return newError(ErrorInvalid, "adapter 协议名为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[p] = a
	return nil
}

// Register 注册（或更新）一个 provider。
//
// 幂等语义：同一 Name 重复注册 = **覆盖**（不报错）——LR-10 的增量对账依赖此语义
// （改配置 → 重新 Register，未变更的 Spec 不产生副作用）。Name / Protocol 为空 → invalid。
// **apiKey 只随 Spec 存于内存，Router 不落任何日志。**
func (r *Router) Register(spec Spec) error {
	norm, err := normalizeSpec(spec)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs[norm.Name] = norm
	return nil
}

// Unregister 注销一个 provider；未注册 → invalid（对齐 LR-10 对账的显式失败）。
func (r *Router) Unregister(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return newError(ErrorInvalid, "provider 名不能为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.specs[name]; !ok {
		return newError(ErrorInvalid, fmt.Sprintf("provider %q 未注册", name))
	}
	delete(r.specs, name)
	return nil
}

// Call 发起「**一次**」调用（一次 LLM 请求—响应），返回事件通道；done / error 之后通道关闭。
//
// 前置校验失败（provider 未注册 / 协议无适配器）→ 返回 *Error{Kind: invalid} 且不返回通道。
// 返回的通道由本方法负责关闭（适配器 Stream 返回即关闭）；消费方需持续读取直至关闭。
// Options.Model 为空 → 回落到 Spec.DefaultModel。
//
// **内置兜底**（D-30）：`Request.Provider` 为空或等于内置保留名（`echo`）→ 回落内置 echo provider
// （见 `builtinFallback`）；**其它未注册名仍报 invalid**（不静默兜底）。
//
// **连接层透明重试**（2026-09-22 定案，见 `retry.go` 与 40 §LR §1.1）：仅当**尚未向上层输出过任何
// 事件**且失败属连接层（`Kind: network`）时，透明重发 ≤1 次（毫秒级退避）；已输出过任何事件（哪怕
// 一个 `text_delta`）→ 立即透出错误，绝不重发。超时 / 429 / 5xx 等归 llm 决策（本层不重发）。
func (r *Router) Call(ctx context.Context, req Request) (<-chan Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req.Provider = strings.TrimSpace(req.Provider)
	r.mu.RLock()
	spec, ok := r.specs[req.Provider]
	var ad adaptor.Adapter
	if ok {
		ad = r.adapters[spec.Protocol]
	}
	r.mu.RUnlock()

	// 内置兜底（D-30）：无可用 provider（空名）或命中内置保留名 → 走内置 echo。
	if req.Provider == "" || req.Provider == builtinFallback.Name {
		spec, ok = builtinFallback, true
		ad = r.adapters[builtinFallback.Protocol]
	}
	if !ok {
		return nil, newError(ErrorInvalid, fmt.Sprintf("provider %q 未注册", req.Provider))
	}
	if ad == nil {
		return nil, newError(ErrorInvalid, fmt.Sprintf("provider %q 的协议 %q 未注册适配器", req.Provider, spec.Protocol))
	}
	if req.Options.Model == "" {
		req.Options.Model = spec.DefaultModel
	}

	out := make(chan Event)
	go func() {
		defer close(out)
		for attempt := 0; ; attempt++ {
			emitted, err := attemptOnce(ctx, ad, spec, req, out)
			if err == nil {
				return // 成功（含已投 done）
			}
			// 透明重发：唯一判据 = 尚未向上层输出过任何事件 + 连接层失败 + 次数未用尽。
			if emitted == 0 && attempt < transparentRetryMax && transparentRetryable(err) && ctx.Err() == nil {
				if !sleepCtx(ctx, transparentRetryBackoff) {
					return // 退避期被取消：不再投事件
				}
				continue
			}
			if ctx.Err() != nil {
				return // 消费方已离开 / 已取消：不投错误事件（err 为取消类）
			}
			select {
			case out <- Event{Type: EvError, Model: req.Options.Model, Err: err}:
			case <-ctx.Done(): // 消费方已离开 / 已取消：不阻塞
			}
			return
		}
	}()
	return out, nil
}

// Capabilities 返回某 provider 的能力矩阵；provider 未注册或其协议无适配器 → 零值 Caps。
func (r *Router) Capabilities(provider string) Caps {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[provider]
	if !ok {
		return Caps{}
	}
	ad, ok := r.adapters[spec.Protocol]
	if !ok {
		return Caps{}
	}
	return ad.Caps()
}

// normalizeSpec 校验并归一一个 Spec（Name / Protocol 去空白；Protocol 小写；其余字段原样保留，
// 含 `BaseURL` —— 尾斜杠由各适配器在拼 URL 时处理）。
// 失败 → *Error{Kind: invalid}（Register 与 LR-10 对账共用同一口径）。
func normalizeSpec(spec Spec) (Spec, *Error) {
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" {
		return spec, newError(ErrorInvalid, "provider 名不能为空")
	}
	spec.Protocol = normalizeProtocol(spec.Protocol)
	if spec.Protocol == "" {
		return spec, newError(ErrorInvalid, "provider 协议不能为空")
	}
	return spec, nil
}

// normalizeProtocol 归一协议名（去空白 + 小写）；空串保持空串（由调用方决定拒绝还是回落）。
// 不做「未知 → openai」的兜底：未注册的协议必须在 Call 时报明确错误（LR-2 验收）。
func normalizeProtocol(p string) string {
	return strings.ToLower(strings.TrimSpace(p))
}
