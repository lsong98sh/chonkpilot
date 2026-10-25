// data-<domain>-* 消息面的**信封层**：解析 wire 载荷 → 调用对应域门面实现 → 按 61 §3 形状回载荷。
//
// 位置（23-工程与部署拓扑 §7）：门面实现已下沉 `chonkpilot-data/internal/<域>/`（本文件不承载
// 任何域逻辑）——故 MQ 绑定与门面 inline 绑定是**同一份实现**（行为等价有测试）。应答走
// persist 统一格式 {req_id, ok, result:{...}}（§3 头：publish + Promise 收集 result）。
//
// 各域信封位置（2026-10-25：按域拆分，本文件只留 config / session / snapshot）：
//
//	本文件：config 四域（prj-config/prompt/prj-security/user-config）+ session 全域 + snapshot
//	scenario.go / mcp.go / tasktree.go / knowledge.go / memory.go / filelist.go / index.go
//	inbox.go / turnevent.go / ask.go（内部面）
//	载荷定死结构：payload.go（`{data:{…}}` 与顶层扁平两形都接受，`data` 优先、顶层补缺）
package persist

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/config"
	"github.com/chonkpilot/chonkpilot-data/internal/snapshot"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// ─── config 四域（prj-config / prompt / prj-security）───────────────

// handleConfigKV 处理 data-prj-config-* / data-prompt-* / data-prj-security-*（配置 kv 域 MQ 面）。
//
// 2026-10-25（payload struct 化第一批）：载荷按 `ConfigPayload` **定死解析**（不再 map 宽松取值）；
// 逻辑仍在 internal/config（门面实现），本 handler 只做信封（reply/fail，61 §3.1 应答形状不变）。
func (s *Service) handleConfigKV(domain, op string, payload []byte) {
	method := "data-" + domain + "-" + op
	var req ConfigPayload
	if err := json.Unmarshal(payload, &req); err != nil {
		s.failID(method, req.ReqID, fmt.Errorf("payload: %w", err))
		return
	}
	switch op {
	case "list":
		resp, err := s.ConfigKVList(facade.ConfigKVListRequest{Domain: domain, InstanceID: req.InstanceID})
		if err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldList: resp.List})
	case "load":
		id := req.key()
		resp, err := s.ConfigKVGet(facade.ConfigKVGetRequest{
			Domain: domain, InstanceID: req.InstanceID, Keys: []string{id},
		})
		if err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldData: resp.Values[id]})
	case "save":
		entries, id, ok := configEntries(req.Data)
		if !ok {
			s.failID(method, req.ReqID, errors.New("key required"))
			return
		}
		if _, err := s.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain: domain, InstanceID: req.InstanceID, Entries: entries,
		}); err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: id})
	case "delete":
		id := req.key()
		if _, err := s.ConfigKVDelete(facade.ConfigKVDeleteRequest{
			Domain: domain, InstanceID: req.InstanceID, Keys: []string{id},
		}); err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldOk: true})
	}
}

// handleUserConfig 处理 data-user-config-{list,load,save,delete}（存储 = 逐 key + 专用表）。
//
// 2026-10-25（payload struct 化第一批）：载荷按 `UserConfigPayload` 定死解析。
func (s *Service) handleUserConfig(op string, payload []byte) {
	method := "data-user-config-" + op
	var req UserConfigPayload
	if err := json.Unmarshal(payload, &req); err != nil {
		s.failID(method, req.ReqID, fmt.Errorf("payload: %w", err))
		return
	}
	switch op {
	case "list":
		resp, err := s.UserConfigView(facade.UserConfigViewRequest{})
		if err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldList: resp.List})
	case "load":
		// load 返回**合并后**的有效值：usr 基线 + 项目层（prjusr → prj）覆盖可继承键
		// （defaultLLM/defaultScenario）。list 仍为 usr 主库视图（"usr 是否已有配置"语义）。
		resp, err := s.UserConfigGet(facade.UserConfigGetRequest{InstanceID: req.InstanceID})
		if err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldData: resp.Config})
	case "save":
		if _, err := s.UserConfigSet(facade.UserConfigSetRequest{
			Entries: req.Data, InstanceID: req.InstanceID,
		}); err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: config.UserConfigID})
	case "delete":
		// 带 key/id = 删除该项（继承控件「重置继承」→ 回落上级）；无 key = 清空整份用户配置；
		// 未知键由实现侧**明确报错**（P0：未知键曾把 theme/locale/llms/超时一并清掉），不兜底清空。
		key := req.key()
		keys := []string{}
		if key != "" {
			keys = append(keys, key)
		}
		if _, err := s.UserConfigDelete(facade.UserConfigDeleteRequest{
			Keys: keys, InstanceID: req.InstanceID,
		}); err != nil {
			s.failID(method, req.ReqID, err)
			return
		}
		if key != "" {
			s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: key})
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldOk: true})
	}
}

