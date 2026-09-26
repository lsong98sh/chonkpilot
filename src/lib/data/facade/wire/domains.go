// 本文件是 **tasktree / knowledge / filelist / scenario / memory 五域**的
// 「消息面载荷（61 绑定）↔ 门面领域 DTO」翻译层（续 `wire.go` 的 session / turn / message 三域）。
//
// 为什么单独一层（G-33：wire 形状与门面 DTO 是两件事，不合并）：
//   - 门面 DTO 用**领域字段**（任务节点的 `ID` / `ParentID`、知识库的文档领域形态、清单条目的
//     `DocIDs`、场景的 `Agents`、记忆条目的 `Category/Content`）；消息面用**绑定形状**
//     （表列名 `node_id` / `doc_ids` / `systemPrompt`、契约分区文本、`{id,key}` 双写等）；
//   - 三处消费方都讲同一套 wire：① persist 的 `data-<domain>-*` MQ handler（信封层）
//     ② gui 桥 `dataCall`（同进程门面优先）③ browser 入口 `dataViaPersist`（同构）。
//     把翻译收在本包 = **一份翻译**，三条路径的应答载荷因此**逐字一致**（零分叉）。
//
// 归属：本包在 data 组件内（`chonkpilot-data/facade/wire`）——即 23 §7「翻译留在门面实现侧」
// 的实现侧；门面定义包（`facade`）本身不引入任何 wire 知识。
//
// 边界：本包只做「字段搬运 + 形状改写」，不含业务规则（不查库、不判定、不落盘）。
package wire

import (
	"encoding/json"

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
		"workdir":       n.WorkDir,
		"state":         n.State,
		"args_digest":   n.ArgsDigest,
		"result_digest": n.ResultDigest,
		"exec_json":     n.ExecJSON,
		"started_at":    n.StartedAt,
		"done_at":       n.DoneAt,
		"deleted_at":    n.DeletedAt,
	} {
		put(row, k, v)
	}
	if n.Closed != nil {
		row["closed"] = *n.Closed
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
		WorkDir:      str(m["workdir"]),
		ArgsDigest:   str(m["args_digest"]),
		ResultDigest: str(m["result_digest"]),
		ExecJSON:     str(m["exec_json"]),
		StartedAt:    str(m["started_at"]),
		DoneAt:       str(m["done_at"]),
		DeletedAt:    str(m["deleted_at"]),
	}
	if v, ok := m["closed"]; ok { // 有键才带（含 false：与"无键"语义不同）
		closed := truthy(v)
		n.Closed = &closed
	}
	return n
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
	return row
}

// TasktreeListResult 组装 `data-tasktree-list` 结果载荷 `{nodes:[节点行…]}`。
func TasktreeListResult(nodes []facade.TaskNode) map[string]any {
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, TaskNodeToWire(n))
	}
	return map[string]any{"nodes": out}
}

// TasktreeTasksResult 组装 `data-tasktree-tasks` 结果载荷 `{list:[任务行…]}`。
func TasktreeTasksResult(list []facade.Task) map[string]any {
	out := make([]any, 0, len(list))
	for _, t := range list {
		out = append(out, TaskToWire(t))
	}
	return map[string]any{"list": out}
}

// TasktreeListFromWire 解析 `{top_session?, id?, mode?, include_closed?, shadow?}` → 门面入参。
func TasktreeListFromWire(m map[string]any) facade.TasktreeListRequest {
	top := str(m["top_session"])
	if top == "" {
		top = RequestID(m)
	}
	return facade.TasktreeListRequest{
		TopSession:    top,
		Mode:          str(m["mode"]),
		IncludeClosed: truthy(m["include_closed"]),
		Shadow:        truthy(m["shadow"]),
	}
}

// TasktreeTasksFromWire 解析 `{session_id?, top_session?, include_closed?, shadow?}` → 门面入参。
func TasktreeTasksFromWire(m map[string]any) facade.TasktreeTasksRequest {
	return facade.TasktreeTasksRequest{
		SessionID:     str(m["session_id"]),
		TopSession:    str(m["top_session"]),
		IncludeClosed: truthy(m["include_closed"]),
		Shadow:        truthy(m["shadow"]),
	}
}

