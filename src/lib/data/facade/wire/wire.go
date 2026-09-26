// Package wire 是 **消息面载荷（61 绑定）↔ 门面领域 DTO** 的翻译层，专供
// session / turn / message 三域（`data-session-*` 家族）使用。
//
// 为什么单独一层（G-33：wire 形状与门面 DTO 是两件事，不合并）：
//   - 门面 DTO 用领域字段（`messages`/`turn`/平铺工具调用），消息面用**绑定形状**
//     （`history` JSON 字符串、`snapshot_turn`、`tool_calls[].function{name,arguments}`、
//     `{call,result,async}`、视图行的固定键集）；
//   - 三处消费方都讲同一套 wire：① persist 的 `data-session-*` MQ handler（信封层）
//     ② gui 桥 `dataCall`（同进程门面优先）③ browser 入口 `dataViaPersist`（同构）。
//     把翻译收在本包 = **一份翻译**，三条路径的应答载荷因此**逐字一致**（零分叉）。
//
// 归属：本包在 data 组件内（`chonkpilot-data/facade/wire`）——即 23 §7「翻译留在门面实现侧」
// 的实现侧；门面定义包（`facade`）本身不引入任何 wire 知识。
//
// 边界：本包只做「字段搬运 + 形状改写」，不含业务规则（不查库、不判定、不生成摘要）。
package wire

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// ── 会话（session 域）──────────────────────────────────────────────

// SessionFromWire 把消息面会话行转成门面 DTO（`history`/`snapshot_turn` 两列 → 领域快照）。
// 行缺 `history` 键 = 该会话从未写过快照 → DTO 的 Snapshot 为 nil（保真"无快照"语义）。
func SessionFromWire(row map[string]any) facade.Session {
	s := facade.Session{
		ID:           str(row["session_id"]),
		Title:        str(row["title"]),
		ParentID:     str(row["parent_id"]),
		CreatedAt:    str(row["created_at"]),
		UpdatedAt:    str(row["updated_at"]),
		LastActivity: str(row["last_activity"]),
	}
	if _, ok := row["history"]; ok {
		var hist []data.ChatMsg
		if raw := str(row["history"]); raw != "" {
			_ = json.Unmarshal([]byte(raw), &hist)
		}
		snap := data.SnapshotToFacade(s.ID, data.Snapshot{
			History: hist, SnapshotTurn: str(row["snapshot_turn"]),
		})
		s.Snapshot = &snap
	}
	return s
}

// SessionToWire 把门面会话 DTO 转回消息面行（快照 → `history` JSON 字符串 + `snapshot_turn`）。
// 非空字段才写键（与既有 `RecordView` 的"存在即带"口径一致）。
func SessionToWire(s facade.Session) map[string]any {
	row := map[string]any{"session_id": s.ID}
	put(row, "title", s.Title)
	put(row, "parent_id", s.ParentID)
	put(row, "created_at", s.CreatedAt)
	put(row, "updated_at", s.UpdatedAt)
	put(row, "last_activity", s.LastActivity)
	if s.Snapshot != nil {
		kern := data.SnapshotFromFacade(*s.Snapshot)
		b, err := json.Marshal(kern.History)
		if err != nil {
			b = []byte("null")
		}
		row["history"] = string(b)
		row["snapshot_turn"] = kern.SnapshotTurn
	}
	return row
}

// SessionListResult 组装 `data-session-list` 结果载荷 `{list:[会话行…]}`。
func SessionListResult(list []facade.Session) map[string]any {
	out := make([]any, 0, len(list))
	for _, s := range list {
		out = append(out, SessionToWire(s))
	}
	return map[string]any{"list": out}
}

// SessionGetResult 组装 `data-session-get` 结果载荷 `{data: 会话行|null}`（不存在 = null）。
func SessionGetResult(found bool, s facade.Session) map[string]any {
	if !found {
		return map[string]any{"data": nil}
	}
	return map[string]any{"data": SessionToWire(s)}
}

// SessionIDResult 组装 `{session_id: …}`（latest / active-get 共用）。
func SessionIDResult(sessionID string) map[string]any {
	return map[string]any{"session_id": sessionID}
}

// ── 轮次（turn 域）────────────────────────────────────────────────

// TurnFromWire 把消息面轮次行转成门面 DTO（`summary`/`finish_reason` 用指针保"有键/无键"）。
func TurnFromWire(row map[string]any) facade.Turn {
	t := facade.Turn{
		ID:        str(row["turn_id"]),
		SessionID: str(row["session_id"]),
		Status:    str(row["status"]),
		CreatedAt: str(row["created_at"]),
		UpdatedAt: str(row["updated_at"]),
	}
	if v, ok := row["summary"]; ok {
		s := str(v)
		t.Summary = &s
	}
	if v, ok := row["finish_reason"]; ok {
		s := str(v)
		t.FinishReason = &s
	}
	// P3 预存 token（存在即带；缺键 = 该轮无预存值 → nil，消费方回退实时估算）
	if v, ok := row["full_tokens"]; ok && v != nil {
		n := intOf(v)
		t.FullTokens = &n
	}
	if v, ok := row["brief_tokens"]; ok && v != nil {
		n := intOf(v)
		t.BriefTokens = &n
	}
	return t
}

