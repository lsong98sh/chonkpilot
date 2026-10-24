// `next_run` 自动续跑（NEXTRUN-1）。
//
// 设计见 docs/next-run/next_run自动续跑设计方案.md：
//   - 模型经 `next_run(prompt)` 把**下一轮提示词**登记为 inbox `next_turn`（source=llm，
//     turn_id=当前 turn）；本轮正常结束后由既有 `finish → consumeNextTurn` 起**新 turn**；
//   - 会话级计数器 `sessions.next_run_count`：用户发送即重置 0，每次成功受理 +1，
//     `>= nextRunMax`（默认 30，0 = 关闭）返回固定文案且不再登记；
//   - 同一 turn 多次调用**只保留最后一条**（就地覆盖 content，计数按次 +1）；
//   - **零新增 MQ 主题**：仅 `data-inbox-append/list/update` 可选字段 + 门面直调计数器。
package server

import (
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

const (
	// nextRunTool 是工具契约名（= 文件名）。
	nextRunTool = "next_run"
	// nextRunDefaultMax 是 `nextRunMax` 未配置时的默认上限（0 = 关闭）。
	nextRunDefaultMax = 30
	// nextRunKind 是消费时落库消息的 `kind`（来源可识别，仍属新 turn 边界的 user 消息）。
	nextRunKind = "next_run"
	// nextRunSourceLLM 是 inbox 行的来源标识。
	nextRunSourceLLM = "llm"
)

// llmNextRunMaxFrom 从已读 usr 配置折算 `nextRunMax`（缺省 30；显式 0 = 关闭）。
func llmNextRunMaxFrom(d map[string]any) int {
	max := nextRunDefaultMax
	if d == nil {
		return max
	}
	if n, ok := configInt(d, "nextRunMax"); ok && n >= 0 {
		max = n
	}
	return max
}

// runNextRun 实现 `next_run`（返回文本唯一准则见设计方案 §3.1）。
func (s *Server) runNextRun(tc *turnCtx, args map[string]any) (string, bool) {
	if tc == nil {
		return "域工具必须经 server turn 调用（turn 上下文缺失）", true
	}
	prompt := strings.TrimSpace(str(args["prompt"]))
	if prompt == "" {
		return "错误: prompt 必填", true
	}
	if n := len([]rune(prompt)); n > maxChatInputChars {
		return fmt.Sprintf("错误: prompt 超长（%d 字符，上限 %d）", n, maxChatInputChars), true
	}
	// 仅主会话：子聊天 / 群聊 / 子会话由各自调度器驱动（其 next_turn 不会被消费）。
	if len(tc.req.Parents) > 0 || s.sessionRow(tc.req.InstanceID, tc.req.Session).Kind != "" {
		return "next_run 仅支持主会话（子聊天 / 群聊 / 子会话由各自调度器驱动）", true
	}
	if s.cfg == nil {
		return "错误: 登记下一轮失败（数据门面不可用）", true
	}
	max := tc.nextRunMax
	if max <= 0 {
		return "next_run 未启用（自动接续上限为 0）", false
	}
	// ① 先计数（上限判定与自增原子完成）；拒绝时不 +1、不动已登记项。
	res, err := s.cfg.SessionNextRunCheckAndAdd(facade.SessionNextRunCheckAndAddRequest{
		InstanceID: tc.req.InstanceID, SessionID: tc.req.Session, Max: max,
		DialogID: s.dialogOf(tc.req.InstanceID, tc.req.Session),
	})
	if err != nil {
		return "错误: 登记下一轮失败（" + err.Error() + "）", true
	}
	if !res.Allowed {
		return fmt.Sprintf("已自动接续 %d 次，需用户判断是否继续", max), false
	}
	// ② 落 inbox（last-wins：本轮已登记 → 就地覆盖 content；否则新增一行）。
	tc.nextRunMu.Lock()
	itemID := tc.nextRunItemID
	tc.nextRunMu.Unlock()
	if itemID != "" {
		if err := s.inboxUpdate(tc.req.InstanceID, itemID, "", "", prompt); err != nil {
			s.nextRunCompensate(tc.req.InstanceID, tc.req.Session)
			return "错误: 登记下一轮失败（" + err.Error() + "）", true
		}
		if it, ok := s.inboxLookup(itemID); ok {
			it.Content = prompt
			s.inboxRemember(it)
		}
		return "OK", false
	}
	id, err := s.inboxAppend(tc.req.InstanceID, facade.InboxAppendRequest{
		SessionID: tc.req.Session, TurnID: tc.req.Turn, Type: "next_turn",
		Content: prompt, Source: nextRunSourceLLM,
		// 幂等键：同轮重试不产生第二行（本轮唯一 LLM 排队项）。
		IdempotencyKey: "next-run:" + tc.req.Turn,
	})
	if err != nil {
		s.nextRunCompensate(tc.req.InstanceID, tc.req.Session)
		return "错误: 登记下一轮失败（" + err.Error() + "）", true
	}
	tc.nextRunMu.Lock()
	tc.nextRunItemID = id
	tc.nextRunMu.Unlock()
	s.inboxRemember(facade.InboxItem{
		ID: id, SessionID: tc.req.Session, TurnID: tc.req.Turn, Type: "next_turn",
		Content: prompt, Status: "pending", Source: nextRunSourceLLM,
	})
	return "OK", false
}

// nextRunCompensate 回滚本次自增（inbox 落库 / 覆盖失败）。
func (s *Server) nextRunCompensate(instanceID, sessionID string) {
	if s.cfg == nil {
		return
	}
	if _, err := s.cfg.SessionNextRunDec(facade.SessionNextRunDecRequest{
		InstanceID: instanceID, SessionID: sessionID,
		DialogID: s.dialogOf(instanceID, sessionID),
	}); err != nil {
		logf("[chonkpilot-server] next_run 计数补偿失败（session=%s）: %v\n", sessionID, err)
	}
}

// resetNextRunCount 用户发送即把计数器归零（NEXTRUN-1 §3.3.3）。
//
// 触发点：外部 `llm-start`（用户发新轮 / 点「继续」）· `llm-send{type:text-user}` ·
// `llm-inbox-add` · `llm-inbox-promote`。**内部** `acceptStart`（consumeNextTurn 起自动轮）
// 不重置（否则自动链永远只能接续 1 次）。
//
// 返回 false = 未落地（如首轮自举前 dialog_id 尚未知 → `dialog_required`）；调用方可在建会话后补一次。
func (s *Server) resetNextRunCount(instanceID, sessionID string) bool {
	if s.cfg == nil || instanceID == "" || sessionID == "" {
		return true
	}
	if _, err := s.cfg.SessionNextRunReset(facade.SessionNextRunResetRequest{
		InstanceID: instanceID, SessionID: sessionID,
		DialogID: s.dialogOf(instanceID, sessionID),
	}); err != nil {
		// 重置失败不阻断用户发送（仅留痕；下次发送会再试）。
		logf("[chonkpilot-server] next_run 计数重置失败（session=%s）: %v\n", sessionID, err)
		return false
	}
	return true
}

// discardNextRunForTurn 终态收敛（NEXTRUN-1 §4）：本轮 error / interrupted / cancelled →
// 丢弃**本 turn** 的 LLM 排队项，防止用户结束本轮后"僵尸提示词"自动触发。
func (s *Server) discardNextRunForTurn(instanceID, sessionID, turnID string) {
	items, err := s.inboxList(instanceID, sessionID, "pending")
	if err != nil {
		logf("[chonkpilot-server] next_run 终态收敛读取失败（session=%s）: %v\n", sessionID, err)
		return
	}
	for _, it := range items {
		if it.Source != nextRunSourceLLM || it.TurnID != turnID {
			continue
		}
		if err := s.inboxUpdate(instanceID, it.ID, "discarded", "", ""); err != nil {
			logf("[chonkpilot-server] next_run 终态丢弃失败（id=%s）: %v\n", it.ID, err)
			continue
		}
		s.inboxSetStatus(it.ID, "discarded")
	}
}
