// Package snapshot 是 data 组件的 **snapshot 域实现**（阶段 4 试点：由 persist 下沉至此）。
//
// 位置含义（23-工程与部署拓扑 §2「隔离」）：`internal` 是**编译器强制**的可见性边界——
// `chonkpilot-data/internal/...` 只允许 `chonkpilot-data/...` 之内的包引用，模块外的
// 调用方（插件 / 测试 / 外壳）一旦直接 import 即编译失败（"use of internal package
// ... not allowed"）。原 `persist.GetSnapshot` / `persist.SetSnapshot` 是导出且带 `*data.DB`
// 参数的越界口子（plugin-compress 曾直接持库调用），现收进本包。
//
// 本包对外可见面 = 门面（`chonkpilot-data/facade`）的实现：见 service.go。
package snapshot

import (
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot-data"
)

// Get 读会话快照（无快照/无记录 → ok=false）。prj = 实例的 prjusr 主库（会话/快照层）。
func Get(prj *data.DB, sessionID string) (data.Snapshot, bool, error) {
	var snap data.Snapshot
	var rec data.Record
	ok, err := prj.Table("sessions").Get(sessionID, &rec)
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

// Set 写会话快照（history + snapshot_turn；保留记录其他字段；session 不存在则建）。
func Set(prj *data.DB, sessionID string, snap data.Snapshot) error {
	tb := prj.Table("sessions")
	var rec data.Record
	ok, err := tb.Get(sessionID, &rec)
	if err != nil {
		return err
	}
	if !ok {
		rec = data.Record{}
	}
	delete(rec, data.KeyField)
	b, err := json.Marshal(snap.History)
	if err != nil {
		return err
	}
	rec["history"] = string(b)
	rec["snapshot_turn"] = snap.SnapshotTurn
	return tb.Upsert(sessionID, rec)
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
