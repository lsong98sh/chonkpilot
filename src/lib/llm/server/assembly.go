// 上下文组装（四步压缩策略，2026-11-14）：实现已下沉到纯 lib 组件 `chonkpilot-session`，
// 本文件保留**宿主适配**——取数端口实现 + 四步策略编排（阈值判定 / 简化 / 同步压缩 / 报错）。
//
// 四步语义（用户 2026-11-14 定案，简化版）：
//
//	2.1 全量：system prompt/指引 + 全部历史轮原文 + 当前轮；总输入 <= 阈值 → 直接返回。
//	2.2 简化：历史轮去 reasoning / tool_calls / tool 结果（当前轮仍完整）；<= 阈值 → 返回并
//	    记录本轮内存标识（simplified / simplifiedFrom）。
//	2.3 压缩：历史消息（去 system）末尾追加摘要要求 → 进程内单轮 LLM 生成摘要 → 组装结果为
//	    `[system 摘要] + 当前轮`；<= 阈值 → 完成，并把 `[system 摘要]` 落快照（CAS）+
//	    替换本轮 `tc.hist`。每轮最多一次。
//	2.4 报错：压缩后仍超阈值 / 无历史轮可压缩 → 返回明确错误（不静默截断）。
//
// 阈值对象 = **本次请求总输入**（`Assembly.Usage.Total` = 固定开销 + 历史 + 当前轮）。
package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-agent/session"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
)

// 组装结果类型（定义在组件包；别名保持调用点不变）。
type (
	// Assembly 是一次 LLM 请求的组装结果。
	Assembly = session.Assembly
	// ZoneIDs 是三段边界的 turn id 表达（旧语义；四步策略下仅诊断用）。
	ZoneIDs = session.ZoneIDs
	// TokenUsage 是组装结果的 token 估算（固定开销 + 历史 + 当前轮）。
	TokenUsage = session.TokenUsage
)

// buildAssembly 组装本轮上下文（引擎 ContextProvider 的唯一取数实现 + 四步压缩策略）。
//
// 每次调用按当前 `tc.hist` + 本轮已落库消息重新组装；返回 error 时调用方必须走错误终态。
// 恢复 / 重试轮（forceFull）保持全量拼接，不参与压缩（恢复轮没有可压缩的历史上下文）。
func (tc *turnCtx) buildAssembly() (session.Result, error) {
	ctx := tc.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	composer := tc.composer()
	trigger := tc.compressTriggerTokens()

	// 2.1 全量（恢复 / 重试轮直接落到全量，跳过四步压缩）。
	full := composer.Build(ctx, session.Request{
		CurrentTurnID: tc.req.Turn, ForceFull: tc.forceFull, Mode: session.ModeFull,
	})
	if tc.forceFull || full.Usage.Total <= trigger {
		return full, nil
	}
	// 子会话 + 项目未开「压缩子会话」→ 不做简化 / 压缩（既有口径：保留全量，允许厂商超窗兜底）。
	if tc.isSubsession() && !tc.server.compressSubsessionOn(tc.req.InstanceID) {
		logf("[chonkpilot-server] compress: 子会话未开启压缩 → 跳过四步压缩（session=%s turn=%s tokens=%d trigger=%d）\n",
			tc.req.Session, tc.req.Turn, full.Usage.Total, trigger)
		return full, nil
	}
	if full.HistTurns() == 0 {
		return session.Result{}, tc.compactionError(full.Usage.Total, trigger)
	}

	// 2.2 简化：历史轮简化态 + 当前轮完整。
	simplified := composer.Build(ctx, session.Request{
		CurrentTurnID: tc.req.Turn, Mode: session.ModeSimplified,
	})
	if simplified.Usage.Total <= trigger {
		if !tc.simplified {
			tc.simplified = true
			tc.simplifiedFrom = tc.firstHistoryTurnID(full.HistTurns())
			logf("[chonkpilot-server] compress: 已简化历史轮（session=%s turn=%s tokens=%d trigger=%d from=%s）\n",
				tc.req.Session, tc.req.Turn, simplified.Usage.Total, trigger, tc.simplifiedFrom)
		}
		return simplified, nil
	}

	// 2.3 压缩：历史 → 摘要；每轮最多一次。
	if tc.compressAttempted {
		return session.Result{}, tc.compactionError(simplified.Usage.Total, trigger)
	}
	tc.compressAttempted = true
	summary, err := tc.compressHistory(ctx, full)
	if err != nil {
		return session.Result{}, err
	}
	// 压缩结果 = `[system 摘要] + 当前轮`：替换本轮历史（turnTokens 清空 → 终态写快照以
	// `tc.hist + 本轮消息` 为基；`snapshotWriteBase` 自然回落）。
	tc.hist = []ChatMsg{summary}
	tc.turnTokens = nil
	tc.simplified, tc.simplifiedFrom, tc.compressed = false, "", true

	compacted := composer.Build(ctx, session.Request{CurrentTurnID: tc.req.Turn, Mode: session.ModeFull})
	if compacted.Usage.Total > trigger {
		return session.Result{}, tc.compactionError(compacted.Usage.Total, trigger)
	}
	return compacted, nil
}

// composer 构造本轮装配器（端口全部落在宿主取数点）。
func (tc *turnCtx) composer() *session.Composer {
	return &session.Composer{
		History: turnHistorySource{tc: tc},
		Layers:  turnLayerSource{tc: tc},
		Guides:  turnGuideSource{tc: tc},
		Policy:  turnPolicySource{tc: tc},
		Tools:   turnToolSource{tc: tc},
	}
}

