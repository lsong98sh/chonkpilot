// dialog.go —— 对话目录（prjusr `dialogs` 表）与「一树一库」文件操作（SESSION-COMP-1 P0）。
//
// 布局：
//
//	prjusr/chonkpilot.db
//	  dialogs {dialog_id(PK), file, title, kind, updated_at}   ← 可重建索引（列表展示用）
//	dialogs/dialog-<hex(dialog_id)>.db   ← 一库 = 一棵会话树（sessions/messages/turns/tasktree/inbox/snapshot）
//
// 职责边界（P0）：本文件只提供**存储原语**；会话域门面仍读旧布局（`sessions` 表 +
// `sessions/<hex>/`），读路径切换见 P3。旧布局原语保留在 store.go（`Store`），P3 完成后删除。
package sessionstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
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

// NewDialogID 生成新的对话 id（委托 data.NewDialogID；命名规则单一定义在 data）。
func NewDialogID() (string, error) { return data.NewDialogID() }

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

// DeleteDialog 删除对话库文件 + 目录行（幂等：两者都不存在也算成功）。
func DeleteDialog(catalog *data.DB, dialogID string) error {
	if catalog == nil {
		return fmt.Errorf("sessionstore: catalog is nil")
	}
	if dialogID == "" {
		return nil
	}
	// 行先删（spec §8「删除中途失败」：行先删，文件删除幂等可重试）。
	if err := catalog.Table(DialogTable).Delete(dialogID); err != nil && err != data.ErrNotFound {
		return err
	}
	return data.DeleteDialog(catalog.Path(), dialogID)
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
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
