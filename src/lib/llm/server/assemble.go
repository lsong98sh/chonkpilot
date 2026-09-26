// 上下文拼接（内置固定规则，2026-09-12 取代原 context.assemble.* 可配置方案）。
//
// 按 turn 为单位把会话历史拼进 LLM 请求（**三段结构，2026-09-25 口径 X**）：
//   - 完整区（最近 `keep_full_max_turns` 轮 / `keep_full_max_tokens` 内，**本轮恒保留**）→ 该 turn 的
//     全部消息原文（用户提问 / 中间文本 / 带 tool_calls 的 assistant / role=tool 结果 / 思考）；
//   - 简化区（再往前，brief token 累计 <= `compress_token_threshold`）→ **【简化态原文】仅 text**
//     （`data.BriefMessages`：去 reasoning / tool_call / tool_result）；
//   - 摘要区（更早）→ 已由压缩插件落为前导 system 摘要（原样透传）；若仍有残留（压缩未及）→ 同按简化态投影。
//   - 被重试 / 恢复的 turn → 强制全量（forceFullLast）。
//
// 三段边界算法唯一来源 = `data.LocateZones`（**与压缩插件同一函数**）：完整区 = `LocateFullTurnCount`
// （N/M 取先到；本轮恒保留、两条件均不启用 = 全量）；简化区 = 自完整区向前逐轮累加 brief token <= T。
//
// 2026-09-25（口径 V/W）：**本轮（最新轮）恒保留完整**由共享函数的下限保证；**两条件均不启用
// （N==0 且 M==0）→ 不压缩**（保留全量）。摘要压缩仍由压缩插件落快照（本处只做粒度收敛与
// 简化态投影，不做摘要、不写快照）。
//
// turn 边界：role=user 且非系统注入消息（Kind=notify 工具通知 / continue 自动续写 / resume 恢复续轮）。
// 只认 Kind 标记、不认内容——"用户正常发的新消息"（Kind=text，即便文本恰为"继续"）仍是新轮边界。
// 前导 system 消息（场景 system prompt / 快照摘要 / 记忆带出指引）原样透传，不参与投影。
//
// 思考内容回传不受任何配置控制：恒按 DeepSeek 协议规则（applyReasoningRule）。
package server

import (
	"strconv"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
)

// defaultKeepFullTurns 完整区轮数缺省值（对应项目级键 `keep_full_max_turns`；与压缩插件
// DefaultOptions.RetainTurns 一致）。
const defaultKeepFullTurns = 10

// defaultKeepFullTokens 完整区**完整态 token 上限**缺省值（对应项目级键 `keep_full_max_tokens`；
// 与压缩插件 DefaultOptions.KeepFullTokens 一致）。
const defaultKeepFullTokens = 24000

// defaultBriefBudget 简化区 brief token 预算缺省值（对应项目级键 `compress_token_threshold`；
// 与压缩插件 DefaultOptions.TokenMax 一致）——组装侧判简化区用**同一键**（三段边界两侧一致）。
const defaultBriefBudget = 20000

// 系统注入的用户消息 Kind（不新开 turn 边界——仍属当前 turn；与压缩插件 isUserText 口径一致）：
//   - assembleContinueKind：S6 断链/length 自动续写注入的"继续"；
//   - assembleResumeKind：重启/死 turn 恢复续轮注入的 user 消息（恢复路径如注入用户消息用此标记）。
const (
	assembleContinueKind = "continue"
	assembleResumeKind   = "resume"
)

// assembleContinueText 自动续写注入的用户消息文本（配合 Kind=assembleContinueKind）。
const assembleContinueText = "继续"

// 三段边界项目级键（2026-09-24，D1 改名 + A 加 token 上限；2026-09-25 加简化区预算 T）：
// `keep_full_max_turns`（新）/ `keep_full_turns`（旧）/ `keep_full_max_tokens` / `compress_token_threshold`。
const (
	keepFullMaxTurnsKey       = "keep_full_max_turns"
	keepFullMaxTurnsLegacyKey = "keep_full_turns"
	keepFullMaxTokensKey      = "keep_full_max_tokens"
	briefBudgetKey            = "compress_token_threshold"
)