// TasktreeUpsertFromWire 解析 `data-tasktree-upsert` 载荷 → 门面入参（节点字段 = 领域字段）。
func TasktreeUpsertFromWire(m map[string]any) facade.TasktreeUpsertRequest {
	node := TaskNodeFromWire(m)
	if node.Title == "" {
		node.Title = str(m["name"]) // 兼容旧形态：name 即标题
	}
	return facade.TasktreeUpsertRequest{Node: node, Shadow: truthy(m["shadow"])}
}

// TasktreeDeleteFromWire 解析 `{node_id | id | filter.node_id, shadow?}` → 门面入参。
func TasktreeDeleteFromWire(m map[string]any) facade.TasktreeDeleteRequest {
	id := str(m["node_id"])
	if id == "" {
		id = RequestID(m)
	}
	if id == "" {
		if f, ok := m["filter"].(map[string]any); ok {
			id = str(f["node_id"])
		}
	}
	return facade.TasktreeDeleteRequest{NodeID: id, Shadow: truthy(m["shadow"])}
}

// ── 知识库（knowledge 域）────────────────────────────────────────

// KnowledgeRootResult 组装 `data-knowledge-root` 结果载荷 `{root, kind}`。
func KnowledgeRootResult(resp facade.KnowledgeRootResponse) map[string]any {
	return map[string]any{"root": resp.Root, "kind": resp.Kind}
}

// KnowledgeListResult 组装 `data-knowledge-list` 结果载荷 `{dir, dirs:[{name,path}], files:[…]}`。
func KnowledgeListResult(resp facade.KnowledgeListResponse) map[string]any {
	var dirs []any
	for _, d := range resp.Dirs {
		dirs = append(dirs, map[string]any{"name": d.Name, "path": d.Path})
	}
	var files []any
	for _, f := range resp.Files {
		files = append(files, map[string]any{
			"name": f.Name, "path": f.Path, "type": f.Type,
			"description": f.Description, "modified": f.Modified,
		})
	}
	return map[string]any{"dir": resp.Dir, "dirs": dirs, "files": files}
}

// KnowledgeReadResult 组装 `data-knowledge-read` 结果载荷 `{source, doc}`。
func KnowledgeReadResult(resp facade.KnowledgeReadResponse) map[string]any {
	return map[string]any{"source": resp.Source, "doc": resp.Doc}
}

// KnowledgeDocFromWire 解析载荷里的 `doc` 对象 → 门面文档领域形态。
func KnowledgeDocFromWire(m map[string]any) facade.KnowledgeDoc {
	var doc facade.KnowledgeDoc
	b, err := json.Marshal(m)
	if err != nil {
		return doc
	}
	_ = json.Unmarshal(b, &doc)
	return doc
}

// KnowledgeRootFromWire 解析 `{kind?}` → 门面入参。
func KnowledgeRootFromWire(m map[string]any) facade.KnowledgeRootRequest {
	return facade.KnowledgeRootRequest{Kind: str(m["kind"])}
}

// KnowledgeListFromWire 解析 `{dir?}` → 门面入参。
func KnowledgeListFromWire(m map[string]any) facade.KnowledgeListRequest {
	return facade.KnowledgeListRequest{Dir: str(m["dir"])}
}

// KnowledgeReadFromWire 解析 `{path}` → 门面入参。
func KnowledgeReadFromWire(m map[string]any) facade.KnowledgeReadRequest {
	return facade.KnowledgeReadRequest{Path: str(m["path"])}
}

// KnowledgeSaveFromWire 解析 `{path, doc}` → 门面入参。
func KnowledgeSaveFromWire(m map[string]any) facade.KnowledgeSaveRequest {
	doc, _ := m["doc"].(map[string]any)
	return facade.KnowledgeSaveRequest{Path: str(m["path"]), Doc: KnowledgeDocFromWire(doc)}
}

// KnowledgeCreateFromWire 解析 `{dir?, type, name}` → 门面入参。
func KnowledgeCreateFromWire(m map[string]any) facade.KnowledgeCreateRequest {
	return facade.KnowledgeCreateRequest{Dir: str(m["dir"]), Type: str(m["type"]), Name: str(m["name"])}
}

// KnowledgeDeleteFromWire 解析 `{path}` → 门面入参。
func KnowledgeDeleteFromWire(m map[string]any) facade.KnowledgeDeleteRequest {
	return facade.KnowledgeDeleteRequest{Path: str(m["path"])}
}

