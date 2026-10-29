// dialog_route.go —— session 域按 `dialog_id` 路由（SESSION-COMP-1 P3-1b）。
//
// 口径（61 §0.6 / 2G §3.3）：
//   - 会话类调用**一律**带 `dialog_id`，只操作对话库（一树一库）；库缺失 → 报错
//     （不静默建空库、不回退 prjusr）。旧布局已整体退场（DATA-OPT-1 ②），无过渡分支。
//   - **主 session 即对话**：删主 session = 删整个对话（目录行 + 库文件）；子会话删除只级联自身数据。
package session

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
	"github.com/chonkpilot/chonkpilot-data/internal/storeadapter"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// contentDB 选内容库（turns / messages / snapshot 所在层）：
// `dialog_id` 非空 → 对话库（缺失即报错）；空 → `dialog_required` 明确失败。
func (s *Service) contentDB(prj *data.DB, dialogID string) (*data.DB, func(), error) {
	return kernel.ContentDB(prj, dialogID)
}

// openContentDB 是「解析实例 → 打开内容（对话）库」的**唯一实现**（S-2 · DATA-OPT-1 P3）：
// 返回 prjusr 库（部分域还要读 `dialogs` / 快照）与对话库 + 释放句柄，调用方 `defer release()`。
// 任一步失败原样上抛（错误文案不变）。
func (s *Service) openContentDB(instanceID string, scope facade.Scope, dialogID string) (prj, db *data.DB, release func(), err error) {
	prj, err = s.PrjUsrFor(instanceID, scope)
	if err != nil {
		return nil, nil, nil, err
	}
	db, release, err = s.contentDB(prj, dialogID)
	if err != nil {
		return nil, nil, nil, err
	}
	return prj, db, release, nil
}

// sessionEnsureInDialog 幂等建会话（写对话库 `sessions` 表；已存在不覆盖、不重复广播）。
func (s *Service) sessionEnsureInDialog(prj *data.DB, req facade.SessionEnsureRequest) (facade.SessionEnsureResponse, error) {
	sdb, release, err := data.OpenExistingDialog(prj.Path(), req.DialogID)
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	defer release()
	var rec data.Record
	ok, err := sdb.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	if ok {
		return facade.SessionEnsureResponse{OK: true, DialogID: req.DialogID}, nil
	}
	row := sessionRowFromEnsure(req, time.Now().UTC().Format(kernel.RFC3339FixedNano))
	if err := sdb.Table("sessions").Insert(req.SessionID, row); err != nil {
		return facade.SessionEnsureResponse{}, err
	}
	s.emitSessionNew(req.SessionID, req.ParentSessionID, req.InstanceID, req.DialogID)
	return facade.SessionEnsureResponse{OK: true, DialogID: req.DialogID}, nil
}

// sessionGetInDialog 读对话库内单个会话（不存在 → Found=false）。
func (s *Service) sessionGetInDialog(prj *data.DB, req facade.SessionGetRequest) (facade.SessionGetResponse, error) {
	sdb, release, err := data.OpenExistingDialog(prj.Path(), req.DialogID)
	if err != nil {
		return facade.SessionGetResponse{}, err
	}
	defer release()
	var rec data.Record
	ok, err := sdb.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		return facade.SessionGetResponse{}, err
	}
	if !ok {
		return facade.SessionGetResponse{}, nil
	}
	sess := wire.SessionFromWire(kernel.RecordView(rec, "session_id"))
	sess.DialogID = req.DialogID
	return facade.SessionGetResponse{Found: true, Session: sess}, nil
}

// sessionTitleInDialog 改会话标题；主 session 额外同步 `dialogs.title`（尽力双写，失败上抛）。
func (s *Service) sessionTitleInDialog(prj *data.DB, req facade.SessionTitleRequest) (facade.SessionTitleResponse, error) {
	sdb, release, err := data.OpenExistingDialog(prj.Path(), req.DialogID)
	if err != nil {
		return facade.SessionTitleResponse{}, err
	}
	defer release()
	var rec data.Record
	ok, err := sdb.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		return facade.SessionTitleResponse{}, err
	}
	if !ok {
		return facade.SessionTitleResponse{}, fmt.Errorf("session not found: %s", req.SessionID)
	}
	isMain := kernel.Sval(rec["parent_id"]) == ""
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	if err := sdb.Table("sessions").UpdateIn(req.SessionID, func(cur data.Record) data.Record {
		if len(cur) == 0 {
			return nil
		}
		cur["title"] = req.Title
		cur["updated_at"] = now
		return cur
	}); err != nil {
		return facade.SessionTitleResponse{}, err
	}
	if isMain {
		if err := prj.Table("dialogs").UpdateIn(req.DialogID, func(row data.Record) data.Record {
			if len(row) == 0 {
				return nil
			}
			row["title"] = req.Title
			row["updated_at"] = now
			return row
		}); err != nil {
			return facade.SessionTitleResponse{}, err
		}
	}
	s.emitSessionTitleChanged(req.SessionID, req.Title, req.DialogID)
	return facade.SessionTitleResponse{OK: true}, nil
}