// ─── scenario / mcp ──────────────────────────────────────────────
//
// 两域 handler 已按域拆分为独立文件（scenario.go / mcp.go）。

// ─── session 全域 ────────────────────────────────────────────────

// handleSession 分发 data-session-<action>（信封层：解析 → 门面 → 回载荷）；
// 未知 action（含 -refresh 误入）忽略不回复。
//
// 2026-10-25（payload struct 化第三批）：载荷按 `SessionPayload` 定死解析（`{data:{…}}` 与顶层
// 扁平两形都接受，`data` 优先、顶层补缺）；标量用 ID/IntValue/BoolValue 兼容历史「字符串与数字」两形。
// 例外：`append-message`（`msg` 为多态嵌套对象）仍按 map 归一（翻译收在 wire.MessageAppendFromWire），
// 待 append 面后续批次收口。
func (s *Service) handleSession(action string, payload []byte) {
	method := "data-session-" + action
	var p SessionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, fmt.Errorf("payload: %w", err))
		return
	}
	f := p.fields()
	okf := func(result map[string]any) { s.replyID(method, p.ReqID, result) }
	failf := func(format string, args ...any) { s.failID(method, p.ReqID, fmt.Errorf(format, args...)) }
	// sid：data.session_id → 顶层 id（与旧 sessionIDOf 同口径）。
	sid := f.SessionID
	if sid == "" {
		sid = string(f.ID)
	}

	switch action {
	case "list":
		resp, err := s.SessionList(facade.SessionListRequest{InstanceID: p.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionListResult(resp.List))
	case "get":
		resp, err := s.SessionGet(facade.SessionGetRequest{
			InstanceID: p.InstanceID, SessionID: sid, DialogID: f.DialogID,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionGetResult(resp.Found, resp.Session))
	case "history":
		resp, err := s.TurnHistory(facade.TurnHistoryRequest{
			InstanceID: p.InstanceID, SessionID: sid, DialogID: f.DialogID,
			BeforeTurnID: f.BeforeTurnID, TargetMessages: int(f.TargetMessages), TargetBytes: int(f.TargetBytes),
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.TurnHistoryResult(resp))
	case "latest":
		resp, err := s.SessionLatest(facade.SessionLatestRequest{InstanceID: p.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionLatestResult(resp.SessionID, resp.DialogID))
	case "title":
		if _, err := s.SessionTitle(facade.SessionTitleRequest{
			InstanceID: p.InstanceID, SessionID: string(f.ID), DialogID: f.DialogID, Title: f.Title,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "delete":
		if _, err := s.SessionDelete(facade.SessionDeleteRequest{
			InstanceID: p.InstanceID, SessionID: string(f.ID), DialogID: f.DialogID,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "fork":
		resp, err := s.SessionFork(facade.SessionForkRequest{
			InstanceID: p.InstanceID,
			DialogID:   f.DialogID,
			Mode:       f.Mode,
			Title:      f.Title,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{"ok": resp.OK, "dialog_id": resp.DialogID, "session_id": resp.SessionID})
	case "active-set":
		if _, err := s.SessionActiveSet(facade.SessionActiveSetRequest{
			InstanceID: p.InstanceID, SessionID: sid, DialogID: f.DialogID,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "active-get":
		resp, err := s.SessionActiveGet(facade.SessionActiveGetRequest{InstanceID: p.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionIDResult(resp.SessionID))
	case "content":
		resp, err := s.MessageContent(facade.MessageContentRequest{
			InstanceID: p.InstanceID, SessionID: sid, DialogID: f.DialogID, Keys: f.Keys,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.MessageContentResult(resp.Contents))
	// A3 运行时扩展（server sessionStore 总线化原语；语义对齐 chonkpilot-server/session.go）
	case "ensure-session":
		resp, err := s.SessionEnsure(facade.SessionEnsureRequest{
			InstanceID:      p.InstanceID,
			SessionID:       sid,
			DialogID:        f.DialogID,
			ParentSessionID: f.ParentSessionID,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		res := wire.OKResult()
		if resp.DialogID != "" {
			res["dialog_id"] = resp.DialogID // P3 过渡期：根会话自举回带（旧布局不写键）
		}
		okf(res)
	case "ensure-turn":
		if _, err := s.TurnEnsure(facade.TurnEnsureRequest{
			InstanceID: p.InstanceID,
			TurnID:     f.TurnID,
			SessionID:  sid,
			DialogID:   f.DialogID,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "append-message":
		// `msg` = 多态嵌套对象（ChatMsg + 若干领域字段），且允许「消息字段直接平铺在载荷顶层」；
		// 该形暂按 map 归一（见函数头注释），翻译仍收在 wire.MessageAppendFromWire。
		var raw map[string]any
		if err := json.Unmarshal(payload, &raw); err != nil {
			failf("payload: %v", err)
			return
		}
		m := map[string]any{}
		if d, ok := raw["data"].(map[string]any); ok {
			for k, v := range d {
				m[k] = v
			}
		}
		for k, v := range raw {
			if k == "data" {
				continue
			}
			if _, ok := m[k]; !ok {
				m[k] = v
			}
		}
		r := wire.MessageAppendFromWire(m)
		r.InstanceID = p.InstanceID
		if r.SessionID == "" {
			r.SessionID = sid
		}
		resp, err := s.MessageAppend(r)
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: resp.ID})
	case "set-summary":
		if _, err := s.TurnSetSummary(facade.TurnSetSummaryRequest{
			InstanceID: p.InstanceID,
			TurnID:     f.TurnID,
			DialogID:   f.DialogID,
			Summary:    f.Summary,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "complete-turn":
		// P3（2026-09-25）：payload 只增字段 full_tokens/brief_tokens（可选；缺省 = 不写该键，
		// 向后兼容——ReopenTurn 等不携带即保持原行为）。
		if _, err := s.TurnComplete(facade.TurnCompleteRequest{
			InstanceID:   p.InstanceID,
			TurnID:       f.TurnID,
			DialogID:     f.DialogID,
			Status:       f.Status,
			FinishReason: f.FinishReason,
			FullTokens:   f.FullTokens,
			BriefTokens:  f.BriefTokens,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "cleanup-stale":
		resp, err := s.TurnCleanupStale(facade.TurnCleanupStaleRequest{InstanceID: p.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.CleanupResult(resp.Count))
	case "load-messages":
		resp, err := s.MessageLoad(facade.MessageLoadRequest{
			InstanceID: p.InstanceID, TurnID: f.TurnID, DialogID: f.DialogID,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.MessageLoadResult(resp.Messages))
	case "context":
		resp, err := s.MessageContext(facade.MessageContextRequest{
			InstanceID:      p.InstanceID,
			SessionID:       sid,
			DialogID:        f.DialogID,
			ExcludeTurn:     f.ExcludeTurn,
			IncludeSnapshot: bool(f.IncludeSnapshot),
		})
		if err != nil {
			failf("%v", err)
			return
		}
		// P3（2026-09-25）：结果载荷**只增**伴随数组 turn_tokens（每项 {turn_id,full,brief}）；
		// 无可带项时**不写该键** → 与旧载荷逐字节等价（旧订阅方零影响）。
		okf(wire.ContextResult(resp.Messages, resp.TurnTokens))
	// ── 群聊两表（SUBCHAT-3 阶段 E，2026-10-16；prjusr：group_participants /
	// group_round_targets）──
	// 载荷接受扁平与 `{data:{…}}` 两形（fields() 已合并）。**不广播 -refresh**（名册/进度类表；
	// 读方 = 群聊调度器，按其节奏读）。
	case "group-participants-load":
		resp, err := s.GroupParticipantsLoad(facade.GroupParticipantsLoadRequest{
			InstanceID: p.InstanceID, SessionID: sid,
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.GroupParticipantsResult(resp.List))
	case "group-participants-save":
		list := make([]facade.GroupParticipant, 0, len(f.GroupParticipants))
		for _, it := range f.GroupParticipants {
			if it.Agent == "" {
				continue
			}
			list = append(list, facade.GroupParticipant{SessionID: sid, Agent: it.Agent, Role: it.Role})
		}
		if _, err := s.GroupParticipantsSave(facade.GroupParticipantsSaveRequest{
			InstanceID: p.InstanceID, SessionID: sid, List: list,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "group-round-targets-load":
		resp, err := s.GroupRoundTargetsLoad(facade.GroupRoundTargetsLoadRequest{
			InstanceID: p.InstanceID, SessionID: sid, Round: int(f.Round),
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.GroupRoundTargetsResult(resp.List))
	case "group-round-targets-save":
		if _, err := s.GroupRoundTargetsSave(facade.GroupRoundTargetsSaveRequest{
			InstanceID: p.InstanceID, SessionID: sid,
			Round:  int(f.Round),
			Agents: f.Agents,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	}
}

// ─── snapshot ────────────────────────────────────────────────────

// handleSnapshot 分发 data-snapshot-<action>（get/set）。未知 action 忽略不回复。
//
// 2026-10-25（payload struct 化第二批）：载荷按 `SnapshotPayload` 定死解析（不再 map 宽松取值）；
// 落库仍经 internal/snapshot（与门面 inline 绑定同源）。
func (s *Service) handleSnapshot(action string, payload []byte) {
	method := "data-snapshot-" + action
	var req SnapshotPayload
	if err := json.Unmarshal(payload, &req); err != nil {
		s.failID(method, req.ReqID, fmt.Errorf("payload: %w", err))
		return
	}
	failf := func(format string, args ...any) { s.failID(method, req.ReqID, fmt.Errorf(format, args...)) }
	catalog, err := s.sessionDBBy(req.InstanceID)
	if err != nil {
		s.failID(method, req.ReqID, err)
		return
	}
	sessionID := req.sessionID()
	if sessionID == "" {
		failf("session_id required")
		return
	}
	// 布局选择（P3 路由）：带 dialog_id → 对话库（快照与轮次/消息同库，缺失即报错）；
	// 空 → 旧布局：快照落会话库（2026-10-23 轻量分片），prjusr 只留会话目录行。
	dialogID := req.Data.DialogID
	var sidb *data.DB
	var release func()
	if dialogID != "" {
		sidb, release, err = data.OpenExistingDialog(catalog.Path(), dialogID)
	} else {
		sidb, release, err = data.OpenSession(catalog.Path(), sessionID)
	}
	if err != nil {
		failf("%v", err)
		return
	}
	defer release()
	if dialogID == "" {
		if err := snapshot.MigrateFromCatalog(catalog, sidb, sessionID); err != nil {
			failf("%v", err)
			return
		}
	}
	switch action {
	case "get":
		snap, ok, err := snapshot.Get(sidb, sessionID)
		if err != nil {
			failf("%v", err)
			return
		}
		if !ok {
			s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldSnapshot: nil}) // 无快照/无记录 → snapshot=null（调用方回退 BuildHistory）
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldSnapshot: snap})
	case "set":
		// 缺 `snapshot` 字段（或空对象）→ 明确失败：否则会写入零值快照并可能补建会话行
		// （与门面 SnapshotSet 的入参校验对齐：不得静默落零值）。
		raw, has := req.snapshotObject()
		if !has {
			failf("snapshot required")
			return
		}
		var snap data.Snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			failf("snapshot: %v", err)
			return
		}
		if dialogID != "" {
			if err := snapshot.SetInDialog(sidb, sessionID, snap); err != nil {
				failf("%v", err)
				return
			}
		} else if err := snapshot.Set(catalog, sidb, sessionID, snap); err != nil {
			failf("%v", err)
			return
		}
		s.replyID(method, req.ReqID, map[string]any{msgkeys.FieldOk: true})
	}
}

// ─── tasktree / knowledge / memory ───────────────────────────────
//
// 三域 handler 已按域拆分为独立文件（tasktree.go / knowledge.go / memory.go）。

// ─── filelist / index ────────────────────────────────────────────
//
// 两域 handler 已按域拆分为独立文件（filelist.go / index.go）。
