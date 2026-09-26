// llm ↔ router 接线（LR-11）：llm 侧**不再直连各 provider** —— 把「一次来回（`step`）」调用交给
// `chonkpilot-router`（`Call`），本文件负责三件事：
//
//  1. **provider 解析**：usr `llms` 记录 / 内置保留名 / exe flags 隐含默认 → `router.Spec`，
//     并登记进 `router`（`Register` 幂等；配置变更经 `reconcileLLMProviders` 增量对账）；
//  2. **请求折算**：`ChatMsg` → canonical `router.Message`（含图片块展开）/ `ToolDef` →
//     `router.ToolDef` / `ChatOptions` → `router.CallOptions`；
//  3. **事件折算**：`router.Event`（七类，D-32）→ `StreamEvent`（llm 侧既有消费契约，**形状不变**），
//     并把 `tool_call`（完整值）聚成终态 `Done` 事件附带的 `ToolCalls`（与旧 chat 路径语义一致）。
//
// 与旧直连路径的**已知线格式差异**（事件流语义不变，见 40 §LR §5.1 的 shadow 比对说明）：
//   - 请求体恒带 `stream_options.include_usage`（router 适配器统一开启；旧 llm 不带）；
//   - 输出上限字段按模型名择一（`o1/o3/o4/gpt-5*` → `max_completion_tokens`；旧 llm 恒 `max_tokens`）；
//   - 工具无参数 schema 时补 `{"type":"object","properties":{}}`（旧 llm 发 `null`）；
//   - 含图片的消息若文本为空，省略空文本块（旧 llm 恒发空文本块）；
//   - 非 2xx 错误体信息串按 JSON 取值（旧 llm 取体前 512 字节原文）。
//
// 保留点（40 §LR §5.1「必须保留」）：重试循环 / `probeBeforeRetry` / `sleepCtx` 仍在 `turn.go`；
// 120s/60s 超时、`finish_reason=length` 与 `EMPTY_REPLY` 语义、`retryCount`/`retryDelay` 四级配置
// 语义均不变。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-router"
)

// exeDefaultSpecName 是**exe flags 隐含默认**（`-llm-base`/`-llm-model`）在 router 中的保留 provider 名。
//
// 为何不传空名：`router.Call` 的 `Request.Provider` 为空 = **无可用 provider** → 回落内置兜底
// `echo`（「系统默认（启动参数）」语义将变成回显，属行为变更）。故系统默认折算为本保留名并显式
// 注册（前端 `llm-start.llm = ""` 即系统默认，见 ChatPanel.SYSTEM_DEFAULT_LLM）。
const exeDefaultSpecName = "__default__"

// exeDefaultSpec 是 exe flags 隐含默认的 Spec（base/model 必非空：`New` 已补缺省）。
func (s *Server) exeDefaultSpec() router.Spec {
	return router.Spec{
		Name:         exeDefaultSpecName,
		Protocol:     ProtocolOpenAI,
		BaseURL:      s.opts.LLMBase,
		DefaultModel: s.opts.LLMModel,
	}
}

// specFor 把本轮命中的 provider 配置折算为 `router.Spec`：
//   - `pcfg == nil`（未命中 usr llms / 非保留名）→ exe flags 隐含默认；
//   - `BaseURL` / `Model` 为空 → 回落 exe flags（与旧 `llmClientFor` 的回落口径逐字一致）；
//   - 协议已由 `loadLLMProvider` 归一（openai / responses / echo）。
func (s *Server) specFor(pcfg *llmProviderCfg) router.Spec {
	if pcfg == nil {
		return s.exeDefaultSpec()
	}
	return router.Spec{
		Name:         pcfg.Name,
		Protocol:     pcfg.Protocol,
		BaseURL:      orDefault(pcfg.BaseURL, s.opts.LLMBase),
		APIKey:       pcfg.APIKey,
		DefaultModel: orDefault(pcfg.Model, s.opts.LLMModel),
	}
}

