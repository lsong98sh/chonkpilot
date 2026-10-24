// INBOX 门面实现（prjusr 级 `inbox_items` 表）。
//
// 设计见 `docs/agent-chat/中途插话设计方案.md` §6.2.3：
//   - 所有发送先落本表（status=pending），实际插入 / 下一轮开始时才写 `messages`；
//   - type = insert（立即插入）| next_turn（等下一轮）；
//   - 消费 = 落 messages + 置 consumed（调用方保证原子 / 幂等键去重）。
package session

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

const inboxTable = "inbox_items"

// inboxSourceLLM 是 `source` 的唯一合法取值（NEXTRUN-1 §3.4：模型经 `next_run` 登记）。
const inboxSourceLLM = "llm"

// inboxMaxPending 是单会话 pending 上限（设计 §4：每会话 ≤ 50）。
const inboxMaxPending = 50

// inboxRetention 是终态 inbox 行（consumed / discarded）的保留时长（设计 §5.2：7 天）。
// pending 永不自动删（用户输入不得静默丢失）。
const inboxRetention = 7 * 24 * time.Hour

// cleanupInboxTTL 删除超过保留期的终态 inbox 行（consumed / discarded）。
//
// 触发点 = 启动/首次 llm-start 的 TurnCleanupStale（`data-session-cleanup-stale`），
// 不新增消息主题/定时器；启动清理尽力而为，失败仅告警、不阻断启动。
// 时间基准优先 `updated_at`（终态落定时间），缺失回落 `created_at`。
func (s *Service) cleanupInboxTTL(prj *data.DB, now time.Time) {
	cutoff := now.Add(-inboxRetention)
	var keys []string
	if err := prj.Table(inboxTable).ForEach(func(k string, rec data.Record) error {
		st := kernel.Sval(rec["status"])
		if st != "consumed" && st != "discarded" {
			return nil // pending 永不自动删
		}
		ts := kernel.Sval(rec["updated_at"])
		if ts == "" {
			ts = kernel.Sval(rec["created_at"])
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !t.Before(cutoff) {
			return nil // 时间不可解析 → 保守保留（宁可留、不误删）
		}
		keys = append(keys, k)
		return nil
	}); err != nil && s.Warnf != nil {
		s.Warnf("session: inbox TTL 清理扫描失败：%v", err)
	}
	for _, k := range keys {
		if err := prj.Table(inboxTable).Delete(k); err != nil && !errors.Is(err, data.ErrNotFound) && s.Warnf != nil {
			s.Warnf("session: inbox TTL 清理删除 %s 失败：%v", k, err)
		}
	}
}

// deleteInboxBySession 级联删除某会话的全部 inbox 行（含 pending）。
// 会话删除（含幂等删除不存在的会话）调用；`inbox_items` 无二级索引 → 不传 OrderBy，
// 量级小、全桶过滤可接受。
func deleteInboxBySession(prj *data.DB, sessionID string) error {
	recs, _, err := prj.Table(inboxTable).Query(data.Query{
		Where: data.Record{"session_id": sessionID},
	})
	if err != nil {
		return fmt.Errorf("query inbox_items: %w", err)
	}
	var errs []error
	for _, r := range recs {
		k := kernel.Sval(r[data.KeyField])
		if err := prj.Table(inboxTable).Delete(k); err != nil && !errors.Is(err, data.ErrNotFound) {
			errs = append(errs, fmt.Errorf("delete inbox_items/%s: %w", k, err))
		}
	}
	return errors.Join(errs...)
}

// InboxAppend 写入一条待处理发送。
func (s *Service) InboxAppend(req facade.InboxAppendRequest) (facade.InboxAppendResponse, error) {
	if req.SessionID == "" {
		return facade.InboxAppendResponse{}, errors.New("inbox: session_id required")
	}
	if req.Type != "insert" && req.Type != "next_turn" {
		return facade.InboxAppendResponse{}, errors.New("inbox: type must be insert|next_turn")
	}
	if strings.TrimSpace(req.Content) == "" {
		return facade.InboxAppendResponse{}, errors.New("inbox: content required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxAppendResponse{}, err
	}
	db, release, err := s.contentDB(prj, req.DialogID, req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxAppendResponse{}, err
	}
	defer release()

	// 幂等：同会话 + 同幂等键已存在 → 直接返回既有行（网络重试不产生重复）。
	if req.IdempotencyKey != "" {
		recs, _, err := db.Table(inboxTable).Query(data.Query{
			Where: data.Record{"session_id": req.SessionID, "idempotency_key": req.IdempotencyKey},
			Limit: 1,
		})
		if err != nil {
			return facade.InboxAppendResponse{}, err
		}
		if len(recs) > 0 {
			return facade.InboxAppendResponse{OK: true, ID: kernel.Sval(recs[0][data.KeyField])}, nil
		}
	}

	// 上限：只统计 pending（consumed / discarded 不占额度）。
	pending, _, err := db.Table(inboxTable).Query(data.Query{
		Where: data.Record{"session_id": req.SessionID, "status": "pending"},
	})
	if err != nil {
		return facade.InboxAppendResponse{}, err
	}
	if len(pending) >= inboxMaxPending {
		return facade.InboxAppendResponse{}, errors.New("inbox: full（每会话 pending 上限 50）")
	}

	key, err := kernel.NewInboxKey()
	if err != nil {
		return facade.InboxAppendResponse{}, err
	}
	rec := data.Record{
		"session_id": req.SessionID,
		"type":       req.Type,
		"content":    req.Content,
		"status":     "pending",
		"created_at": time.Now().UTC().Format(kernel.RFC3339FixedNano),
	}
	if req.TurnID != "" {
		rec["turn_id"] = req.TurnID
	}
	if req.LLM != "" {
		rec["llm"] = req.LLM
	}
	if req.Think != nil {
		rec["think"] = req.Think
	}
	if req.Effort != nil {
		rec["effort"] = req.Effort
	}
	if req.ScenarioID != "" {
		rec["scenario_id"] = req.ScenarioID
	}
	if req.IdempotencyKey != "" {
		rec["idempotency_key"] = req.IdempotencyKey
	}
	// 来源标识（NEXTRUN-1 §3.4）：仅 `"llm"` 有意义；空 = 用户输入（不写键 → 行形状不变）。
	if req.Source != "" {
		if req.Source != inboxSourceLLM {
			return facade.InboxAppendResponse{}, errors.New("inbox: source must be llm")
		}
		rec["source"] = req.Source
	}
	if err := db.Table(inboxTable).Insert(key, rec); err != nil {
		return facade.InboxAppendResponse{}, err
	}
	return facade.InboxAppendResponse{OK: true, ID: key}, nil
}

// InboxList 按会话读 inbox 项（status 缺省 = pending，按 created_at 升序）。
func (s *Service) InboxList(req facade.InboxListRequest) (facade.InboxListResponse, error) {
	if req.SessionID == "" {
		return facade.InboxListResponse{}, errors.New("inbox: session_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxListResponse{}, err
	}
	db, release, err := s.contentDB(prj, req.DialogID, req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxListResponse{}, err
	}
	defer release()
	status := req.Status
	if status == "" {
		status = "pending"
	}
	where := data.Record{"session_id": req.SessionID}
	if status != "all" {
		where["status"] = status
	}
	recs, _, err := db.Table(inboxTable).Query(data.Query{Where: where})
	if err != nil {
		return facade.InboxListResponse{}, err
	}
	sort.SliceStable(recs, func(i, j int) bool {
		return kernel.Sval(recs[i]["created_at"]) < kernel.Sval(recs[j]["created_at"])
	})
	out := make([]facade.InboxItem, 0, len(recs))
	for _, r := range recs {
		out = append(out, facade.InboxItem{
			ID:             kernel.Sval(r[data.KeyField]),
			SessionID:      kernel.Sval(r["session_id"]),
			TurnID:         kernel.Sval(r["turn_id"]),
			Type:           kernel.Sval(r["type"]),
			LLM:            kernel.Sval(r["llm"]),
			Content:        kernel.Sval(r["content"]),
			Think:          r["think"],
			Effort:         r["effort"],
			ScenarioID:     kernel.Sval(r["scenario_id"]),
			Status:         kernel.Sval(r["status"]),
			IdempotencyKey: kernel.Sval(r["idempotency_key"]),
			CreatedAt:      kernel.Sval(r["created_at"]),
			Source:         kernel.Sval(r["source"]),
		})
	}
	return facade.InboxListResponse{OK: true, Items: out}, nil
}

// InboxCommit 原子消费一条 inbox（MOD-1 M6）：**同一 bbolt 写事务**内落 messages + 置 consumed。
//
//   - 消息主键确定性（`kernel.InboxMessageKey`）→ 重试幂等：消息行已存在则只补置 consumed；
//   - 已 discarded → 明确失败（不消费）；
//   - SessionID 空 → 取 inbox 行的 session_id；
//   - 消息行额外写 `inbox_id`（仅本路径）——排障 / 回查用，普通消息行形状不变。
func (s *Service) InboxCommit(req facade.InboxCommitRequest) (facade.InboxCommitResponse, error) {
	if req.InboxID == "" {
		return facade.InboxCommitResponse{}, errors.New("inbox: id required")
	}
	if req.TurnID == "" {
		return facade.InboxCommitResponse{}, errors.New("inbox: turn_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxCommitResponse{}, err
	}
	db, release, err := s.contentDB(prj, req.DialogID, req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxCommitResponse{}, err
	}
	defer release()
	m := data.MessageFromFacade(req.Message)
	// INPUTVAL-1 P0-4：与 MessageAppend 同口径（角色枚举 + 正文长度）。
	if !messageRoleValid(m.Role) {
		return facade.InboxCommitResponse{}, fmt.Errorf("inbox: invalid message role %q", m.Role)
	}
	if err := validateMessageContent(m.Role, m.Content); err != nil {
		return facade.InboxCommitResponse{}, err
	}
	key := kernel.InboxMessageKey(req.InboxID)
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	sessionID := req.SessionID
	err = db.Update(func(tx *data.Tx) error {
		var inbox data.Record
		ok, err := tx.Get(inboxTable, req.InboxID, &inbox)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("inbox: not found")
		}
		if kernel.Sval(inbox["status"]) == "discarded" {
			return errors.New("inbox: discarded")
		}
		if sessionID == "" {
			sessionID = kernel.Sval(inbox["session_id"])
		}
		exists, err := tx.Exists("messages", key)
		if err != nil {
			return err
		}
		if !exists {
			rec := buildMessageRecord(req.TurnID, sessionID, now, m, req.ToolCallStatus, req.Brief, "", "", nil)
			rec["inbox_id"] = req.InboxID
			if err := tx.Insert("messages", key, rec); err != nil {
				return err
			}
		}
		if kernel.Sval(inbox["status"]) != "consumed" {
			inbox["status"] = "consumed"
			inbox["updated_at"] = now
			if err := tx.Put(inboxTable, req.InboxID, inbox); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return facade.InboxCommitResponse{}, err
	}
	return facade.InboxCommitResponse{OK: true, ID: key}, nil
}

// inboxStatusValid 判定 inbox 状态是否属既定三态（pending / consumed / discarded）。
func inboxStatusValid(st string) bool {
	return st == "pending" || st == "consumed" || st == "discarded"
}

// InboxUpdate 更新 inbox 项（空字段 = 不改）。
func (s *Service) InboxUpdate(req facade.InboxUpdateRequest) (facade.InboxUpdateResponse, error) {
	if req.ID == "" {
		return facade.InboxUpdateResponse{}, errors.New("inbox: id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxUpdateResponse{}, err
	}
	db, release, err := s.contentDB(prj, req.DialogID, req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxUpdateResponse{}, err
	}
	defer release()
	// 枚举白名单（INPUTVAL-1 P0-4）：写坏值会污染消费侧相位判定（pending/consumed/discarded、
	// insert/next_turn），故写侧拒绝而非静默落库。
	if req.Status != "" && !inboxStatusValid(req.Status) {
		return facade.InboxUpdateResponse{}, fmt.Errorf("inbox: invalid status %q", req.Status)
	}
	if req.Type != "" && req.Type != "insert" && req.Type != "next_turn" {
		return facade.InboxUpdateResponse{}, fmt.Errorf("inbox: invalid type %q", req.Type)
	}
	if err := db.Table(inboxTable).UpdateIn(req.ID, func(cur data.Record) data.Record {
		if len(cur) == 0 {
			return nil
		}
		if req.Status != "" {
			cur["status"] = req.Status
		}
		if req.Type != "" {
			cur["type"] = req.Type
		}
		if req.TurnID != "" {
			cur["turn_id"] = req.TurnID
		}
		if req.Content != "" {
			cur["content"] = req.Content
		}
		cur["updated_at"] = time.Now().UTC().Format(kernel.RFC3339FixedNano)
		return cur
	}); err != nil {
		return facade.InboxUpdateResponse{}, err
	}
	return facade.InboxUpdateResponse{OK: true}, nil
}