// sessionDeleteInDialog 删会话：主 session → 整对话删除；子会话 → 只级联自身数据。
func (s *Service) sessionDeleteInDialog(prj *data.DB, req facade.SessionDeleteRequest) (facade.SessionDeleteResponse, error) {
	sdb, release, err := data.OpenExistingDialog(prj.Path(), req.DialogID)
	if err != nil {
		// 库已不存在 = 已被删除（幂等）→ 只做**补偿**（清活动位）后返回成功。
		// 两库无跨库事务：删除（对话库）与清活动位（宿主库）分两次提交，此处是补偿点。
		// 判错必须用 `clearActiveIfMatches` 自己的返回值，**不得**复用上面的 open err
		// （2026-10-28 修复：原写法 `if clearActiveIfMatches(...); err != nil` 复用了 open err
		//  → 该幂等分支恒返回「对话库不存在」，补偿清空永不执行）。
		if cerr := clearActiveIfMatches(prj, req.SessionID); cerr != nil {
			return facade.SessionDeleteResponse{}, cerr
		}
		return facade.SessionDeleteResponse{OK: true}, nil
	}
	var rec data.Record
	ok, err := sdb.Table("sessions").Get(req.SessionID, &rec)
	if err != nil {
		release()
		return facade.SessionDeleteResponse{}, err
	}
	if !ok {
		// 会话不存在视为成功（幂等），但 **inbox 级联仍执行**：历史孤儿行（会话先被删/旧版本
		// 遗留）在此收敛，避免 `session_id` 过滤下永不被消费的无主行长期占空间。
		if err := deleteBySessionIn(sdb, "inbox_items", req.SessionID); err != nil {
			release()
			return facade.SessionDeleteResponse{}, err
		}
		release()
		if err := clearActiveIfMatches(prj, req.SessionID); err != nil {
			return facade.SessionDeleteResponse{}, err
		}
		return facade.SessionDeleteResponse{OK: true}, nil
	}
	isMain := kernel.Sval(rec["parent_id"]) == ""
	if isMain { // 主 session = 对话本体 → 整对话删（行先删，文件删除幂等）
		release()
		if err := prj.Table("dialogs").Delete(req.DialogID); err != nil && !errors.Is(err, data.ErrNotFound) {
			return facade.SessionDeleteResponse{}, err
		}
		if err := data.DeleteDialog(prj.Path(), req.DialogID); err != nil {
			return facade.SessionDeleteResponse{}, err
		}
		if err := clearActiveIfMatches(prj, req.SessionID); err != nil {
			return facade.SessionDeleteResponse{}, err
		}
		return facade.SessionDeleteResponse{OK: true}, nil
	}

	// 子会话：库内级联（session 行 / snapshot / turns / messages / inbox_items）。
	cascErrs := []error{}
	if err := sdb.Table("sessions").Delete(req.SessionID); err != nil && !errors.Is(err, data.ErrNotFound) {
		cascErrs = append(cascErrs, fmt.Errorf("delete session: %w", err))
	}
	for _, table := range []string{"snapshot", "turns", "messages", "inbox_items"} {
		if table == "snapshot" {
			if err := sdb.Table(table).Delete(req.SessionID); err != nil && !errors.Is(err, data.ErrNotFound) {
				cascErrs = append(cascErrs, fmt.Errorf("delete snapshot: %w", err))
			}
			continue
		}
		if err := deleteBySessionIn(sdb, table, req.SessionID); err != nil {
			cascErrs = append(cascErrs, err)
		}
	}
	release()
	if err := errors.Join(cascErrs...); err != nil {
		return facade.SessionDeleteResponse{}, err
	}
	if err := clearActiveIfMatches(prj, req.SessionID); err != nil {
		return facade.SessionDeleteResponse{}, err
	}
	return facade.SessionDeleteResponse{OK: true}, nil
}

