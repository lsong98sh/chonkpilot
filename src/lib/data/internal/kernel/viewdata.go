// 会话/消息视图与工具消息契约 helper（原 persist/view.go + persist_session.go 的 helper
// 逐字下移；原 persist.go 的 svalOf 并入 SvalOf）。
//
// 存储形态（12-数据层）：messages 表 role=tool 的 content = {call,result,async} JSON；
// 旧形态 tool_pair 的 content = ToolPairPayload JSON（读取侧自动归一，保证既有会话仍可解析）。
// messages 表主键 = NewMessageKey（自带时间序）。
package kernel

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// ToolPair status 常量（tool_pair 消息生命周期）。
const (
	ToolStatusPending     = "pending"
	ToolStatusRunning     = "running"
	ToolStatusProvisional = "provisional"
	ToolStatusCompleted   = "completed"
	ToolStatusFailed      = "failed"
	ToolStatusCancelled   = "cancelled"   // 用户主动取消（同步工具被强杀 / 后台任务被 task-stop）
	ToolStatusInterrupted = "interrupted" // 进程异常/应用关闭中断（abort），可手动重试
)

// ToolPairPayload 是 type="tool_pair" 消息的 content JSON 结构（存储 schema）。
// tool_call + tool_result 两条历史合并为一条：ToolCallID（LLM 生成）与 TaskID（executor
// 生成）落在同一条上，索引（by_call/by_task）都指向它。
type ToolPairPayload struct {
	ToolCallID string `json:"tool_call_id"`    // LLM 生成的 id（LLM 发起时才有）
	TaskID     string `json:"task_id"`         // executor 生成，必有
	Name       string `json:"name"`            // 工具名
	Args       any    `json:"args"`            // 参数
	Result     string `json:"result"`          // 结果内容
	Reply      string `json:"reply,omitempty"` // 转后台任务的正式完成结果
	Status     string `json:"status"`
	Notify     bool   `json:"notify"` // true=调用方已离开，结果写 reply
}

// ── role=tool 消息 content 结构（{call,result,async}）──
//
// messages 表 role=tool 的 content = ToolContent JSON：call 承载"发起内容"（工具名/参数/
// tool_call_id），result 承载"结果"，async 承载"转异步"（何时转后台/后台任务 id/pending）。
// 兼容旧形态：旧 tool_pair 消息 content 为 ToolPairPayload（tool_call_id/task_id/name/args/
// result/...），ParseToolContent 自动归一，保证既有会话仍可解析。

// ToolContent 是 role=tool 消息 content 的 JSON 结构。
type ToolContent struct {
	Call   *ToolCallContent   `json:"call,omitempty"`   // 发起内容
	Result *ToolResultContent `json:"result,omitempty"` // 结果
	Async  *ToolAsyncContent  `json:"async,omitempty"`  // 转异步（何时转后台/后台任务 id/pending）
}

// ToolCallContent 是工具发起内容。
type ToolCallContent struct {
	ToolCallID string `json:"tool_call_id,omitempty"` // LLM 生成的 tool-call id
	Name       string `json:"name,omitempty"`         // 工具名
	Arguments  any    `json:"arguments,omitempty"`    // 参数（JSON 对象）
}

// ToolResultContent 是工具结果。
type ToolResultContent struct {
	Content string `json:"content,omitempty"` // 结果内容
	Status  string `json:"status,omitempty"`  // 生命周期状态（completed/failed/cancelled/...）
}

// ToolAsyncContent 是"转异步/转后台"信息。
type ToolAsyncContent struct {
	Pending bool   `json:"pending,omitempty"`  // 是否已转后台挂起
	TaskID  string `json:"task_id,omitempty"`  // 后台任务 id
	MovedAt string `json:"moved_at,omitempty"` // 何时转后台（RFC3339）
}

