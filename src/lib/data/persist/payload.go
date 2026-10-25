// data-* 请求的**定死结构**（61 §3；2026-10-25 起逐域替换 `dataReq` 的宽松 map 解析）。
//
// 约定：每个 payload struct 内嵌 `Header`（req_id + instance_id），其余字段名/类型按 61 声明，
// 不新增包装层（JSON 仍是扁平键）→ 前端 / 插件 / 61 契约零变更。
package persist

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// Header 是所有 data-* 请求的公共头。
type Header struct {
	ReqID      string `json:"req_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

// ConfigPayload 是 prj-config / prompt / prj-security 四动作（list/load/save/delete）的载荷。
type ConfigPayload struct {
	Header
	ID   string         `json:"id,omitempty"`
	Data ConfigSaveData `json:"data,omitempty"`
}

// ConfigSaveData 是 save 的 data：批量 `entries` 优先，回落单键 `key`/`value`（61 §3.1）。
type ConfigSaveData struct {
	ID      string          `json:"id,omitempty"`
	Key     string          `json:"key,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	Entries map[string]any  `json:"entries,omitempty"`
}

// key 取单键操作的键：顶层 id → data.id → data.key（与旧 reqKey 同口径）。
func (p ConfigPayload) key() string {
	if p.ID != "" {
		return p.ID
	}
	if p.Data.ID != "" {
		return p.Data.ID
	}
	return p.Data.Key
}

// UserConfigPayload 是 user-config 四动作的载荷（save 的 data = 整份配置对象）。
type UserConfigPayload struct {
	Header
	ID   string         `json:"id,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

// key 取 user-config 单键操作的键：顶层 id → data.id → data.key。
func (p UserConfigPayload) key() string {
	if p.ID != "" {
		return p.ID
	}
	if p.Data == nil {
		return ""
	}
	if id := scalarString(p.Data["id"]); id != "" {
		return id
	}
	return scalarString(p.Data["key"])
}

// scalarString 把载荷标量转存储字符串（string 原样；数字/bool 取字面量；nil → ""）。
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String()
	case float64, int, int64:
		b, _ := json.Marshal(t) // 1.0 → "1"，1.5 → "1.5"（最短表示）
		return string(b)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// rawScalar 把 JSON 原始值转存储字符串（`"x"` → x；`1` → 1；对象/数组 → 原文）。
func rawScalar(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	return scalarString(v)
}

// SnapshotPayload 是 data-snapshot-{get,set} 的载荷（会话/对话快照；61 §3.5）。
type SnapshotPayload struct {
	Header
	ID   string              `json:"id,omitempty"`
	Data SnapshotPayloadData `json:"data,omitempty"`
}

// SnapshotPayloadData 是 snapshot 的 data：会话/对话定位 + 快照本体（原样 JSON 对象）。
type SnapshotPayloadData struct {
	SessionID string          `json:"session_id,omitempty"`
	DialogID  string          `json:"dialog_id,omitempty"`
	Snapshot  json.RawMessage `json:"snapshot,omitempty"`
}

// sessionID 取会话 id：data.session_id → 顶层 id（与旧 sessionIDOf 同口径）。
func (p SnapshotPayload) sessionID() string {
	if p.Data.SessionID != "" {
		return p.Data.SessionID
	}
	return p.ID
}

// snapshotObject 取快照对象（必须是**非空 JSON 对象**；否则 ok=false → 明确失败）。
func (p SnapshotPayload) snapshotObject() (json.RawMessage, bool) {
	raw := bytes.TrimSpace(p.Data.Snapshot)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return nil, false
	}
	return raw, true
}

// ── 宽容标量（消息面历史载荷出现过「字符串/数字」两种形；定死结构但仍兼容）──

// ID 是 id 标量：接受 JSON 字符串或数字（统一存字符串）。
type ID string

// UnmarshalJSON 兼容 `"s-1"` 与 `123` 两种写法（null/缺省 → 空串）。
func (x *ID) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*x = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*x = ID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*x = ID(n.String())
	return nil
}

// IntValue 是整数标量：接受数字或数字字符串（历史载荷两种都出现过）。
type IntValue int

// UnmarshalJSON 兼容 `12` 与 `"12"`。
func (x *IntValue) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*x = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			*x = 0
			return nil
		}
		*x = IntValue(n)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	i, err := n.Int64()
	if err != nil {
		f, _ := n.Float64()
		i = int64(f)
	}
	*x = IntValue(i)
	return nil
}

// BoolValue 是布尔标量：接受 JSON 布尔与字符串 "true"/"false"（见 61 §3.2 context）。
type BoolValue bool

// UnmarshalJSON 兼容 `true` / `"true"`（其余非空字符串按 false）。
func (x *BoolValue) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*x = false
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*x = BoolValue(strings.EqualFold(strings.TrimSpace(s), "true"))
		return nil
	}
	var v bool
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*x = BoolValue(v)
	return nil
}

// SessionData 是 session 域的业务字段（顶层扁平与 `data` 内两形都接受）。
type SessionData struct {
	ID              ID        `json:"id,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	DialogID        string    `json:"dialog_id,omitempty"`
	TurnID          string    `json:"turn_id,omitempty"`
	BeforeTurnID    string    `json:"before_turn_id,omitempty"`
	ExcludeTurn     string    `json:"exclude_turn,omitempty"`
	IncludeSnapshot BoolValue `json:"include_snapshot,omitempty"`
	Title           string    `json:"title,omitempty"`
	Status          string    `json:"status,omitempty"`
	FinishReason    string    `json:"finish_reason,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	Mode            string    `json:"mode,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	Keys            []string  `json:"keys,omitempty"`
	Mentions        []string  `json:"mentions,omitempty"`
	Agents          []string  `json:"agents,omitempty"`
	Round           IntValue  `json:"round,omitempty"`
	TargetMessages  IntValue  `json:"target_messages,omitempty"`
	TargetBytes     IntValue  `json:"target_bytes,omitempty"`
	FullTokens      *int      `json:"full_tokens,omitempty"`
	BriefTokens     *int      `json:"brief_tokens,omitempty"`
	// GroupParticipants = 群聊名册 save 载荷的 `list:[{agent, role?}]`。
	GroupParticipants []GroupParticipantItem `json:"list,omitempty"`
}

