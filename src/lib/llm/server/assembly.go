// 上下文组装（SESSION-1 阶段二）：实现已下沉到纯 lib 组件 `chonkpilot-session`，
// 本文件只剩**宿主适配**——取数端口实现 + 组装结果别名。
//
// 组装语义（三段结构 / 边界 / reasoning / 指引顺序 / token 统计）的唯一实现在组件包
// （`src/lib/chonkpilot-session/compose.go`）。本文件不得再写一份组装逻辑。
package server

import (
	"context"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-session"
)

// 组装结果类型（定义在组件包；别名保持调用点不变）。
type (
	// Assembly 是一次 LLM 请求的组装结果。
	Assembly = session.Assembly
	// ZoneIDs 是三段边界的 turn id 表达。
	ZoneIDs = session.ZoneIDs
	// TokenUsage 是组装结果的 token 估算（固定开销 + 历史）。
	TokenUsage = session.TokenUsage
)

// buildAssembly 组装本轮上下文（生产路径 `msgs()` 的唯一实现）。
//
// 顺序与改动前逐字一致（实现见 `chonkpilot-session.Composer`）：
//  1. 当前轮已落库消息（`LoadMessages(turn)`）+ `tc.hist`（快照前缀 + 未压缩轮）；
//  2. 三段投影（完整区原文 / 简化区仅 text / 摘要区残留同简化）；
//  3. `ApplyReasoningRule`（仅带 tool_calls 的 assistant 回传 reasoning）；
//  4. 首位依次插入 assetGuide / memoryGuide 两条 system 指引（最终顺序 `[资产, 记忆, ...]`）。
//
// 边界 / 用量在**投影后**的消息上计算（turn id 由 `turn_tokens` 尾部对齐，见组件 TurnGroups）。
func (tc *turnCtx) buildAssembly() session.Result {
	ctx := tc.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return (&session.Composer{
		History: turnHistorySource{tc: tc},
		Layers:  turnLayerSource{tc: tc},
		Guides:  turnGuideSource{tc: tc},
		Policy:  turnPolicySource{tc: tc},
		Tools:   turnToolSource{tc: tc},
	}).Build(ctx, session.Request{CurrentTurnID: tc.req.Turn, ForceFull: tc.forceFull})
}

// assembly 返回对外口径的组装结果（不含分组明细）。
func (tc *turnCtx) assembly() Assembly { return tc.buildAssembly().Assembly }

// ─── 端口实现（取数点全部落在宿主）────────────────────────────────

// turnHistorySource 实现 `session.HistorySource`：历史（含 turn_tokens）+ 本轮已落库消息。
type turnHistorySource struct{ tc *turnCtx }

func (h turnHistorySource) Load(context.Context) (session.History, error) {
	cur := newSessionStore(h.tc.server.bus, h.tc.req.InstanceID).LoadMessages(h.tc.req.Turn)
	return session.History{
		Messages:     h.tc.hist,
		TurnTokens:   sessionTurnTokens(h.tc.turnTokens),
		TurnMessages: cur,
	}, nil
}

// sessionTurnTokens 把门面 `turn_tokens` 折成组件的纯 lib 形态（nil 安全）。
func sessionTurnTokens(tokens []facade.TurnToken) []session.TurnToken {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]session.TurnToken, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, session.TurnToken{TurnID: t.TurnID, Full: t.Full, Brief: t.Brief})
	}
	return out
}

// turnLayerSource 实现 `session.LayerSource`：本轮系统提示词五层拼装结果（宿主已算好）。
type turnLayerSource struct{ tc *turnCtx }

func (l turnLayerSource) SystemPrompt(context.Context) string { return l.tc.sysPromptText }

// turnGuideSource 实现 `session.GuideSource`：两条指引（**单轮只读一次**，缓存仍在宿主）。
type turnGuideSource struct{ tc *turnCtx }

// read 单轮只读一次：本函数每轮工具迭代都会被组装调用（最多 20 次），
// 指引依赖 instance/workDir/配置，单轮内视为不变。
func (g turnGuideSource) read() {
	g.tc.guideOnce.Do(func() {
		g.tc.memGuideText = g.tc.server.memoryGuide(g.tc.req.InstanceID, g.tc.req.WorkDir)
		g.tc.assetGuideText = g.tc.server.assetGuide(g.tc.req.InstanceID)
	})
}

func (g turnGuideSource) Memory(context.Context) string { g.read(); return g.tc.memGuideText }
func (g turnGuideSource) Asset(context.Context) string  { g.read(); return g.tc.assetGuideText }

// turnPolicySource 实现 `session.PolicySource`：三段边界配置（启动时加载一次）。
type turnPolicySource struct{ tc *turnCtx }

func (p turnPolicySource) KeepFullTurns(context.Context) int  { return p.tc.keepFullTurns }
func (p turnPolicySource) KeepFullTokens(context.Context) int { return p.tc.keepFullTokens }
func (p turnPolicySource) BriefBudget(context.Context) int    { return p.tc.briefBudget }

// turnToolSource 实现 `session.ToolSource`：本轮下发工具面（仅用于 schema token 估算）。
type turnToolSource struct{ tc *turnCtx }

func (t turnToolSource) Tools(context.Context) []session.ToolSchema {
	return toolSchemas(t.tc.llmTools())
}

// toolSchemas 把宿主工具定义折成组件的估算视图（口径字段同源，不发 LLM）。
func toolSchemas(defs []ToolDef) []session.ToolSchema {
	if len(defs) == 0 {
		return nil
	}
	out := make([]session.ToolSchema, 0, len(defs))
	for _, d := range defs {
		out = append(out, session.ToolSchema{Name: d.Name, Description: d.Description, Parameters: d.Parameters})
	}
	return out
}

// toolsTokenEstimate 估算工具 schema 的 token 量（组件 `ToolsTokenEstimate` 的宿主适配；
// 组装侧用量计算与白盒断言共用同一口径）。
func toolsTokenEstimate(tools []ToolDef) int { return session.ToolsTokenEstimate(toolSchemas(tools)) }

// ─── 快照 DTO 互转（压缩回写 / 读已提交快照用）─────────────────────

// facadeMessagesFromChat 把内部消息转为门面 DTO（压缩回写快照用；字段与快照 DTO 一一对应）。
func facadeMessagesFromChat(msgs []ChatMsg) []facade.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]facade.Message, 0, len(msgs))
	for _, m := range msgs {
		msg := facade.Message{
			Role: m.Role, Kind: m.Kind, Content: m.Content,
			ToolCallID: m.ToolCallID, Reasoning: m.Reasoning, Meta: m.Meta,
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, facade.ToolCall{
				ID: tc.ID, Type: "function", Name: tc.Name, Arguments: tc.Arguments,
			})
		}
		out = append(out, msg)
	}
	return out
}

// chatMessagesFromFacade 是 facadeMessagesFromChat 的逆变换（读已提交快照用）。
func chatMessagesFromFacade(msgs []facade.Message) []ChatMsg {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]ChatMsg, 0, len(msgs))
	for _, m := range msgs {
		msg := ChatMsg{
			Role: m.Role, Kind: m.Kind, Content: m.Content,
			ToolCallID: m.ToolCallID, Reasoning: m.Reasoning, Meta: m.Meta,
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out = append(out, msg)
	}
	return out
}
