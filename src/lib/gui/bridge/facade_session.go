// session 域（session / turn / message 三域面）的**门面直调**分支（阶段 4 第三批 / 41 G-34）。
//
// 与 `data.go` 的 config 分支（第二批）同构：gui 桥的 `dataCall` 对 `data-session-*` 优先走
// data 门面（inline 绑定 = 同进程直接函数调用，不经 MQ），未命中/未接线（-no-server 薄客户端）
// 回落到总线 persist 路径（dataViaPersist）。
//
// **前端消息面一字不变**：主题名与请求 payload 不变（本文件只解析既有字段），应答 payload 的
// 键名与取值形态由 `facade/wire` 生成——与 MQ 路径（persist 信封层用同一份 wire 翻译）
// **逐字一致**（61 §3.2 / §3.2a）。写入后的变更广播（session-new）由门面实现侧发出 → 订阅方
// （前端 / server）照旧收到。
package bridge

import (
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// sessionSubjectPrefix 是 session 域消息面前缀（61 §3.2 / §3.2a：`data-session-<action>`）。
const sessionSubjectPrefix = "data-session-"

// isSessionSubject 判定主题是否属 session 域（data-session-*）。
func isSessionSubject(subject string) bool {
	return strings.HasPrefix(subject, sessionSubjectPrefix)
}

// sessionViaFacade 处理 `data-session-<action>`：走 data 门面（inline 绑定，同进程直调）。
// handled=false = 未知动作 / 未注入门面绑定 → 交回总线转发（不静默丢弃）。
func (b *Bridge) sessionViaFacade(action, payloadJSON string) (any, []error, bool) {
	if b.cfg == nil {
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
	sessionID := func() string {
		if v, _ := req["session_id"].(string); v != "" {
			return v
		}
		return wire.RequestID(req)
	}

	switch action {
	case "list":
		resp, err := b.cfg.SessionList(facade.SessionListRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.SessionListResult(resp.List), nil, true
	case "get":
		resp, err := b.cfg.SessionGet(facade.SessionGetRequest{
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
		resp, err := b.cfg.TurnHistory(r)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.TurnHistoryResult(resp), nil, true
	case "latest":
		resp, err := b.cfg.SessionLatest(facade.SessionLatestRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.SessionIDResult(resp.SessionID), nil, true
	case "title":
		if _, err := b.cfg.SessionTitle(facade.SessionTitleRequest{
			InstanceID: instanceID, SessionID: wire.RequestID(req), Title: sval(req["title"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "delete":
		if _, err := b.cfg.SessionDelete(facade.SessionDeleteRequest{
			InstanceID: instanceID, SessionID: wire.RequestID(req), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "active-set":
		if _, err := b.cfg.SessionActiveSet(facade.SessionActiveSetRequest{
			InstanceID: instanceID, SessionID: sessionID(), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "active-get":
		resp, err := b.cfg.SessionActiveGet(facade.SessionActiveGetRequest{InstanceID: instanceID, Scope: scope})
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
		resp, err := b.cfg.MessageContent(r)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.MessageContentResult(resp.Contents), nil, true
	case "ensure-session":
		if _, err := b.cfg.SessionEnsure(facade.SessionEnsureRequest{
			InstanceID: instanceID, SessionID: sessionID(),
			ParentSessionID: sval(req["parent_session_id"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "ensure-turn":
		if _, err := b.cfg.TurnEnsure(facade.TurnEnsureRequest{
			InstanceID: instanceID, TurnID: sval(req["turn_id"]), SessionID: sessionID(), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "append-message":
		r := wire.MessageAppendFromWire(req)
		r.InstanceID, r.Scope = instanceID, scope
		if _, err := b.cfg.MessageAppend(r); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "set-summary":
		if _, err := b.cfg.TurnSetSummary(facade.TurnSetSummaryRequest{
			InstanceID: instanceID, TurnID: sval(req["turn_id"]), Summary: sval(req["summary"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "complete-turn":
		if _, err := b.cfg.TurnComplete(facade.TurnCompleteRequest{
			InstanceID: instanceID, TurnID: sval(req["turn_id"]), Status: sval(req["status"]),
			FinishReason: sval(req["finish_reason"]), Scope: scope,
		}); err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.OKResult(), nil, true
	case "cleanup-stale":
		resp, err := b.cfg.TurnCleanupStale(facade.TurnCleanupStaleRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		return wire.CleanupResult(resp.Count), nil, true
	case "load-messages":
		r := wire.MessageLoadFromWire(req)
		r.InstanceID, r.Scope = instanceID, scope
		resp, err := b.cfg.MessageLoad(r)
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
		resp, err := b.cfg.MessageContext(r)
		if err != nil {
			res, errs := fail(err)
			return res, errs, true
		}
		// 与 MQ 信封路径（persist `envelope.go` context）逐字一致（I-145）：携带可选伴随数组
		// turn_tokens（为空时 wire.ContextResult 不写该键 → 与旧 {messages} 逐字节等价）。
		return wire.ContextResult(resp.Messages, resp.TurnTokens), nil, true
	}
	return nil, nil, false // 未知动作 → 交回总线（不静默丢弃）
}