// GroupParticipantItem 是名册 save 的单行（session_id 由外层载荷统一提供）。
type GroupParticipantItem struct {
	Agent string `json:"agent,omitempty"`
	Role  string `json:"role,omitempty"`
}

// SessionPayload 是 data-session-* 的载荷（内嵌顶层业务字段 + 原样 data）。
type SessionPayload struct {
	Header
	ID ID `json:"id,omitempty"`
	SessionData
	Data json.RawMessage `json:"data,omitempty"`
}

// fields 合并两形载荷（`data` 优先；顶层只补 `data` 未给的键 —— 与旧 parseDataReq 同口径）。
// id 例外：顶层 `id` 优先于 `data.id`（旧 reqKey/标题、删除用的就是顶层 id 优先）。
func (p SessionPayload) fields() SessionData {
	f := mergeFlat(p.SessionData, p.Data)
	if p.ID != "" {
		f.ID = p.ID
	}
	return f
}

// mergeFlat 把内嵌结构（载荷顶层业务字段）与被 `data` 包裹的同构结构合并：
// **`data` 优先，顶层只补缺**（= 旧 parseDataReq「顶层字段仅在 data 未提供时并入」）。
func mergeFlat[T any](flat T, raw json.RawMessage) T {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return flat
	}
	var d T
	if json.Unmarshal(raw, &d) != nil {
		return flat
	}
	b1, err1 := json.Marshal(flat)
	b2, err2 := json.Marshal(d)
	if err1 != nil || err2 != nil {
		return flat
	}
	m1, m2 := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	_ = json.Unmarshal(b1, &m1)
	_ = json.Unmarshal(b2, &m2)
	for k, v := range m1 {
		if _, ok := m2[k]; !ok {
			m2[k] = v
		}
	}
	out, _ := json.Marshal(m2)
	var merged T
	_ = json.Unmarshal(out, &merged)
	return merged
}

// ── inbox（INBOX 内部面：llm-server → persist；61 §3.2）──

