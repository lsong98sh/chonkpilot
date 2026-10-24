// shard_bootstrap.go —— `session.db.shard=on` 时的对话自举（SESSION-COMP-1 P3-4a）。
//
// 过渡口径（61 §0.6）：
//   - 开关 on 后，根会话首次 `data-session-ensure-session` 不带 `dialog_id` → 服务端按
//     `sha256("chonkpilot-dialog:"+根 session_id)` 确定性建对话（与 P0 迁移同一算法），
//     回带 `dialog_id`；调用方据此回填后续会话类调用的 `dialog_id`。
//   - 子会话不参与自举（必须由调用方带父会话的 `dialog_id`）。
//   - 列表（`data-session-list`）改由 `dialogs` 目录派生（ID = 主会话 id，另带 `dialog_id`），
//     无需为每个对话开库（目录行缺主会话 id 时才兜底开库）。
//
// 失败口径：目录行写失败不影响会话行（`dialogs` 是可重建索引，启动对账会补行）。
package session

import (
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
)

// shardOn 读 prj 级开关（`session.db.shard`，缺省 on；读失败按 off 处理——不阻断旧布局）。
// 唯一实现 = kernel.Base.ShardOn（与 persist 搬运 / ContentDB 判定同源）。
func (s *Service) shardOn(instanceID string, scope facade.Scope) bool {
	return s.ShardOn(instanceID, scope)
}

// sessionEnsureProvisionDialog 根会话自举：确定性 dialog_id + 建库 + 写会话行 + 目录行。
func (s *Service) sessionEnsureProvisionDialog(prj *data.DB, req facade.SessionEnsureRequest) (facade.SessionEnsureResponse, error) {
	dialogID := sessionstore.MigratedDialogID(req.SessionID)
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)

	sdb, release, err := data.OpenDialog(prj.Path(), dialogID)
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	var rec data.Record
	ok, err := sdb.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		release()
		return facade.SessionEnsureResponse{}, err
	}
	inserted := false
	if !ok {
		if err := sdb.Table("sessions").Insert(req.SessionID, sessionRowFromEnsure(req, now)); err != nil {
			release()
			return facade.SessionEnsureResponse{}, err
		}
		inserted = true
	}
	release()

	// 目录行（尽力双写；缺失才补，已存在不覆盖既有 title/kind）。
	if _, exists, err := sessionstore.GetDialog(prj, dialogID); err == nil && !exists {
		kind := req.Kind
		if kind == "" {
			kind = "main"
		}
		if err := sessionstore.PutDialog(prj, sessionstore.DialogRow{
			DialogID:      dialogID,
			Title:         req.SessionID,
			Kind:          kind,
			MainSessionID: req.SessionID,
			UpdatedAt:     now,
		}); err != nil && s.Warnf != nil {
			s.Warnf("session: 写 dialogs 行失败（可由启动对账补齐）dialog=%s: %v", dialogID, err)
		}
	}
	if inserted {
		s.emitSessionNew(req.SessionID, req.ParentSessionID, req.InstanceID, dialogID)
	}
	return facade.SessionEnsureResponse{OK: true, DialogID: dialogID}, nil
}

// sessionListDialogs 从 `dialogs` 目录派生会话列表（ID = 主会话 id）。
func (s *Service) sessionListDialogs(prj *data.DB) ([]facade.Session, error) {
	rows, err := sessionstore.ListDialogs(prj)
	if err != nil {
		return nil, err
	}
	out := make([]facade.Session, 0, len(rows))
	for _, r := range rows {
		mainID := r.MainSessionID
		if mainID == "" {
			mainID = mainSessionIDOf(prj, r.DialogID) // 目录行缺主会话 id → 开库兜底
			if mainID == "" {
				continue // 半创建 / 损坏：保留文件，由对账收敛
			}
		}
		out = append(out, facade.Session{
			ID:        mainID,
			DialogID:  r.DialogID,
			Title:     r.Title,
			Kind:      r.Kind,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// latestDialogSession 返回最近更新对话的主会话（无目录行 → 空）。
func (s *Service) latestDialogSession(prj *data.DB) (sessionID, dialogID string) {
	rows, err := sessionstore.ListDialogs(prj)
	if err != nil || len(rows) == 0 {
		return "", ""
	}
	mainID := rows[0].MainSessionID
	if mainID == "" {
		mainID = mainSessionIDOf(prj, rows[0].DialogID)
	}
	return mainID, rows[0].DialogID
}

// mainSessionIDOf 读对话库内主会话 id（库缺失 / 无主会话 → 空）。
func mainSessionIDOf(prj *data.DB, dialogID string) string {
	sdb, release, err := data.OpenExistingDialog(prj.Path(), dialogID)
	if err != nil {
		return ""
	}
	defer release()
	id := ""
	if err := sdb.Table("sessions").ForEach(func(k string, rec data.Record) error {
		if kernel.Sval(rec["parent_id"]) != "" || id != "" {
			return nil
		}
		id = kernel.Sval(rec["session_id"])
		if id == "" {
			id = k
		}
		return nil
	}); err != nil {
		return ""
	}
	return id
}
