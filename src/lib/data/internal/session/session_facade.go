// session / turn / message 三域门面实现（`chonkpilot-data/facade` 的 SessionAPI / TurnAPI /
// MessageAPI）：会话 CRUD 与查询 + 轮次 + 消息（= `data-session-*` 全量 promise 面）。
//
// 位置（23-工程与部署拓扑 §7「门面 = 翻译层」）：本文件是**实现侧**——把「域名 + 领域标识」
// 翻译成 prjusr 库的表访问（sessions / turns / messages），再落到既有视图与消息 helper。
//
// 阶段 4「internal 下沉」：本文件由 `chonkpilot-data/persist` 整体下移（逻辑逐字未改；
// 视图/消息 helper 取自 internal/kernel，M 表落库键取自 kernel.NewMessageKey）。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/snapshot"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// 编译期断言：实现完整 session / turn / message 三域门面面（缺方法即编译不过）。
var (
	_ facade.SessionAPI = (*Service)(nil)
	_ facade.TurnAPI    = (*Service)(nil)
	_ facade.MessageAPI = (*Service)(nil)
)

// sessionActivity 会话活动时间（最近活动优先，回落更新时间；与既有排序口径一致）。
func sessionActivity(s facade.Session) string {
	if s.LastActivity != "" {
		return s.LastActivity
	}
	return s.UpdatedAt
}

// sortSessionsByActivity 按最近活动降序稳定排序（data-session-list）。
func sortSessionsByActivity(list []facade.Session) {
	sort.SliceStable(list, func(i, j int) bool {
		return sessionActivity(list[i]) > sessionActivity(list[j])
	})
}

// emitSessionNew 广播会话创建事件 session-new（会话行首次落库成功后发；幂等由 SessionEnsure
// 的存在性检查保证）。payload = {instance_id, session_id}，子会话再带 {parent_session_id}
// （snake_case，61-消息一览 §命名约定）；桥按未映射主题原名直通前端 type。
func (s *Service) emitSessionNew(sessionID, parentSessionID, instanceID string) {
	payload := map[string]any{"instance_id": instanceID, "session_id": sessionID}
	if parentSessionID != "" {
		payload["parent_session_id"] = parentSessionID
	}
	b, _ := json.Marshal(payload)
	_ = s.Bus.Emit(context.Background(), msgkeys.TopicSessionNew, b)
}

// emitSessionTitleChanged 广播会话标题变更事件 data-session-title-changed（61 §3.2）：由
// SessionTitle 在写库**成功后**发出。payload = {session_id, title}，**不带 instance_id**
// —— 61 §3.2 明载「不带 instance_id → 全局」：每个窗口的桥各自转发 → **所有窗口都收到**
// （含**另一 instance 的对话窗口**，即本主题的用途；发起窗口重复应用幂等）。用途 = 主窗侧
// 改会话标题 → 已开对话窗口标题跟随（G-48 ⑦ / #12）；与 data-<domain>-refresh 家族的区别
// 正在于此（后者带 instance_id → 只到来源窗口）。
func (s *Service) emitSessionTitleChanged(sessionID, title string) {
	if s == nil || s.Bus == nil || sessionID == "" {
		return // 无总线（测试/无订阅场景）或空会话 id：无接收方/无内容，静默跳过
	}
	b, _ := json.Marshal(map[string]any{"session_id": sessionID, "title": title})
	_ = s.Bus.Emit(context.Background(), msgkeys.TopicDataSessionTitleChanged, b)
}

// ── session 域 ────────────────────────────────────────────────────

// SessionList 读顶层会话列表（仅 parent_id 为空的行；按最近活动降序）。
func (s *Service) SessionList(req facade.SessionListRequest) (facade.SessionListResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionListResponse{}, err
	}
	recs, _, err := prj.Table("sessions").Query(data.Query{OrderBy: "created_at", OrderDesc: true})
	if err != nil {
		return facade.SessionListResponse{}, fmt.Errorf("list sessions: %v", err)
	}
	list := make([]facade.Session, 0, len(recs))
	for _, r := range recs {
		row := kernel.RecordView(r, "session_id")
		if kernel.Sval(row["parent_id"]) != "" {
			continue // 仅顶层会话
		}
		list = append(list, wire.SessionFromWire(row))
	}
	sortSessionsByActivity(list)
	return facade.SessionListResponse{List: list}, nil
}