// KnowledgeRenameFromWire 解析 `{path, new_name}` → 门面入参。
func KnowledgeRenameFromWire(m map[string]any) facade.KnowledgeRenameRequest {
	return facade.KnowledgeRenameRequest{Path: str(m["path"]), NewName: str(m["new_name"])}
}

// KnowledgeMkdirFromWire 解析 `{parent?, name}` → 门面入参。
func KnowledgeMkdirFromWire(m map[string]any) facade.KnowledgeMkdirRequest {
	return facade.KnowledgeMkdirRequest{Parent: str(m["parent"]), Name: str(m["name"])}
}

// KnowledgeRmdirFromWire 解析 `{path}` → 门面入参。
func KnowledgeRmdirFromWire(m map[string]any) facade.KnowledgeRmdirRequest {
	return facade.KnowledgeRmdirRequest{Path: str(m["path"])}
}

// KnowledgeRenameDirFromWire 解析 `{path, new_name}` → 门面入参。
func KnowledgeRenameDirFromWire(m map[string]any) facade.KnowledgeRenameDirRequest {
	return facade.KnowledgeRenameDirRequest{Path: str(m["path"]), NewName: str(m["new_name"])}
}

// ── 文件清单（filelist 域）────────────────────────────────────────

// FileListListResult 组装 `data-filelist-list` 结果载荷 `{list:[条目…], total}`。
func FileListListResult(resp facade.FileListListResponse) map[string]any {
	out := make([]any, 0, len(resp.List))
	for _, e := range resp.List {
		out = append(out, FileListEntryToWire(e))
	}
	return map[string]any{"list": out, "total": resp.Total}
}

// FileListEntryToWire 把门面清单条目转回消息面条目（字段集 = 清单规范字段）。
func FileListEntryToWire(e facade.FileListEntry) map[string]any {
	docIDs := make([]any, 0, len(e.DocIDs))
	for _, d := range e.DocIDs {
		docIDs = append(docIDs, d)
	}
	return map[string]any{
		"key": e.Key, "path": e.Path, "size": e.Size, "mtime": e.MTime, "md5": e.MD5,
		"doc_ids": docIDs, "chunks": e.Chunks, "indexed_at": e.IndexedAt,
	}
}

// FileListEntryFromWire 解析清单条目对象 → 门面 DTO。
func FileListEntryFromWire(m map[string]any) facade.FileListEntry {
	return facade.FileListEntry{
		Key:       str(m["key"]),
		Path:      str(m["path"]),
		Size:      int64(intOf(m["size"])),
		MTime:     str(m["mtime"]),
		MD5:       str(m["md5"]),
		DocIDs:    strList(m["doc_ids"]),
		Chunks:    intOf(m["chunks"]),
		IndexedAt: str(m["indexed_at"]),
	}
}

// FileListListFromWire 解析 `{prefix?, offset?, limit?}` → 门面入参。
func FileListListFromWire(m map[string]any) facade.FileListListRequest {
	return facade.FileListListRequest{
		Prefix: str(m["prefix"]), Offset: intOf(m["offset"]), Limit: intOf(m["limit"]),
	}
}

// FileListPutFromWire 解析清单条目（数据内平铺字段）→ 门面入参；key 空回落请求 id。
func FileListPutFromWire(m map[string]any) facade.FileListPutRequest {
	e := FileListEntryFromWire(m)
	if e.Key == "" {
		e.Key = RequestID(m)
	}
	return facade.FileListPutRequest{Entry: e}
}

// FileListDeleteFromWire 解析 `{keys:[…]}`（兼容顶端单键）→ 门面入参。
func FileListDeleteFromWire(m map[string]any) facade.FileListDeleteRequest {
	keys := strList(m["keys"])
	if id := RequestID(m); id != "" {
		keys = append(keys, id)
	}
	return facade.FileListDeleteRequest{Keys: keys}
}

// FileListIDResult 组装 `{ok:true, id:<键>}`（put）。
func FileListIDResult(id string) map[string]any {
	return map[string]any{"ok": true, "id": id}
}

// FileListDeletedResult 组装 `{ok:true, deleted:<条数>}`（del）。
func FileListDeletedResult(n int) map[string]any {
	return map[string]any{"ok": true, "deleted": n}
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
		agents = append(agents, row)
	}
	out := map[string]any{
		"id": sc.ID, "key": sc.ID, "name": sc.Name, "description": sc.Description,
		"level": sc.Level, "agents": agents, "systemPrompt": sc.SystemPrompt,
	}
	put(out, "createdAt", sc.CreatedAt)
	put(out, "updatedAt", sc.UpdatedAt)
	return out
}

