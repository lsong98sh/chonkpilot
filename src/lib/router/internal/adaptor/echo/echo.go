// Package echo 是 router 的**内置兜底**适配器（D-30）：不发任何 HTTP、不下发工具、不做工具调用
// —— 以**固定回复文案**回一条（单段 `text_delta` + `done{finish_reason: "stop"}`）：
// `收到<最后一条真实用户消息>，目前无法回复，请设置LLM。`（2026-09-24 用户口径：不再原文回显，
// 只提示"收到但无法回复"；`<…>` = 本条用户消息内容）。
//
// 启用时机 = **没有任何可用 LLM provider 时**（`Router.Call` 的 `Request.Provider` 为空或等于
// 内置保留名 `echo` → `src/lib/router/router.go` 的 `builtinFallback`）。它**不进入 provider 注册表
// `specs`**，也不出现在任何「可配置 provider 列表」中。
//
// 与「按 `Caps` 声明式降级」（LR-7）的差异（**刻意**）：本适配器**不调用** `adaptor.Degrade` ——
// 带工具 / 图片的请求**忽略**这两项（而非硬报错）。理由：兜底要在「未配置可用 provider」时仍能
// 出一段可读回复，硬报错会让兜底彻底不可用；此口径与 llm 侧 `chatEcho`（忽略 tools）一致。
//
// 本包只 import 标准库与 `internal/{adaptor,canon}`（不 import router 根包，无 import 环）。
package echo

import (
	"context"
	"strings"

	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// 系统注入消息的 Kind（与 llm 侧 isTurnBoundary 同口径）：非「真实用户消息」，不参与回显取文。
const (
	kindNotify   = "notify"   // 工具通知
	kindContinue = "continue" // 自动续写
	kindResume   = "resume"   // 恢复续轮
)

// 固定回复文案（2026-09-24 用户口径，**唯一文案源**）：收到<用户输入>，目前无法回复，请设置LLM。
// <用户输入> = 最后一条真实用户消息内容（见 lastUserText）；文案本身不再随输入变化。
const (
	replyPrefix = "收到"
	replySuffix = "，目前无法回复，请设置LLM。"
)

// replyText 组装固定回复文案；空输入 → 空串（调用方据此不发 text_delta）。
func replyText(userText string) string {
	if userText == "" {
		return ""
	}
	return replyPrefix + userText + replySuffix
}

// Adapter 是内置 echo 适配器（无状态、无出网客户端）。
type Adapter struct{}

// New 构造内置 echo 适配器。
func New() *Adapter { return &Adapter{} }

// Protocol 返回归一协议名（canon.ProtocolEcho）。
func (a *Adapter) Protocol() string { return canon.ProtocolEcho }

// Caps 返回能力矩阵：只声明流式（其余一律 false —— 无工具 / 无思考 / 无图片 / 无 usage / 无 system
// 角色语义）。注意本适配器**不据 Caps 降级**（见包注释）。
func (a *Adapter) Caps() canon.Caps { return canon.Caps{Stream: true} }

// Stream 以固定文案回复（取文 = 最后一条真实用户消息；无则只投 done）。返回 error 仅可能是
// 「消费方已离开」。
func (a *Adapter) Stream(ctx context.Context, spec canon.Spec, req canon.Request, out chan<- canon.Event) error {
	model := req.Options.Model
	if model == "" {
		model = spec.DefaultModel
	}
	if text := replyText(lastUserText(req.Messages)); text != "" {
		if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvTextDelta, Text: text, Model: model}); err != nil {
			return err
		}
	}
	return adaptor.Emit(ctx, out, canon.Event{Type: canon.EvDone, FinishReason: "stop", Model: model})
}

// lastUserText 倒序取最后一条**真实用户消息**的文本：role=user 且 Kind 不属
// notify / continue / resume（系统注入消息）；无 → 空串（调用方据此不发 text_delta）。
func lastUserText(msgs []canon.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != canon.RoleUser {
			continue
		}
		switch m.Kind {
		case kindNotify, kindContinue, kindResume:
			continue
		}
		return textOf(m.Content)
	}
	return ""
}

// textOf 拼接文本块（图片块与空块跳过 —— echo 不处理多模态，图片引用按其文本形态回显）。
func textOf(parts []canon.Part) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Image == nil && p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}
