// Package snapshot 是 data 组件的 **snapshot 域实现**（阶段 4 试点：由 persist 下沉至此）。
//
// 位置含义（23-工程与部署拓扑 §2「隔离」）：`internal` 是**编译器强制**的可见性边界——
// `chonkpilot-data/internal/...` 只允许 `chonkpilot-data/...` 之内的包引用，模块外的
// 调用方（插件 / 测试 / 外壳）一旦直接 import 即编译失败（"use of internal package
// ... not allowed"）。原 `persist.GetSnapshot` / `persist.SetSnapshot` 是导出且带 `*data.DB`
// 参数的越界口子（plugin-compress 曾直接持库调用），现收进本包。
//
// 本包对外可见面 = 门面（`chonkpilot-data/facade`）的实现：见 service.go。
//
// **存储位置（2026-10-23 轻量分片）**：快照数据落**会话库**的 `snapshot` 表；
// prjusr 的 `sessions` 行只保留会话列表元信息（title/parent_id/updated_at 等）。
// 历史数据（快照字段曾内嵌在 sessions 行）由 MigrateFromCatalog 一次性搬入会话库，**非破坏**（目录行字段保留）。
package snapshot

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// tableName 是会话库内的快照表（key = session_id）。
const tableName = "snapshot"

// Get 读会话快照（sdb = 会话库；无快照 → ok=false）。
func Get(sdb *data.DB, sessionID string) (data.Snapshot, bool, error) {
	var snap data.Snapshot
	var rec data.Record
	ok, err := sdb.Table(tableName).Get(sessionID, &rec)
	if err != nil || !ok {
		return snap, false, err
	}
	snap.SnapshotTurn = sval(rec["snapshot_turn"])
	if raw := sval(rec["history"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &snap.History); err != nil {
			return snap, false, err
		}
	}
	return snap, snap.SnapshotTurn != "" || len(snap.History) > 0, nil
}

// Set 写会话快照：数据 → 会话库 `snapshot`；会话目录行 → prjusr `sessions`（旧布局）。
//
// 目录行语义保持 A-15：会话不存在 → 补建**完整会话行**（created_at / parent_id / title 齐备）；
// 已存在 → 仅刷新 updated_at（纳秒口径，A-11）。
func Set(catalog, sdb *data.DB, sessionID string, snap data.Snapshot) error {
	b, err := json.Marshal(snap.History)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	if err := catalog.Table("sessions").UpdateIn(sessionID, func(rec data.Record) data.Record {
		if len(rec) == 0 { // 会话不存在 → 补建完整会话行
			rec = data.Record{
				"session_id": sessionID, "title": sessionID,
				"created_at": now, "updated_at": now, "parent_id": "",
			}
		} else {
			rec["updated_at"] = now
		}
		return rec
	}); err != nil {
		return err
	}
	return sdb.Table(tableName).Upsert(sessionID, data.Record{
		"session_id":    sessionID,
		"history":       string(b),
		"snapshot_turn": snap.SnapshotTurn,
		"updated_at":    now,
	})
}

// SetInDialog 写会话快照（新布局：一树一库）。
//
// 与 [Set] 的差异：会话目录行在**库内**（`dialogs/dialog-<hex>.db` 的 `sessions` 表），
// 因此不再写 prjusr；且**要求会话行已存在**（对话模式下不允许补建根会话，避免出现第 2 个主 session）。
func SetInDialog(sdb *data.DB, sessionID string, snap data.Snapshot) error {
	b, err := json.Marshal(snap.History)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	ok := false
	if err := sdb.Table("sessions").UpdateIn(sessionID, func(rec data.Record) data.Record {
		if len(rec) == 0 {
			return nil // 不存在 → 下面统一报错
		}
		ok = true
		rec["updated_at"] = now
		return rec
	}); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("snapshot: session %s 不在该对话库内", sessionID)
	}
	return sdb.Table(tableName).Upsert(sessionID, data.Record{
		"session_id":    sessionID,
		"history":       string(b),
		"snapshot_turn": snap.SnapshotTurn,
		"updated_at":    now,
	})
}

// MigrateFromCatalog 把历史快照（prjusr.sessions 行内 history / snapshot_turn）搬入会话库。
//
// 幂等且**非破坏**：会话库已有快照 → 直接返回；目录行字段保留（回滚时仍可读旧位置）。
func MigrateFromCatalog(catalog, sdb *data.DB, sessionID string) error {
	var cur data.Record
	if ok, err := sdb.Table(tableName).Get(sessionID, &cur); err != nil {
		return err
	} else if ok {
		return nil
	}
	var rec data.Record
	ok, err := catalog.Table("sessions").Get(sessionID, &rec)
	if err != nil || !ok {
		return err
	}
	turn, history := sval(rec["snapshot_turn"]), sval(rec["history"])
	if turn == "" && history == "" {
		return nil
	}
	return sdb.Table(tableName).Upsert(sessionID, data.Record{
		"session_id":    sessionID,
		"history":       history,
		"snapshot_turn": turn,
		"updated_at":    time.Now().UTC().Format(kernel.RFC3339FixedNano),
	})
}

// sval 把 Record 值转字符串（Record 由 json.Unmarshal 产生：string/float64/bool/nil）。
func sval(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
