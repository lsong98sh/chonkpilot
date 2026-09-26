// 三段边界定位（对齐 docs/spec/20-modules/28-plugins.md §3.1）：
// 快照内向前探索，不读 messages 表——快照内容本身即历史（含完整 turn 消息）。
//
// 2026-09-21（阶段 4 前置·类型双轨收敛，41 G-33）：本文件纯函数一律用**门面 DTO**
// （`facade.Message`）——消费方只认门面领域模型，不再翻回内核类型（协议嵌套
// `function{name,arguments}`、`_meta` 等消息面/存储形状留在数据组件内部，见 23 §7）。
//
// 2026-09-25（口径 X，三段结构）：边界算法唯一来源 = `data.LocateZones`（**与组装侧同一函数**）——
// 完整区（本轮 + 最近 N 轮，N/M 取先到）/ 简化区（再往前，brief token 累计 <= T）/
// 摘要区（更早；压缩 = 摘要）。简化态构造 = `data.BriefFacadeMessages`（**仅 text**，
// 与组装侧 `data.BriefMessages` 同源）。
package compress

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// isUserText 判定"用户发起消息"（turn 起点）：role=user 且非系统注入消息。
// kind 缺失（旧快照）→ 视为 text（向后兼容）；显式 kind=notify（工具通知）/
// continue（自动续写）/ resume（恢复续轮）才排除——口径与 chonkpilot-llm 组装侧
// isTurnBoundary 一致（只认标记、不认内容；"继续"文本的真实提问仍计入）。
func isUserText(m facade.Message) bool {
	if m.Role != "user" {
		return false
	}
	switch m.Kind {
	case "notify", "continue", "resume":
		return false
	}
	return true
}

// Zones 是三段**消息下标**边界（升序）：
//
//	摘要区 = msgs[:SummaryStart]（prelude 既有摘要 + 更早轮）→ 压缩为摘要；
//	简化区 = msgs[SummaryStart:BriefStart]... 见下（SummaryStart == BriefStart 时简化区为空）；
//	完整区 = msgs[FullStart:]。
//
// 无摘要区（SummaryStart == BriefStart）→ 无可压缩内容。
type Zones struct {
	FullStart    int // 完整区起点（该下标及之后全保留完整原文）
	BriefStart   int // 简化区起点（简化区 = [BriefStart, FullStart)）
	SummaryStart int // 摘要区起点（摘要区 = [SummaryStart, BriefStart)；无摘要区时 == BriefStart）
}

// LocateThreeZones 三段定位（**压缩侧唯一定位入口**，算法唯一来源 = `data.LocateZones`）。
//
// 返回消息下标 `Zones`：
//   - 完整区 = msgs[FullStart:]（本轮恒保留、N/M 取先到；两条件均不启用 → FullStart = 首轮起点 = 无压缩）；
//   - 简化区 = msgs[BriefStart:FullStart]（brief token 累计 <= briefBudget=T）；
//   - 摘要区 = msgs[SummaryStart:BriefStart]（**turn 维度**：首轮起点起、到简化区前；
//     首轮之前的 prelude（既有 system 摘要等）不计入摘要区，单独随压缩合并）；
//   - **无摘要区** → SummaryStart == BriefStart（调用方据此判定"无可压缩内容"）。
//
// storedFull / storedBrief = 各轮**预存**完整态 / 简化态 token（升序、末尾=最新轮；可只覆盖最近
// 若干轮，也可比本轮数**长**——如全会话 turn_tokens 而快照仅留尾部，按尾部对齐，见
// `data.ResolveStoredTokens`）；缺值回退实时估算（估算口径与预存同源：完整态 = 全消息、
// 简化态 = `data.BriefFacadeMessages`）。
func LocateThreeZones(msgs []facade.Message, maxTurns, maxTokens, briefBudget int, storedFull, storedBrief []int) Zones {
	if len(msgs) == 0 {
		return Zones{}
	}
	// turn 起点下标（升序）：role=user 且 kind 非 notify/continue/resume（isUserText）。
	var bounds []int
	for i, m := range msgs {
		if isUserText(m) {
			bounds = append(bounds, i)
		}
	}
	if len(bounds) == 0 {
		// 无轮边界（仅 system/摘要）→ 无轮可压缩：全量视为"完整区"。
		return Zones{FullStart: 0, BriefStart: 0, SummaryStart: 0}
	}
	// 各轮完整态 / 简化态 token（升序，末尾 = 最新轮）→ 预存值优先、缺值回退估算 → 共享三段算法。
	full := make([]int, len(bounds))
	brief := make([]int, len(bounds))
	for i, from := range bounds {
		to := len(msgs)
		if i+1 < len(bounds) {
			to = bounds[i+1]
		}
		turn := msgs[from:to]
		full[i] = data.EstimateTokensOfFacadeMessages(turn)
		brief[i] = data.EstimateTokensOfFacadeMessages(data.BriefFacadeMessages(turn))
	}
	full = data.ResolveStoredTokens(storedFull, full)
	brief = data.ResolveStoredTokens(storedBrief, brief)
	z := data.LocateZones(full, brief, maxTurns, maxTokens, briefBudget)
	n := len(bounds)
	fullStart := bounds[n-z.FullTurns]
	// 简化区 / 摘要区在**轮维度**切分（prelude 不参与）。
	briefStart := fullStart
	if z.BriefTurns > 0 {
		briefStart = bounds[n-z.FullTurns-z.BriefTurns]
	}
	summaryStart := briefStart
	if summaryIdx := n - z.FullTurns - z.BriefTurns; summaryIdx > 0 {
		summaryStart = bounds[0] // 存在摘要区（首个未纳入简化区的更早轮 = 首轮）
	}
	return Zones{FullStart: fullStart, BriefStart: briefStart, SummaryStart: summaryStart}
}

// EstimateTokens 粗略估算快照 token 数（压缩阈值判定用，非精确计数）。
// 口径唯一来源 = chonkpilot-data（字符数/2）；收敛后记忆库（plugin-memory）与压缩共用同一
// 口径，不再各写一套（41 I-21）——门面 DTO 入口见 data.EstimateTokensOfFacadeMessages。
// 导出供独立测试模块与内部复用。
func EstimateTokens(msgs []facade.Message) int {
	return data.EstimateTokensOfFacadeMessages(msgs)
}
