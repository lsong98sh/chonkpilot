// token 估算 / 三段边界（数据面出口）：**唯一实现已内化到 `chonkpilot-session`**
// （2026-10-24 Persist Provider 改造），本文件只做薄转发 + 门面 DTO 入口，口径不分叉。
//
// token 口径：字符数 / 2（CJK 约 2 字符/token、英文约 4 字符/token）。
package data

import (
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-session"
	"github.com/chonkpilot/chonkpilot-session/store"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TokenDivisor 是估算口径除数：token 数 ≈ 字符数 / TokenDivisor。
const TokenDivisor = session.TokenDivisor

// Zones 是三段上下文的轮数划分（唯一定义在 store 包）。
type Zones = store.Zones

// EstimateTokens 粗略估算文本 token 数（字符数 / TokenDivisor，非精确计数）。
func EstimateTokens(text string) int { return session.EstimateTokens(text) }

// EstimateTokensOfMessages 估算会话消息 token 总量（正文 + 工具调用名与参数）。
func EstimateTokensOfMessages(msgs []ChatMsg) int { return session.EstimateTokensOfMessages(msgs) }

// EstimateTokensOfFacadeMessages 估算门面 DTO 消息（`facade.Message`）的 token 总量——
// **与 EstimateTokensOfMessages 同一口径**（正文 + 工具调用名与参数，÷ TokenDivisor）。
func EstimateTokensOfFacadeMessages(msgs []facade.Message) int {
	n := 0
	for _, m := range msgs {
		n += utf8.RuneCountInString(m.Content)
		for _, tc := range m.ToolCalls {
			n += utf8.RuneCountInString(tc.Name)
			n += utf8.RuneCountInString(tc.Arguments)
		}
	}
	return n / TokenDivisor
}

// LocateFullTurnCount 计算「保留完整」的轮数（压缩侧与组装侧共用的唯一边界算法）。
func LocateFullTurnCount(fullTokens []int, maxTurns, maxTokens int) int {
	return session.LocateFullTurnCount(fullTokens, maxTurns, maxTokens)
}

// ResolveStoredTokens 用预存 token 值覆盖实时估算值（尾部对齐；缺值回退）。
func ResolveStoredTokens(stored, estimated []int) []int {
	return session.ResolveStoredTokens(stored, estimated)
}

// LocateZones 计算三段边界（压缩侧与组装侧共用的唯一三段定位算法）。
func LocateZones(fullTokens, briefTokens []int, maxTurns, maxTokens, briefBudget int) Zones {
	return session.LocateZones(fullTokens, briefTokens, maxTurns, maxTokens, briefBudget)
}