// ParseToolContent 解析 role=tool 消息 content：优先新结构 {call,result,async}；
// 旧结构 ToolPairPayload 自动归一（兼容既有会话）。无法识别 → ok=false。
func ParseToolContent(content string) (ToolContent, bool) {
	if strings.TrimSpace(content) == "" {
		return ToolContent{}, false
	}
	var tc ToolContent
	if json.Unmarshal([]byte(content), &tc) == nil && (tc.Call != nil || tc.Result != nil || tc.Async != nil) {
		return tc, true
	}
	// 旧结构兜底（tool_pair content = ToolPairPayload）
	var p ToolPairPayload
	if json.Unmarshal([]byte(content), &p) != nil || (p.Name == "" && p.Args == nil && p.Result == "") {
		return ToolContent{}, false
	}
	out := ToolContent{
		Call:   &ToolCallContent{ToolCallID: p.ToolCallID, Name: p.Name, Arguments: p.Args},
		Result: &ToolResultContent{Content: p.Result, Status: p.Status},
	}
	if p.TaskID != "" {
		out.Async = &ToolAsyncContent{TaskID: p.TaskID, Pending: p.Notify}
	}
	return out, true
}

// NormalizeToolContent 把 role=tool 消息 content 规整为新结构 JSON：
// 已是新结构 → 补全 tool_call_id/status；其余（纯文本 / 旧 ToolPairPayload / 空）→ 包成
// {result:{content}}（空内容且无关联字段 → 返回空串，不落冗余 JSON）。
func NormalizeToolContent(content, toolCallID, status string) string {
	tc, ok := ParseToolContent(content)
	if !ok {
		if strings.TrimSpace(content) == "" && toolCallID == "" && status == "" {
			return ""
		}
		tc = ToolContent{}
		if content != "" {
			tc.Result = &ToolResultContent{Content: content}
		}
	}
	if tc.Call == nil && toolCallID != "" {
		tc.Call = &ToolCallContent{}
	}
	if tc.Call != nil && tc.Call.ToolCallID == "" {
		tc.Call.ToolCallID = toolCallID
	}
	if tc.Result == nil {
		tc.Result = &ToolResultContent{}
	}
	if tc.Result.Status == "" {
		tc.Result.Status = status
	}
	b, _ := json.Marshal(tc)
	return string(b)
}

// ToolBrief 生成工具消息 brief（简化的工具调用内容 = 工具名 + 关键参数摘要）。
func ToolBrief(tc ToolContent) string {
	if tc.Call == nil || tc.Call.Name == "" {
		return ""
	}
	argsJSON := ""
	if tc.Call.Arguments != nil {
		if b, err := json.Marshal(tc.Call.Arguments); err == nil {
			argsJSON = string(b)
		}
	}
	return tc.Call.Name + "(" + CompactArgs(argsJSON) + ")"
}

// ReasoningBrief 生成 assistant 消息 brief（简化的思维链内容 = 前 3 行）。
func ReasoningBrief(reasoning string) string {
	preview, _ := TruncateLines(reasoning, 3)
	return preview
}

// ToolLLMContent 提取 role=tool 记录给 LLM 重放用的**结果文本**：新结构 {call,result,async}
// → result.content；旧 ToolPairPayload → result；纯文本 → 原样返回。
//
// 存储形态（content = {call,result,async} JSON）是给视图/前端消费的，不能直接喂回 LLM
// （LLM 需要的是裸结果文本，与 tool_call_id 配对）；故读取侧在此归一。
func ToolLLMContent(content string) string {
	if tc, ok := ParseToolContent(content); ok {
		if tc.Result != nil {
			return tc.Result.Content
		}
		return ""
	}
	return content
}