// SessionGet 读单个会话详情（不存在 → Found=false，不是错误）。
func (s *Service) SessionGet(req facade.SessionGetRequest) (facade.SessionGetResponse, error) {
	if req.SessionID == "" {
		return facade.SessionGetResponse{}, errors.New("session: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionGetResponse{}, err
	}
	var rec data.Record
	ok, err := prj.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		return facade.SessionGetResponse{}, err
	}
	if !ok {
		return facade.SessionGetResponse{}, nil
	}
	sess := wire.SessionFromWire(kernel.RecordView(rec, "session_id"))
	return facade.SessionGetResponse{Found: true, Session: sess}, nil
}

// SessionLatest 读最近活动顶层会话 id（无 → 空串）。
func (s *Service) SessionLatest(req facade.SessionLatestRequest) (facade.SessionLatestResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionLatestResponse{}, err
	}
	rec, err := kernel.LatestTopSession(prj)
	if err != nil {
		return facade.SessionLatestResponse{}, err
	}
	id := ""
	if rec != nil {
		id = kernel.Sval(rec[data.KeyField])
	}
	return facade.SessionLatestResponse{SessionID: id}, nil
}

// SessionTitle 改会话标题（刷新更新时间）；写库**成功后**广播 data-session-title-changed
// （61 §3.2：不带 instance_id = 全局 → 已开的**对话窗口**标题跟随；失败不广播）。
func (s *Service) SessionTitle(req facade.SessionTitleRequest) (facade.SessionTitleResponse, error) {
	if req.SessionID == "" {
		return facade.SessionTitleResponse{}, errors.New("session: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionTitleResponse{}, err
	}
	var rec data.Record
	ok, err := prj.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		return facade.SessionTitleResponse{}, err
	}
	if !ok {
		return facade.SessionTitleResponse{}, fmt.Errorf("session not found: %s", req.SessionID)
	}
	rec["title"] = req.Title
	rec["updated_at"] = kernel.RFC3339Now()
	if err := prj.Table("sessions").Update(req.SessionID, rec); err != nil {
		return facade.SessionTitleResponse{}, err
	}
	s.emitSessionTitleChanged(req.SessionID, req.Title)
	return facade.SessionTitleResponse{OK: true}, nil
}

// SessionDelete 删会话及其轮次/消息（不存在 → OK=true 幂等）；若删的是活动会话则清活动态。
func (s *Service) SessionDelete(req facade.SessionDeleteRequest) (facade.SessionDeleteResponse, error) {
	if req.SessionID == "" {
		return facade.SessionDeleteResponse{}, errors.New("session: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionDeleteResponse{}, err
	}
	var check data.Record
	if ok, _ := prj.Table("sessions").Get(req.SessionID, &check); !ok {
		return facade.SessionDeleteResponse{OK: true}, nil // 不存在视为成功（幂等）
	}
	if err := prj.Table("sessions").Delete(req.SessionID); err != nil {
		return facade.SessionDeleteResponse{}, err
	}
	for _, table := range []string{"turns", "messages"} {
		recs, _, err := prj.Table(table).Query(data.Query{Where: data.Record{"session_id": req.SessionID}})
		if err != nil {
			continue
		}
		for _, r := range recs {
			_ = prj.Table(table).Delete(kernel.Sval(r[data.KeyField]))
		}
	}
	if cur, _ := kernel.PrjConfigVal(prj, "active_session_id"); cur == req.SessionID {
		_ = kernel.PrjConfigSetVal(prj, "active_session_id", "")
	}
	return facade.SessionDeleteResponse{OK: true}, nil
}

// SessionActiveSet 写「当前活动会话」（活动态单值存 prj config 表 active_session_id）。
func (s *Service) SessionActiveSet(req facade.SessionActiveSetRequest) (facade.SessionActiveSetResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionActiveSetResponse{}, err
	}
	if err := kernel.PrjConfigSetVal(prj, "active_session_id", req.SessionID); err != nil {
		return facade.SessionActiveSetResponse{}, err
	}
	return facade.SessionActiveSetResponse{OK: true}, nil
}

