// dialog.go —— 对话目录（prjusr `dialogs` 表）与「一树一库」文件操作（SESSION-COMP-1 P0）。
//
// 布局：
//
//	prjusr/chonkpilot.db
//	  dialogs {dialog_id(PK), file, title, kind, updated_at}   ← 可重建索引（列表展示用）
//	dialogs/dialog-<hex(dialog_id)>.db   ← 一库 = 一棵会话树（sessions/messages/turns/tasktree/inbox/snapshot）
//
// 职责边界：本文件只提供**存储原语** + 目录对账（`Reconcile`）。旧布局
// （`sessions` 表 + `sessions/<hex>/`）与其搬运实现已随 DATA-OPT-1 ② 整体退场。
package sessionstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-util/strx"
)

// DialogTable 是 prjusr 内的对话目录表名。
const DialogTable = "dialogs"

// DialogRow 是 `dialogs` 表的一行（列表展示用的可重建副本）。
type DialogRow struct {
	DialogID string `json:"dialog_id"`
	File     string `json:"file"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	// MainSessionID 主会话 id（= 库内 `parent_id==""` 的那一行；列表直接取 ID，
	// 免去为每个对话短开库文件）。
	MainSessionID string `json:"main_session_id,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

// Record 转 `dialogs` 行记录。
func (r DialogRow) Record() data.Record {
	rec := data.Record{
		"dialog_id":  r.DialogID,
		"file":       r.File,
		"title":      r.Title,
		"kind":       r.Kind,
		"updated_at": r.UpdatedAt,
	}
	if r.MainSessionID != "" {
		rec["main_session_id"] = r.MainSessionID
	}
	return rec
}

// rowFromRecord 从 `dialogs` 行记录还原（缺失键 → 空串）。
func rowFromRecord(dialogID string, rec data.Record) DialogRow {
	return DialogRow{
		DialogID:      firstNonEmpty(sval(rec["dialog_id"]), dialogID),
		File:          sval(rec["file"]),
		Title:         sval(rec["title"]),
		Kind:          sval(rec["kind"]),
		MainSessionID: sval(rec["main_session_id"]),
		UpdatedAt:     sval(rec["updated_at"]),
	}
}

// DialogFileName 返回 dialog_id 对应的库文件名（委托 data.DialogFileName）。
func DialogFileName(dialogID string) string { return data.DialogFileName(dialogID) }

// DialogIDFromFile 从库文件名反解 dialog_id（委托 data.DialogIDFromFile）。
func DialogIDFromFile(name string) (string, bool) { return data.DialogIDFromFile(name) }

// OpenDialog 打开对话库（短开：调用方配对 release）。
func OpenDialog(catalog *data.DB, dialogID string) (*data.DB, func(), error) {
	if catalog == nil {
		return nil, nil, fmt.Errorf("sessionstore: catalog is nil")
	}
	return data.OpenDialog(catalog.Path(), dialogID)
}

// GetDialog 读目录行（ok=false = 不存在）。
func GetDialog(catalog *data.DB, dialogID string) (DialogRow, bool, error) {
	var rec data.Record
	ok, err := catalog.Table(DialogTable).Get(dialogID, &rec)
	if err != nil || !ok {
		return DialogRow{}, false, err
	}
	return rowFromRecord(dialogID, rec), true, nil
}

// PutDialog 写入 / 覆盖目录行（file 缺省 → 按 dialogID 计算）。
func PutDialog(catalog *data.DB, row DialogRow) error {
	if row.DialogID == "" {
		return fmt.Errorf("sessionstore: dialog_id required")
	}
	if row.File == "" {
		row.File = DialogFileName(row.DialogID)
	}
	if row.UpdatedAt == "" {
		row.UpdatedAt = nowNano()
	}
	return catalog.Table(DialogTable).Upsert(row.DialogID, row.Record())
}

// ListDialogs 列出目录行（updated_at 降序、dialog_id 升序稳定）。
func ListDialogs(catalog *data.DB) ([]DialogRow, error) {
	out := make([]DialogRow, 0, 16)
	err := catalog.Table(DialogTable).ForEach(func(key string, rec data.Record) error {
		out = append(out, rowFromRecord(key, rec))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].DialogID < out[j].DialogID
	})
	return out, nil
}

// ListDialogFiles 返回 prjusr 数据根下 `dialogs/` 目录里的库文件名（不含目录；目录缺失 = 空）。
func ListDialogFiles(prjUsrDBPath string) ([]string, error) {
	dir := dialogDir(prjUsrDBPath)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if _, ok := DialogIDFromFile(e.Name()); ok {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// dialogDir 返回 dialogs 目录路径（从 data.DialogDBPath 取父目录，不重复拼路径规则）。
func dialogDir(prjUsrDBPath string) string {
	return filepath.Dir(data.DialogDBPath(prjUsrDBPath, "x"))
}

// nowNano 返回与 data.RFC3339FixedNano 同口径的当前 UTC 时间。
func nowNano() string {
	return time.Now().UTC().Format(data.RFC3339FixedNano)
}

// firstNonEmpty 返回第一个非空串。
func firstNonEmpty(vals ...string) string {
	return strx.FirstNonEmpty(vals...)
}

// sval 把 Record 值转字符串（nil → ""）。
func sval(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// MigratedDialogID 返回根会话自举使用的对话 id（确定性：同一根 session_id 恒同一 id）。
//
// 仅根会话自举（`data-session-ensure-session` 缺 `dialog_id`）使用；`CreateDialog` 等新建
// 路径用随机 [data.NewDialogID]。确定性保证重入幂等，且 dialog_id ≠ session_id
// （取 sha256 前 16 字节并置 UUID v5 版本位）。
func MigratedDialogID(rootSessionID string) string {
	sum := sha256.Sum256([]byte("chonkpilot-dialog:" + rootSessionID))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50 // version 5（名字派生）
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// ReconcileResult 汇总一次目录对账的修正量。
type ReconcileResult struct {
	Added   int // 文件在、目录行缺失 → 补行
	Updated int // 文件在、行内 title/kind/updated_at 漂移 → 回填
	Removed int // 行在、文件缺失 → 删行
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
			continue // 库内无主 session（半创建 / 损坏）→ 保留文件，交由 EnsureDialog 收敛
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
	if err = sdb.Table("sessions").ForEach(func(_ string, rec data.Record) error {
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