// ToolViewFields 从消息记录提取 tool 视图字段（新 {call,result,async} 结构；兼容旧
// ToolPairPayload）。返回 argsJSON 为空串表示无参数。
func ToolViewFields(m data.Record) (toolCallID, taskID, name, argsJSON, result, status string) {
	tc, _ := ParseToolContent(Sval(m["content"]))
	if tc.Call != nil {
		toolCallID, name = tc.Call.ToolCallID, tc.Call.Name
		if tc.Call.Arguments != nil {
			if b, err := json.Marshal(tc.Call.Arguments); err == nil {
				argsJSON = string(b)
			}
		}
	}
	if tc.Result != nil {
		result, status = tc.Result.Content, tc.Result.Status
	}
	if tc.Async != nil {
		taskID = tc.Async.TaskID
	}
	if toolCallID == "" {
		toolCallID = Sval(m["tool_call_id"])
	}
	if status == "" {
		status = Sval(m["tool_call_status"])
	}
	return
}

// Sval 把 Record 值转字符串（Record 由 json.Unmarshal 产生：string/float64/bool/nil）。
func Sval(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// SvalOf 取任意值的字符串形态（字符串原样；float64/int64 等数字归一为十进制）。
func SvalOf(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// RecordView 把 data.Record 转为可序列化 map：剥离 _key；keyField 缺失时用桶主键补。
func RecordView(r data.Record, keyField string) map[string]any {
	out := make(map[string]any, len(r))
	for k, v := range r {
		if k == data.KeyField {
			continue
		}
		out[k] = v
	}
	if keyField != "" {
		if _, ok := out[keyField]; !ok {
			if k, ok := r[data.KeyField]; ok {
				out[keyField] = k
			}
		}
	}
	return out
}

// ── 会话列表 helper（data-session-list / latest / active-get 共用）──

// SessionActivity 返回会话活动时间（last_activity 优先，回落 updated_at）。
func SessionActivity(s map[string]any) string {
	if a := Sval(s["last_activity"]); a != "" {
		return a
	}
	return Sval(s["updated_at"])
}

// SortSessionsByActivity 按最近活动降序稳定排序会话列表（data-session-list）。
func SortSessionsByActivity(sessions []map[string]any) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return SessionActivity(sessions[i]) > SessionActivity(sessions[j])
	})
}

// LatestTopSession 返回最近活动的顶层会话记录（无则 nil）。
func LatestTopSession(prj *data.DB) (data.Record, error) {
	recs, _, err := prj.Table("sessions").Query(data.Query{OrderBy: "created_at", OrderDesc: true})
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		if Sval(r["parent_id"]) == "" {
			return r, nil
		}
	}
	return nil, nil
}

// ── 历史分页 helper（data-session-history）──

// TurnIDOf 取 turn 记录 id（turn_id 优先，回落桶主键）。
func TurnIDOf(r data.Record) string {
	if id := Sval(r["turn_id"]); id != "" {
		return id
	}
	return Sval(r[data.KeyField])
}

// TurnStatsByMap 统计一个 turn 的消息数与内容字节（基于按 turn 分组的全量消息）。
func TurnStatsByMap(byTurn map[string][]data.Record, turnID string) (int, int) {
	cnt, sz := 0, 0
	for _, m := range byTurn[turnID] {
		cnt++
		sz += len(Sval(m["content"]))
		sz += len(Sval(m["brief"]))
	}
	return cnt, sz
}

// TurnIDOfMsg 取消息所属 turn_id（兼容 key 兜底）。
func TurnIDOfMsg(r data.Record) string {
	if id := Sval(r["turn_id"]); id != "" {
		return id
	}
	return Sval(r[data.KeyField])
}

// ── 消息全文（data-session-content）──

