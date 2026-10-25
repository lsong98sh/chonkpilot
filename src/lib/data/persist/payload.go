// data-* 请求的**定死结构**（61 §3；2026-10-25 起逐域替换 `dataReq` 的宽松 map 解析）。
//
// 约定：每个 payload struct 内嵌 `Header`（req_id + instance_id），其余字段名/类型按 61 声明，
// 不新增包装层（JSON 仍是扁平键）→ 前端 / 插件 / 61 契约零变更。
package persist

import (
	"bytes"
	"encoding/json"
	"sort"
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