// loadKeepFullTurns 读项目级维持轮数 `keep_full_max_turns`（缺失/非数字 → 10；启动时加载一次）。
//
// 读时兼容（D1）：新键缺失/非数字 → 回落旧键 `keep_full_turns`（2026-09-24 改名前的历史项目配置），
// 只读不写、不静默丢配置。两键同域同读序（prjusr → prj → usr，见 prjConfigValues）。
//
// 取值语义（2026-09-25 口径 W，**与压缩侧 resolveOpts 同口径**）：键**存在且为数字** → 原样采用
// （含 `0` = 该条件不启用、负数 = 非法按不启用处理）；**缺失/空/非数字** → 回落默认 10。
func (s *Server) loadKeepFullTurns(instanceID string) int {
	vals := s.prjConfigValues(instanceID, keepFullMaxTurnsKey, keepFullMaxTurnsLegacyKey)
	for _, k := range [...]string{keepFullMaxTurnsKey, keepFullMaxTurnsLegacyKey} {
		if n, err := strconv.Atoi(strings.TrimSpace(vals[k])); err == nil {
			return n
		}
	}
	return defaultKeepFullTurns
}

// loadKeepFullTokens 读项目级维持轮**完整态 token 上限** `keep_full_max_tokens`
// （缺失/非数字 → 24000；启动时加载一次）——与压缩插件同键、同域、同读序。
//
// 取值语义（2026-09-25 口径 W，**与压缩侧 resolveOpts 同口径**）：键**存在且为数字** → 原样采用
// （含 `0` = 该条件不启用、负数 = 非法按不启用处理）；**缺失/空/非数字** → 回落默认 24000。
func (s *Server) loadKeepFullTokens(instanceID string) int {
	vals := s.prjConfigValues(instanceID, keepFullMaxTokensKey)
	if n, err := strconv.Atoi(strings.TrimSpace(vals[keepFullMaxTokensKey])); err == nil {
		return n
	}
	return defaultKeepFullTokens
}

// loadBriefBudget 读项目级**简化区 brief token 预算** `compress_token_threshold`
// （缺失/非数字 → 20000；启动时加载一次）——与压缩插件同键、同域、同读序。
//
// 取值语义（**与压缩侧 resolveOpts 同口径**）：键**存在且为数字** → 原样采用（含 `0` = 预算 0，
// 即不进简化区）；**缺失/空/非数字** → 回落默认 20000。
func (s *Server) loadBriefBudget(instanceID string) int {
	vals := s.prjConfigValues(instanceID, briefBudgetKey)
	if n, err := strconv.Atoi(strings.TrimSpace(vals[briefBudgetKey])); err == nil {
		return n
	}
	return defaultBriefBudget
}

// isTurnBoundary 判断一条消息是否为 turn 起点（用户提问）：role=user 且非系统注入消息
// （Kind=notify 工具通知 / continue 自动续写 / resume 恢复续轮）。
// 只按 Kind 标记判定、不按内容——用户真实提问（Kind=text）即便文本为"继续"也是新轮边界。
func isTurnBoundary(m ChatMsg) bool {
	if m.Role != "user" {
		return false
	}
	switch m.Kind {
	case "notify", assembleContinueKind, assembleResumeKind:
		return false
	}
	return true
}

