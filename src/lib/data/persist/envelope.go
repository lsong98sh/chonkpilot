// data-<domain>-* 消息面的**信封层**：解析 wire 载荷 → 调用对应域门面实现 → 按 61 §3 形状回载荷。
//
// 位置（23-工程与部署拓扑 §7）：门面实现已下沉 `chonkpilot-data/internal/<域>/`（本文件不承载
// 任何域逻辑）——故 MQ 绑定与门面 inline 绑定是**同一份实现**（行为等价有测试）。应答走
// persist 统一格式 {req_id, ok, result:{...}}（§3 头：publish + Promise 收集 result）。
//
// 各域信封来源（下沉前位置 → 现位置）：
//
//	config 四域（prj-config/prompt/prj-security/user-config）：prj-config/persist.go:handleConfigKV
//	  · persist_userconfig.go:handleUserConfig
//	scenario：persist.go:handleScenario
//	session 全域：persist_session.go:handleSession
//	snapshot：persist_snapshot.go:handleSnapshot
//	tasktree：persist_tasktree.go:handleTasktree
//	knowledge：persist_knowledge.go:handleKnowledge
//	memory：persist_memory.go:handleMemory
//	filelist：persist_filelist.go:handleFileList
package persist

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/config"
	"github.com/chonkpilot/chonkpilot-data/internal/indexignored"
	"github.com/chonkpilot/chonkpilot-data/internal/snapshot"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// ─── config 四域（prj-config / prompt / prj-security）───────────────

// handleConfigKV 处理 data-prj-config-* / data-prompt-* / data-prj-security-*（配置 kv 域 MQ 面）。
//
// 阶段 4 第二批（41 G-34）：**逻辑已下沉到 config 域门面实现**（internal/config 的
// ConfigKV* 方法），本 handler 只做 MQ 信封（reply/fail，61 §3.1 应答形状不变）——
// 故 MQ 绑定与门面 inline 绑定是**同一份实现**（行为等价有测试），写入后的变更广播
// （data-<domain>-refresh）也由实现侧发出（订阅方两路径同源收到）。
func (s *Service) handleConfigKV(domain, op string, req dataReq) {
	method := "data-" + domain + "-" + op
	switch op {
	case "list":
		resp, err := s.ConfigKVList(facade.ConfigKVListRequest{Domain: domain, InstanceID: req.InstanceID})
		if err != nil {
			s.fail(method, req, err)
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldList: resp.List})
	case "load":
		id := reqKey(req)
		resp, err := s.ConfigKVGet(facade.ConfigKVGetRequest{
			Domain: domain, InstanceID: req.InstanceID, Keys: []string{id},
		})
		if err != nil {
			s.fail(method, req, err)
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldData: resp.Values[id]})
	case "save":
		// 报文 `data` 兼容两形（61 §3.1）：批量 `{entries:{…}}` 优先，回落既有单键
		// `{key,value}`（entries 为非空 map 时忽略 key/value）。翻译见 wire.ConfigSaveEntries。
		entries, id, ok := wire.ConfigSaveEntries(req.Data)
		if !ok {
			s.fail(method, req, errors.New("key required"))
			return
		}
		if _, err := s.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain: domain, InstanceID: req.InstanceID, Entries: entries,
		}); err != nil {
			s.fail(method, req, err)
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: id})
	case "delete":
		id := reqKey(req)
		if _, err := s.ConfigKVDelete(facade.ConfigKVDeleteRequest{
			Domain: domain, InstanceID: req.InstanceID, Keys: []string{id},
		}); err != nil {
			s.fail(method, req, err)
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldOk: true})
	}
}

