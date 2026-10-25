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
