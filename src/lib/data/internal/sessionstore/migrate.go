// migrate.go —— 旧布局（`sessions` 表 + `sessions/<hex>/chonkpilot.db`）→ 新布局
// （`dialogs` 目录 + `dialogs/dialog-<hex(dialog_id)>.db` 一树一库）的搬运与启动对账。
//
// 设计（对齐 [2G-会话组件] §9 / §8）：
//   - **复制不删源**：prjusr 旧 `sessions` 行与旧 `sessions/` 目录原样保留 → 可回退；
//   - **幂等**：迁移期 `dialog_id` 由「根 session_id」确定性派生（sha256 → UUID 形状），
//     重跑必落同一文件；目标库已含根 session 行 → 整组跳过，绝不覆盖迁移后的新写入；
//   - **一树一库**：按 `parent_id` 归组（根 = 顶层会话；父缺失的孤儿行自成一组，避免丢数据）；
//   - **快照搬家**：`sessions.history` / `snapshot_turn` 写入库内 `snapshot` 表（新库 session 行不再带这两个字段）；
//   - **对账**：`Reconcile` 扫 `dialogs/` 补目录行（文件在、行缺失），删失效行（行在、文件缺失），
//     并按库内主 session 回填 title/kind/updated_at。
package sessionstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sort"

	"github.com/chonkpilot/chonkpilot-data"
)

// 旧布局表名（prjusr）。
const legacySessionsTable = "sessions"

// MigrateResult 汇总一次搬运的规模（便于日志 / 测试断言）。
type MigrateResult struct {
	Dialogs   int // 新建的对话数
	Skipped   int // 已迁移过而跳过的对话数
	Sessions  int // 搬入库内的 session 行数
	Snapshots int // 搬入 snapshot 表的历史快照数
	Messages  int
	Turns     int
	Tasktree  int
	Inbox     int
}

// ReconcileResult 汇总一次启动对账的修正量。
type ReconcileResult struct {
	Added   int // 文件在、目录行缺失 → 补行
	Updated int // 文件在、行内 title/kind/updated_at 漂移 → 回填
	Removed int // 行在、文件缺失 → 删行
}

// MigratedDialogID 返回历史会话迁移时使用的对话 id（确定性：同一根 session_id 恒同一 id）。
//
// 仅迁移路径使用；`CreateDialog` 等新建路径用随机 [NewDialogID]。确定性保证重跑幂等，
// 且 dialog_id ≠ session_id（取 sha256 前 16 字节并置 UUID v5 版本位）。
func MigratedDialogID(rootSessionID string) string {
	sum := sha256.Sum256([]byte("chonkpilot-dialog:" + rootSessionID))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50 // version 5（名字派生）
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Migrate 把 prjusr 旧布局搬到 `dialogs/`（幂等、非破坏）。
//
// 调用点：`session.db.shard=on` 时由数据服务在实例登记后触发（见 persist）。
// 失败语义：返回首个错误；已完成的对话保留（重跑从失败处继续，幂等）。
func Migrate(catalog *data.DB) (MigrateResult, error) {
	var res MigrateResult
	if catalog == nil {
		return res, errors.New("sessionstore: catalog is nil")
	}
	sessions := map[string]data.Record{}
	if err := catalog.Table(legacySessionsTable).ForEach(func(key string, rec data.Record) error {
		sessions[key] = rec
		return nil
	}); err != nil {
		return res, err
	}
	if len(sessions) == 0 {
		return res, nil
	}

	groups := groupByRoot(sessions)
	roots := make([]string, 0, len(groups))
	for root := range groups {
		roots = append(roots, root)
	}
	sort.Strings(roots) // 确定性顺序（便于测试 / 失败可续）

	for _, root := range roots {
		ids := groups[root]
		dialogID := MigratedDialogID(root)
		row, exists, err := GetDialog(catalog, dialogID)
		if err != nil {
			return res, err
		}
		if !exists {
			row = DialogRow{
				DialogID:      dialogID,
				File:          DialogFileName(dialogID),
				Title:         dialogTitle(sessions, root),
				Kind:          dialogKind(sessions, root),
				MainSessionID: root,
				UpdatedAt:     groupUpdatedAt(sessions, ids),
			}
		}

		sdb, release, err := OpenDialog(catalog, dialogID)
		if err != nil {
			return res, err
		}
		already, err := dialogHasSession(sdb, root)
		if err != nil {
			release()
			return res, err
		}
		if already {
			release()
			if !exists { // 文件已迁移但目录行缺失（对账前的半完成态）→ 补行
				if err := PutDialog(catalog, row); err != nil {
					return res, err
				}
			}
			res.Skipped++
			continue
		}

		if err := copyLegacyGroup(catalog, sdb, sessions, ids, root, &res); err != nil {
			release()
			return res, err
		}
		release()
		if err := PutDialog(catalog, row); err != nil {
			return res, err
		}
		res.Dialogs++
	}
	return res, nil
}

// groupByRoot 按根 session 归组（根 = parent_id 为空；父行缺失的孤儿自成一组）。
func groupByRoot(sessions map[string]data.Record) map[string][]string {
	groups := map[string][]string{}
	for sid := range sessions {
		root := rootOf(sessions, sid)
		groups[root] = append(groups[root], sid)
	}
	for root := range groups {
		ids := groups[root]
		sort.Slice(ids, func(i, j int) bool {
			ci, cj := sval(sessions[ids[i]]["created_at"]), sval(sessions[ids[j]]["created_at"])
			if ci != cj {
				return ci < cj
			}
			return ids[i] < ids[j]
		})
		groups[root] = ids
	}
	return groups
}

// rootOf 沿 parent_id 上溯到根（父缺失 / 环 / 超深 → 当前节点作为根，保证不丢数据）。
func rootOf(sessions map[string]data.Record, sid string) string {
	cur := sid
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		if seen[cur] {
			return cur // 环：就地断链
		}
		seen[cur] = true
		rec, ok := sessions[cur]
		if !ok {
			return cur
		}
		parent := sval(rec["parent_id"])
		if parent == "" {
			return cur
		}
		if _, ok := sessions[parent]; !ok {
			return cur // 父行缺失（历史孤儿）→ 自成一树
		}
		cur = parent
	}
	return cur
}