// assembleTurns 按 turn 分组拼接（**三段结构**，2026-09-25 口径 X）：
//   - 完整区（受 `keepFullTurns`(N) + `keepFullTokens`(M) 约束，与压缩侧同一算法
//     `data.LocateZones`）→ 整轮**原文**；**最新轮恒全量**（口径 V，由共享函数下限 1 保证）；
//   - 简化区（再往前，brief token 累计 <= briefBudget=T）→ **【简化态原文】仅 text**
//     （`data.BriefMessages`，去 reasoning / tool_call / tool_result）；
//   - 摘要区（更早）→ 本应已由压缩插件压缩为前导 system 摘要（原样透传）；若残留 → 同按简化态投影。
//   - forceFullLast=true 时最后一段（重试/恢复的 turn）强制全量。
//
// storedFull/storedBrief = 各轮**预存** token（P3；随 `data-session-context` 的 `turn_tokens`
// 伴随数组带回；升序、可短可长，按**尾部对齐**，缺值回退实时估算，见 `data.ResolveStoredTokens`）——
// 生产判定**真正读取预存值**，不再对已预存轮另做实时估算覆盖。
// 只返回新切片（不修改入参元素），保证 tc.hist / 快照不被污染。
func assembleTurns(hist []ChatMsg, keepFullTurns, keepFullTokens, briefBudget int, storedFull, storedBrief []int, forceFullLast bool) []ChatMsg {
	if len(hist) == 0 {
		return hist
	}
	// 以首个轮边界把前导（system 摘要等）与各 turn 切开；无用户提问 → 原样透传。
	first := -1
	for i, m := range hist {
		if isTurnBoundary(m) {
			first = i
			break
		}
	}
	if first < 0 {
		return hist
	}
	turns := splitTurns(hist[first:])
	// 各轮完整态 / 简化态 token（简化态构造 = data.BriefMessages，与压缩侧/预存同源）→ 预存优先。
	fullTokens := make([]int, len(turns))
	briefTokens := make([]int, len(turns))
	for i, t := range turns {
		fullTokens[i] = data.EstimateTokensOfMessages(t)
		briefTokens[i] = data.EstimateTokensOfMessages(data.BriefMessages(t))
	}
	fullTokens = data.ResolveStoredTokens(storedFull, fullTokens)
	briefTokens = data.ResolveStoredTokens(storedBrief, briefTokens)
	z := data.LocateZones(fullTokens, briefTokens, keepFullTurns, keepFullTokens, briefBudget)
	fullStart := len(turns) - z.FullTurns
	out := make([]ChatMsg, 0, len(hist))
	out = append(out, hist[:first]...)
	for i, t := range turns {
		if i >= fullStart || (forceFullLast && i == len(turns)-1) {
			out = append(out, t...) // 完整区：原文
			continue
		}
		// 简化区 / 摘要区残留：仅 text（去 reasoning / tool_call / tool_result）。
		out = append(out, data.BriefMessages(t)...)
	}
	return out
}

// splitTurns 按轮边界切分 turn 段（msgs[0] 必为轮边界，由 assembleTurns 保证）。
func splitTurns(msgs []ChatMsg) [][]ChatMsg {
	var turns [][]ChatMsg
	for _, m := range msgs {
		if isTurnBoundary(m) {
			turns = append(turns, []ChatMsg{m})
			continue
		}
		turns[len(turns)-1] = append(turns[len(turns)-1], m)
	}
	return turns
}

// turnTokenCounts 计算一轮的**完整态**与**简化态** token 数（P3/P4 轮次结束时预存）。
//   - 完整态 = 该轮全部消息（与组装侧 `EstimateTokensOfMessages(t)` 同一估算源）；
//   - 简化态 = **仅 text**（`data.BriefMessages`：去 reasoning / tool_call / tool_result）——
//     **与组装侧简化区投影、压缩侧简化区预算同一构造**（P4 口径一致性）。
func turnTokenCounts(turn []ChatMsg) (full, brief int) {
	full = data.EstimateTokensOfMessages(turn)
	brief = data.EstimateTokensOfMessages(data.BriefMessages(turn))
	return full, brief
}

// applyReasoningRule 按 DeepSeek 协议规则决定思维链回传（对最终请求消息列表生效）：
//   - 带 tool_calls 的 assistant：reasoning 必须回传（后续所有轮次，线格式 reasoning_content）；
//   - 不带 tool_calls 的 assistant（及 tool/user/system）：不回传（协议会忽略，传了也无效）。
//
// 只返回新切片（不修改入参元素），保证 tc.hist / 快照不被污染。
func applyReasoningRule(msgs []ChatMsg) []ChatMsg {
	out := make([]ChatMsg, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role != "assistant" || len(out[i].ToolCalls) == 0 {
			out[i].Reasoning = ""
		}
	}
	return out
}