// MessageContentsByKey 按 key 批量取消息全文：message:<id> → messages 表 content（须属
// 该会话）；tool_call:<id> → 该会话 tool_pair 消息 content 解析出的完整参数 JSON；
// tool_result:<tool_call_id> → 该会话 role=tool 消息 **content 原文**（{call,result,async} JSON
// 字符串，2026-09-18 为「异步结果读 message 表」新增的 key 形态——调用方解析 result/async 段）。
// 返回 {<key>: <内容字符串>}；找不到的 key 省略。
func MessageContentsByKey(prj *data.DB, sessionID string, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		switch {
		case strings.HasPrefix(k, "message:"):
			id := strings.TrimPrefix(k, "message:")
			var rec data.Record
			ok, _ := prj.Table("messages").Get(id, &rec)
			if ok && Sval(rec["session_id"]) == sessionID {
				out[k] = Sval(rec["content"])
			}
		case strings.HasPrefix(k, "tool_call:"):
			id := strings.TrimPrefix(k, "tool_call:")
			recs, _, err := prj.Table("messages").Query(data.Query{
				Where: data.Record{"session_id": sessionID, "tool_call_id": id},
			})
			if err == nil && len(recs) > 0 {
				// 新结构 {call,result,async} 取 call.arguments；旧 ToolPairPayload 由
				// ToolViewFields 内部归一（兼容）。
				_, _, _, argsJSON, _, _ := ToolViewFields(recs[0])
				out[k] = argsJSON
			}
		case strings.HasPrefix(k, "tool_result:"):
			id := strings.TrimPrefix(k, "tool_result:")
			recs, _, err := prj.Table("messages").Query(data.Query{
				Where: data.Record{"session_id": sessionID, "tool_call_id": id},
			})
			if err == nil && len(recs) > 0 {
				for _, rec := range recs {
					if Sval(rec["role"]) != "tool" {
						continue
					}
					out[k] = Sval(rec["content"])
					break
				}
			}
		}
	}
	return out
}

// ── 消息视图（data-session-history；tool_pair 展开 / reasoning 截断）──

// MetaOfRecord 取消息记录上的工具 `_meta`（I-60）：map 直取；字符串形态（兼容）尝试 JSON 解析；
// 无/非法 → nil。
func MetaOfRecord(rec data.Record) map[string]any {
	switch v := rec["_meta"].(type) {
	case map[string]any:
		if len(v) > 0 {
			return v
		}
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(v), &m) == nil && len(m) > 0 {
			return m
		}
	}
	return nil
}

// MessageView 把 messages 表记录转为前端消费格式：
//   - tool 消息（role=tool，content={call,result,async}）与旧 tool_pair 消息展开为 map
//     （tool_call_id/tool/arguments/brief/simplified/status/...）
//   - reasoning + brief 截断 3 行（has_more）
//   - 其余原样透传
func MessageView(m data.Record, brief bool) any {
	role := Sval(m["role"])
	typ := Sval(m["type"])
	msgID := Sval(m["message_id"])
	if msgID == "" {
		msgID = Sval(m[data.KeyField])
	}
	if role == "tool" || (role == "assistant" && typ == "tool_pair") {
		toolCallID, taskID, name, argsJSON, result, status := ToolViewFields(m)
		if status == "" {
			status = ToolStatusCompleted
		}
		pair := map[string]any{
			"role":              role,
			"type":              "tool_pair",
			"message_id":        msgID,
			"turn_id":           Sval(m["turn_id"]),
			"tool_call_id":      toolCallID,
			"task_id":           taskID,
			"tool":              name,
			"arguments":         argsJSON,
			"brief":             Sval(m["brief"]), // 落库即 purpose（LLM 必填的调用理由）/简化调用内容
			"result_success":    status == ToolStatusCompleted,
			"simplified":        name + "(" + CompactArgs(argsJSON) + ")",
			"result_simplified": SummarizeResult(result),
			"status":            status,
			"created_at":        Sval(m["created_at"]),
		}
		if brief {
			pair["result"] = ""
			pair["has_more"] = result != ""
		} else {
			pair["result"] = result
		}
		// _meta：该工具自身的 meta 子集（I-60）——前端工具卡片据此本地判断超时裁决项。
		if meta := MetaOfRecord(m); len(meta) > 0 {
			pair["_meta"] = meta
		}
		return pair
	}
	out := RecordView(m, "message_id")
	if brief && role == "assistant" && typ == "reasoning" {
		preview, hasMore := TruncateLines(Sval(m["content"]), 3)
		out["content"] = preview
		out["has_more"] = hasMore
	}
	return out
}