// forEachDialogDB 逐个短开对话库执行 fn（`fn` 返回 true = 命中，立即停止）。
// 打不开的库（锁 / 损坏）跳过，不阻断其它对话。
func (s *Service) forEachDialogDB(prj *data.DB, fn func(dialogID string, sdb *data.DB) (bool, error)) (bool, error) {
	files, err := sessionstore.ListDialogFiles(prj.Path())
	if err != nil {
		return false, err
	}
	for _, f := range files {
		dialogID, ok := data.DialogIDFromFile(f)
		if !ok {
			continue
		}
		sdb, release, err := data.OpenExistingDialog(prj.Path(), dialogID)
		if err != nil {
			continue
		}
		hit, err := fn(dialogID, sdb)
		release()
		if err != nil {
			return false, err
		}
		if hit {
			return true, nil
		}
	}
	return false, nil
}

// SessionFork 派生对话（SESSION-COMP-1 P4）：**实现唯一 = 会话组件**
// （`component.ForkDialog`，存储经 `store.Store` 端口 = 本模块的 bbolt 适配器）。
func (s *Service) SessionFork(req facade.SessionForkRequest) (facade.SessionForkResponse, error) {
	if req.DialogID == "" {
		return facade.SessionForkResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SessionForkResponse{}, err
	}
	comp, err := component.New(component.Options{Store: storeadapter.New(prj.Path())})
	if err != nil {
		return facade.SessionForkResponse{}, err
	}
	res, err := comp.ForkDialog(component.ForkDialogCmd{
		DialogID: req.DialogID, Mode: req.Mode, Title: req.Title,
	})
	if err != nil {
		return facade.SessionForkResponse{}, err
	}
	s.emitSessionNew(res.SessionID, "", req.InstanceID, res.DialogID)
	return facade.SessionForkResponse{OK: true, DialogID: res.DialogID, SessionID: res.SessionID}, nil
}

// deleteBySessionIn 按 session_id 删某表的行（对话库内；量级小，全桶过滤）。
//
// F-4（DATA-OPT-1 P4）：原实现逐行 `Delete`（每行一个写事务）→ **单事务批删**（读在同一写事务
// 内一次流式完成；键先收集再删）。删除集合与顺序无关，结果集逐项不变；任一行失败整体回滚。
func deleteBySessionIn(db *data.DB, table, sessionID string) error {
	return db.Update(func(tx *data.Tx) error {
		var keys []string
		if err := tx.ForEach(table, func(k string, rec data.Record) error {
			if kernel.Sval(rec["session_id"]) == sessionID {
				keys = append(keys, k)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("query %s: %w", table, err)
		}
		for _, k := range keys {
			if err := tx.Delete(table, k); err != nil && !errors.Is(err, data.ErrNotFound) {
				return fmt.Errorf("delete %s/%s: %w", table, k, err)
			}
		}
		return nil
	})
}

// clearActiveIfMatches 若活动会话指向被删会话则清空（活动态存 prj 配置）。
func clearActiveIfMatches(prj *data.DB, sessionID string) error {
	cur, err := kernel.PrjConfigVal(prj, "active_session_id")
	if err != nil {
		return err
	}
	if cur != sessionID {
		return nil
	}
	return kernel.PrjConfigSetVal(prj, "active_session_id", "")
}

// latestDialogMainSession 返回最近更新对话的主 session id（P3 活动态回落；无 → ""）。
//
// 只读 prjusr 目录行 + 打开该对话库读主 session；不做 session → dialog 全局反查。
func latestDialogMainSession(prj *data.DB) string {
	type row struct {
		id      string
		updated string
	}
	var rows []row
	_ = prj.Table("dialogs").ForEach(func(key string, rec data.Record) error {
		rows = append(rows, row{id: firstNonEmptyStr(kernel.Sval(rec["dialog_id"]), key), updated: kernel.Sval(rec["updated_at"])})
		return nil
	})
	if len(rows) == 0 {
		return ""
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].updated > rows[j].updated })
	sdb, release, err := data.OpenExistingDialog(prj.Path(), rows[0].id)
	if err != nil {
		return ""
	}
	defer release()
	mainID := ""
	_ = sdb.Table("sessions").ForEach(func(key string, rec data.Record) error {
		if kernel.Sval(rec["parent_id"]) == "" && mainID == "" {
			mainID = firstNonEmptyStr(kernel.Sval(rec["session_id"]), key)
		}
		return nil
	})
	return mainID
}

// firstNonEmptyStr 返回第一个非空串。
func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
