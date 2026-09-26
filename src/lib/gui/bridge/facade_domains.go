// tasktree / knowledge / filelist / scenario / memory 五域的**门面直调**分支
// （阶段 4 第四批 / 41 G-36）。
//
// 与 `facade_session.go`（第三批）同构：gui 桥的 `dataCall` 对这些域优先走 data 门面
// （inline 绑定 = 同进程直接函数调用，不经 MQ），未命中/未接线（-no-server 薄客户端）回落到
// 总线 persist 路径（dataViaPersist）。
//
// **前端消息面一字不变**：主题名与请求 payload 不变（本文件只解析既有字段），应答 payload 的
// 键名与取值形态由 `facade/wire` 生成——与 MQ 路径（persist 信封层用同一份 wire 翻译）
// **逐字一致**（61 §3.3 / §3.4 / §3.5·memory·filelist）。写入后的变更广播（task-deleted /
// data-scenario-refresh / data-memory-refresh）由门面实现侧发出 → 订阅方（前端 / server）照旧收到。
package bridge

import (
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// domainFacadeDomains 是**由 data 门面承载**的第四批域（61 §3 数据面；`data-<域>-<动作>`）。
var domainFacadeDomains = []string{"tasktree", "knowledge", "filelist", "scenario", "memory"}

// splitDomainSubject 把 data-<domain>-<op> 解析为（域名, 动作）；非本批域 → ok=false。
func splitDomainSubject(subject string) (domain, op string, ok bool) {
	rest := strings.TrimPrefix(subject, "data-")
	for _, d := range domainFacadeDomains {
		if strings.HasPrefix(rest, d+"-") {
			return d, strings.TrimPrefix(rest, d+"-"), true
		}
	}
	return "", "", false
}

// domainViaFacade 处理第四批五域的 `data-<domain>-<op>`（走 data 门面）。
// handled=false = 非本批域 / 未知动作 → 交回总线转发（不静默丢弃）。
func (b *Bridge) domainViaFacade(subject, payloadJSON string) (any, []error, bool) {
	if b.cfg == nil {
		return nil, nil, false
	}
	domain, op, ok := splitDomainSubject(subject)
	if !ok {
		return nil, nil, false
	}
	var req map[string]any
	if json.Unmarshal([]byte(payloadJSON), &req) != nil || req == nil {
		req = map[string]any{}
	}
	wire.FlatPayload(req) // {data:{…}} 形态并入顶层（与 persist parseDataReq 同口径）
	instanceID, _ := req["instance_id"].(string)
	if instanceID == "" {
		instanceID = b.instanceID
	}
	scope := facade.Scope{WorkDir: b.workDir, DataDir: b.dataDir}
	fail := func(err error) (any, []error) {
		return map[string]any{"ok": false, "error": err.Error()}, []error{err}
	}

	switch domain {
	case "tasktree":
		switch op {
		case "list":
			r := wire.TasktreeListFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.TasktreeList(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.TasktreeListResult(resp.Nodes), nil, true
		case "tasks":
			r := wire.TasktreeTasksFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.TasktreeTasks(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.TasktreeTasksResult(resp.List), nil, true
		case "upsert":
			r := wire.TasktreeUpsertFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.TasktreeUpsert(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "delete":
			r := wire.TasktreeDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.TasktreeDelete(r); err != nil {
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
			resp, err := b.cfg.KnowledgeRoot(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.KnowledgeRootResult(resp), nil, true
		case "list":
			r := wire.KnowledgeListFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.KnowledgeList(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.KnowledgeListResult(resp), nil, true
		case "read":
			r := wire.KnowledgeReadFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.KnowledgeRead(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.KnowledgeReadResult(resp), nil, true
		case "save":
			r := wire.KnowledgeSaveFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.KnowledgeSave(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "create":
			r := wire.KnowledgeCreateFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.KnowledgeCreate(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return map[string]any{"ok": true, "path": resp.Path}, nil, true
		case "delete":
			r := wire.KnowledgeDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.KnowledgeDelete(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "rename":
			r := wire.KnowledgeRenameFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.KnowledgeRename(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "mkdir":
			r := wire.KnowledgeMkdirFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.KnowledgeMkdir(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return map[string]any{"ok": true, "path": resp.Path}, nil, true
		case "rmdir":
			r := wire.KnowledgeRmdirFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.KnowledgeRmdir(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		case "rename-dir":
			r := wire.KnowledgeRenameDirFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.KnowledgeRenameDir(r); err != nil {
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
			resp, err := b.cfg.FileListList(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.FileListListResult(resp), nil, true
		case "put":
			r := wire.FileListPutFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.FileListPut(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.FileListIDResult(resp.ID), nil, true
		case "del":
			r := wire.FileListDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.FileListDelete(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.FileListDeletedResult(resp.Deleted), nil, true
		}
	case "scenario":
		switch op {
		case "list":
			resp, err := b.cfg.ScenarioList(facade.ScenarioListRequest{InstanceID: instanceID, Scope: scope})
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.ScenarioListResult(resp.List), nil, true
		case "load":
			r := wire.ScenarioGetFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.ScenarioGet(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.ScenarioGetResult(resp.Scenario), nil, true
		case "save":
			r := wire.ScenarioSaveFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.ScenarioSave(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.ScenarioIDResult(resp.ID), nil, true
		case "delete":
			r := wire.ScenarioDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			if _, err := b.cfg.ScenarioDelete(r); err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.OKResult(), nil, true
		}
	case "memory":
		switch op {
		case "list":
			resp, err := b.cfg.MemoryList(facade.MemoryListRequest{InstanceID: instanceID, Scope: scope})
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryListResult(resp.List), nil, true
		case "read":
			r := wire.MemoryGetFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.MemoryGet(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryGetResult(resp.Doc), nil, true
		case "save":
			r := wire.MemorySaveFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.MemorySave(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryIDResult(resp.ID), nil, true
		case "delete":
			r := wire.MemoryDeleteFromWire(req)
			r.InstanceID, r.Scope = instanceID, scope
			resp, err := b.cfg.MemoryDelete(r)
			if err != nil {
				res, errs := fail(err)
				return res, errs, true
			}
			return wire.MemoryIDResult(resp.ID), nil, true
		}
	}
	return nil, nil, false // 未知动作 → 交回总线（不静默丢弃）
}
