// 本文件是 **tasktree / knowledge / scenario / memory** 等域的
// 「门面领域 DTO → 消息面应答载荷」组装层（续 `wire.go` 的 session / turn / message 三域）。
//
// 2026-10-25（payload struct 化）：**MQ 入参解析**已由 persist 各域的定死 struct（`persist/payload.go`）
// 承担，逐域 `*FromWire` 随迁删除；本包只剩**出参组装**与 `ScenarioFromWire`（场景**读盘行** → DTO，
// 供 internal/scenario 列举/定位）。
//
// 为什么保留出参组装（G-33：wire 形状与门面 DTO 是两件事，不合并）：
//   - 门面 DTO 用**领域字段**（任务节点的 `ID` / `ParentID`、知识库的文档领域形态、清单条目的
//     `DocIDs`、场景的 `Agents`、记忆条目的 `Category/Content`）；消息面用**绑定形状**
//     （表列名 `node_id` / `doc_ids`、`{id,key}` 双写、可选键「非空才带」等）；
//   - 应答载荷在 MQ（persist）与 browser 入口（`dataViaPersist`）两条路径共用同一份组装 →
//     外形逐字一致（零分叉）。
//
// 归属：本包在 data 组件内（`chonkpilot-data/facade/wire`）——即 23 §7「翻译留在门面实现侧」
// 的实现侧；门面定义包（`facade`）本身不引入任何 wire 知识。
//
// 边界：本包只做「字段搬运 + 形状改写」，不含业务规则（不查库、不判定、不落盘）。
package wire

import (
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// ── 任务树（tasktree 域）──────────────────────────────────────────

// TaskNodeToWire 把门面任务节点转回消息面节点行（表列名口径；语义与 `RecordView(r,"node_id")`
// 一致：节点固定列恒带，可选列非空才带）。
func TaskNodeToWire(n facade.TaskNode) map[string]any {
	row := map[string]any{
		"node_id":        n.ID,
		"node_type":      n.NodeType,
		"top_session":    n.TopSession,
		"session_id":     n.SessionID,
		"task_id":        n.ID,
		"kind":           n.Kind,
		"parent_node_id": n.ParentID,
		"title":          n.Title,
		"status":         n.Status,
		"created_at":     n.CreatedAt,
		"updated_at":     n.UpdatedAt,
	}
	for k, v := range map[string]string{
		"finished_at":   n.FinishedAt,
		"tool_call_id":  n.ToolCallID,
		"instance_id":   n.InstanceID,
		"work_dir":      n.WorkDir,
		"state":         n.State,
		"args_digest":   n.ArgsDigest,
		"result_digest": n.ResultDigest,
		"exec_json":     n.ExecJSON,
		"started_at":    n.StartedAt,
		"done_at":       n.DoneAt,
		"deleted_at":    n.DeletedAt,
		"return_kind":   n.ReturnKind,
		"return_inline": n.ReturnInline,
		"return_file":   n.ReturnFile,
	} {
		put(row, k, v)
	}
	if n.Closed != nil {
		row["closed"] = *n.Closed
	}
	// DSL-3 展示字段（非零才带；缺省与既有载荷逐字节等价）。
	if n.LoopCurrent != 0 {
		row["loop_current"] = n.LoopCurrent
	}
	if n.LoopTotal != 0 {
		row["loop_total"] = n.LoopTotal
	}
	if n.ReturnSize != 0 {
		row["return_size"] = n.ReturnSize
	}
	if n.Shadow {
		row["shadow"] = true
	}
	if len(n.Steps) > 0 {
		row["steps"] = DslStepsToWire(n.Steps)
	}
	return row
}

// TaskNodeFromWire 把消息面节点行 / 落库入参转成门面 DTO（`node_id` 优先，回落 `task_id`）。
func TaskNodeFromWire(m map[string]any) facade.TaskNode {
	id := str(m["node_id"])
	if id == "" {
		id = str(m["task_id"])
	}
	n := facade.TaskNode{
		ID:           id,
		NodeType:     str(m["node_type"]),
		TopSession:   str(m["top_session"]),
		SessionID:    str(m["session_id"]),
		ParentID:     str(m["parent_node_id"]),
		Kind:         str(m["kind"]),
		Title:        str(m["title"]),
		Status:       str(m["status"]),
		ToolCallID:   str(m["tool_call_id"]),
		CreatedAt:    str(m["created_at"]),
		UpdatedAt:    str(m["updated_at"]),
		FinishedAt:   str(m["finished_at"]),
		State:        str(m["state"]),
		InstanceID:   str(m["instance_id"]),
		WorkDir:      str(m["work_dir"]),
		ArgsDigest:   str(m["args_digest"]),
		ResultDigest: str(m["result_digest"]),
		ExecJSON:     str(m["exec_json"]),
		StartedAt:    str(m["started_at"]),
		DoneAt:       str(m["done_at"]),
		DeletedAt:    str(m["deleted_at"]),

		LoopCurrent:  intOf(m["loop_current"]),
		LoopTotal:    intOf(m["loop_total"]),
		Steps:        DslStepsFromWire(m["steps"]),
		Shadow:       truthy(m["shadow"]),
		ReturnKind:   str(m["return_kind"]),
		ReturnInline: str(m["return_inline"]),
		ReturnFile:   str(m["return_file"]),
		ReturnSize:   intOf(m["return_size"]),
	}
	if v, ok := m["closed"]; ok { // 有键才带（含 false：与"无键"语义不同）
		closed := truthy(v)
		n.Closed = &closed
	}
	return n
}

// DslStepsToWire 把门面 DSL 步骤记录转回消息面数组（`dsl_job.steps[]`）。
func DslStepsToWire(steps []facade.DslStep) []any {
	out := make([]any, 0, len(steps))
	for _, s := range steps {
		row := map[string]any{"no": s.No}
		put(row, "status", s.Status)
		put(row, "purpose", s.Purpose)
		if s.ElapsedMs != 0 {
			row["elapsed_ms"] = s.ElapsedMs
		}
		put(row, "created_at", s.CreatedAt)
		put(row, "session_id", s.SessionID)
		put(row, "statement_id", s.StatementID)
		out = append(out, row)
	}
	return out
}

// DslStepsFromWire 解析消息面 `steps` 数组为门面 DSL 步骤记录（非数组 / 元素非对象 → 跳过）。
func DslStepsFromWire(v any) []facade.DslStep {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]facade.DslStep, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, facade.DslStep{
			No:          intOf(m["no"]),
			Status:      str(m["status"]),
			Purpose:     str(m["purpose"]),
			ElapsedMs:   int64(intOf(m["elapsed_ms"])),
			CreatedAt:   str(m["created_at"]),
			SessionID:   str(m["session_id"]),
			StatementID: str(m["statement_id"]),
		})
	}
	return out
}