// ScenarioFromWire 把消息面场景对象 / 保存载荷转成门面 DTO。
func ScenarioFromWire(m map[string]any) facade.Scenario {
	id := str(m["id"])
	if id == "" {
		id = str(m["key"])
	}
	sc := facade.Scenario{
		ID:           id,
		Name:         str(m["name"]),
		Description:  str(m["description"]),
		Level:        str(m["level"]),
		SystemPrompt: str(m["systemPrompt"]),
		CreatedAt:    str(m["createdAt"]),
		UpdatedAt:    str(m["updatedAt"]),
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
				LLMRef:       str(am["llmRef"]),
				DelegateCond: str(am["delegateCond"]),
			})
		}
	}
	return sc
}

// ScenarioListResult 组装 `data-scenario-list` 结果载荷 `{ok:true, list:[…]}`。
func ScenarioListResult(list []facade.Scenario) map[string]any {
	out := make([]any, 0, len(list))
	for _, sc := range list {
		out = append(out, ScenarioToWire(sc))
	}
	return map[string]any{"ok": true, "list": out}
}

// ScenarioGetResult 组装 `data-scenario-load` 结果载荷 `{ok:true, data: 场景}`。
func ScenarioGetResult(sc facade.Scenario) map[string]any {
	return map[string]any{"ok": true, "data": ScenarioToWire(sc)}
}

// ScenarioIDResult 组装 `{ok:true, id:<场景 id>}`（save）。
func ScenarioIDResult(id string) map[string]any {
	return map[string]any{"ok": true, "id": id}
}

// ScenarioGetFromWire 解析 `{id | data.id, level?}` → 门面入参。
func ScenarioGetFromWire(m map[string]any) facade.ScenarioGetRequest {
	return facade.ScenarioGetRequest{ScenarioID: RequestID(m), Level: str(m["level"])}
}

// ScenarioSaveFromWire 解析保存载荷（场景字段 + level）→ 门面入参。
func ScenarioSaveFromWire(m map[string]any) facade.ScenarioSaveRequest {
	return facade.ScenarioSaveRequest{Scenario: ScenarioFromWire(m)}
}

// ScenarioDeleteFromWire 解析 `{id, level?}` → 门面入参。
func ScenarioDeleteFromWire(m map[string]any) facade.ScenarioDeleteRequest {
	return facade.ScenarioDeleteRequest{ScenarioID: RequestID(m), Level: str(m["level"])}
}

// ── 记忆库（memory 域）───────────────────────────────────────────

// MemoryListResult 组装 `data-memory-list` 结果载荷 `{list:[{category,level,path,tokens}]}`。
func MemoryListResult(list []facade.MemoryCategory) map[string]any {
	out := make([]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"category": c.Category, "level": c.Level, "path": c.Path, "tokens": c.Tokens,
		})
	}
	return map[string]any{"list": out}
}

// MemoryGetResult 组装 `data-memory-read` 结果载荷 `{data:{…全文…}}`。
func MemoryGetResult(doc facade.MemoryDoc) map[string]any {
	return map[string]any{"data": map[string]any{
		"category": doc.Category, "level": doc.Level, "path": doc.Path,
		"content": doc.Content, "tokens": doc.Tokens,
	}}
}

// MemoryIDResult 组装 `{ok:true, id:<类别>}`（save / delete）。
func MemoryIDResult(id string) map[string]any {
	return map[string]any{"ok": true, "id": id}
}

// MemoryGetFromWire 解析 `{category}` → 门面入参（读单类全文）。
func MemoryGetFromWire(m map[string]any) facade.MemoryGetRequest {
	return facade.MemoryGetRequest{Category: str(m["category"])}
}

// MemorySaveFromWire 解析 `{category, content}` → 门面入参。
func MemorySaveFromWire(m map[string]any) facade.MemorySaveRequest {
	return facade.MemorySaveRequest{Category: str(m["category"]), Content: str(m["content"])}
}

// MemoryDeleteFromWire 解析 `{category}` → 门面入参（删自定义类别）。
func MemoryDeleteFromWire(m map[string]any) facade.MemoryDeleteRequest {
	return facade.MemoryDeleteRequest{Category: str(m["category"])}
}
