// session 域（session / turn / message 三域面）的**门面直调**分支（阶段 4 第三批 / 41 G-34；
// browser 入口，与 GUI 桥 `bridge/dataViaFacade` 同构）。
//
// browser 形态的 `POST /publish` 收到 `data-session-*` 时**优先走 data 门面**（inline 绑定，
// 服务端进程内直调，不经 MQ），未命中/未注入时回落到总线 persist 路径（publish.go
// dataViaPersist → persist 的 MQ 信封）。
//
// **前端消息面一字不变**：主题名与请求 payload 不变，应答 payload 由 `facade/wire` 生成——
// 与 MQ 路径（persist 信封层用同一份 wire 翻译）逐字一致（61 §3.2 / §3.2a）。
// 写入后的变更广播（session-new）由门面实现侧发出 → SSE 订阅方照旧收到。
package httpapi

import (
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// sessionSubjectPrefix 是 session 域消息面前缀（61 §3.2 / §3.2a：`data-session-<action>`）。
const sessionSubjectPrefix = "data-session-"

// dataViaFacade 处理**已进门面**的 data-<domain>-<op> 上行请求（browser 入口）。
// 覆盖面（与 GUI 桥同构）：session 域（第三批，见本文件）· config 类（prj-config / prompt /
// prj-security / user-config）· tasktree / knowledge / filelist / scenario / memory（第四批，
// 见 facade_domains.go）。
// handled=false = 非上述域 / 未注入门面绑定 → 交回总线（publish.go 的 dataViaPersist）。
func (s *Server) dataViaFacade(subject, payloadJSON string) (any, []error, bool) {
	if s.facade == nil {
		return nil, nil, false
	}
	if strings.HasPrefix(subject, sessionSubjectPrefix) {
		return s.sessionViaFacade(strings.TrimPrefix(subject, sessionSubjectPrefix), payloadJSON)
	}
	if res, errs, ok := s.domainViaFacade(subject, payloadJSON); ok {
		return res, errs, true
	}
	return s.configViaFacade(subject, payloadJSON)
}

// sessionViaFacade 处理 `data-session-<action>`：走 data 门面（inline 绑定，同进程直调）。
func (s *Server) sessionViaFacade(action, payloadJSON string) (any, []error, bool) {
	var req map[string]any
	if json.Unmarshal([]byte(payloadJSON), &req) != nil || req == nil {
		req = map[string]any{}
	}
	wire.FlatPayload(req) // {data:{…}} 形态并入顶层（与 persist parseDataReq 同口径）
	instanceID, _ := req["instance_id"].(string)
	if instanceID == "" {
		instanceID = s.instanceID
	}
	scope := facade.Scope{WorkDir: s.workDir, DataDir: s.dataDir}
	fail := func(err error) (any, []error) {
		return map[string]any{"ok": false, "error": err.Error()}, []error{err}
	}
	sessionID := func() string {
		if v, _ := req["session_id"].(string); v != "" {
			return v
		}
		return wire.RequestID(req)
	}

	switch action {
	case "list":
		resp, err := s.facade.SessionList(facade.SessionListRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.SessionListResult(resp.List), nil, true
	case "get":
		resp, err := s.facade.SessionGet(facade.SessionGetRequest{
			InstanceID: instanceID, SessionID: sessionID(), Scope: scope,
		})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.SessionGetResult(resp.Found, resp.Session), nil, true
	case "history":
		r := wire.TurnHistoryFromWire(req)
		r.InstanceID, r.Scope = instanceID, scope
		if r.SessionID == "" {
			r.SessionID = sessionID()
		}
		resp, err := s.facade.TurnHistory(r)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.TurnHistoryResult(resp), nil, true
	case "latest":
		resp, err := s.facade.SessionLatest(facade.SessionLatestRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.SessionIDResult(resp.SessionID), nil, true
	case "title":
		if _, err := s.facade.SessionTitle(facade.SessionTitleRequest{
			InstanceID: instanceID, SessionID: wire.RequestID(req), Title: str(req["title"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "delete":
		if _, err := s.facade.SessionDelete(facade.SessionDeleteRequest{
			InstanceID: instanceID, SessionID: wire.RequestID(req), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "active-set":
		if _, err := s.facade.SessionActiveSet(facade.SessionActiveSetRequest{
			InstanceID: instanceID, SessionID: sessionID(), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "active-get":
		resp, err := s.facade.SessionActiveGet(facade.SessionActiveGetRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.SessionIDResult(resp.SessionID), nil, true
	case "content":
		r := wire.MessageContentFromWire(req)
		r.InstanceID, r.Scope = instanceID, scope
		if r.SessionID == "" {
			r.SessionID = wire.RequestID(req)
		}
		resp, err := s.facade.MessageContent(r)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.MessageContentResult(resp.Contents), nil, true
	case "ensure-session":
		if _, err := s.facade.SessionEnsure(facade.SessionEnsureRequest{
			InstanceID: instanceID, SessionID: sessionID(),
			ParentSessionID: str(req["parent_session_id"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "ensure-turn":
		if _, err := s.facade.TurnEnsure(facade.TurnEnsureRequest{
			InstanceID: instanceID, TurnID: str(req["turn_id"]), SessionID: sessionID(), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "append-message":
		r := wire.MessageAppendFromWire(req)
		r.InstanceID, r.Scope = instanceID, scope
		if _, err := s.facade.MessageAppend(r); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "set-summary":
		if _, err := s.facade.TurnSetSummary(facade.TurnSetSummaryRequest{
			InstanceID: instanceID, TurnID: str(req["turn_id"]), Summary: str(req["summary"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "complete-turn":
		if _, err := s.facade.TurnComplete(facade.TurnCompleteRequest{
			InstanceID: instanceID, TurnID: str(req["turn_id"]), Status: str(req["status"]),
			FinishReason: str(req["finish_reason"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "cleanup-stale":
		resp, err := s.facade.TurnCleanupStale(facade.TurnCleanupStaleRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.CleanupResult(resp.Count), nil, true
	case "load-messages":
		r := wire.MessageLoadFromWire(req)
		r.InstanceID, r.Scope = instanceID, scope
		resp, err := s.facade.MessageLoad(r)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.MessageLoadResult(resp.Messages), nil, true
	case "context":
		r := wire.MessageContextFromWire(req)
		r.InstanceID, r.Scope = instanceID, scope
		if r.SessionID == "" {
			r.SessionID = sessionID()
		}
		resp, err := s.facade.MessageContext(r)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.MessageLoadResult(resp.Messages), nil, true
	}
	return nil, nil, false // 未知动作 → 交回总线（不静默丢弃）
}

// str 值转字符串（string 原样；nil → ""；其余 JSON/字面量兜底）。
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	out := string(b)
	if len(out) > 0 && out[0] == '"' && out[len(out)-1] == '"' {
		return out[1 : len(out)-1]
	}
	return out
}