// TaskToWire 把门面任务快照转回消息面任务行（固定键集 + 存在即带的增补字段；
// 既有外形逐字不变：`type`/`purpose` 为别名、`elapsed_seconds` 恒 0）。
func TaskToWire(t facade.Task) map[string]any {
	row := map[string]any{
		"task_id":         t.ID,
		"status":          t.Status,
		"kind":            t.Kind,
		"type":            t.Kind,
		"session_id":      t.SessionID,
		"top_session":     t.TopSession,
		"name":            t.Name,
		"purpose":         t.Name,
		"started_at":      t.StartedAt,
		"elapsed_seconds": int64(0),
	}
	put(row, "tool_call_id", t.ToolCallID)
	put(row, "state", t.State)
	put(row, "exec_json", t.ExecJSON)
	if t.Awaiting != nil {
		row["awaiting"] = t.Awaiting
	}
	// DSL-3 展示字段（非零才带）。
	put(row, "return_kind", t.ReturnKind)
	put(row, "return_inline", t.ReturnInline)
	put(row, "return_file", t.ReturnFile)
	if t.LoopCurrent != 0 {
		row["loop_current"] = t.LoopCurrent
	}
	if t.LoopTotal != 0 {
		row["loop_total"] = t.LoopTotal
	}
	if t.ReturnSize != 0 {
		row["return_size"] = t.ReturnSize
	}
	if t.Shadow {
		row["shadow"] = true
	}
	if len(t.Steps) > 0 {
		row["steps"] = DslStepsToWire(t.Steps)
	}
	return row
}

// TasktreeListPayload 是 `data-tasktree-list` 结果 DTO。
type TasktreeListPayload struct {
	Nodes []any `json:"nodes"`
}

// TasktreeListResult 组装 `data-tasktree-list` 结果载荷 `{nodes:[节点行…]}`。
func TasktreeListResult(nodes []facade.TaskNode) TasktreeListPayload {
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, TaskNodeToWire(n))
	}
	return TasktreeListPayload{Nodes: out}
}