// handleUserConfig 处理 data-user-config-{list,load,save,delete}（存储 = 逐 key + 专用表）。
//
// 阶段 4 第二批（41 G-34）：**逻辑已下沉到 config 域门面实现**（internal/config 的
// UserConfig* 方法），本 handler 只做 MQ 信封（reply/fail，61 §3.1 应答形状不变）——
// MQ 绑定与门面 inline 绑定同一份实现；写入后的变更广播由实现侧发出。
func (s *Service) handleUserConfig(op string, req dataReq) {
	method := "data-user-config-" + op
	switch op {
	case "list":
		resp, err := s.UserConfigView(facade.UserConfigViewRequest{})
		if err != nil {
			s.fail(method, req, err)
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldList: resp.List})
	case "load":
		// load 返回**合并后**的有效值：usr 基线 + 项目层（prjusr → prj）覆盖可继承键
		// （defaultLLM/defaultScenario）。list 仍为 usr 主库视图（"usr 是否已有配置"语义）。
		resp, err := s.UserConfigGet(facade.UserConfigGetRequest{InstanceID: req.InstanceID})
		if err != nil {
			s.fail(method, req, err)
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldData: resp.Config})
	case "save":
		if _, err := s.UserConfigSet(facade.UserConfigSetRequest{
			Entries: req.Data, InstanceID: req.InstanceID,
		}); err != nil {
			s.fail(method, req, err)
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: config.UserConfigID})
	case "delete":
		// 带 key/id = 删除该项（继承控件「重置继承」→ 回落上级）；无 key = 清空整份用户配置；
		// 未知键由实现侧**明确报错**（P0：未知键曾把 theme/locale/llms/超时一并清掉），不兜底清空。
		key := reqKey(req)
		keys := []string{}
		if key != "" {
			keys = append(keys, key)
		}
		if _, err := s.UserConfigDelete(facade.UserConfigDeleteRequest{
			Keys: keys, InstanceID: req.InstanceID,
		}); err != nil {
			s.fail(method, req, err)
			return
		}
		if key != "" {
			s.reply(method, req, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: key})
			return
		}
		s.reply(method, req, map[string]any{msgkeys.FieldOk: true})
	}
}

// reqKey 取请求 id：顶层 id → data.id → data.key（统一转字符串）。
func reqKey(req dataReq) string {
	if id := idKey(req.ID); id != "" {
		return id
	}
	if req.Data == nil {
		return ""
	}
	if id := idKey(req.Data[msgkeys.FieldId]); id != "" {
		return id
	}
	if k, _ := req.Data[msgkeys.FieldKey].(string); k != "" {
		return k
	}
	return ""
}

// ─── scenario ────────────────────────────────────────────────────

