// config 类（prj-config / prompt / prj-security / user-config）与 tasktree / knowledge /
// filelist / scenario / memory 五域的**门面直调**分支（阶段 4 第二/四批；browser 入口，
// 与 GUI 桥 `bridge/dataViaFacade` 同构，见 41 G-34 / G-36）。
//
// browser 形态的 `POST /publish` 收到这些域的 `data-<domain>-<action>` 时**优先走 data 门面**
// （inline 绑定，服务端进程内直调，不经 MQ），未命中/未注入时回落到总线 persist 路径
// （publish.go dataViaPersist → persist 的 MQ 信封）。
//
// **前端消息面一字不变**：主题名与请求 payload 不变，应答 payload 由 `facade/wire` 生成——
// 与 MQ 路径（persist 信封层用同一份 wire 翻译）逐字一致（61 §3.1 / §3.3 / §3.4 及其余）。
// 写入后的变更广播（data-<domain>-refresh、config-refresh、task-deleted、scenario/memory 刷新）
// 由门面实现侧发出 → SSE 订阅方照旧收到。
package httpapi

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// errKeyRequired 与 persist 侧同文案（kv save 缺 key；避免两路径错误文案分叉）。
var errKeyRequired = errors.New("key required")

// configFacadeDomains 是**由 data 门面承载**的 config 类域（61 §3.1 的 kv / 用户配置域）。
var configFacadeDomains = []string{"prj-config", "prompt", "prj-security", "user-config"}

// domainFacadeDomains 是**由 data 门面承载**的第四批域（61 §3 数据面其余域）。
var domainFacadeDomains = []string{"tasktree", "knowledge", "filelist", "scenario", "memory"}

// splitFacadeSubject 把 data-<domain>-<op> 按给定域表解析为（域名, 动作）；未命中 → ok=false。
func splitFacadeSubject(subject string, domains []string) (domain, op string, ok bool) {
	rest := strings.TrimPrefix(subject, "data-")
	for _, d := range domains {
		if strings.HasPrefix(rest, d+"-") {
			return d, strings.TrimPrefix(rest, d+"-"), true
		}
	}
	return "", "", false
}

// reqMapOf 解析上行 payload 为 map（坏载荷 → 空 map）。
func reqMapOf(payloadJSON string) map[string]any {
	var req map[string]any
	if json.Unmarshal([]byte(payloadJSON), &req) != nil || req == nil {
		return map[string]any{}
	}
	return req
}

// ── config 类（prj-config / prompt / prj-security / user-config）──────

// configViaFacade 处理 config 类 `data-<domain>-<op>`（走 data 门面）。
// handled=false = 非 config 类 / 未知动作 → 交回总线（不静默丢弃）。
func (s *Server) configViaFacade(subject, payloadJSON string) (any, []error, bool) {
	domain, op, ok := splitFacadeSubject(subject, configFacadeDomains)
	if !ok {
		return nil, nil, false
	}
	req := reqMapOf(payloadJSON)
	instanceID, _ := req["instance_id"].(string)
	if instanceID == "" {
		instanceID = s.instanceID
	}
	scope := facade.Scope{WorkDir: s.workDir, DataDir: s.dataDir}
	fail := func(err error) (any, []error) {
		return map[string]any{"ok": false, "error": err.Error()}, []error{err}
	}

	if domain == "user-config" {
		switch op {
		case "list":
			resp, err := s.facade.UserConfigView(facade.UserConfigViewRequest{})
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return map[string]any{"list": resp.List}, nil, true
		case "load":
			resp, err := s.facade.UserConfigGet(facade.UserConfigGetRequest{
				InstanceID: instanceID, Scope: scope,
			})
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return map[string]any{"data": resp.Config}, nil, true
		case "save":
			entries, _ := req["data"].(map[string]any)
			resp, err := s.facade.UserConfigSet(facade.UserConfigSetRequest{
				Entries: entries, InstanceID: instanceID,
			})
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return map[string]any{"ok": true, "id": resp.ID}, nil, true
		case "delete":
			key := wire.RequestID(req)
			keys := []string{}
			if key != "" {
				keys = append(keys, key)
			}
			if _, err := s.facade.UserConfigDelete(facade.UserConfigDeleteRequest{
				Keys: keys, InstanceID: instanceID,
			}); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			if key != "" && key != "user_config" { // user_config = legacy 整块键（清空整份，应答无 id）
				return map[string]any{"ok": true, "id": key}, nil, true
			}
			return map[string]any{"ok": true}, nil, true
		}
		return nil, nil, false
	}

	switch op {
	case "list":
		resp, err := s.facade.ConfigKVList(facade.ConfigKVListRequest{
			Domain: domain, InstanceID: instanceID, Scope: scope,
		})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"list": resp.List}, nil, true
	case "load":
		id := wire.RequestID(req)
		resp, err := s.facade.ConfigKVGet(facade.ConfigKVGetRequest{
			Domain: domain, InstanceID: instanceID, Keys: []string{id}, Scope: scope,
		})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"data": resp.Values[id]}, nil, true
	case "save":
		// 报文语义同 persist：data:{key,value}（value 恒为字符串，非字符串 → 空串，与既有
		// 结构体反序列化口径一致 —— 不改前端、不改消息面）。
		var kv struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if req["data"] != nil {
			raw, _ := json.Marshal(req["data"])
			_ = json.Unmarshal(raw, &kv)
		}
		if kv.Key == "" {
			res, errs := fail(errKeyRequired)
			return res, errs, true
		}
		if _, err := s.facade.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain: domain, InstanceID: instanceID,
			Entries: map[string]string{kv.Key: kv.Value}, Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"ok": true, "id": kv.Key}, nil, true
	case "delete":
		if _, err := s.facade.ConfigKVDelete(facade.ConfigKVDeleteRequest{
			Domain: domain, InstanceID: instanceID, Keys: []string{wire.RequestID(req)}, Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return map[string]any{"ok": true}, nil, true
	}
	return nil, nil, false // 未知动作 → 交回总线（不静默丢弃）
}