// TasktreeTasksPayload 是 `data-tasktree-tasks` 结果 DTO。
type TasktreeTasksPayload struct {
	List []any `json:"list"`
}

// TasktreeTasksResult 组装 `data-tasktree-tasks` 结果载荷 `{list:[任务行…]}`。
func TasktreeTasksResult(list []facade.Task) TasktreeTasksPayload {
	out := make([]any, 0, len(list))
	for _, t := range list {
		out = append(out, TaskToWire(t))
	}
	return TasktreeTasksPayload{List: out}
}

// ── 知识库（knowledge 域）────────────────────────────────────────

// KnowledgeRootPayload 是 `data-knowledge-root` 结果 DTO。
type KnowledgeRootPayload struct {
	Root string `json:"root"`
	Kind string `json:"kind"`
}

// KnowledgeRootResult 组装 `data-knowledge-root` 结果载荷 `{root, kind}`。
func KnowledgeRootResult(resp facade.KnowledgeRootResponse) KnowledgeRootPayload {
	return KnowledgeRootPayload{Root: resp.Root, Kind: resp.Kind}
}

// KnowledgeListPayload 是 `data-knowledge-list` 结果 DTO。
//
// S-7（DATA-OPT-1 P3）：目录 / 文件行**直接复用门面类型**（`facade.KnowledgeDir` /
// `facade.KnowledgeFile` —— 字段与 json tag 与既有行 DTO 逐字一致），不再另立同名 Row。
type KnowledgeListPayload struct {
	Dir   string                 `json:"dir"`
	Dirs  []facade.KnowledgeDir  `json:"dirs"`
	Files []facade.KnowledgeFile `json:"files"`
}

// KnowledgeListResult 组装 `data-knowledge-list` 结果载荷 `{dir, dirs:[{name,path}], files:[…]}`。
// 空集合归一为 `nil`（JSON `null`）——与拆分前「逐项 append 到 nil 切片」的载荷逐字节一致。
func KnowledgeListResult(resp facade.KnowledgeListResponse) KnowledgeListPayload {
	if len(resp.Dirs) == 0 {
		resp.Dirs = nil
	}
	if len(resp.Files) == 0 {
		resp.Files = nil
	}
	return KnowledgeListPayload{Dir: resp.Dir, Dirs: resp.Dirs, Files: resp.Files}
}

// KnowledgeReadPayload 是 `data-knowledge-read` 结果 DTO。
type KnowledgeReadPayload struct {
	Source string              `json:"source"`
	Doc    facade.KnowledgeDoc `json:"doc"`
}

// KnowledgeReadResult 组装 `data-knowledge-read` 结果载荷 `{source, doc}`。
func KnowledgeReadResult(resp facade.KnowledgeReadResponse) KnowledgeReadPayload {
	return KnowledgeReadPayload{Source: resp.Source, Doc: resp.Doc}
}

// ── 场景（scenario 域）───────────────────────────────────────────

// ScenarioToWire 把门面场景转回消息面场景对象（`id` + `key` 双写、级别与 agent 序列）
// ——既有外形逐字不变（v6 文件化前后一致）。
func ScenarioToWire(sc facade.Scenario) map[string]any {
	agents := make([]any, 0, len(sc.Agents))
	for _, a := range sc.Agents {
		row := map[string]any{
			"name": a.Name, "description": a.Description, "roleTag": a.RoleTag,
			"isMain": a.IsMain, "prompt": a.Prompt,
		}
		put(row, "tools", a.Tools)
		put(row, "llmRef", a.LLMRef)
		put(row, "delegateCond", a.DelegateCond)
		put(row, "ref", a.Ref)
		// 参考名单（skills / resources；可选，非空才带）：供 capfs。agentToDoc 的 refListOf 消费，
		// 同时作为 list/get 应答载荷字段（61 §3.4 agents[] 元素形状）。
		if len(a.Skills) > 0 {
			row["skills"] = strSliceAny(a.Skills)
		}
		if len(a.Resources) > 0 {
			row["resources"] = strSliceAny(a.Resources)
		}
		agents = append(agents, row)
	}
	out := map[string]any{
		"id": sc.ID, "key": sc.ID, "name": sc.Name, "description": sc.Description,
		"level": sc.Level, "agents": agents,
	}
	put(out, "createdAt", sc.CreatedAt)
	put(out, "updatedAt", sc.UpdatedAt)
	return out
}