// dialogHasSession 报告库内是否已有该 session 行（= 该组已迁移过）。
func dialogHasSession(sdb *data.DB, sessionID string) (bool, error) {
	var rec data.Record
	return sdb.Table(legacySessionsTable).Get(sessionID, &rec)
}

// copyLegacyGroup 把一组（一棵树）的会话行 / 快照 / 消息 / 轮次 / 任务树 / 收件箱搬入对话库。
func copyLegacyGroup(catalog, sdb *data.DB, sessions map[string]data.Record, ids []string,
	root string, res *MigrateResult) error {
	idSet := make(map[string]bool, len(ids))
	for _, sid := range ids {
		idSet[sid] = true
	}

	// ① 会话行 + 快照
	for _, sid := range ids {
		rec := sessions[sid]
		stored := make(data.Record, len(rec))
		for k, v := range rec {
			switch k {
			case "history", "snapshot_turn":
				continue // 搬到 snapshot 表（新库 sessions 行只留目录元信息）
			default:
				stored[k] = v
			}
		}
		if err := sdb.Table(legacySessionsTable).Upsert(sid, stored); err != nil {
			return err
		}
		res.Sessions++
		history, turn := sval(rec["history"]), sval(rec["snapshot_turn"])
		if history == "" && turn == "" {
			continue
		}
		if err := sdb.Table("snapshot").Upsert(sid, data.Record{
			"session_id":    sid,
			"history":       history,
			"snapshot_turn": turn,
			"updated_at":    nowNano(),
		}); err != nil {
			return err
		}
		res.Snapshots++
	}

	// ② 轮次（先建 turn_id → session_id 映射，供 messages 的孤儿行归属）
	turnSession := map[string]string{}
	turns, err := copyFiltered(catalog, sdb, "turns", func(rec data.Record) (bool, data.Record) {
		sid := sval(rec["session_id"])
		if !idSet[sid] {
			return false, nil
		}
		if tid := sval(rec["turn_id"]); tid != "" {
			turnSession[tid] = sid
		}
		return true, rec
	})
	if err != nil {
		return err
	}
	res.Turns += turns

	// ③ 消息（session_id 空 → 按 turn_id 回填；回填不到则留在旧库）
	msgs, err := copyFiltered(catalog, sdb, "messages", func(rec data.Record) (bool, data.Record) {
		sid := sval(rec["session_id"])
		if sid == "" {
			if mapped := turnSession[sval(rec["turn_id"])]; mapped != "" {
				cp := cloneRecord(rec)
				cp["session_id"] = mapped
				return true, cp
			}
			return false, nil
		}
		return idSet[sid], nil
	})
	if err != nil {
		return err
	}
	res.Messages += msgs

	// ④ 任务树（按归属会话；top_session = 根的节点一并带上）
	tasks, err := copyFiltered(catalog, sdb, "tasktree", func(rec data.Record) (bool, data.Record) {
		return idSet[sval(rec["session_id"])] || sval(rec["top_session"]) == root, nil
	})
	if err != nil {
		return err
	}
	res.Tasktree += tasks

	// ⑤ 收件箱
	inbox, err := copyFiltered(catalog, sdb, "inbox_items", func(rec data.Record) (bool, data.Record) {
		return idSet[sval(rec["session_id"])], nil
	})
	if err != nil {
		return err
	}
	res.Inbox += inbox
	return nil
}

