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
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
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

// Set 写会话快照（history + snapshot_turn；保留记录其他字段）。
// 会话不存在 → 补建**完整会话行**（created_at / parent_id / title 齐备，A-15，口径同 SessionEnsure）：
// 消除原先只落快照字段、导致「查得到却列不出」的幽灵会话。
// 单事务 update-or-insert（A-19）：Get→改→Upsert 跨两事务会与 SessionTitle 等并发写互丢字段
// （整行覆盖），收进 UpdateIn 后快照字段与 title 等既有字段同事务读改；快照写刷新 updated_at
// 的原语义保留（纳秒口径，A-11）。
func Set(prj *data.DB, sessionID string, snap data.Snapshot) error {
	b, err := json.Marshal(snap.History)
	if err != nil {
		return err
	}
	return prj.Table("sessions").UpdateIn(sessionID, func(rec data.Record) data.Record {
		now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
		if len(rec) == 0 { // 会话不存在 → 补建完整会话行
			rec = data.Record{
				"session_id": sessionID, "title": sessionID,
				"created_at": now, "updated_at": now, "parent_id": "",
			}
		} else {
			rec["updated_at"] = now
		}
		rec["history"] = string(b)
		rec["snapshot_turn"] = snap.SnapshotTurn
		return rec
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
