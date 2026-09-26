// token 估算口径（唯一来源，对齐 42 §2 (27) / 41 I-21）：
// 压缩插件（chonkpilot-plugin-compress）与记忆库（persist memory 域 / plugin-memory）
// 共用同一估算口径——**字符数 / 2**（CJK 约 2 字符/token、英文约 4 字符/token）。
// 收敛于此避免两处各写一套导致口径分叉。
package data

import (
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TokenDivisor 是估算口径除数：token 数 ≈ 字符数 / TokenDivisor。
const TokenDivisor = 2

// EstimateTokens 粗略估算文本 token 数（字符数 / TokenDivisor，非精确计数）。
func EstimateTokens(text string) int {
	return utf8.RuneCountInString(text) / TokenDivisor
}

// estimateTokensOf 按同一口径累加一组消息（正文 + 工具调用名与参数）；口径**单一来源**
// —— 内核消息与门面 DTO 两条入口共用本函数，避免各写一套导致分叉（见 41 I-21）。
func estimateTokensOf[T any](msgs []T, visit func(T, func(string))) int {
	n := 0
	add := func(s string) { n += utf8.RuneCountInString(s) }
	for _, m := range msgs {
		visit(m, add)
	}
	return n / TokenDivisor
}

// EstimateTokensOfMessages 估算会话消息 token 总量（口径同上；会话压缩判定与记忆库沉淀
// 阈值共用）。内容含正文 + 工具调用名与参数。
func EstimateTokensOfMessages(msgs []ChatMsg) int {
	return estimateTokensOf(msgs, func(m ChatMsg, add func(string)) {
		add(m.Content)
		for _, tc := range m.ToolCalls {
			add(tc.Function.Name)
			add(tc.Function.Arguments)
		}
	})
}

// EstimateTokensOfFacadeMessages 估算门面 DTO 消息（`facade.Message`）的 token 总量——
// **与 EstimateTokensOfMessages 同一口径**（正文 + 工具调用名与参数，÷ TokenDivisor）。
//
// 存在理由（2026-09-21，41 G-33「类型双轨收敛」）：门面已进面的消费方（压缩插件纯函数）
// 只认门面领域 DTO，不再翻回内核类型（协议嵌套形状不外溢），故需从 DTO 侧提供同一口径入口。
func EstimateTokensOfFacadeMessages(msgs []facade.Message) int {
	return estimateTokensOf(msgs, func(m facade.Message, add func(string)) {
		add(m.Content)
		for _, tc := range m.ToolCalls {
			add(tc.Name)
			add(tc.Arguments)
		}
	})
}

// LocateFullTurnCount 计算「保留完整」的轮数——**压缩侧与组装侧共用的唯一边界算法**
// （2026-09-24 A：消两处重复实现；两侧只需各自按轮提供 `fullTokens`，口径由本包统一）。
//
// fullTokens = 各轮**完整态** token 数，**升序**（末尾 = 最新轮）。语义（用户口径逐字落地，
// 2026-09-25 口径 V/W 修正）：
//
//	本轮（最新轮）**恒保留完整**（下限 1）——即使其自身完整态 token 已超 maxTokens
//	  （口径 V：「至少保留一轮，这一轮就是本轮」；「本轮就超了 maxToken 也不允许压缩本轮」）。
//	在本轮基础上从更近轮向前逐轮纳入第 i 轮（i = 1,2,…）：
//	  已纳入轮数 >= maxTurns（maxTurns > 0）或 累计完整态 token > maxTokens（maxTokens > 0）
//	  → 第 i 轮起（含该轮）全部进入简化区，保留完整 = 本轮 + 第 1..i-1 轮。
//
// 边界（口径 W，2026-09-25）：
//   - maxTurns == 0 → **轮次条件不启用**（只用 token 条件）；maxTokens == 0 → **token 条件不启用**（只用轮次条件）；
//   - **两者均 == 0 → 不压缩**（返回 len(fullTokens)：保留段=全量、无简化区、不生成摘要）；
//   - **负数（非法）→ 按「不启用」处理**（等价 0；后端不猜、由前端提示 + 调用方可观测）；
//   - 逐轮纳入且从未触发 → 全量保留（返回 len(fullTokens)，= 无简化区）。
func LocateFullTurnCount(fullTokens []int, maxTurns, maxTokens int) int {
	n := len(fullTokens)
	if n == 0 {
		return 0
	}
	if maxTurns <= 0 && maxTokens <= 0 {
		return n // 两条件均不启用（含非法负值）→ 不压缩（保留全量、无简化区）
	}
	included := 1 // 口径 V：本轮恒保留完整（下限保护）
	cum := fullTokens[n-1]
	for i := 1; i < n; i++ {
		if maxTurns > 0 && included >= maxTurns {
			break // 已达轮次上限 → 第 i 轮起进入简化区
		}
		cum += fullTokens[n-1-i]
		if maxTokens > 0 && cum > maxTokens {
			break // 累计超 token 上限 → 第 i 轮起（含）进入简化区
		}
		included++
	}
	return included
}

// ResolveStoredTokens 用**预存 token 值**覆盖实时估算值（缺值回退，绝不把缺值当 0）。
//
// 两侧均为**升序**（末尾 = 最新轮），按**尾部对齐**（最新轮对最新轮）——本轮（最新轮）恒保留，
// 尾部对齐即"自最新轮向前对齐"。两侧长度可不等：
//   - stored 比 estimated 短（只覆盖最近若干轮，历史轮无预存值属正常）；
//   - stored 比 estimated 长（消费方持有全会话轮、而 estimated 只是其尾部子集，如压缩插件拿到
//     全会话 turn_tokens 而快照仅保留最近若干轮）→ 取 stored 的**尾部** len(estimated) 项对齐。
//
// 逐位置：stored > 0 → 用预存值；stored 缺位 / <= 0 → 保留 estimated 同位置（实时估算回退）。
// 返回长度 = len(estimated)。
func ResolveStoredTokens(stored, estimated []int) []int {
	out := make([]int, len(estimated))
	copy(out, estimated)
	off := len(estimated) - len(stored) // >0：stored 短（尾部对齐，前若干位无预存值）
	start := 0
	if off < 0 {
		start = -off // stored 长 → 跳过 stored 头部，只对齐其尾部
		off = 0
	}
	for i, v := range stored[start:] {
		if v <= 0 {
			continue // 缺值 → 保留估算（绝不把缺值当 0）
		}
		if j := off + i; j >= 0 && j < len(out) {
			out[j] = v
		}
	}
	return out
}

// Zones 是**三段上下文**的轮数划分（升序：摘要区 < 简化区 < 完整区）：
//
//	完整区 = 末尾 FullTurns 轮（完整原文：含 reasoning / tool_call+result / text）；
//	简化区 = 完整区之前的 BriefTurns 轮（【简化态原文】：仅 text，构造见 BriefMessages）；
//	摘要区 = 其余更早轮（已压缩为摘要）。
//
// FullTurns + BriefTurns == 轮总数 → 无摘要区（不新增摘要）。
type Zones struct {
	FullTurns  int // 完整区轮数（>= 1，本轮恒保留；两条件均不启用时 = 全量）
	BriefTurns int // 简化区轮数（brief token 累计 <= briefBudget；briefBudget <= 0 → 0）
}

// LocateZones 计算三段边界（**压缩侧与组装侧共用的唯一三段定位算法**，2026-09-25 口径 X）：
//
//   - 完整区：`fullTokens`（各轮**完整态** token，升序、末尾=最新轮）→ 复用 `LocateFullTurnCount`
//     （N/M 取先到；本轮恒保留、两条件均不启用 = 全量；见该函数注释）；
//   - 简化区：自完整区前一轮向前**逐轮累加 `briefTokens`**（各轮**简化态** token，同一构造 = `BriefMessages`），
//     累计 <= `briefBudget`（= `compress_token_threshold`，T）即纳入；下一个把累计推过 T 的轮起**全部进摘要区**。
//     `briefBudget <= 0` → 简化区轮数 = 0（不进简化区，直接摘要）。
//
// 输入数组长度须一致（同一批轮）；不等长时按较短者取齐（防御）。
func LocateZones(fullTokens, briefTokens []int, maxTurns, maxTokens, briefBudget int) Zones {
	n := len(fullTokens)
	if n == 0 {
		return Zones{}
	}
	full := LocateFullTurnCount(fullTokens, maxTurns, maxTokens) // 含「两条件均不启用 → 全量」
	fullStart := n - full
	brief := 0
	if briefBudget > 0 && fullStart > 0 && fullStart <= len(briefTokens) {
		cum := 0
		for i := fullStart - 1; i >= 0; i-- {
			if cum+briefTokens[i] > briefBudget {
				break
			}
			cum += briefTokens[i]
			brief++
		}
	}
	return Zones{FullTurns: full, BriefTurns: brief}
}