// handleScenario 处理 data-scenario-*（list/load/save/delete）——**MQ 信封层**
// （解析 → 门面 → 回载荷，翻译收在 facade/wire；逻辑见 internal/scenario 的 ScenarioAPI）。
// v6（12-数据层 · 25-MCP与场景分层模型 §6）：数据源 = 文件化 **capability 根下的 `scenarios/` 子目录**
// （`<级别根>/capability/scenarios/`；四级 app/user/project/prjusr，场景 id 全局唯一跨级不重名），
// 不再落 usr DB。
// 应答外形与旧版一致（list/data/id/ok），另加 level 字段标识来源级别。
func (s *Service) handleScenario(op string, req dataReq) {
	method := "data-scenario-" + op
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(err error) { s.fail(method, req, err) }
	m := flatReqMap(req)

	switch op {
	case "list":
		resp, err := s.ScenarioList(facade.ScenarioListRequest{InstanceID: req.InstanceID})
		if err != nil {
			failf(err)
			return
		}
		okf(wire.ScenarioListResult(resp.List))
	case "load":
		r := wire.ScenarioGetFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.ScenarioGet(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.ScenarioGetResult(resp.Scenario))
	case "save":
		r := wire.ScenarioSaveFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.ScenarioSave(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.ScenarioIDResult(resp.ID))
	case "delete":
		r := wire.ScenarioDeleteFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.ScenarioDelete(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	default:
		failf(errors.New("unsupported scenario action: " + op))
	}
}

// ─── mcp ─────────────────────────────────────────────────────────

// handleMCP 处理 data-mcp-*（list/load/save/delete）——**MQ 信封层**（解析 → 门面 → 回载荷，
// 翻译收在 facade/wire；逻辑见 internal/mcp 的 McpAPI）。
// 存储 = 四级 `<级别>/capability/mcps/<名>.json`（app/user/project/prjusr；同名最具体级优先）。
// 应答外形：list `{list}` / load `{data}` / save `{ok,name}` / delete `{ok}`。
func (s *Service) handleMCP(op string, req dataReq) {
	method := "data-mcp-" + op
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(err error) { s.fail(method, req, err) }
	m := flatReqMap(req)

	switch op {
	case "list":
		resp, err := s.McpList(facade.McpListRequest{InstanceID: req.InstanceID})
		if err != nil {
			failf(err)
			return
		}
		okf(wire.McpListResult(resp.List))
	case "load":
		r := wire.McpGetFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.McpGet(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.McpGetResult(resp.Server))
	case "save":
		r := wire.McpSaveFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.McpSave(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.McpNameResult(resp.Name))
	case "delete":
		r := wire.McpDeleteFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.McpDelete(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	default:
		failf(errors.New("unsupported mcp action: " + op))
	}
}

// flatReqMap 把 MQ 请求载荷归一为「平铺领域字段」视图（data 内字段 + 顶层 id/filter），
// 供门面入参解析复用（与入口侧 `wire.FlatPayload` 同口径的一份翻译；`parseDataReq` 已把
// 顶层业务字段并入 Data，此处只补它不承接的 id / filter）。
func flatReqMap(req dataReq) map[string]any {
	m := make(map[string]any, len(req.Data)+2)
	for k, v := range req.Data {
		m[k] = v
	}
	if _, ok := m[msgkeys.FieldId]; !ok && req.ID != nil {
		m[msgkeys.FieldId] = req.ID
	}
	if _, ok := m[msgkeys.FieldFilter]; !ok && req.Filter != nil {
		m[msgkeys.FieldFilter] = req.Filter
	}
	return m
}

// ─── session 全域 ────────────────────────────────────────────────

// handleSession 分发 data-session-<action>（信封层：解析 → 门面 → 回载荷）；
// 未知 action（含 -refresh 误入）忽略不回复。
func (s *Service) handleSession(action string, req dataReq, payload []byte) {
	method := "data-session-" + action
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(format string, args ...any) {
		s.fail(method, req, fmt.Errorf(format, args...))
	}
	wireReq := req.Data

	switch action {
	case "list":
		resp, err := s.SessionList(facade.SessionListRequest{InstanceID: req.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionListResult(resp.List))
	case "get":
		resp, err := s.SessionGet(facade.SessionGetRequest{
			InstanceID: req.InstanceID, SessionID: sessionIDOf(req),
		})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionGetResult(resp.Found, resp.Session))
	case "history":
		r := wire.TurnHistoryFromWire(wireReq)
		r.InstanceID = req.InstanceID
		if r.SessionID == "" {
			r.SessionID = sessionIDOf(req)
		}
		resp, err := s.TurnHistory(r)
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.TurnHistoryResult(resp))
	case "latest":
		resp, err := s.SessionLatest(facade.SessionLatestRequest{InstanceID: req.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionIDResult(resp.SessionID))
	case "title":
		id := idKey(req.ID)
		if v, _ := req.Data[msgkeys.FieldId].(string); id == "" {
			id = v
		}
		title, _ := req.Data[msgkeys.FieldTitle].(string)
		if _, err := s.SessionTitle(facade.SessionTitleRequest{
			InstanceID: req.InstanceID, SessionID: id, Title: title,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "delete":
		id := idKey(req.ID)
		if v, _ := req.Data[msgkeys.FieldId].(string); id == "" {
			id = v
		}
		if _, err := s.SessionDelete(facade.SessionDeleteRequest{
			InstanceID: req.InstanceID, SessionID: id,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "active-set":
		if _, err := s.SessionActiveSet(facade.SessionActiveSetRequest{
			InstanceID: req.InstanceID, SessionID: sessionIDOf(req),
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "active-get":
		resp, err := s.SessionActiveGet(facade.SessionActiveGetRequest{InstanceID: req.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.SessionIDResult(resp.SessionID))
	case "content":
		r := wire.MessageContentFromWire(wireReq)
		r.InstanceID = req.InstanceID
		if r.SessionID == "" {
			r.SessionID = idKey(req.ID)
		}
		resp, err := s.MessageContent(r)
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.MessageContentResult(resp.Contents))
	// A3 运行时扩展（server sessionStore 总线化原语；语义对齐 chonkpilot-server/session.go）
	case "ensure-session":
		if _, err := s.SessionEnsure(facade.SessionEnsureRequest{
			InstanceID:      req.InstanceID,
			SessionID:       sessionIDOf(req),
			ParentSessionID: Sval(req.Data[msgkeys.FieldParentSessionId]),
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "ensure-turn":
		if _, err := s.TurnEnsure(facade.TurnEnsureRequest{
			InstanceID: req.InstanceID,
			TurnID:     Sval(req.Data[msgkeys.FieldTurnId]),
			SessionID:  sessionIDOf(req),
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "append-message":
		r := wire.MessageAppendFromWire(wireReq)
		r.InstanceID = req.InstanceID
		resp, err := s.MessageAppend(r)
		if err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldId: resp.ID})
	case "set-summary":
		if _, err := s.TurnSetSummary(facade.TurnSetSummaryRequest{
			InstanceID: req.InstanceID,
			TurnID:     Sval(req.Data[msgkeys.FieldTurnId]),
			Summary:    Sval(req.Data[msgkeys.FieldSummary]),
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "complete-turn":
		// P3（2026-09-25）：payload 只增字段 full_tokens/brief_tokens（可选；缺省 = 不写该键，
		// 向后兼容——ReopenTurn 等不携带即保持原行为）。
		var tok struct {
			FullTokens  *int `json:"full_tokens"`
			BriefTokens *int `json:"brief_tokens"`
		}
		if req.Data != nil {
			if raw, err := json.Marshal(req.Data); err == nil {
				_ = json.Unmarshal(raw, &tok)
			}
		}
		if _, err := s.TurnComplete(facade.TurnCompleteRequest{
			InstanceID:   req.InstanceID,
			TurnID:       Sval(req.Data[msgkeys.FieldTurnId]),
			Status:       Sval(req.Data[msgkeys.FieldStatus]),
			FinishReason: Sval(req.Data[msgkeys.FieldFinishReason]),
			FullTokens:   tok.FullTokens,
			BriefTokens:  tok.BriefTokens,
		}); err != nil {
			failf("%v", err)
			return
		}
		okf(wire.OKResult())
	case "cleanup-stale":
		resp, err := s.TurnCleanupStale(facade.TurnCleanupStaleRequest{InstanceID: req.InstanceID})
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.CleanupResult(resp.Count))
	case "load-messages":
		r := wire.MessageLoadFromWire(wireReq)
		r.InstanceID = req.InstanceID
		resp, err := s.MessageLoad(r)
		if err != nil {
			failf("%v", err)
			return
		}
		okf(wire.MessageLoadResult(resp.Messages))
	case "context":
		r := wire.MessageContextFromWire(wireReq)
		r.InstanceID = req.InstanceID
		if r.SessionID == "" {
			r.SessionID = sessionIDOf(req)
		}
		resp, err := s.MessageContext(r)
		if err != nil {
			failf("%v", err)
			return
		}
		// P3（2026-09-25）：结果载荷**只增**伴随数组 turn_tokens（每项 {turn_id,full,brief}）；
		// 无可带项时**不写该键** → 与旧载荷逐字节等价（旧订阅方零影响）。
		okf(wire.ContextResult(resp.Messages, resp.TurnTokens))
	}
}

// sessionIDOf 会话 id：优先 data.session_id，回落 envelope id。
func sessionIDOf(req dataReq) string {
	if v, _ := req.Data[msgkeys.FieldSessionId].(string); v != "" {
		return v
	}
	return idKey(req.ID)
}

// ─── snapshot ────────────────────────────────────────────────────

// handleSnapshot 分发 data-snapshot-<action>（get/set）。未知 action 忽略不回复。
//
// 阶段 4 试点：表访问器已下沉 `chonkpilot-data/internal/snapshot`（原 persist.GetSnapshot /
// SetSnapshot 的带 `*data.DB` 导出面收窄为 internal，模块外不再可达）；本 MQ 面只做
// 信封与实例解析，落库经该实现（与门面 inline 绑定**同源**，见 facade/inline）。
func (s *Service) handleSnapshot(action string, req dataReq) {
	method := "data-snapshot-" + action
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(format string, args ...any) { s.fail(method, req, fmt.Errorf(format, args...)) }
	prj, err := s.sessionDB(req)
	if err != nil {
		s.fail(method, req, err)
		return
	}
	sessionID := sessionIDOf(req)
	if sessionID == "" {
		failf("session_id required")
		return
	}
	switch action {
	case "get":
		snap, ok, err := snapshot.Get(prj, sessionID)
		if err != nil {
			failf("%v", err)
			return
		}
		if !ok {
			okf(map[string]any{msgkeys.FieldSnapshot: nil}) // 无快照/无记录 → snapshot=null（调用方回退 BuildHistory）
			return
		}
		okf(map[string]any{msgkeys.FieldSnapshot: snap})
	case "set":
		var snap data.Snapshot
		if raw, ok := req.Data[msgkeys.FieldSnapshot].(map[string]any); ok && len(raw) > 0 {
			b, _ := json.Marshal(raw)
			if err := json.Unmarshal(b, &snap); err != nil {
				failf("snapshot: %v", err)
				return
			}
		}
		if err := snapshot.Set(prj, sessionID, snap); err != nil {
			failf("%v", err)
			return
		}
		okf(map[string]any{msgkeys.FieldOk: true})
	}
}

// ─── tasktree ────────────────────────────────────────────────────

// handleTasktree 分发 data-tasktree-<action>（信封层：解析 → 门面 → 回载荷）；
// 未知 action（含 -refresh 误入）忽略不回复。
// 阶段 4 第四批（41 G-36）：逻辑已下沉 internal/tasktree（影子域回滚开关与逻辑删除语义不变）。
func (s *Service) handleTasktree(action string, req dataReq, _ []byte) {
	method := "data-tasktree-" + action
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(err error) { s.fail(method, req, err) }
	m := flatReqMap(req)

	switch action {
	case "list":
		r := wire.TasktreeListFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.TasktreeList(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.TasktreeListResult(resp.Nodes))
	case "tasks":
		r := wire.TasktreeTasksFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.TasktreeTasks(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.TasktreeTasksResult(resp.List))
	case "upsert": // A3 运行时扩展（P2 起 = 任务层唯一写入路径）
		r := wire.TasktreeUpsertFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.TasktreeUpsert(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	case "delete":
		r := wire.TasktreeDeleteFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.TasktreeDelete(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	}
}

// ─── knowledge ───────────────────────────────────────────────────

// handleKnowledge 分发 data-knowledge-<action>（信封层：解析 → 门面 → 回载荷）；
// 未知 action（含 -refresh 误入）忽略不回复。
// 阶段 4 第四批（41 G-36）：逻辑已下沉 internal/knowledge（G-26 作用域校验在实现侧，不绕过）。
func (s *Service) handleKnowledge(action string, req dataReq, _ []byte) {
	method := "data-knowledge-" + action
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(err error) { s.fail(method, req, err) }
	m := flatReqMap(req)

	switch action {
	case "root": // root 只解析 kind 根，不依赖实例（app 根可独立存在）
		r := wire.KnowledgeRootFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.KnowledgeRoot(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.KnowledgeRootResult(resp))
	case "list":
		r := wire.KnowledgeListFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.KnowledgeList(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.KnowledgeListResult(resp))
	case "read":
		r := wire.KnowledgeReadFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.KnowledgeRead(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.KnowledgeReadResult(resp))
	case "save":
		r := wire.KnowledgeSaveFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.KnowledgeSave(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	case "create":
		r := wire.KnowledgeCreateFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.KnowledgeCreate(r)
		if err != nil {
			failf(err)
			return
		}
		okf(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldPath: resp.Path})
	case "delete":
		r := wire.KnowledgeDeleteFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.KnowledgeDelete(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	case "rename":
		r := wire.KnowledgeRenameFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.KnowledgeRename(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	case "mkdir":
		r := wire.KnowledgeMkdirFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.KnowledgeMkdir(r)
		if err != nil {
			failf(err)
			return
		}
		okf(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldPath: resp.Path})
	case "rmdir":
		r := wire.KnowledgeRmdirFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.KnowledgeRmdir(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	case "rename-dir":
		r := wire.KnowledgeRenameDirFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.KnowledgeRenameDir(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	}
}

// ─── memory ──────────────────────────────────────────────────────

// handleMemory 处理 data-memory-{list,read,save,delete}——**MQ 信封层**（解析 → 门面 →
// 回载荷，翻译收在 facade/wire；逻辑见 internal/memory 的 MemoryAPI）。
//
// 报文形状与语义（list {list} / read {data} / save {ok,id} / delete {ok,id}）：
//   - list：预置类别（首次预置） + 自定义类别（目录扫描）；
//   - read：预置恒可读；自定义须已存在（否则 unknown）；
//   - save：写/建类别内容（payload `{data:{category, content}}`）；
//   - delete：**删除自定义类别**（payload `{data:{category}}`；移除清单项 + 内容文件）；
//     预置类别不可删除（只可清空内容），报 preset memory category cannot be deleted。
//
// save/delete 后广播 data-memory-refresh（既有主题）——由门面实现侧发出。
func (s *Service) handleMemory(op string, req dataReq) {
	method := "data-memory-" + op
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(err error) { s.fail(method, req, err) }
	m := flatReqMap(req)

	switch op {
	case "list":
		resp, err := s.MemoryList(facade.MemoryListRequest{InstanceID: req.InstanceID})
		if err != nil {
			failf(err)
			return
		}
		okf(wire.MemoryListResult(resp.List))
	case "read":
		r := wire.MemoryGetFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.MemoryGet(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.MemoryGetResult(resp.Doc))
	case "save":
		r := wire.MemorySaveFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.MemorySave(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.MemoryIDResult(resp.ID))
	case "delete":
		r := wire.MemoryDeleteFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.MemoryDelete(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.MemoryIDResult(resp.ID))
	// 记忆提取进度专用表（data-memory-extract-*；OP-05/06，2026-10-06）：
	// 记录 (会话, 类别) → 最后已成功提取的 turn，落 prjusr（不再用 config 键）。
	case "extract-load":
		r := wire.MemoryExtractLoadFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.MemoryExtractLoad(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.MemoryExtractListResult(resp.List))
	case "extract-save":
		r := wire.MemoryExtractSaveFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.MemoryExtractSave(r)
		if err != nil {
			failf(err)
			return
		}
		okf(map[string]any{msgkeys.FieldOk: resp.OK, msgkeys.FieldId: resp.ID})
	case "extract-delete":
		r := wire.MemoryExtractDeleteFromWire(m)
		r.InstanceID = req.InstanceID
		if _, err := s.MemoryExtractDelete(r); err != nil {
			failf(err)
			return
		}
		okf(wire.OKResult())
	}
}

// ─── filelist ────────────────────────────────────────────────────

// handleFileList 处理 data-filelist-{list,put,del}（项目级 prj 库 file_list 表）。
// 阶段 4 第四批（41 G-36）：逻辑已下沉 internal/filelist（FileListAPI）。
func (s *Service) handleFileList(op string, req dataReq) {
	method := "data-filelist-" + op
	okf := func(result map[string]any) { s.reply(method, req, result) }
	failf := func(err error) { s.fail(method, req, err) }
	m := flatReqMap(req)

	switch op {
	case "list":
		r := wire.FileListListFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.FileListList(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.FileListListResult(resp))
	case "put":
		r := wire.FileListPutFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.FileListPut(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.FileListIDResult(resp.ID))
	case "del":
		r := wire.FileListDeleteFromWire(m)
		r.InstanceID = req.InstanceID
		resp, err := s.FileListDelete(r)
		if err != nil {
			failf(err)
			return
		}
		okf(wire.FileListDeletedResult(resp.Deleted))
	default:
		failf(errors.New("unsupported filelist action: " + op))
	}
}

// ─── index（索引排除判定，只读；2026-09-27 新增）────────────────────

// handleIndex 处理 data-index-<op>（当前仅 ignored）——**只读查询**：判定一组 workdir 相对路径
// 是否被 codegraph / vfts 的「索引排除规则」排除（供前端文件树灰显被排除条目）。
//
// 逻辑在 internal/indexignored（复用 github.com/chonkpilot/chonkpilot-ignore 单一实现，
// 判定口径与引擎实际索引范围一致）；本 handler 只做信封：解析 paths → 解析实例 workdir +
// 单批读 prj 配置 → 判定 → 回载荷（61 §3.6）。**零副作用**：不写配置/状态、不触发索引；
// workdir 无效 / 配置不可读 → 降级（enabled 按配置、ignored 为空），不抛错。
func (s *Service) handleIndex(op string, req dataReq) {
	if op != "ignored" {
		return // 未知动作忽略不回复（与其余域一致）
	}
	method := "data-index-" + op
	workdir, _, _ := s.InstBindingFor(req.InstanceID, facade.Scope{})
	values := s.indexConfigValues(req.InstanceID)
	get := func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
	res := indexignored.Query(workdir, stringSlice(req.Data[msgkeys.FieldPaths]), get, s.Warnf)
	result := map[string]any{
		msgkeys.FieldCodegraph: engineIgnoredResult(res.Codegraph),
		msgkeys.FieldVfts:      engineIgnoredResult(res.Vfts),
	}
	if res.Truncated {
		result[msgkeys.FieldTruncated] = true // 超限才带（缺省不出现；见 61 §3.6）
	}
	s.reply(method, req, result)
}

// indexConfigValues 单批读取本判定需要的 prj 配置键（一次 ConfigKVGet）；
// 失败 → nil（get 全部未命中 = 各键按缺省），并写日志。
func (s *Service) indexConfigValues(instanceID string) map[string]string {
	resp, err := s.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Keys: indexignored.ConfigKeys(),
	})
	if err != nil {
		if s.Warnf != nil {
			s.Warnf("data-index-ignored: 读 prj 配置失败（instance=%q）：%v", instanceID, err)
		}
		return nil
	}
	return resp.Values
}

// engineIgnoredResult 组装单引擎结果载荷（ignored 恒为数组，不用 null）。
func engineIgnoredResult(e indexignored.EngineResult) map[string]any {
	ignored := make([]any, 0, len(e.Ignored))
	for _, p := range e.Ignored {
		ignored = append(ignored, p)
	}
	// "enabled"/"ignored" 为嵌套数组键（非契约顶层字段，保留字面量）。
	return map[string]any{"enabled": e.Enabled, "ignored": ignored}
}

// stringSlice 从消息面载荷取字符串数组（缺省 / 非字符串元素跳过；非法 → nil）。
func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