// TurnToWire 把门面轮次 DTO 转回消息面行。
func TurnToWire(t facade.Turn) map[string]any {
	row := map[string]any{"turn_id": t.ID}
	put(row, "session_id", t.SessionID)
	put(row, "status", t.Status)
	if t.Summary != nil {
		row["summary"] = *t.Summary
	}
	if t.FinishReason != nil {
		row["finish_reason"] = *t.FinishReason
	}
	if t.FullTokens != nil {
		row["full_tokens"] = *t.FullTokens
	}
	if t.BriefTokens != nil {
		row["brief_tokens"] = *t.BriefTokens
	}
	put(row, "created_at", t.CreatedAt)
	put(row, "updated_at", t.UpdatedAt)
	return row
}

// TurnHistoryResult 组装 `data-session-history` 结果载荷
// `{messages:{turns:[…], messages:[…], has_more:bool}}`（既有外形，逐字不变）。
func TurnHistoryResult(resp facade.TurnHistoryResponse) map[string]any {
	turns := make([]any, 0, len(resp.Turns))
	for _, t := range resp.Turns {
		turns = append(turns, TurnToWire(t))
	}
	msgs := make([]any, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		msgs = append(msgs, MessageViewToWire(m))
	}
	return map[string]any{"messages": map[string]any{
		"turns": turns, "messages": msgs, "has_more": resp.HasMore,
	}}
}

// ── 消息（message 域）─────────────────────────────────────────────

// MessageViewToWire 把消息视图 DTO 转回消息面视图行：
//   - 工具卡片（ToolPair=true）→ **固定键集**（既有外形：arguments/simplified/result_simplified/
//     result_success/… 全带，空值也带）；
//   - 普通消息 → 非空字段才带键（与既有 `RecordView` 口径一致）。
func MessageViewToWire(v facade.MessageView) map[string]any {
	if v.ToolPair {
		row := map[string]any{
			"role":              v.Role,
			"type":              "tool_pair",
			"message_id":        v.MessageID,
			"turn_id":           v.TurnID,
			"tool_call_id":      v.ToolCallID,
			"task_id":           v.TaskID,
			"tool":              v.Tool,
			"arguments":         v.Arguments,
			"brief":             v.Brief,
			"result_success":    v.ResultSuccess != nil && *v.ResultSuccess,
			"simplified":        v.Simplified,
			"result_simplified": v.ResultSimplified,
			"status":            v.Status,
			"created_at":        v.CreatedAt,
			"result":            v.Result,
		}
		if v.HasMore != nil {
			row["has_more"] = *v.HasMore
		}
		if len(v.Meta) > 0 {
			row["_meta"] = v.Meta
		}
		return row
	}
	row := map[string]any{
		"role":       v.Role,
		"message_id": v.MessageID,
		"content":    v.Content,
	}
	put(row, "type", v.Type)
	put(row, "turn_id", v.TurnID)
	put(row, "session_id", v.SessionID)
	put(row, "kind", v.Kind)
	put(row, "brief", v.Brief)
	put(row, "tool_call_id", v.ToolCallID)
	put(row, "tool_call_status", v.ToolCallStatus)
	put(row, "reasoning", v.Reasoning)
	put(row, "created_at", v.CreatedAt)
	if len(v.ToolCalls) > 0 {
		// 存储/消息面形状 = JSON 字符串（工具调用嵌套 function{name,arguments}）
		if b, err := json.Marshal(messagesToolCalls(v.ToolCalls)); err == nil {
			row["tool_calls"] = string(b)
		}
	}
	if len(v.Meta) > 0 {
		row["_meta"] = v.Meta
	}
	if v.HasMore != nil {
		row["has_more"] = *v.HasMore
	}
	return row
}

// messagesToolCalls 把门面平铺工具调用翻回内核嵌套形态（供 JSON 落消息面形状）。
func messagesToolCalls(calls []facade.ToolCall) []data.ToolCall {
	out := make([]data.ToolCall, 0, len(calls))
	for _, tc := range calls {
		out = append(out, data.ToolCallFromFacade(tc))
	}
	return out
}

// MessageLoadResult 组装 `{messages:[消息…]}`（load-messages / context 共用；
// 消息面形状 = 内核 ChatMsg 的 JSON：`tool_calls[].function{name,arguments}` 嵌套 + `_meta`）。
func MessageLoadResult(msgs []facade.Message) map[string]any {
	out := make([]data.ChatMsg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, data.MessageFromFacade(m))
	}
	return map[string]any{"messages": out}
}

// TurnTokenToWire 把轮次 token 伴随项转回消息面行（键 turn_id/full/brief；缺值省略键，
// 与既有"存在即带"口径一致；消费方缺值回退实时估算）。
func TurnTokenToWire(t facade.TurnToken) map[string]any {
	row := map[string]any{"turn_id": t.TurnID}
	if t.Full != nil {
		row["full"] = *t.Full
	}
	if t.Brief != nil {
		row["brief"] = *t.Brief
	}
	return row
}