// assembly 返回对外口径的组装结果（不含分组明细）。错误路径返回零值 Assembly（调用方应改走
// `buildAssembly` 以拿到 error）。
func (tc *turnCtx) assembly() Assembly {
	a, _ := tc.buildAssembly()
	return a.Assembly
}

// compressTriggerTokens 返回本轮实际生效的触发阈值：
// 显式 `compress_trigger_tokens` > 0 优先；否则最大上下文的 70%（窗口缺省 128000 → 89600）。
func (tc *turnCtx) compressTriggerTokens() int {
	return effectiveCompressTrigger(tc.compressTrigger, tc.llmMaxContextToken)
}

// compactionError 构造 2.4 的明确错误（含 token 实测值与阈值，便于诊断）。
func (tc *turnCtx) compactionError(tokens, trigger int) error {
	return fmt.Errorf("context compaction failed: input tokens %d > trigger %d after full/simplified/compressed stages (session=%s turn=%s)",
		tokens, trigger, tc.req.Session, tc.req.Turn)
}

// firstHistoryTurnID 取第一条历史轮的 turn id（尾部对齐 `turn_tokens`；无法映射 → ""）。
func (tc *turnCtx) firstHistoryTurnID(histTurns int) string {
	if histTurns <= 0 {
		return ""
	}
	i := len(tc.turnTokens) - histTurns
	if i < 0 || i >= len(tc.turnTokens) {
		return ""
	}
	return tc.turnTokens[i].TurnID
}

// compressHistory 执行一次同步压缩（2.3）：
//   - 输入 = 全部历史消息（去 system）+ 末尾追加摘要要求（`capability/system/summary.md`）；
//   - 摘要 = 进程内单轮 LLM（`llmOnceMessages`，5 分钟上限、随本轮 ctx 取消）；
//   - 落快照 = `[system 摘要]`（CAS：快照仍停在最后一个历史轮才写）+ Meta 携带压缩标识。
//
// 返回摘要消息（system，含 Meta）；error 非 nil 时调用方必须走错误终态。
func (tc *turnCtx) compressHistory(ctx context.Context, full session.Result) (ChatMsg, error) {
	s := tc.server
	if s == nil {
		return ChatMsg{}, errors.New("context compaction failed: server unavailable")
	}
	anchor := full.LastTurnID
	if anchor == "" {
		return ChatMsg{}, errors.New("context compaction failed: no history turn to compress")
	}
	requirement := s.summaryPrompt(tc.req.InstanceID)
	if requirement == "" {
		return ChatMsg{}, errors.New("context compaction failed: summary prompt is empty (capability/system/summary.md)")
	}
	input := session.CompressionInput(tc.hist, requirement)
	if len(input) == 0 {
		return ChatMsg{}, errors.New("context compaction failed: no history messages to compress")
	}
	// 压缩前触发一次既有记忆沉淀（跟随压缩的既有口径）。
	s.flushMemoryBeforeCompress(tc.req.InstanceID, tc.req.Session)
	job := compressJob{
		InstanceID: tc.req.InstanceID, SessionID: tc.req.Session, TurnID: tc.req.Turn, Anchor: anchor,
	}
	s.emitCompressNotice(job, noticeCompressStart, "正在压缩上下文…")
	summary, err := s.llmOnceMessages(ctx, s.compressSpec(tc.req.InstanceID), input)
	if err != nil {
		s.pluginNotify(hook.Notice{
			Plugin: "compress", Kind: "compress", Reason: err.Error(),
			InstanceID: job.InstanceID, Session: job.SessionID, Turn: job.TurnID,
		})
		s.emitCompressNotice(job, noticeCompressDone, "上下文压缩失败")
		return ChatMsg{}, fmt.Errorf("context compaction failed: summarize: %w", err)
	}
	msg := session.SummaryMessage(summary, map[string]any{
		"compress": map[string]any{
			"compressed_from": anchor,
			"simplified_from": tc.simplifiedFrom,
		},
	})
	// 快照 CAS：仅当仍停在压缩基准时写；CAS 未命中仍在内存使用本次摘要（只记日志）。
	if s.cfg != nil {
		res, err := session.CommitSummary(ctx, s.snapshotStoreFor(tc.req.InstanceID, tc.req.Session), anchor, msg)
		if err != nil {
			logf("[chonkpilot-server] compress: 快照写入失败（session=%s turn=%s）: %v\n", tc.req.Session, tc.req.Turn, err)
		} else if res.CASMiss {
			logf("[chonkpilot-server] compress: CAS 丢弃（session=%s snapshot_turn=%q anchor=%q）\n",
				tc.req.Session, res.Snapshot.Turn, anchor)
		} else if res.Written {
			logf("[chonkpilot-server] compress: 已压缩（session=%s snapshot_turn=%s）\n", tc.req.Session, anchor)
		}
	}
	s.emitCompressNotice(job, noticeCompressDone, "上下文压缩完成")
	return msg, nil
}

// ─── 端口实现（取数点全部落在宿主）────────────────────────────────

// turnHistorySource 实现 `session.HistorySource`：历史（含 turn_tokens）+ 本轮已落库消息。
type turnHistorySource struct{ tc *turnCtx }

func (h turnHistorySource) Load(context.Context) (session.History, error) {
	cur := h.tc.server.newSessionStore(h.tc.req.InstanceID).LoadMessages(h.tc.req.Turn)
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

// turnPolicySource 实现 `session.PolicySource`：旧三段配置（四步策略不再读取，恒 0）。
type turnPolicySource struct{ tc *turnCtx }

func (p turnPolicySource) KeepFullTurns(context.Context) int  { return 0 }
func (p turnPolicySource) KeepFullTokens(context.Context) int { return 0 }
func (p turnPolicySource) BriefBudget(context.Context) int    { return 0 }

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
