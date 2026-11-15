// 上下文拼接的**宿主侧配置读取**（四步压缩策略的阈值来源，2026-11-14 取代旧三段可配置方案）。
//
// 组装语义（全量 / 简化 / 压缩 / 报错）的唯一实现在宿主 `assembly.go`（`buildAssembly`），
// 纯投影与 token 估算在组件包 `chonkpilot-session`（`ProjectSimplified` / `MessageTokens`）。
// 本文件只保留：
//   - 触发阈值的项目级键（`compress_trigger_tokens`；0/缺省 = 按最大上下文 70% 自动）；
//   - 会话注入消息的 Kind 常量（取值唯一来源 = 组件）；
//   - 两个薄适配（`isTurnBoundary` / `turnTokenCounts`，供本包既有调用点使用）。
//
// 旧三段键（`keep_full_max_turns` / `keep_full_max_tokens` / `compress_token_threshold`）自
// 2026-11-14 起**不再读取**（数据层仍接受旧键写入以兼容旧配置，但不再影响组装）。
package server

import (
	"strconv"
	"strings"

	"github.com/lsong98sh/chonkpilot-agent/session"
)

const (
	// defaultContextWindowTokens 是自动触发阈值的**窗口回落值**：provider 未配置
	// `maxContextToken`（或其 <= 0）时按 128000 计算 70%（= 89600）。
	defaultContextWindowTokens = 128000
	// autoCompressTriggerPercent 是自动阈值百分比（最大上下文的 70%）。
	autoCompressTriggerPercent = 70
)

// 系统注入的用户消息 Kind（不新开 turn 边界——仍属当前 turn；取值唯一来源 = 组件）：
//   - assembleContinueKind：S6 断链/length 自动续写注入的"继续"；
//   - assembleResumeKind：重启/死 turn 恢复续轮注入的 user 消息（恢复路径如注入用户消息用此标记）。
const (
	assembleContinueKind = session.KindContinue
	assembleResumeKind   = session.KindResume
)

// assembleContinueText 自动续写注入的用户消息文本（配合 Kind=assembleContinueKind）。
const assembleContinueText = session.ContinueText

// compressTriggerTokensKey 是项目级「压缩触发阈值（tokens）」键：
// 0 / 缺省 = 自动（最大上下文的 70%）；>0 = 显式阈值。
const compressTriggerTokensKey = "compress_trigger_tokens"

// compressTriggerTokensOf 从**已读** prj 配置值表折算显式触发阈值（纯函数）：
// 键存在且为正整数 → 采用；0 / 缺失 / 空 / 非数字 / 负数 → 0（= 自动）。
func compressTriggerTokensOf(vals map[string]string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(vals[compressTriggerTokensKey])); err == nil && n > 0 {
		return n
	}
	return 0
}

// autoCompressTriggerOf 按 provider 的最大上下文窗口计算自动阈值（70%）：
// maxContext 为空 / <= 0 → 回落 defaultContextWindowTokens（128000 → 89600）。
func autoCompressTriggerOf(maxContext *int) int {
	window := defaultContextWindowTokens
	if maxContext != nil && *maxContext > 0 {
		window = *maxContext
	}
	return window * autoCompressTriggerPercent / 100
}

// effectiveCompressTrigger 返回本轮实际生效的触发阈值（显式值优先，否则自动 70%）。
func effectiveCompressTrigger(explicit int, maxContext *int) int {
	if explicit > 0 {
		return explicit
	}
	return autoCompressTriggerOf(maxContext)
}

// isTurnBoundary 判断一条消息是否为 turn 起点（薄适配：口径唯一来源 = 组件）。
func isTurnBoundary(m ChatMsg) bool { return session.IsTurnBoundary(m) }

// turnTokenCounts 计算一轮的**完整态**与**简化态** token 数（轮次结束时预存）。
// 薄适配：估算口径唯一来源 = 组件。
func turnTokenCounts(turn []ChatMsg) (full, brief int) { return session.TurnTokenCounts(turn) }