// ── 第四批五域（tasktree / knowledge / filelist / scenario / memory）──

// domainViaFacade 处理第四批五域的 `data-<domain>-<op>`（走 data 门面）。
// handled=false = 非本批域 / 未知动作 → 交回总线（不静默丢弃）。
func (s *Server) domainViaFacade(subject, payloadJSON string) (any, []error, bool) {
	domain, op, ok := splitFacadeSubject(subject, domainFacadeDomains)
	if !ok {
		return nil, nil, false
	}
	req := reqMapOf(payloadJSON)
	wire.FlatPayload(req) // {data:{…}} 形态并入顶层（与 persist parseDataReq 同口径）
	instanceID, _ := req["instance_id"].(string)
	if instanceID == "" {
		instanceID = s.instanceID
	}
	scope := facade.Scope{WorkDir: s.workDir, DataDir: s.dataDir}
	fail := func(err error) (any, []error) {
		return map[string]any{"ok": false, "error": err.Error()}, []error{err}
	}

	switch domain {
	case "tasktree":
		switch op {
		case "list":
			r := wire.TasktreeListFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.TasktreeList(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.TasktreeListResult(resp.Nodes), nil, true
		case "tasks":
			r := wire.TasktreeTasksFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.TasktreeTasks(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.TasktreeTasksResult(resp.List), nil, true
		case "upsert":
			r := wire.TasktreeUpsertFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.TasktreeUpsert(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "delete":
			r := wire.TasktreeDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.TasktreeDelete(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		}
	case "knowledge":
		switch op {
		case "root":
			r := wire.KnowledgeRootFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.KnowledgeRoot(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.KnowledgeRootResult(resp), nil, true
		case "list":
			r := wire.KnowledgeListFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.KnowledgeList(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.KnowledgeListResult(resp), nil, true
		case "read":
			r := wire.KnowledgeReadFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.KnowledgeRead(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.KnowledgeReadResult(resp), nil, true
		case "save":
			r := wire.KnowledgeSaveFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.KnowledgeSave(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "create":
			r := wire.KnowledgeCreateFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.KnowledgeCreate(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return map[string]any{"ok": true, "path": resp.Path}, nil, true
		case "delete":
			r := wire.KnowledgeDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.KnowledgeDelete(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "rename":
			r := wire.KnowledgeRenameFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.KnowledgeRename(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "mkdir":
			r := wire.KnowledgeMkdirFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.KnowledgeMkdir(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return map[string]any{"ok": true, "path": resp.Path}, nil, true
		case "rmdir":
			r := wire.KnowledgeRmdirFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.KnowledgeRmdir(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "rename-dir":
			r := wire.KnowledgeRenameDirFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.KnowledgeRenameDir(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		}
	case "filelist":
		switch op {
		case "list":
			r := wire.FileListListFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.FileListList(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.FileListListResult(resp), nil, true
		case "put":
			r := wire.FileListPutFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.FileListPut(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.FileListIDResult(resp.ID), nil, true
		case "del":
			r := wire.FileListDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.FileListDelete(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.FileListDeletedResult(resp.Deleted), nil, true
		}
	case "scenario":
		switch op {
		case "list":
			resp, err := s.facade.ScenarioList(facade.ScenarioListRequest{InstanceID: instanceID, Scope: scope})
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.ScenarioListResult(resp.List), nil, true
		case "load":
			r := wire.ScenarioGetFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.ScenarioGet(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.ScenarioGetResult(resp.Scenario), nil, true
		case "save":
			r := wire.ScenarioSaveFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.ScenarioSave(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.ScenarioIDResult(resp.ID), nil, true
		case "delete":
			r := wire.ScenarioDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := s.facade.ScenarioDelete(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		}
	case "memory":
		switch op {
		case "list":
			resp, err := s.facade.MemoryList(facade.MemoryListRequest{InstanceID: instanceID, Scope: scope})
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryListResult(resp.List), nil, true
		case "read":
			r := wire.MemoryGetFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.MemoryGet(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryGetResult(resp.Doc), nil, true
		case "save":
			r := wire.MemorySaveFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.MemorySave(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryIDResult(resp.ID), nil, true
		case "delete":
			r := wire.MemoryDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := s.facade.MemoryDelete(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryIDResult(resp.ID), nil, true
		}
	}
	return nil, nil, false // 未知动作 → 交回总线（不静默丢弃）
}