// copyFiltered 单次遍历源表：keep=false 跳过；keep=true 且 out 非 nil 时写 out（可改字段）。
func copyFiltered(catalog, sdb *data.DB, table string, keep func(rec data.Record) (bool, data.Record)) (int, error) {
	n := 0
	err := catalog.Table(table).ForEach(func(key string, rec data.Record) error {
		ok, out := keep(rec)
		if !ok {
			return nil
		}
		if out == nil {
			out = rec
		}
		if err := sdb.Table(table).Upsert(key, out); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

// cloneRecord 浅拷贝一行（改字段不改源记录）。
func cloneRecord(rec data.Record) data.Record {
	cp := make(data.Record, len(rec)+1)
	for k, v := range rec {
		cp[k] = v
	}
	return cp
}

// dialogTitle / dialogKind / groupUpdatedAt 从旧 sessions 行取目录展示字段。
func dialogTitle(sessions map[string]data.Record, root string) string {
	return firstNonEmpty(sval(sessions[root]["title"]), root)
}

func dialogKind(sessions map[string]data.Record, root string) string {
	return firstNonEmpty(sval(sessions[root]["kind"]), "main")
}

func groupUpdatedAt(sessions map[string]data.Record, ids []string) string {
	best := ""
	for _, sid := range ids {
		if v := sval(sessions[sid]["updated_at"]); v > best {
			best = v
		}
	}
	if best == "" {
		best = nowNano()
	}
	return best
}

// Reconcile 启动对账（spec §8）：扫 dialogs/ 补目录行、删失效行、回填 title/kind/updated_at。
//
// 口径：文件在、行缺失 → 补行；行在、文件缺失 → **删行**（与「标记 broken」二选一，此处取删行，
// 因为 `dialogs` 是可重建索引，删除后可由文件重新补齐，不留不可达状态）；title/kind 以库内
// 主 session 为准（主 session 改名后尽力双写失败时由本函数收敛）。
func Reconcile(catalog *data.DB) (ReconcileResult, error) {
	var res ReconcileResult
	if catalog == nil {
		return res, errors.New("sessionstore: catalog is nil")
	}
	files, err := ListDialogFiles(catalog.Path())
	if err != nil {
		return res, err
	}
	seen := map[string]bool{}
	for _, name := range files {
		dialogID, ok := DialogIDFromFile(name)
		if !ok {
			continue
		}
		seen[dialogID] = true
		sdb, release, err := OpenDialog(catalog, dialogID)
		if err != nil {
			return res, err
		}
		title, kind, updated, mainID, ok, err := mainSessionMeta(sdb)
		release()
		if err != nil {
			return res, err
		}
		if !ok {
			continue // 库内无主 session（半创建 / 损坏）→ 保留文件，交由 P1 EnsureDialog 收敛
		}
		cur, exists, err := GetDialog(catalog, dialogID)
		if err != nil {
			return res, err
		}
		want := DialogRow{DialogID: dialogID, File: name, Title: title, Kind: kind, MainSessionID: mainID, UpdatedAt: updated}
		if !exists {
			if err := PutDialog(catalog, want); err != nil {
				return res, err
			}
			res.Added++
			continue
		}
		if cur.File == want.File && cur.Title == want.Title && cur.Kind == want.Kind &&
			cur.MainSessionID == want.MainSessionID && cur.UpdatedAt == want.UpdatedAt {
			continue
		}
		if err := PutDialog(catalog, want); err != nil {
			return res, err
		}
		res.Updated++
	}

	rows, err := ListDialogs(catalog)
	if err != nil {
		return res, err
	}
	for _, row := range rows {
		if seen[row.DialogID] {
			continue
		}
		if _, statErr := os.Stat(data.DialogDBPath(catalog.Path(), row.DialogID)); statErr == nil {
			continue
		} else if !os.IsNotExist(statErr) {
			return res, statErr
		}
		if err := catalog.Table(DialogTable).Delete(row.DialogID); err != nil && err != data.ErrNotFound {
			return res, err
		}
		res.Removed++
	}
	return res, nil
}

// mainSessionMeta 读库内主 session（parent_id=""）的展示字段；无主 session → ok=false。
func mainSessionMeta(sdb *data.DB) (title, kind, updated, mainID string, ok bool, err error) {
	var best data.Record
	bestUpdated := ""
	if err = sdb.Table(legacySessionsTable).ForEach(func(_ string, rec data.Record) error {
		if sval(rec["parent_id"]) != "" {
			return nil
		}
		u := sval(rec["updated_at"])
		if best == nil || u > bestUpdated {
			best, bestUpdated = rec, u
		}
		return nil
	}); err != nil {
		return "", "", "", "", false, err
	}
	if best == nil {
		return "", "", "", "", false, nil
	}
	return firstNonEmpty(sval(best["title"]), sval(best["session_id"])),
		firstNonEmpty(sval(best["kind"]), "main"), bestUpdated, sval(best["session_id"]), true, nil
}