// ScenarioFromWire 把场景行（`scenario.json` 读盘结果）转成门面 DTO（internal/scenario 列举/定位用）。
func ScenarioFromWire(m map[string]any) facade.Scenario {
	id := str(m["id"])
	if id == "" {
		id = str(m["key"])
	}
	sc := facade.Scenario{
		ID:          id,
		Name:        str(m["name"]),
		Description: str(m["description"]),
		Level:       str(m["level"]),
		CreatedAt:   str(m["createdAt"]),
		UpdatedAt:   str(m["updatedAt"]),
	}
	if raw, ok := m["agents"].([]any); ok {
		for _, e := range raw {
			am, ok := e.(map[string]any)
			if !ok {
				continue
			}
			sc.Agents = append(sc.Agents, facade.ScenarioAgent{
				Name:         str(am["name"]),
				Description:  str(am["description"]),
				RoleTag:      str(am["roleTag"]),
				IsMain:       truthy(am["isMain"]),
				Prompt:       str(am["prompt"]),
				Tools:        str(am["tools"]),
				Skills:       strListJSON(am["skills"]),
				Resources:    strListJSON(am["resources"]),
				LLMRef:       str(am["llmRef"]),
				DelegateCond: str(am["delegateCond"]),
				Ref:          str(am["ref"]),
			})
		}
	}
	return sc
}

// ScenarioListPayload 是 `data-scenario-list` 结果 DTO。
type ScenarioListPayload struct {
	OK   bool  `json:"ok"`
	List []any `json:"list"`
}

// ScenarioListResult 组装 `data-scenario-list` 结果载荷 `{ok:true, list:[…]}`。
func ScenarioListResult(list []facade.Scenario) ScenarioListPayload {
	out := make([]any, 0, len(list))
	for _, sc := range list {
		out = append(out, ScenarioToWire(sc))
	}
	return ScenarioListPayload{OK: true, List: out}
}

// ScenarioGetPayload 是 `data-scenario-load` 结果 DTO。
type ScenarioGetPayload struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

// ScenarioGetResult 组装 `data-scenario-load` 结果载荷 `{ok:true, data: 场景}`。
func ScenarioGetResult(sc facade.Scenario) ScenarioGetPayload {
	return ScenarioGetPayload{OK: true, Data: ScenarioToWire(sc)}
}

// ScenarioIDPayload 是场景 save 结果 DTO。
type ScenarioIDPayload struct {
	OK bool   `json:"ok"`
	ID string `json:"id"`
}

// ScenarioIDResult 组装 `{ok:true, id:<场景 id>}`（save）。
func ScenarioIDResult(id string) ScenarioIDPayload {
	return ScenarioIDPayload{OK: true, ID: id}
}

// ── MCP 配置（mcp 域）────────────────────────────────────────────

// McpServerToWire 把门面 MCP 定义转回消息面对象（字段名 = servers.list / 前端表单 / gateway
// ServerEntry；`level` 随列出/保存承载级别归属）。
func McpServerToWire(s facade.McpServer) map[string]any {
	out := map[string]any{
		"name":      s.Name,
		"enabled":   s.Enabled,
		"level":     s.Level,
		"runtime":   s.Runtime,
		"args":      strSliceAny(s.Args),
		"url":       s.URL,
		"transport": s.Transport,
		"timeout":   s.Timeout,
		"namespace": s.Namespace,
		"cwd":       s.Cwd,
		"env":       strSliceAny(s.Env),
		"hot_tools": strSliceAny(s.HotTools),
	}
	put(out, "description", s.Description)
	put(out, "category", s.Category)
	if len(s.Headers) > 0 {
		h := make(map[string]any, len(s.Headers))
		for k, v := range s.Headers {
			h[k] = v
		}
		out["headers"] = h
	}
	if s.Isolate != nil {
		out["isolate"] = *s.Isolate
	}
	if s.Sandbox != nil {
		out["sandbox"] = *s.Sandbox
	}
	return out
}

// McpListPayload 是 `data-mcp-list` 结果 DTO。
type McpListPayload struct {
	List []any `json:"list"`
}

// McpListResult 组装 `data-mcp-list` 结果载荷 `{list:[…]}`。
func McpListResult(list []facade.McpServer) McpListPayload {
	out := make([]any, 0, len(list))
	for _, s := range list {
		out = append(out, McpServerToWire(s))
	}
	return McpListPayload{List: out}
}

