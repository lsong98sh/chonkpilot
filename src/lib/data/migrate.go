// Schema 与迁移（对齐 12-数据层）：_meta.schema_version / _migrations。
// 三层（usr/prj/prjusr）**同构**：不按层分支，全部桶在每层都建（空桶无妨）。
package data

import (
	"fmt"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	bucketMeta       = "_meta"
	bucketMigrations = "_migrations"
)

// Migration 是一次版本迁移（事务内执行，失败回滚中止启动）。
// 顺序即执行顺序；已应用的版本（_meta.schema_version）不再重复执行。
type Migration struct {
	Version int
	Name    string
	Up      func(tx *bolt.Tx) error
}

// migrations 内置迁移：**已压平为单条最新版**（未发布期无旧库，不留增量史）。
// 一条即建出当前全量桶；ensureBucket 幂等，故既有库（schema_version < 8）跑一次即对齐，
// 且**不主动删除**旧库中已存在的历史桶（如 scenarios / permission 残留，其已无任何读写）。
//
// 注（2026-10-08 a）：原 `sessions_by_created`/`turns_by_session`/`messages_by_turn`/
// `tool_pair_by_turn`/`tool_pair_by_task` 五个索引桶**建而不用**（全仓零读写，仅残留于本清单），
// 已从建桶清单移除；既有库中的这些空桶无害、不主动清理。
//
// 注（2026-10-08 b）：Phase 1「按查询模式建索引」落地——新增二级索引桶
// `sessions_by_top` / `turns_by_session` / `messages_by_session` / `messages_by_turn` /
// `tasktree_by_session`，并入影子表的 `task_shadow_by_session`；索引由 Table 写入口
// 在同一事务内维护（见 indexes.go）。原影子索引桶 `task_shadow_by_instance` 已无写方、
// 由 `task_shadow_by_session` 取代，从清单移除（既有库残留空桶不清理）。
//
// 注（2026-10-08 c）：A-01 索引回填——升级库（schema_version < 8）中主行早于索引存在，
// 其后任一新写入即令索引桶非空，query.go 的「整桶空」防线随之失效，旧行前缀区段为空 →
// Seek 落空 → 静默漏行（数据凭空消失）。故建桶后**遍历 5 张主表逐行重建索引项**
// （indexPut 幂等：已存在键 Put 覆盖）；坏行/解码失败跳过（与查询坏行跳过口径一致）。
//
// 注（2026-10-16 d）：SUBCHAT-3 阶段 E 新增**群聊两表**（prjusr）—— `group_participants`
// （参与者名单 + 角色）与 `group_round_targets`（每【大轮次】的投递对象）。两表**无二级索引**
// （查询模式 = 单前缀 Seek，主键首段即 `session_id`，见 indexes.go IndexesFor 未登记 +
// facade/group.go 文件头）；**不参与**下方主表索引回填（无索引可建）。旧库（已有行）不受影响。
var migrations = []Migration{
	{Version: 8, Name: "schema", Up: func(tx *bolt.Tx) error {
		for _, b := range []string{
			bucketMeta, bucketMigrations, "config",
			"llms", "mcps",
			"sessions", "turns", "messages", "tasktree", "meta",
			"file_list",
			"sessions_by_top",
			"turns_by_session",
			"messages_by_session", "messages_by_turn",
			"tasktree_by_parent", "tasktree_by_top", "tasktree_by_session",
			"task_shadow", "task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_session",
			"memory_extract",
			"group_participants", "group_round_targets",
		} {
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		// 索引回填（A-01）：遍历主表逐行重建二级索引项。写的是**索引桶**（与主表不同桶），
		// 故可与主表 ForEach 共存；空库/新库无行 → 空转（幂等）。
		for _, table := range []string{"sessions", "turns", "messages", "tasktree", "task_shadow"} {
			b := tx.Bucket([]byte(table))
			if b == nil {
				continue
			}
			if err := b.ForEach(func(k, v []byte) error {
				rec, ok := decodeRecord(v, string(k))
				if !ok {
					return nil // 坏行/空值 → 跳过
				}
				return indexPut(tx, table, string(k), rec)
			}); err != nil {
				return err
			}
		}
		return nil
	}},
	// v9（2026-10-17，INBOX）：中途插话 / 等下一轮的 prjusr 级待处理队列。
	// 只加桶、无二级索引（查询模式 = 按 session_id 全桶过滤后按 created_at 排序，量级 ≤ 50/会话）。
	{Version: 9, Name: "inbox", Up: func(tx *bolt.Tx) error {
		return ensureBucket(tx, "inbox_items")
	}},
	// v10（resident indexer，P1）：持久化待索引队列（prjusr 级 `index_queue` 表）。
	// 写入方 = filesys（create/write/remove/rename），消费方 = resident indexer（取一批 → 索引 →
	// 回执）。查询模式 = 按 `work_dir` + `state` 过滤、`created_at` 升序；队列量小，
	// **不建二级索引**（全桶过滤后内存排序即可）。
	{Version: 10, Name: "index_queue", Up: func(tx *bolt.Tx) error {
		return ensureBucket(tx, "index_queue")
	}},
	// v11（MOD-1 M3，2026-10-20）：TurnState 事件日志（prjusr 级 `turn_events` 表）。
	// 主键 `<turn_id>/<seq 12 位>` → 同 turn 顺序扫描即重放；无二级索引（按 turn 前缀扫，
	// 清理按 turns 终态 + 保留天数过滤）。只加桶，不涉及既有表。
	{Version: 11, Name: "turn_events", Up: func(tx *bolt.Tx) error {
		return ensureBucket(tx, "turn_events")
	}},
	// v12（SESSION-COMP-1 P0，2026-10-24）：会话 → 对话（一树一库）。
	//   - `dialogs`：prjusr 侧的可重建目录（dialog_id/file/title/kind/updated_at）；
	//   - `messages_by_client_msg`：幂等键索引 `(session_id, client_msg_id)`；
	//   - 回填：既有 messages 行按新索引重建索引项（indexPut 幂等；空 client_msg_id 亦入索引，
	//     键尾为主键 → 不冲突）。
	// 会话库（`dialogs/dialog-<hex>.db`）由 `Internal/sessionstore.Migrate` 搬运，不在本迁移内。
	{Version: 12, Name: "dialogs", Up: func(tx *bolt.Tx) error {
		for _, b := range []string{"dialogs", "messages_by_client_msg"} {
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		b := tx.Bucket([]byte("messages"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			rec, ok := decodeRecord(v, string(k))
			if !ok {
				return nil // 坏行 → 跳过（与查询口径一致）
			}
			return indexPut(tx, "messages", string(k), rec)
		})
	}},
}

// readSchemaVersion 读 _meta.schema_version（默认 0）。
func readSchemaVersion(tx *bolt.Tx) int {
	b := tx.Bucket([]byte(bucketMeta))
	if b == nil {
		return 0
	}
	v, _ := strconv.Atoi(string(b.Get([]byte("schema_version"))))
	return v
}

// recordMigration 写 _migrations + 更新 _meta.schema_version。
func recordMigration(tx *bolt.Tx, m Migration) error {
	mg := tx.Bucket([]byte(bucketMigrations))
	if mg == nil {
		return fmt.Errorf("data: _migrations bucket missing")
	}
	rec := map[string]any{
		"version":    m.Version,
		"name":       m.Name,
		"applied_at": time.Now().UTC().Format(time.RFC3339),
	}
	if err := putRecord(mg, strconv.Itoa(m.Version), rec); err != nil {
		return err
	}
	mt := tx.Bucket([]byte(bucketMeta))
	if mt == nil {
		return fmt.Errorf("data: _meta bucket missing")
	}
	return mt.Put([]byte("schema_version"), []byte(strconv.Itoa(m.Version)))
}