// InboxData 是 inbox 域的业务字段（append / list / update / commit 共用）。
type InboxData struct {
	ID             ID     `json:"id,omitempty"`
	InboxID        string `json:"inbox_id,omitempty"`
	DialogID       string `json:"dialog_id,omitempty"`
	Session        string `json:"session,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	Type           string `json:"type,omitempty"`
	LLM            string `json:"llm,omitempty"`
	Content        string `json:"content,omitempty"`
	Think          any    `json:"think,omitempty"`
	Effort         any    `json:"effort,omitempty"`
	ScenarioID     string `json:"scenario_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Source         string `json:"source,omitempty"`
	Status         string `json:"status,omitempty"`
	Brief          string `json:"brief,omitempty"`
	ToolCallStatus string `json:"tool_call_status,omitempty"`
	// Msg 是 commit 的消息体（ChatMsg 形态，多态字段 → 原样 JSON，由调用侧解码）。
	Msg json.RawMessage `json:"msg,omitempty"`
}

// InboxPayload 是 data-inbox-* 的载荷（内嵌顶层业务字段 + 原样 data）。
type InboxPayload struct {
	Header
	ID ID `json:"id,omitempty"`
	InboxData
	Data json.RawMessage `json:"data,omitempty"`
}

// fields 合并两形载荷（`data` 优先；顶层 id 例外优先）。
func (p InboxPayload) fields() InboxData {
	f := mergeFlat(p.InboxData, p.Data)
	if p.ID != "" {
		f.ID = p.ID
	}
	return f
}

// ── turn-event（TURN EVENT 内部面；61 §3.2）──

// TurnEventData 是 turn-event 域的业务字段（append / cleanup 共用）。
type TurnEventData struct {
	SessionID     string          `json:"session_id,omitempty"`
	TurnID        string          `json:"turn_id,omitempty"`
	Seq           IntValue        `json:"seq,omitempty"`
	Type          string          `json:"type,omitempty"`
	At            string          `json:"at,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	RetentionDays IntValue        `json:"retention_days,omitempty"`
}

// TurnEventPayload 是 data-turn-event-* 的载荷（内嵌顶层业务字段 + 原样 data）。
type TurnEventPayload struct {
	Header
	TurnEventData
	Data json.RawMessage `json:"data,omitempty"`
}

// fields 合并两形载荷（`data` 优先、顶层补缺）。
func (p TurnEventPayload) fields() TurnEventData {
	return mergeFlat(p.TurnEventData, p.Data)
}

// ── ask（未答 / 失效回查；61 §3.2）──

// AskData 是 ask 域的业务字段（当前仅 pending）。
type AskData struct {
	DialogID    ID        `json:"dialog_id,omitempty"`
	AskID       string    `json:"ask_id,omitempty"`
	PendingOnly BoolValue `json:"pending_only,omitempty"`
}

// AskPayload 是 data-ask-* 的载荷（内嵌顶层业务字段 + 原样 data）。
type AskPayload struct {
	Header
	AskData
	Data json.RawMessage `json:"data,omitempty"`
}

// fields 合并两形载荷（`data` 优先、顶层补缺）。
func (p AskPayload) fields() AskData {
	return mergeFlat(p.AskData, p.Data)
}

// ── tasktree（任务树域；61 §3.4）──

// TaskNodePayload 是节点行（消息面**表列名口径**：node_id / parent_node_id / task_id 别名）。
type TaskNodePayload struct {
	NodeID       ID     `json:"node_id,omitempty"`
	TaskID       ID     `json:"task_id,omitempty"`
	NodeType     string `json:"node_type,omitempty"`
	TopSession   string `json:"top_session,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	ParentNodeID string `json:"parent_node_id,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Title        string `json:"title,omitempty"`
	Name         string `json:"name,omitempty"`
	Status       string `json:"status,omitempty"`
	ToolCallID   string `json:"tool_call_id,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
	State        string `json:"state,omitempty"`
	InstanceID   string `json:"instance_id,omitempty"`
	WorkDir      string `json:"work_dir,omitempty"`
	ArgsDigest   string `json:"args_digest,omitempty"`
	ResultDigest string `json:"result_digest,omitempty"`
	ExecJSON     string `json:"exec_json,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	DoneAt       string `json:"done_at,omitempty"`
	DeletedAt    string `json:"deleted_at,omitempty"`
	// Closed 逻辑删除标记（nil = 载荷未带该键，与 false 语义不同）。
	Closed      *BoolValue       `json:"closed,omitempty"`
	LoopCurrent IntValue         `json:"loop_current,omitempty"`
	LoopTotal   IntValue         `json:"loop_total,omitempty"`
	Steps       []DslStepPayload `json:"steps,omitempty"`
	Shadow      BoolValue        `json:"shadow,omitempty"`
	ReturnKind  string           `json:"return_kind,omitempty"`
	// ReturnInline inline 全文 / ReturnFile 文件名 / ReturnSize 字节数（DSL-3 展示字段）。
	ReturnInline string   `json:"return_inline,omitempty"`
	ReturnFile   string   `json:"return_file,omitempty"`
	ReturnSize   IntValue `json:"return_size,omitempty"`
}