// orDefault 取 v；空白 → fallback。
func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// specFromRecord 把一条 usr `llms` 记录折算为 `router.Spec`（纯函数；口径与 `providerFromLLMs` 一致）。
// name 为空 → ok=false（跳过，不注册非法 Spec）。
func (s *Server) specFromRecord(llm map[string]any) (router.Spec, bool) {
	name := strings.TrimSpace(str(llm["name"]))
	if name == "" {
		return router.Spec{}, false
	}
	return router.Spec{
		Name:         name,
		Protocol:     NormalizeLLMProtocol(strings.TrimSpace(str(llm["protocol"]))),
		BaseURL:      orDefault(strings.TrimSpace(str(llm["baseUrl"])), s.opts.LLMBase),
		APIKey:       strings.TrimSpace(str(llm["apiKey"])),
		DefaultModel: orDefault(strings.TrimSpace(str(llm["model"])), s.opts.LLMModel),
	}, true
}

// reconcileLLMProviders 把 usr `llms` 全量 + exe flags 隐含默认对账进 router（LR-10 能力接线）：
// 配置保存/删除后（`data-user-config-refresh`）与启动时各跑一次 → 「配置修改时 router 动态增减」。
// 读法不变（经 data 门面 `s.cfg`）；**apiKey 只随 Spec 存于内存，本函数不落任何日志**。
func (s *Server) reconcileLLMProviders() {
	if s.rt == nil || s.cfg == nil {
		return
	}
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{})
	if err != nil {
		logf("[chonkpilot-server] reconcileLLMProviders: UserConfigGet failed: %v\n", err)
		return
	}
	specs := []router.Spec{s.exeDefaultSpec()}
	if res.Config != nil {
		for _, item := range asAnySlice(res.Config["llms"]) {
			llm, _ := item.(map[string]any)
			if llm == nil {
				continue
			}
			if spec, ok := s.specFromRecord(llm); ok {
				specs = append(specs, spec)
			}
		}
	}
	if _, err := s.rt.Reconcile(specs); err != nil {
		logf("[chonkpilot-server] reconcileLLMProviders: %v\n", err)
	}
}

// chat 发起「一次来回（`step`）」LLM 调用（经 router）：返回的事件通道语义与旧 `LLMClient.Chat` 一致
// （Content/Reasoning 增量、终态 Done 附带 ToolCalls、Err 分类），调用方无需感知协议。
//
// 登记 + 调用：`spec` 先 `Register`（幂等覆盖）再 `Call`，保证「配置刚改、refresh 未到」时
// （探活的一次性 provider 亦然）也可用。
func (s *Server) chat(ctx context.Context, spec router.Spec, msgs []ChatMsg, tools []ToolDef, opts ChatOptions) (<-chan StreamEvent, error) {
	req, err := buildRouterRequest(spec.Name, msgs, tools, opts)
	if err != nil {
		return nil, err
	}
	if err := s.rt.Register(spec); err != nil {
		return nil, mapRouterError(err)
	}
	events, err := s.rt.Call(ctx, req)
	if err != nil {
		return nil, mapRouterError(err)
	}
	out := make(chan StreamEvent, 8)
	go forwardRouterEvents(ctx, events, out)
	return out, nil
}

// forwardRouterEvents 把 router 事件流折算为 `StreamEvent` 流：
//   - `tool_call`（完整值，D-32）逐条累积，**随 `done` 一并投出**（复刻旧 chat 路径「终态一次带全部
//     ToolCalls」的形状，`turn.go` 的 `calls = ev.ToolCalls` 语义不变）；
//   - `usage` 当前无消费方（LR-9 归一在 router，llm 侧记账未接线）→ 不投；
//   - 通道因取消而关闭且**尚未投过 done** → 补投一条 ctx 错误事件（镜像旧路径「取消静默」的判定）。
func forwardRouterEvents(ctx context.Context, events <-chan router.Event, out chan<- StreamEvent) {
	defer close(out)
	var calls []router.ToolCall
	sentDone := false
	for ev := range events {
		switch ev.Type {
		case router.EvTextDelta:
			out <- StreamEvent{Content: ev.Text, Model: ev.Model}
		case router.EvReasoningDelta:
			out <- StreamEvent{Reasoning: ev.Text, Model: ev.Model}
		case router.EvToolCall:
			if ev.ToolCallFull != nil {
				calls = append(calls, *ev.ToolCallFull)
			}
		case router.EvDone:
			finish := ev.FinishReason
			if len(calls) > 0 {
				finish = "tool_calls" // 旧 chat 路径口径：有工具调用即 tool_calls
			}
			out <- StreamEvent{ToolCalls: FromRouterToolCalls(calls), Done: true, FinishReason: finish, Model: ev.Model}
			sentDone = true
		case router.EvError:
			out <- StreamEvent{Err: mapRouterError(ev.Err)}
		}
	}
	if !sentDone && ctx.Err() != nil {
		select {
		case out <- StreamEvent{Err: ctx.Err()}:
		default: // 消费方已离开：不阻塞
		}
	}
}