// McpGetPayload 是 `data-mcp-load` 结果 DTO。
type McpGetPayload struct {
	Data any `json:"data"`
}

// McpGetResult 组装 `data-mcp-load` 结果载荷 `{data: 定义}`。
func McpGetResult(s facade.McpServer) McpGetPayload {
	return McpGetPayload{Data: McpServerToWire(s)}
}

// McpNamePayload 是 MCP save 结果 DTO。
type McpNamePayload struct {
	OK   bool   `json:"ok"`
	Name string `json:"name"`
}

// McpNameResult 组装 `{ok:true, name:<server 名>}`（save 应答）。
func McpNameResult(name string) McpNamePayload {
	return McpNamePayload{OK: true, Name: name}
}

// strSliceAny 把 []string 转 []any（缺省 nil → 空数组，保证 JSON 恒为数组）。
func strSliceAny(ss []string) []any {
	if ss == nil {
		return []any{}
	}
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// strListJSON 解析场景 agent 的**引用名单**（skills / resources）：兼容
//   - 数组形态（[]any / []string）—— 消息面 / 前端载荷（61 §3.4 agents[] 元素）；
//   - JSON 数组文本（string）—— capfs 读盘结果（agentFromDoc 把契约 refs 序列化为 JSON 数组文本）；
//   - 非 JSON 单值字符串 → 单元素名单。
//
// 空元素被剔除；解析不出 → nil。与 capfs.refListOf 同口径（写/读两侧对同一值可往返）。
func strListJSON(v any) []string {
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, it := range arr {
				if it = strings.TrimSpace(it); it != "" {
					out = append(out, it)
				}
			}
			return out
		}
		return []string{s}
	}
	if ss := strList(v); len(ss) > 0 {
		out := make([]string, 0, len(ss))
		for _, it := range ss {
			if it = strings.TrimSpace(it); it != "" {
				out = append(out, it)
			}
		}
		return out
	}
	return nil
}

// ── 记忆库（memory 域）───────────────────────────────────────────

// MemoryListPayload 是 `data-memory-list` 结果 DTO。
//
// S-7（DATA-OPT-1 P3）：行**直接复用门面类型**（`facade.MemoryCategory` / `facade.MemoryDoc` /
// `facade.MemoryExtractRecord` —— 字段与 json tag 与既有行 DTO 逐字一致），不再另立同名 Row。
type MemoryListPayload struct {
	List []facade.MemoryCategory `json:"list"`
}

// MemoryGetPayload 是 `data-memory-read` 结果 DTO。
type MemoryGetPayload struct {
	Data facade.MemoryDoc `json:"data"`
}

// MemoryIDPayload 是记忆 save / delete 结果 DTO。
type MemoryIDPayload struct {
	OK bool   `json:"ok"`
	ID string `json:"id"`
}

// MemoryExtractListPayload 是 `data-memory-extract-load` 结果 DTO。
type MemoryExtractListPayload struct {
	List []facade.MemoryExtractRecord `json:"list"`
}

// MemoryListResult 组装 `data-memory-list` 结果载荷
// `{list:[{category,level,path,tokens,prompt,prompt_override}]}`（prompt / prompt_override = OP-04
// 类别沉淀提示词有效值与覆盖标记，由后端文件读序解析后下发）。
func MemoryListResult(list []facade.MemoryCategory) MemoryListPayload {
	if list == nil {
		list = []facade.MemoryCategory{}
	}
	return MemoryListPayload{List: list}
}

// MemoryGetResult 组装 `data-memory-read` 结果载荷 `{data:{…全文…}}`。
func MemoryGetResult(doc facade.MemoryDoc) MemoryGetPayload {
	return MemoryGetPayload{Data: doc}
}

// MemoryIDResult 组装 `{ok:true, id:<类别>}`（save / delete）。
func MemoryIDResult(id string) MemoryIDPayload {
	return MemoryIDPayload{OK: true, ID: id}
}

// ── 记忆提取进度（data-memory-extract-*；OP-05/06，2026-10-06）──────────

// MemoryExtractListResult 组装 `data-memory-extract-load` 结果载荷
// `{list:[{session_id,category,last_turn_id}]}`（嵌套行字段，口径同 MemoryListResult）。
func MemoryExtractListResult(list []facade.MemoryExtractRecord) MemoryExtractListPayload {
	if list == nil {
		list = []facade.MemoryExtractRecord{}
	}
	return MemoryExtractListPayload{List: list}
}