// DslStepPayload 是 `steps[]` 单行（DSL 作业的一次 LLM 步骤执行记录）。
type DslStepPayload struct {
	No          IntValue `json:"no,omitempty"`
	Status      string   `json:"status,omitempty"`
	Purpose     string   `json:"purpose,omitempty"`
	ElapsedMs   IntValue `json:"elapsed_ms,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	StatementID string   `json:"statement_id,omitempty"`
}

// TasktreeListPayload 是 data-tasktree-list 的载荷。
type TasktreeListPayload struct {
	Header
	ID  ID `json:"id,omitempty"`
	Key ID `json:"key,omitempty"`
	TasktreeQuery
	Data json.RawMessage `json:"data,omitempty"`
}

// TasktreeTasksPayload 是 data-tasktree-tasks 的载荷。
type TasktreeTasksPayload struct {
	Header
	TasktreeQuery
	Data json.RawMessage `json:"data,omitempty"`
}

// TasktreeQuery 是任务树查询过滤键（list / tasks 共用）。
type TasktreeQuery struct {
	DialogID      string    `json:"dialog_id,omitempty"`
	TopSession    string    `json:"top_session,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	Mode          string    `json:"mode,omitempty"`
	IncludeClosed BoolValue `json:"include_closed,omitempty"`
	Shadow        BoolValue `json:"shadow,omitempty"`
}

// merged 合并两形载荷（`data` 优先、顶层补缺）。
func (p TasktreeListPayload) merged() TasktreeListPayload {
	q := p
	q.Data = nil // 只把顶层业务字段作为「补缺」来源，避免 data 自嵌套
	return mergeFlat(q, p.Data)
}

// fields 合并两形载荷（`data` 优先、顶层补缺）。
func (p TasktreeTasksPayload) fields() TasktreeQuery {
	return mergeFlat(p.TasktreeQuery, p.Data)
}

