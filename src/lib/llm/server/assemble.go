// 上下文拼接的**宿主侧配置读取**（三段结构的内置固定规则，2026-09-12 取代原
// `context.assemble.*` 可配置方案）。
//
// SESSION-1 阶段二：投影 / turn 切分 / reasoning 规则已下沉到纯 lib 组件 `chonkpilot-session`
// （唯一实现，见 `src/lib/session/{turns,project}.go`）；本文件只保留：
//   - 三段边界的项目级配置键与读值（`keep_full_max_turns` / `keep_full_max_tokens` /
//     `compress_token_threshold`）——与压缩侧同键、同域、同读序；
//   - 会话注入消息的 Kind 常量（取值唯一来源 = 组件）；
//   - 两个薄适配（`isTurnBoundary` / `turnTokenCounts`，供本包既有调用点使用）。
//
// 语义摘要（与组件注释一致）：
//   - 完整区（最近 N 轮 / M token 内，**本轮恒保留**）→ 整轮原文；
//   - 简化区（再往前，brief token 累计 <= T）→ 仅 text；
//   - 摘要区（更早）→ 已压缩为前导 system 摘要（原样透传）；残留 → 同简化态投影；
//   - 被重试 / 恢复的 turn → 强制全量（forceFull）。
package server

import (
	"strconv"
	"strings"

	"github.com/lsong98sh/chonkpilot-session"
)

// defaultKeepFullTurns 完整区轮数缺省值（对应项目级键 `keep_full_max_turns`；与压缩侧
// DefaultOptions.RetainTurns 一致）。
const defaultKeepFullTurns = 10

// defaultKeepFullTokens 完整区**完整态 token 上限**缺省值（对应项目级键 `keep_full_max_tokens`；
// 与压缩侧 DefaultOptions.KeepFullTokens 一致）。
const defaultKeepFullTokens = 24000

// defaultBriefBudget 简化区 brief token 预算缺省值（对应项目级键 `compress_token_threshold`；
// 与压缩侧 DefaultOptions.TokenMax 一致）——组装侧判简化区用**同一键**（三段边界两侧一致）。
const defaultBriefBudget = 20000

// 系统注入的用户消息 Kind（不新开 turn 边界——仍属当前 turn；取值唯一来源 = 组件）：
//   - assembleContinueKind：S6 断链/length 自动续写注入的"继续"；
//   - assembleResumeKind：重启/死 turn 恢复续轮注入的 user 消息（恢复路径如注入用户消息用此标记）。
const (
	assembleContinueKind = session.KindContinue
	assembleResumeKind   = session.KindResume
)

// assembleContinueText 自动续写注入的用户消息文本（配合 Kind=assembleContinueKind）。
const assembleContinueText = session.ContinueText

// 三段边界项目级键（2026-09-24，D1 加 token 上限；2026-09-25 加简化区预算 T）：
// `keep_full_max_turns` / `keep_full_max_tokens` / `compress_token_threshold`。
const (
	keepFullMaxTurnsKey  = "keep_full_max_turns"
	keepFullMaxTokensKey = "keep_full_max_tokens"
	briefBudgetKey       = "compress_token_threshold"
)

// loadKeepFullTurns 读项目级维持轮数 `keep_full_max_turns`（缺失/非数字 → 10；启动时加载一次）。
//
// 与压缩侧 resolveOpts 同键、同域、同读序（prjusr → prj → usr，见 prjConfigValues）。
//
// 取值语义（2026-09-25 口径 W，**与压缩侧 resolveOpts 同口径**）：键**存在且为数字** → 原样采用
// （含 `0` = 该条件不启用、负数 = 非法按不启用处理）；**缺失/空/非数字** → 回落默认 10。
func (s *Server) loadKeepFullTurns(instanceID string) int {
	return keepFullTurnsOf(s.prjConfigValues(instanceID, keepFullMaxTurnsKey))
}

// keepFullTurnsOf 从**已读** prj 配置值表折算维持轮数（纯函数，不再读配置）。
func keepFullTurnsOf(vals map[string]string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(vals[keepFullMaxTurnsKey])); err == nil {
		return n
	}
	return defaultKeepFullTurns
}

// loadKeepFullTokens 读项目级维持轮**完整态 token 上限** `keep_full_max_tokens`
// （缺失/非数字 → 24000；启动时加载一次）——与压缩插件同键、同域、同读序。
//
// 取值语义（2026-09-25 口径 W，**与压缩侧 resolveOpts 同口径**）：键**存在且为数字** → 原样采用
// （含 `0` = 该条件不启用、负数 = 非法按不启用处理）；**缺失/空/非数字** → 回落默认 24000。
func (s *Server) loadKeepFullTokens(instanceID string) int {
	return keepFullTokensOf(s.prjConfigValues(instanceID, keepFullMaxTokensKey))
}

// keepFullTokensOf 从**已读** prj 配置值表折算维持轮完整态 token 上限（纯函数，不再读配置）。
func keepFullTokensOf(vals map[string]string) int {
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
	return briefBudgetOf(s.prjConfigValues(instanceID, briefBudgetKey))
}

// briefBudgetOf 从**已读** prj 配置值表折算简化区 brief token 预算（纯函数，不再读配置）。
func briefBudgetOf(vals map[string]string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(vals[briefBudgetKey])); err == nil {
		return n
	}
	return defaultBriefBudget
}

// isTurnBoundary 判断一条消息是否为 turn 起点（薄适配：口径唯一来源 = 组件）。
func isTurnBoundary(m ChatMsg) bool { return session.IsTurnBoundary(m) }

// turnTokenCounts 计算一轮的**完整态**与**简化态** token 数（P3/P4 轮次结束时预存；
// 薄适配：估算口径唯一来源 = 组件，与组装侧投影 / 压缩侧预算同一构造）。
func turnTokenCounts(turn []ChatMsg) (full, brief int) { return session.TurnTokenCounts(turn) }