// buildRouterRequest 把 llm 侧入参折算为 canonical 请求：图片按 `opts.Images` 就地展开为图片块。
func buildRouterRequest(provider string, msgs []ChatMsg, tools []ToolDef, opts ChatOptions) (router.Request, error) {
	imgs, err := expandImages(msgs, opts.Images)
	if err != nil {
		return router.Request{}, err
	}
	return router.Request{
		Provider: provider,
		Messages: routerMessages(msgs, imgs),
		Tools:    routerToolDefs(tools),
		Options:  routerCallOptions(opts),
	}, nil
}

// routerMessages 折算 canonical 消息（顺序保持；单条折算复用 `ToRouterMessage`）：含图片 →
// 在文本块后追加图片块（`expandImages` 的展开结果，见 llm_images.go）；无图片 → 单文本块。
func routerMessages(msgs []ChatMsg, imgs []messageImages) []router.Message {
	out := make([]router.Message, len(msgs))
	for i, m := range msgs {
		msg := ToRouterMessage(m)
		text, images := m.Content, []imagePart(nil)
		if i < len(imgs) {
			text, images = imgs[i].text, imgs[i].images
		}
		if len(images) > 0 {
			parts := make([]router.Part, 0, len(images)+1)
			parts = append(parts, router.Part{Type: router.PartText, Text: text})
			for _, im := range images {
				parts = append(parts, router.Part{
					Type:  router.PartImage,
					Image: &router.ImagePart{MIME: im.mime, DataURL: im.dataURL()},
				})
			}
			msg.Content = parts
		} else if text != m.Content {
			// 无原图但文本已被展开改写（更早轮次的图片引用降级为 `[图片: 名]` 占位）→ 用展开文本。
			msg.Content = []router.Part{{Type: router.PartText, Text: text}}
		}
		out[i] = msg
	}
	return out
}

// routerToolDefs 折算工具定义（`Parameters` 原样透传为 schema JSON）。
func routerToolDefs(tools []ToolDef) []router.ToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]router.ToolDef, 0, len(tools))
	for _, t := range tools {
		params, _ := json.Marshal(t.Parameters)
		out = append(out, router.ToolDef{Name: t.Name, Description: t.Description, Parameters: params})
	}
	return out
}

// routerCallOptions 折算调用参数：`Effort` 优先于 `Think`（旧口径），两者均空 → 不下发 reasoning_effort。
func routerCallOptions(opts ChatOptions) router.CallOptions {
	co := router.CallOptions{
		Model:           opts.Model,
		Temperature:     opts.Temperature,
		MaxTokens:       opts.MaxTokens,
		TopP:            opts.TopP,
		ResponseTimeout: opts.ResponseTimeout,
		StreamTimeout:   opts.StreamTimeout,
	}
	effort := opts.Effort
	if effort == "" {
		effort = opts.Think
	}
	if effort != "" {
		co.Reasoning = &router.Reasoning{Effort: effort}
	}
	return co
}

// mapRouterError 把 router 分类错误折算为 llm 侧 `*LLMError`（`Retryable` 原样透传）。
// `invalid` 在 llm 侧无对应分类（该分类专指 router 前置校验的调用方用法错误）→ 归 `protocol`。
func mapRouterError(err error) error {
	if err == nil {
		return nil
	}
	var re *router.Error
	if errors.As(err, &re) {
		return &LLMError{Kind: routerKindToLLM(re.Kind), Message: re.Message, Retryable: re.Retryable}
	}
	return llmErr(ErrProtocol, err.Error())
}

// routerKindToLLM 归一错误分类（两侧词表一致，仅 llm 无 `invalid`）。
func routerKindToLLM(k router.ErrorKind) ErrKind {
	switch k {
	case router.ErrorNetwork:
		return ErrNetwork
	case router.ErrorTimeout:
		return ErrTimeout
	case router.ErrorRateLimit:
		return ErrRateLimit
	case router.ErrorServer:
		return ErrServer
	case router.ErrorAuth:
		return ErrAuth
	default: // protocol / invalid
		return ErrProtocol
	}
}