// TasktreeUpsertPayload 是 data-tasktree-upsert 的载荷（节点字段平铺）。
//
// 不内嵌 Header：节点行本身也有 `instance_id`，二者同深度会歧义 → 头部字段显式声明（顶层优先）。
type TasktreeUpsertPayload struct {
	ReqID      string `json:"req_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	TaskNodePayload
	DialogID string          `json:"dialog_id,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// merged 合并两形载荷（`data` 优先、顶层补缺）；节点字段与 dialog_id 同层。
func (p TasktreeUpsertPayload) merged() (TaskNodePayload, string) {
	q := p
	q.Data = nil // 只把顶层业务字段作为「补缺」来源，避免 data 自嵌套
	m := mergeFlat(q, p.Data)
	return m.TaskNodePayload, m.DialogID
}

// TasktreeDeletePayload 是 data-tasktree-delete 的载荷（`node_id` / `id` / `filter.node_id` 三形）。
type TasktreeDeletePayload struct {
	Header
	ID       ID        `json:"id,omitempty"`
	Key      ID        `json:"key,omitempty"`
	NodeID   string    `json:"node_id,omitempty"`
	DialogID string    `json:"dialog_id,omitempty"`
	Shadow   BoolValue `json:"shadow,omitempty"`
	// Filter 兼容 `{filter:{node_id}}` 定位。
	Filter struct {
		NodeID string `json:"node_id,omitempty"`
	} `json:"filter,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// merged 合并两形载荷（`data` 优先、顶层补缺）。
func (p TasktreeDeletePayload) merged() TasktreeDeletePayload {
	q := p
	q.Data = nil // 只把顶层业务字段作为「补缺」来源，避免 data 自嵌套
	return mergeFlat(q, p.Data)
}

// ── knowledge（知识库域；61 §3.3）──

// KnowledgePayload 是 data-knowledge-* 的载荷（11 个动作共用一套键）。
type KnowledgePayload struct {
	Header
	KnowledgeData
	Data json.RawMessage `json:"data,omitempty"`
}

// KnowledgeData 是知识库动作的业务字段。
type KnowledgeData struct {
	Kind    string `json:"kind,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Path    string `json:"path,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	NewName string `json:"new_name,omitempty"`
	// Doc 文档领域形态（save；原样 JSON 对象）。
	Doc json.RawMessage `json:"doc,omitempty"`
	// Files 导入清单（import；content = base64 文件字节）。
	Files []KnowledgeImportItem `json:"files,omitempty"`
}

// KnowledgeImportItem 是导入单文件。
type KnowledgeImportItem struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

// fields 合并两形载荷（`data` 优先、顶层补缺）。
func (p KnowledgePayload) fields() KnowledgeData {
	return mergeFlat(p.KnowledgeData, p.Data)
}

// ── memory（记忆库 + 提取进度；61 §3.3）──

// MemoryPayload 是 data-memory-* 的载荷（list/read/save/delete + extract-*）。
type MemoryPayload struct {
	Header
	MemoryData
	Data json.RawMessage `json:"data,omitempty"`
}

// MemoryData 是记忆库动作的业务字段。
type MemoryData struct {
	Category string `json:"category,omitempty"`
	Content  string `json:"content,omitempty"`
	// SessionID / LastTurnID 仅提取进度（extract-*）使用。
	SessionID  string `json:"session_id,omitempty"`
	LastTurnID string `json:"last_turn_id,omitempty"`
}

// fields 合并两形载荷（`data` 优先、顶层补缺）。
func (p MemoryPayload) fields() MemoryData {
	return mergeFlat(p.MemoryData, p.Data)
}

// nonNullRaw 把缺省 / `null` 的原始 JSON 归一为 nil（对齐旧 map 取值的「无该键」语义）。
func nonNullRaw(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// rawText 把原始 JSON 标量/对象转存储字符串（字符串去引号、其余原文 —— 对齐旧 wire.str 口径）。
func rawText(raw json.RawMessage) string {
	raw = nonNullRaw(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}

// rawStrList 解析引用名单：JSON 数组文本 / 数组（两种形态都接受），空元素剔除（= 旧 strListJSON）。
func rawStrList(raw json.RawMessage) []string {
	raw = nonNullRaw(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' { // JSON 数组文本（capfs 读盘形态）
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		var arr []string
		if json.Unmarshal([]byte(text), &arr) != nil {
			return []string{text}
		}
		return cleanStrList(arr)
	}
	var arr []string
	if json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	return cleanStrList(arr)
}

// cleanStrList 去空白元素（全空 → nil）。
func cleanStrList(arr []string) []string {
	out := make([]string, 0, len(arr))
	for _, s := range arr {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ── mcp（MCP 配置域；61 §3.1）──

// McpPayload 是 data-mcp-* 的载荷（server 字段与门面 DTO 同形 → 直接内嵌复用）。
type McpPayload struct {
	Header
	ID  ID `json:"id,omitempty"`
	Key ID `json:"key,omitempty"`
	// OldName / OldLevel 改名前的定位（save）。
	OldName  string `json:"old_name,omitempty"`
	OldLevel string `json:"old_level,omitempty"`
	facade.McpServer
	Data json.RawMessage `json:"data,omitempty"`
}

// merged 合并两形载荷（`data` 优先、顶层补缺）。
func (p McpPayload) merged() McpPayload {
	q := p
	q.Data = nil // 只把顶层业务字段作为「补缺」来源，避免 data 自嵌套
	return mergeFlat(q, p.Data)
}

// target 取请求目标名：`name` → 顶层 id → data.id → data.key（= 旧 RequestID 口径）。
func (p McpPayload) target() string {
	if p.Name != "" {
		return p.Name
	}
	if p.ID != "" {
		return string(p.ID)
	}
	return string(p.Key)
}

// ── scenario（场景域；61 §3.4）──

// ScenarioPayload 是 data-scenario-* 的载荷（场景字段平铺 + `{data:{…}}` 两形）。
type ScenarioPayload struct {
	Header
	ID  ID `json:"id,omitempty"`
	Key ID `json:"key,omitempty"`
	ScenarioData
	Data json.RawMessage `json:"data,omitempty"`
}

// ScenarioData 是场景的业务字段（agent 列表见 ScenarioAgentPayload）。
type ScenarioData struct {
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Level       string                 `json:"level,omitempty"`
	Agents      []ScenarioAgentPayload `json:"agents,omitempty"`
	CreatedAt   string                 `json:"createdAt,omitempty"`
	UpdatedAt   string                 `json:"updatedAt,omitempty"`
}

// ScenarioAgentPayload 是场景内单个 agent（`skills` / `resources` 兼容数组与 JSON 数组文本）。
type ScenarioAgentPayload struct {
	Name         string          `json:"name,omitempty"`
	Description  string          `json:"description,omitempty"`
	RoleTag      string          `json:"roleTag,omitempty"`
	IsMain       BoolValue       `json:"isMain,omitempty"`
	Prompt       string          `json:"prompt,omitempty"`
	Tools        json.RawMessage `json:"tools,omitempty"`
	Skills       json.RawMessage `json:"skills,omitempty"`
	Resources    json.RawMessage `json:"resources,omitempty"`
	LLMRef       string          `json:"llmRef,omitempty"`
	DelegateCond string          `json:"delegateCond,omitempty"`
	Ref          string          `json:"ref,omitempty"`
}

// merged 合并两形载荷（`data` 优先、顶层补缺）。
func (p ScenarioPayload) merged() ScenarioPayload {
	q := p
	q.Data = nil // 只把顶层业务字段作为「补缺」来源，避免 data 自嵌套
	return mergeFlat(q, p.Data)
}

// target 取场景 id：`id` → `key`（= 旧 RequestID 口径）。
func (p ScenarioPayload) target() string {
	if p.ID != "" {
		return string(p.ID)
	}
	return string(p.Key)
}

// scenarioOf 把载荷转门面场景 DTO（id 必填；agents 逐项转领域字段）。
func scenarioOf(p ScenarioPayload) facade.Scenario {
	sc := facade.Scenario{
		ID:          p.target(),
		Name:        p.Name,
		Description: p.Description,
		Level:       p.Level,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
	for _, a := range p.Agents {
		sc.Agents = append(sc.Agents, facade.ScenarioAgent{
			Name:         a.Name,
			Description:  a.Description,
			RoleTag:      a.RoleTag,
			IsMain:       bool(a.IsMain),
			Prompt:       a.Prompt,
			Tools:        rawText(a.Tools),
			Skills:       rawStrList(a.Skills),
			Resources:    rawStrList(a.Resources),
			LLMRef:       a.LLMRef,
			DelegateCond: a.DelegateCond,
			Ref:          a.Ref,
		})
	}
	return sc
}

// configEntries 把 save 的 data 归一为键值批：`entries` 非空优先（id = 字典序首键），
// 否则回落单键 `key`/`value`；无键 → ok=false（61 §3.1 语义，与旧 wire.ConfigSaveEntries 一致）。
func configEntries(d ConfigSaveData) (entries map[string]string, id string, ok bool) {
	if len(d.Entries) > 0 {
		entries = make(map[string]string, len(d.Entries))
		keys := make([]string, 0, len(d.Entries))
		for k, v := range d.Entries {
			entries[k] = scalarString(v)
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return entries, keys[0], true
	}
	if d.Key == "" {
		return nil, "", false
	}
	return map[string]string{d.Key: rawScalar(d.Value)}, d.Key, true
}