// SessionActiveGet 读「当前活动会话」：已失效（会话不存在）→ 回落最近活动顶层会话。
func (s *Service) SessionActiveGet(req facade.SessionActiveGetRequest) (facade.SessionActiveGetResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionActiveGetResponse{}, err
	}
	id := ""
	if v, _ := kernel.PrjConfigVal(prj, "active_session_id"); v != "" {
		var rec data.Record
		if ok, _ := prj.Table("sessions").Get(v, &rec); ok {
			id = v
		}
	}
	if id == "" {
		rec, err := kernel.LatestTopSession(prj)
		if err == nil && rec != nil {
			id = kernel.Sval(rec[data.KeyField])
		}
	}
	return facade.SessionActiveGetResponse{SessionID: id}, nil
}

// SessionEnsure 幂等建会话（已存在直接返回，不覆盖、不重复广播）；**首次**落库成功后
// 广播 session-new（主/子会话唯一发布点，payload 形状不变：61 §4.1）。
func (s *Service) SessionEnsure(req facade.SessionEnsureRequest) (facade.SessionEnsureResponse, error) {
	if req.SessionID == "" {
		return facade.SessionEnsureResponse{}, errors.New("session: session_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	var rec data.Record
	ok, err := prj.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	if ok {
		return facade.SessionEnsureResponse{OK: true}, nil
	}
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	row := data.Record{
		"session_id": req.SessionID, "title": req.SessionID, "created_at": now, "updated_at": now,
	}
	if req.ParentSessionID != "" {
		row["parent_id"] = req.ParentSessionID
	}
	if err := prj.Table("sessions").Upsert(req.SessionID, row); err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	s.emitSessionNew(req.SessionID, req.ParentSessionID, req.InstanceID)
	return facade.SessionEnsureResponse{OK: true}, nil
}

// ── turn 域 ──────────────────────────────────────────────────────

// TurnHistory 读会话历史分页（按 before_turn_id 向前翻页；target_messages/target_bytes 上限，
// 缺省 50 条 / 200KB）→ 轮次数组 + 消息视图数组 + has_more。
func (s *Service) TurnHistory(req facade.TurnHistoryRequest) (facade.TurnHistoryResponse, error) {
	if req.SessionID == "" {
		return facade.TurnHistoryResponse{}, errors.New("session: session_id required")
	}
	targetMessages := req.TargetMessages
	if targetMessages <= 0 {
		targetMessages = 50
	}
	targetBytes := req.TargetBytes
	if targetBytes <= 0 {
		targetBytes = 200 * 1024
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TurnHistoryResponse{}, err
	}
	turnRecs, _, err := prj.Table("turns").Query(data.Query{
		Where:   data.Record{"session_id": req.SessionID},
		OrderBy: "created_at",
	})
	if err != nil {
		return facade.TurnHistoryResponse{}, fmt.Errorf("query turns: %v", err)
	}
	allMsgs, _, err := prj.Table("messages").Query(data.Query{OrderBy: "created_at"})
	if err != nil {
		return facade.TurnHistoryResponse{}, fmt.Errorf("query messages: %v", err)
	}
	byTurn := make(map[string][]data.Record, len(allMsgs))
	for _, m := range allMsgs {
		tid := kernel.TurnIDOfMsg(m)
		byTurn[tid] = append(byTurn[tid], m)
	}

	var selected []data.Record
	accMessages, accBytes, hasMore := 0, 0, false
	skip := req.BeforeTurnID != ""
	for i := len(turnRecs) - 1; i >= 0; i-- {
		t := turnRecs[i]
		if skip {
			if kernel.TurnIDOf(t) == req.BeforeTurnID {
				skip = false
			}
			continue
		}
		if accMessages >= targetMessages || accBytes >= targetBytes {
			hasMore = true
			break
		}
		cnt, sz := kernel.TurnStatsByMap(byTurn, kernel.TurnIDOf(t))
		selected = append(selected, t)
		accMessages += cnt
		accBytes += sz
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}

	resp := facade.TurnHistoryResponse{Turns: make([]facade.Turn, 0, len(selected)), HasMore: hasMore}
	turnIDs := make([]string, 0, len(selected))
	for _, t := range selected {
		turnIDs = append(turnIDs, kernel.TurnIDOf(t))
		resp.Turns = append(resp.Turns, wire.TurnFromWire(kernel.RecordView(t, "turn_id")))
	}
	for _, tid := range turnIDs {
		for _, m := range byTurn[tid] {
			resp.Messages = append(resp.Messages, messageViewToFacade(m, true))
		}
	}
	return resp, nil
}

// TurnEnsure 幂等建轮次（已存在复用，不覆盖）。
func (s *Service) TurnEnsure(req facade.TurnEnsureRequest) (facade.TurnEnsureResponse, error) {
	if req.TurnID == "" {
		return facade.TurnEnsureResponse{}, errors.New("session: turn_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TurnEnsureResponse{}, err
	}
	var rec data.Record
	ok, err := prj.Table("turns").Get(req.TurnID, &rec)
	if err != nil {
		return facade.TurnEnsureResponse{}, err
	}
	if ok {
		return facade.TurnEnsureResponse{OK: true}, nil
	}
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	if err := prj.Table("turns").Upsert(req.TurnID, data.Record{
		"turn_id": req.TurnID, "session_id": req.SessionID, "status": "running",
		"created_at": now, "updated_at": now,
	}); err != nil {
		return facade.TurnEnsureResponse{}, err
	}
	return facade.TurnEnsureResponse{OK: true}, nil
}

// TurnSetSummary 写/更新某轮次摘要（有值 = 该轮已被压缩，turns.summary）。
func (s *Service) TurnSetSummary(req facade.TurnSetSummaryRequest) (facade.TurnSetSummaryResponse, error) {
	if req.TurnID == "" {
		return facade.TurnSetSummaryResponse{}, errors.New("session: turn_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TurnSetSummaryResponse{}, err
	}
	var rec data.Record
	if ok, err := prj.Table("turns").Get(req.TurnID, &rec); err != nil {
		return facade.TurnSetSummaryResponse{}, err
	} else if !ok {
		rec = data.Record{}
	}
	delete(rec, data.KeyField)
	rec["summary"] = req.Summary
	rec["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	if err := prj.Table("turns").Upsert(req.TurnID, rec); err != nil {
		return facade.TurnSetSummaryResponse{}, err
	}
	return facade.TurnSetSummaryResponse{OK: true}, nil
}

// TurnComplete 结束轮次（写 status/finish_reason；保留原字段）。
//
// P3（2026-09-25）：另接受**预存 token**（`FullTokens`/`BriefTokens`，各为指针）——非 nil 才写键，
// nil（如 ReopenTurn 复用本面置回 running）= 不触碰既有值（避免把"未提供"写成 0）。
func (s *Service) TurnComplete(req facade.TurnCompleteRequest) (facade.TurnCompleteResponse, error) {
	if req.TurnID == "" || req.Status == "" {
		return facade.TurnCompleteResponse{}, errors.New("session: turn_id and status required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TurnCompleteResponse{}, err
	}
	var rec data.Record
	if ok, _ := prj.Table("turns").Get(req.TurnID, &rec); !ok {
		rec = data.Record{}
	}
	delete(rec, data.KeyField)
	rec["status"] = req.Status
	rec["finish_reason"] = req.FinishReason
	if req.FullTokens != nil {
		rec["full_tokens"] = *req.FullTokens
	}
	if req.BriefTokens != nil {
		rec["brief_tokens"] = *req.BriefTokens
	}
	rec["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	if err := prj.Table("turns").Upsert(req.TurnID, rec); err != nil {
		return facade.TurnCompleteResponse{}, err
	}
	return facade.TurnCompleteResponse{OK: true}, nil
}

// TurnCleanupStale 启动/首次使用清理（`data-session-cleanup-stale` 触发点 = 启动/首次 llm-start）：
//   - 遗留 running 的轮次标 interrupted（上限 500 条，返回处理条数）；
//   - 一并执行 pair 级清理 cleanupStaleToolPairs（残留非终态 tool_pair → interrupted）。
//
// 两者共用同一启动触发点与同一消息（不新增消息主题/payload，返回仍为 {ok, count}，count = 轮次数）。
func (s *Service) TurnCleanupStale(req facade.TurnCleanupStaleRequest) (facade.TurnCleanupStaleResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TurnCleanupStaleResponse{}, err
	}
	recs, _, err := prj.Table("turns").Query(data.Query{Where: data.Record{"status": "running"}, Limit: 500})
	if err != nil {
		return facade.TurnCleanupStaleResponse{}, err
	}
	n := 0
	for _, r := range recs {
		id := kernel.TurnIDOf(r)
		if id == "" {
			continue
		}
		var rec data.Record
		if ok, _ := prj.Table("turns").Get(id, &rec); !ok {
			rec = data.Record{}
		}
		delete(rec, data.KeyField)
		rec["status"] = "interrupted"
		rec["finish_reason"] = "interrupted"
		rec["updated_at"] = time.Now().UTC().Format(time.RFC3339)
		if err := prj.Table("turns").Upsert(id, rec); err == nil {
			n++
		}
	}
	cleanupStaleToolPairs(prj)
	return facade.TurnCleanupStaleResponse{OK: true, Count: n}, nil
}

// cleanupStaleToolPairs pair 级启动清理（35 §附录 S2/S3）：把消息表中遗留的**非终态** tool_pair
// （role=tool 且 `tool_call_status` ∈ {pending, running, provisional}）标 `interrupted` —— 进程异常/
// 应用关闭（abort）后该工具结果无法回填，统一收敛为可手动重试的「已中断」（同步工具 S2 可重试、
// 转后台 S3 不可重试由前端按 Notify 判定）。
//
//   - 与轮次清理同触发点（TurnCleanupStale 内调用，`data-session-cleanup-stale`），**不新增消息主题/payload**；
//   - 只改 DB 状态，**不改历史文本**（result.Content 原样）——LLM 历史拼接口径不变；
//   - 状态同源：同时更新 `tool_call_status` 列与 content.result.status（存在 result 段时）；
//   - 上限 500 条/状态；单项失败跳过（启动清理尽力而为，不阻断启动）。
//   - 状态常量取自 kernel（与 tool_pair 生命周期常量定义处单一来源一致）。
func cleanupStaleToolPairs(prj *data.DB) {
	for _, st := range []string{kernel.ToolStatusPending, kernel.ToolStatusRunning, kernel.ToolStatusProvisional} {
		recs, _, err := prj.Table("messages").Query(data.Query{
			Where: data.Record{"tool_call_status": st}, Limit: 500,
		})
		if err != nil {
			continue
		}
		for _, rec := range recs {
			key := kernel.Sval(rec[data.KeyField])
			if key == "" {
				continue
			}
			delete(rec, data.KeyField)
			rec["tool_call_status"] = kernel.ToolStatusInterrupted
			// content.result.status 与状态列同源：存在 result 段才需改（无 result 段时视图回退读状态列）。
			if tc, ok := kernel.ParseToolContent(kernel.Sval(rec["content"])); ok && tc.Result != nil {
				tc.Result.Status = kernel.ToolStatusInterrupted
				if b, err := json.Marshal(tc); err == nil {
					rec["content"] = string(b)
				}
			}
			_ = prj.Table("messages").Upsert(key, rec)
		}
	}
}

// ── message 域 ───────────────────────────────────────────────────

// MessageAppend 落一条完整消息（role/kind/content/tool_calls/tool_call_id/reasoning/_meta）：
// 恒写 session_id/turn_id/role/content/created_at；非空才写 brief/kind/tool_call_id/
// tool_call_status/tool_calls/reasoning/_meta；role=tool 的正文规整为工具载荷结构。
func (s *Service) MessageAppend(req facade.MessageAppendRequest) (facade.MessageAppendResponse, error) {
	if req.TurnID == "" {
		return facade.MessageAppendResponse{}, errors.New("session: turn_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MessageAppendResponse{}, err
	}
	m := data.MessageFromFacade(req.Message)

	// session_id：显式优先；否则由轮次记录回填（messages 表恒写 session_id）。
	sessionID := req.SessionID
	if sessionID == "" {
		var trec data.Record
		if ok, _ := prj.Table("turns").Get(req.TurnID, &trec); ok {
			sessionID = kernel.Sval(trec["session_id"])
		}
	}

	// role=tool 的 content 由调用方按 {call,result,async} 结构给出，原样落库。
	content := m.Content

	// 主键（I-176）：显式 key 优先（running → 终态就地回填）；未给 key 且为带 tool_call_id 的
	// role=tool 行 → 复用同轮同 call 的既有行（重启后重试路径无内存主键，避免重复 tool_pair）；
	// 否则生成新键。就地更新时保留原 created_at（消息在轮内的时间序位置不变）。
	key := req.Key
	if key == "" && m.Role == "tool" && m.ToolCallID != "" {
		key = kernel.FindToolMessageKey(prj, req.TurnID, m.ToolCallID)
	}
	createdAt := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	if key == "" {
		key = kernel.NewMessageKey()
	} else {
		var old data.Record
		if ok, _ := prj.Table("messages").Get(key, &old); ok {
			if v := kernel.Sval(old["created_at"]); v != "" {
				createdAt = v
			}
		}
	}

	rec := data.Record{
		"session_id": sessionID, "turn_id": req.TurnID, "role": m.Role, "content": content,
		"created_at": createdAt,
	}
	if m.ToolCallID != "" {
		rec["tool_call_id"] = m.ToolCallID
	}
	if m.Kind != "" {
		rec["kind"] = m.Kind
	}
	if req.ToolCallStatus != "" {
		rec["tool_call_status"] = req.ToolCallStatus
	}
	if m.Reasoning != "" {
		rec["reasoning"] = m.Reasoning
	}
	if len(m.Meta) > 0 {
		rec["_meta"] = m.Meta
	}
	if len(m.ToolCalls) > 0 {
		if b, err := json.Marshal(m.ToolCalls); err == nil {
			rec["tool_calls"] = string(b)
		}
	}
	brief := req.Brief
	if brief == "" {
		if m.Role == "tool" {
			tc, _ := kernel.ParseToolContent(content)
			brief = kernel.ToolBrief(tc)
		} else if m.Reasoning != "" {
			brief = kernel.ReasoningBrief(m.Reasoning)
		}
	}
	if brief != "" {
		rec["brief"] = brief
	}
	if err := prj.Table("messages").Upsert(key, rec); err != nil {
		return facade.MessageAppendResponse{}, err
	}
	return facade.MessageAppendResponse{OK: true, ID: key}, nil
}

// MessageLoad 读该轮次全部消息（created_at 升序，limit 500；供 LLM 会话重建）。
func (s *Service) MessageLoad(req facade.MessageLoadRequest) (facade.MessageLoadResponse, error) {
	if req.TurnID == "" {
		return facade.MessageLoadResponse{}, errors.New("session: turn_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MessageLoadResponse{}, err
	}
	recs, _, err := prj.Table("messages").Query(data.Query{
		Where: data.Record{"turn_id": req.TurnID}, OrderBy: "created_at", Limit: 500,
	})
	if err != nil {
		return facade.MessageLoadResponse{}, err
	}
	msgs := make([]facade.Message, 0, len(recs))
	for _, rec := range recs {
		if m, ok := kernel.MsgRowToChat(rec); ok {
			msgs = append(msgs, data.MessageToFacade(m))
		}
	}
	return facade.MessageLoadResponse{Messages: msgs}, nil
}

// MessageContext 组装会话历史（llm-start 恢复上下文一次到位）：
// IncludeSnapshot 且快照非空 → 快照前缀 + 覆盖轮之后的消息（跳过 interrupted / ExcludeTurn）；
// 否则全量历史（从最新往回找首个有 summary 的轮次作起点；summary → system "[历史摘要] "+…）。
func (s *Service) MessageContext(req facade.MessageContextRequest) (facade.MessageContextResponse, error) {
	if req.SessionID == "" {
		return facade.MessageContextResponse{}, errors.New("session: session_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MessageContextResponse{}, err
	}
	turnRecs, _, err := prj.Table("turns").Query(data.Query{
		Where: data.Record{"session_id": req.SessionID}, OrderBy: "created_at", Limit: 500,
	})
	if err != nil {
		return facade.MessageContextResponse{}, fmt.Errorf("query turns: %v", err)
	}
	allMsgs, _, err := prj.Table("messages").Query(data.Query{OrderBy: "created_at"})
	if err != nil {
		return facade.MessageContextResponse{}, fmt.Errorf("query messages: %v", err)
	}
	byTurn := map[string][]data.ChatMsg{}
	for _, m := range allMsgs {
		if cm, ok := kernel.MsgRowToChat(m); ok {
			byTurn[kernel.TurnIDOfMsg(m)] = append(byTurn[kernel.TurnIDOfMsg(m)], cm)
		}
	}

	// 快照分支：快照非空才走快照上下文，否则回退全量历史。
	turnTokens := turnTokensOf(turnRecs, req.ExcludeTurn) // P3 伴随数组（只增；升序、排除 ExcludeTurn）
	if req.IncludeSnapshot {
		if snap, ok, _ := snapshot.Get(prj, req.SessionID); ok && len(snap.History) > 0 {
			msgs := append([]data.ChatMsg{}, snap.History...)
			found := snap.SnapshotTurn == "" // 快照无覆盖轮 → 全部轮次都拼
			for _, r := range turnRecs {
				id := kernel.TurnIDOf(r)
				if id == "" || id == req.ExcludeTurn {
					continue
				}
				if !found {
					found = id == snap.SnapshotTurn // 命中覆盖轮 → 其自身已含在快照内，跳过
					continue
				}
				if kernel.Sval(r["status"]) == "interrupted" {
					continue
				}
				msgs = append(msgs, byTurn[id]...)
			}
			return facade.MessageContextResponse{Messages: messagesToFacade(msgs), TurnTokens: turnTokens}, nil
		}
	}

	// 全量历史分支
	turnByID := map[string]data.Record{}
	var ids []string
	for _, r := range turnRecs {
		if id := kernel.TurnIDOf(r); id != "" && id != req.ExcludeTurn {
			turnByID[id] = r
			ids = append(ids, id)
		}
	}
	start := 0
	for i := len(ids) - 1; i >= 0; i-- {
		if kernel.Sval(turnByID[ids[i]]["summary"]) != "" {
			start = i
			break
		}
	}
	var msgs []data.ChatMsg
	for i := start; i < len(ids); i++ {
		t := turnByID[ids[i]]
		if i == start && kernel.Sval(t["summary"]) != "" {
			msgs = append(msgs, data.ChatMsg{Role: "system", Content: "[历史摘要] " + kernel.Sval(t["summary"])})
			continue
		}
		msgs = append(msgs, byTurn[ids[i]]...)
	}
	return facade.MessageContextResponse{Messages: messagesToFacade(msgs), TurnTokens: turnTokens}, nil
}

// turnTokensOf 组装轮次 token **伴随数组**（P3，2026-09-25；**只增**通道的取值）：
// 会话各轮按传入顺序（Query OrderBy created_at 升序）、排除 excludeTurn，每项
// `{turn_id, full?, brief?}`——turns 行预存字段存在才带（缺值省略键 → 消费方回退实时估算）。
//
// 代价：**不额外扫表**（复用 MessageContext 已查到的 turnRecs，只读行内 token 列）。
func turnTokensOf(recs []data.Record, excludeTurn string) []facade.TurnToken {
	out := make([]facade.TurnToken, 0, len(recs))
	for _, r := range recs {
		id := kernel.TurnIDOf(r)
		if id == "" || id == excludeTurn {
			continue
		}
		tt := facade.TurnToken{TurnID: id}
		if v, ok := tokenIntOf(r["full_tokens"]); ok {
			tt.Full = &v
		}
		if v, ok := tokenIntOf(r["brief_tokens"]); ok {
			tt.Brief = &v
		}
		out = append(out, tt)
	}
	return out
}

// tokenIntOf 解析 turns 行 token 列（存储值可能是数值或字符串；缺失/非法 → false = 无预存值）。
func tokenIntOf(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// MessageContent 按 key 批量取正文（message:<id> / tool_call:<id> / tool_result:<调用id>）；
// 未命中的 key 省略。
func (s *Service) MessageContent(req facade.MessageContentRequest) (facade.MessageContentResponse, error) {
	if req.SessionID == "" || len(req.Keys) == 0 {
		return facade.MessageContentResponse{}, errors.New("session: session_id and keys required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.MessageContentResponse{}, err
	}
	raw := kernel.MessageContentsByKey(prj, req.SessionID, req.Keys)
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = kernel.Sval(v)
	}
	return facade.MessageContentResponse{Contents: out}, nil
}

// messagesToFacade 把内核消息数组翻成门面 DTO 数组（上下文组装共用）。
func messagesToFacade(msgs []data.ChatMsg) []facade.Message {
	out := make([]facade.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, data.MessageToFacade(m))
	}
	return out
}

// messageViewToFacade 把 messages 表记录转成门面消息视图 DTO（与既有 MessageView 同口径）：
//   - role=tool 消息 → 工具卡片字段组（参数打平、结果摘要、状态）；
//   - 思维链类消息 → 正文截断 3 行 + HasMore；
//   - 其余 → 消息字段直出。
func messageViewToFacade(m data.Record, brief bool) facade.MessageView {
	role := kernel.Sval(m["role"])
	typ := kernel.Sval(m["type"])
	msgID := kernel.Sval(m["message_id"])
	if msgID == "" {
		msgID = kernel.Sval(m[data.KeyField])
	}
	if role == "tool" {
		toolCallID, taskID, name, argsJSON, result, status := kernel.ToolViewFields(m)
		if status == "" {
			status = kernel.ToolStatusCompleted
		}
		success := status == kernel.ToolStatusCompleted
		v := facade.MessageView{
			ToolPair:         true,
			Role:             role,
			Type:             "tool_pair",
			MessageID:        msgID,
			TurnID:           kernel.Sval(m["turn_id"]),
			ToolCallID:       toolCallID,
			TaskID:           taskID,
			Tool:             name,
			Arguments:        argsJSON,
			Brief:            kernel.Sval(m["brief"]),
			Simplified:       name + "(" + kernel.CompactArgs(argsJSON) + ")",
			ResultSimplified: kernel.SummarizeResult(result),
			ResultSuccess:    &success,
			Status:           status,
			CreatedAt:        kernel.Sval(m["created_at"]),
		}
		if brief {
			v.Result = ""
			hasMore := result != ""
			v.HasMore = &hasMore
		} else {
			v.Result = result
		}
		if meta := kernel.MetaOfRecord(m); len(meta) > 0 {
			v.Meta = meta
		}
		return v
	}
	v := facade.MessageView{
		Role:           role,
		Type:           typ,
		MessageID:      msgID,
		TurnID:         kernel.Sval(m["turn_id"]),
		SessionID:      kernel.Sval(m["session_id"]),
		Content:        kernel.Sval(m["content"]),
		Kind:           kernel.Sval(m["kind"]),
		Brief:          kernel.Sval(m["brief"]),
		ToolCallID:     kernel.Sval(m["tool_call_id"]),
		ToolCallStatus: kernel.Sval(m["tool_call_status"]),
		Reasoning:      kernel.Sval(m["reasoning"]),
		Meta:           kernel.MetaOfRecord(m),
		CreatedAt:      kernel.Sval(m["created_at"]),
	}
	if tc := m["tool_calls"]; tc != nil {
		var b []byte
		switch t := tc.(type) {
		case string:
			b = []byte(t)
		default:
			b, _ = json.Marshal(t)
		}
		var calls []data.ToolCall
		if json.Unmarshal(b, &calls) == nil {
			for _, c := range calls {
				v.ToolCalls = append(v.ToolCalls, data.ToolCallToFacade(c))
			}
		}
	}
	if brief && role == "assistant" && typ == "reasoning" {
		preview, hasMore := kernel.TruncateLines(kernel.Sval(m["content"]), 3)
		v.Content = preview
		v.HasMore = &hasMore
	}
	return v
}