// ContextResult 组装 `data-session-context` 结果载荷：`{messages:[…], turn_tokens:[…]}`。
// turn_tokens 为**只增**伴随数组（P3，2026-09-25）；为空时**不写该键**（与旧载荷逐字节等价）。
func ContextResult(msgs []facade.Message, tokens []facade.TurnToken) map[string]any {
	res := MessageLoadResult(msgs)
	if len(tokens) == 0 {
		return res
	}
	arr := make([]any, 0, len(tokens))
	for _, t := range tokens {
		arr = append(arr, TurnTokenToWire(t))
	}
	res["turn_tokens"] = arr
	return res
}

// MessageContentResult 组装 `{contents:{键: 正文}}`（未命中的键省略）。
func MessageContentResult(contents map[string]string) map[string]any {
	out := make(map[string]any, len(contents))
	for k, v := range contents {
		out[k] = v
	}
	return map[string]any{"contents": out}
}

// MessageAppendFromWire 解析 `data-session-append-message` 载荷 → 门面入参。
//
// 兼容既有两种形态（61 §3.2a）：`{turn_id, msg:{…}}`（嵌套）与扁平形态（字段直接在载荷上）。
// msg 的工具调用形状 = 消息面嵌套 `function{name,arguments}`（经内核 ChatMsg 归一为门面平铺）。
func MessageAppendFromWire(m map[string]any) facade.MessageAppendRequest {
	req := facade.MessageAppendRequest{TurnID: str(m["turn_id"])}
	nested, _ := m["msg"].(map[string]any)
	if len(nested) == 0 {
		nested = m
	}
	raw, err := json.Marshal(nested)
	if err != nil {
		return req
	}
	var p struct {
		data.ChatMsg
		SessionID      string `json:"session_id,omitempty"`
		Brief          string `json:"brief,omitempty"`
		ToolCallStatus string `json:"tool_call_status,omitempty"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return req
	}
	req.Message = data.MessageToFacade(p.ChatMsg)
	req.SessionID = p.SessionID
	req.Brief = p.Brief
	req.ToolCallStatus = p.ToolCallStatus
	return req
}

// MessageLoadFromWire 解析 `{turn_id}` → 门面入参。
func MessageLoadFromWire(m map[string]any) facade.MessageLoadRequest {
	return facade.MessageLoadRequest{TurnID: str(m["turn_id"])}
}

// MessageContextFromWire 解析 `{session_id, exclude_turn?, include_snapshot?}` → 门面入参
// （include_snapshot 既接受 JSON 布尔，也接受字符串 "true"——既有 MQ 载荷两种都出现过）。
func MessageContextFromWire(m map[string]any) facade.MessageContextRequest {
	return facade.MessageContextRequest{
		SessionID:       str(m["session_id"]),
		ExcludeTurn:     str(m["exclude_turn"]),
		IncludeSnapshot: truthy(m["include_snapshot"]),
	}
}

// MessageContentFromWire 解析 `{session_id, keys:[…]}` → 门面入参。
func MessageContentFromWire(m map[string]any) facade.MessageContentRequest {
	return facade.MessageContentRequest{SessionID: str(m["session_id"]), Keys: strList(m["keys"])}
}

// TurnHistoryFromWire 解析 `{session_id, before_turn_id?, target_messages?, target_bytes?}` → 门面入参。
func TurnHistoryFromWire(m map[string]any) facade.TurnHistoryRequest {
	return facade.TurnHistoryRequest{
		SessionID:      str(m["session_id"]),
		BeforeTurnID:   str(m["before_turn_id"]),
		TargetMessages: intOf(m["target_messages"]),
		TargetBytes:    intOf(m["target_bytes"]),
	}
}

// ── 通用回复 ─────────────────────────────────────────────────────

// OKResult 组装 `{ok:true}`（写类动作共用）。
func OKResult() map[string]any { return map[string]any{"ok": true} }

// CleanupResult 组装 `{ok:true, count:n}`（cleanup-stale）。
func CleanupResult(count int) map[string]any {
	return map[string]any{"ok": true, "count": count}
}

// ── 请求载荷归一（入口/信封共用）────────────────────────────────────

// FlatPayload 归一入口收到的**请求载荷**：`{data:{…}}` 里的业务字段并入顶层视图
// （**不覆盖**已有顶层键，与 persist `parseDataReq` 同口径）。返回原 map（就地补键）。
func FlatPayload(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	data, _ := m["data"].(map[string]any)
	for k, v := range data {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return m
}

// RequestID 取请求目标 id：顶层 `id` → `data.id` → `data.key`（与 persist `reqKey` 同口径）。
func RequestID(m map[string]any) string {
	if id := str(m["id"]); id != "" {
		return id
	}
	data, _ := m["data"].(map[string]any)
	if id := str(data["id"]); id != "" {
		return id
	}
	return str(data["key"])
}

// ── 小工具 ───────────────────────────────────────────────────────

func put(row map[string]any, key, val string) {
	if val != "" {
		row[key] = val
	}
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s := string(b)
	if len(s) > 0 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func strList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, str(e))
	}
	return out
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	}
	return 0
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	}
	return false
}