// CompactArgs 生成工具参数单行摘要（对齐旧 formatToolCallArgsCompact）。
func CompactArgs(argsStr string) string {
	var argsMap map[string]any
	if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
		return ""
	}
	var parts []string
	for k, v := range argsMap {
		if s, ok := v.(string); ok && len(s) > 80 {
			parts = append(parts, fmt.Sprintf("%s=...</%d chars>", k, len(s)))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if len(parts) > 3 {
		parts = parts[:3]
		return strings.Join(parts, ", ") + ", ..."
	}
	return strings.Join(parts, ", ")
}

// SummarizeResult 生成工具结果单行摘要。
func SummarizeResult(result string) string {
	if result == "" {
		return "failed"
	}
	for _, line := range strings.SplitN(result, "\n", 2) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > 100 {
				return trimmed[:100] + "..."
			}
			return trimmed
		}
	}
	return "ok"
}

// TruncateLines 返回前 maxLines 行（截断标记 hasMore）。
func TruncateLines(s string, maxLines int) (string, bool) {
	if s == "" {
		return "", false
	}
	lines := strings.SplitN(s, "\n", maxLines+1)
	if len(lines) <= maxLines {
		return s, false
	}
	return strings.Join(lines[:maxLines], "\n"), true
}

// RFC3339Now 返回消息/会话落库用 UTC RFC3339 时间戳。
func RFC3339Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// RFC3339FixedNano 固定 9 位纳秒 RFC3339：保证同轮消息 created_at 字符串排序 = 时间序
// （RFC3339Nano 裁剪尾零导致同秒不同位数排序不稳；对齐 server AppendFull）。
const RFC3339FixedNano = "2006-01-02T15:04:05.000000000Z07:00"

// NewMessageKey 生成 messages 表主键：M + UTC 时间戳(yyyyMMddHHmmss, 14 位) + 微秒(6 位)
// + 4 位随机数 = 25 字符（如 M202609121015307123450842）。key 自带时间序（可区间扫描）；
// 读取/排序仍以 created_at 为准，兼容旧 key（m-<rand>）。
func NewMessageKey() string {
	now := time.Now().UTC()
	var b [2]byte
	_, _ = rand.Read(b[:])
	n := (int(b[0])<<8 | int(b[1])) % 10000
	return fmt.Sprintf("M%s%06d%04d", now.Format("20060102150405"), now.Nanosecond()/1000, n)
}

// MsgRowToChat 把 messages 表行转 ChatMsg（tool_calls JSON 还原；role=tool 的 content 由
// {call,result,async} 归一为裸结果文本；role 空 = 非法行）。
func MsgRowToChat(rec data.Record) (data.ChatMsg, bool) {
	m := data.ChatMsg{Role: Sval(rec["role"])}
	if m.Role == "" {
		return m, false
	}
	if m.Role == "tool" {
		// role=tool 存储形态为 {call,result,async} JSON；LLM 重放需要裸结果文本 → 归一。
		m.Content = ToolLLMContent(Sval(rec["content"]))
	} else {
		m.Content = Sval(rec["content"])
	}
	m.Kind = Sval(rec["kind"])
	m.ToolCallID = Sval(rec["tool_call_id"])
	m.Reasoning = Sval(rec["reasoning"])
	m.Meta = MetaOfRecord(rec) // 工具 meta 子集（若落库）随 ChatMsg 回带（load-messages/context）
	if tc := rec["tool_calls"]; tc != nil {
		var b []byte
		switch v := tc.(type) {
		case string:
			b = []byte(v)
		default:
			b, _ = json.Marshal(v)
		}
		var calls []data.ToolCall
		if json.Unmarshal(b, &calls) == nil {
			m.ToolCalls = calls
		}
	}
	return m, true
}
