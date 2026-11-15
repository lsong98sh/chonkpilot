// sessionstatus.go —— 会话状态查询（SESSION-STATUS-1，2026-11-15）。
//
// 动机：`session_create` / `session_send` 以 `wait_sec=0` 返回后，父轮很快结束；此后**没有
// 执行中的工具**可承载结果，也没有 inbox 通知。`task_status` 依赖调用轮次上下文（`plugin/v1/
// task.result` 需要 turn 仍存活），父轮结束后不可用。本能力直接读持久化数据：
//
//	会话行（标题 / 轮数） + 最新（或指定）轮次状态 + 该轮最后一条 assistant 文本。
//
// 因此它可在任意轮次 / 任意会话里查询，并支持 `wait_sec` 轮询到终态；不新增 MQ 主题、不依赖
// 内存中的 turnCtx。跨用户读取沿用统一授权（`resolveTargets`，G-102 历史读取面）。
package plugindata

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// SessionStatusRequest 查会话状态。
type SessionStatusRequest struct {
	PluginID    string
	WorkDir     string
	Targets     []string
	Mode        string // private | llm | public
	SessionID   string
	TurnID      string // 可选：指定轮次；空 = 最新轮
	WaitSec     int    // >0 = 轮询到非 running 或超时
	SelfSession string
	TopSession  string
}

// SessionStatusResult 是会话状态视图。
type SessionStatusResult struct {
	User       string      `json:"user"`
	Session    SessionMeta `json:"session"`
	State      string      `json:"state"` // running | done | incomplete | error | interrupted | cancelled | not_started
	TurnStatus string      `json:"turn_status,omitempty"`
	TurnID     string      `json:"turn_id,omitempty"`
	Text       string      `json:"text,omitempty"`   // 该轮最后一条 assistant 正文（终态 = 最终回复）
	Output     string      `json:"output,omitempty"` // running 时已产出的部分文本
	UpdatedAt  string      `json:"updated_at,omitempty"`
	WaitedMs   int64       `json:"waited_ms,omitempty"`
}

// maxStatusWaitSec 是 core 侧等待上限（server / 插件再收窄一次）。
const maxStatusWaitSec = 600

// SessionStatus 读取会话状态（可等待终态）。
func (s *Store) SessionStatus(ctx context.Context, req SessionStatusRequest) (SessionStatusResult, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return SessionStatusResult{}, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session_id 必填"}
	}
	users, err := s.resolveTargets("session.status", req.PluginID, req.WorkDir, req.Mode, req.Targets)
	if err != nil {
		return SessionStatusResult{}, err
	}
	user, dialogID, db, release, err := s.openSessionForUsers(req.WorkDir, users, req.SessionID, "")
	if err != nil {
		return SessionStatusResult{}, err
	}
	defer release()

	var rec data.Record
	ok, gerr := db.Table(tableSessions).Get(req.SessionID, &rec)
	if gerr != nil {
		return SessionStatusResult{}, gerr
	}
	if !ok || staleDialogRow(rec, dialogID) {
		return SessionStatusResult{}, errSessionNotFound(req.SessionID)
	}
	counts, cerr := turnCounts(db)
	if cerr != nil {
		return SessionStatusResult{}, cerr
	}
	meta := metaOf(user, dialogID, rec, counts[req.SessionID], s.currentUser(), req.SelfSession, req.TopSession)

	wait := req.WaitSec
	if wait < 0 {
		wait = 0
	}
	if wait > maxStatusWaitSec {
		wait = maxStatusWaitSec
	}
	started := time.Now()
	deadline := started.Add(time.Duration(wait) * time.Second)

	for {
		st, serr := sessionStatusOnce(db, req.SessionID, req.TurnID)
		if serr != nil {
			return SessionStatusResult{}, serr
		}
		st.User = user
		st.Session = meta
		st.WaitedMs = time.Since(started).Milliseconds()
		if st.State != "running" || wait == 0 || !time.Now().Before(deadline) {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return st, nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// sessionStatusOnce 读一次最新（或指定）轮次的状态与文本。
func sessionStatusOnce(db *data.DB, sessionID, turnID string) (SessionStatusResult, error) {
	recs, _, err := db.Table(tableTurns).Query(data.Query{
		Where:   map[string]any{"session_id": sessionID},
		OrderBy: "created_at",
	})
	if err != nil {
		return SessionStatusResult{}, err
	}
	sort.SliceStable(recs, func(i, j int) bool {
		if sval(recs[i], "created_at") != sval(recs[j], "created_at") {
			return sval(recs[i], "created_at") < sval(recs[j], "created_at")
		}
		return sval(recs[i], "_key") < sval(recs[j], "_key")
	})
	if len(recs) == 0 {
		return SessionStatusResult{State: "not_started"}, nil
	}

	var turn data.Record
	if strings.TrimSpace(turnID) != "" {
		for _, rec := range recs {
			if sval(rec, "turn_id") == turnID || sval(rec, "_key") == turnID {
				turn = rec
				break
			}
		}
		if turn == nil {
			return SessionStatusResult{}, &pluginbridge.Error{
				Code: pluginbridge.CodeNotFound, Msg: "轮次不在该会话中: " + turnID,
			}
		}
	} else {
		turn = recs[len(recs)-1]
	}

	tid := sval(turn, "turn_id")
	if tid == "" {
		tid = sval(turn, "_key")
	}
	rawStatus := sval(turn, "status")
	state := normalizeTurnStatus(rawStatus)
	text := lastAssistantText(db, tid)
	updated := sval(turn, "updated_at")
	if updated == "" {
		updated = sval(turn, "created_at")
	}
	res := SessionStatusResult{
		State: state, TurnStatus: rawStatus, TurnID: tid, UpdatedAt: updated,
	}
	if state == "running" {
		res.Output = text
	} else {
		res.Text = text
	}
	return res, nil
}

// normalizeTurnStatus 把持久化状态归一为稳定集合；未知值原样返回（便于诊断）。
func normalizeTurnStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "unknown"
	case "running":
		return "running"
	case "done", "complete", "completed", "success":
		return "done"
	case "incomplete":
		return "incomplete"
	case "error", "failed":
		return "error"
	case "interrupted":
		return "interrupted"
	case "cancelled", "canceled":
		return "cancelled"
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

// lastAssistantText 取该轮最后一条 assistant 正文（text 优先；running 时即已产出内容）。
func lastAssistantText(db *data.DB, turnID string) string {
	if turnID == "" {
		return ""
	}
	msgs, _, err := db.Table(tableMessages).Query(data.Query{
		Where:   map[string]any{"turn_id": turnID},
		OrderBy: "created_at",
	})
	if err != nil {
		return ""
	}
	sort.SliceStable(msgs, func(i, j int) bool {
		if sval(msgs[i], "created_at") != sval(msgs[j], "created_at") {
			return sval(msgs[i], "created_at") < sval(msgs[j], "created_at")
		}
		return sval(msgs[i], "_key") < sval(msgs[j], "_key")
	})
	text := ""
	for _, m := range msgs {
		if sval(m, "role") != "assistant" {
			continue
		}
		c := sval(m, "content")
		if strings.TrimSpace(c) == "" {
			continue
		}
		switch sval(m, "type") {
		case "", "text":
			text = c // 普通正文优先，后面的覆盖前面的
		default:
			if text == "" {
				text = c // 没有正文时容忍 reasoning 等（诊断用）
			}
		}
	}
	return text
}
