// Schema 与迁移（对齐 12-数据层）：_meta.schema_version / _migrations。
//
// 2026-10-25：**不再三层同构** —— 各层只建自己实际使用的桶（`bucketsFor`，v14 清旧库多余空桶）。
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

// personalBuckets 是 prjusr（个人运行态）实际使用的桶（含二级索引）。
var personalBuckets = []string{
	"sessions", "turns", "messages", "tasktree", "task_shadow",
	"memory_extract", "group_participants", "group_round_targets",
	"inbox_items", "index_queue", "turn_events", "dialogs",
	"sessions_by_top", "turns_by_session",
	"messages_by_session", "messages_by_turn", "messages_by_client_msg",
	"tasktree_by_parent", "tasktree_by_top", "tasktree_by_session",
	"task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_session",
}

// shardBuckets 是一树一库（旧 `sessions/<hex>/`、新 `dialogs/dialog-<hex>.db`）实际使用的桶。
var shardBuckets = []string{
	"sessions", "turns", "messages", "tasktree", "task_shadow",
	"inbox_items", "turn_events", "dialogs",
	"sessions_by_top", "turns_by_session",
	"messages_by_session", "messages_by_turn", "messages_by_client_msg",
	"tasktree_by_parent", "tasktree_by_top", "tasktree_by_session",
	"task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_session",
}

// bucketsFor 返回该层应建的桶（按层实际使用；缺写时 Table/索引仍会 CreateBucketIfNotExists）。
// 层标识为空（`Open` 未指定层：旧调用方 / 通用工具 / 测试）→ 返回**并集**，保持历史「全建」行为。
func bucketsFor(layer Layer) []string {
	base := []string{bucketMeta, bucketMigrations, "config"}
	switch layer {
	case LayerUsr:
		return append(base, "llms")
	case LayerPrj:
		return base // 项目级只剩 config（file_list 已随 vfts 插件自持退场，v15 清桶）
	case LayerPrjUsr:
		return append(base, personalBuckets...)
	case LayerSession, LayerDialog:
		return append(base, shardBuckets...)
	}
	out := append([]string{}, base...)
	out = append(out, "llms")
	out = append(out, personalBuckets...)
	seen := make(map[string]bool, len(out))
	uniq := out[:0]
	for _, b := range out {
		if seen[b] {
			continue
		}
		seen[b] = true
		uniq = append(uniq, b)
	}
	return uniq
}

// hasBucket 判定该层的桶集合是否含指定桶。
func hasBucket(layer Layer, name string) bool {
	for _, b := range bucketsFor(layer) {
		if b == name {
			return true
		}
	}
	return false
}

// Migration 是一次版本迁移（事务内执行，失败回滚中止启动）。
// 顺序即执行顺序；已应用的版本（_meta.schema_version）不再重复执行。
// UpLayer 非空时按目标层执行（按层建桶 / 清桶用），否则调用 Up。
type Migration struct {
	Version int
	Name    string
	Up      func(tx *bolt.Tx) error
	UpLayer func(tx *bolt.Tx, layer Layer) error
}

// migrations 内置迁移：v8 为**压平后的全量 schema**（未发布期无旧库，不再保留 v1~v7 增量史），
// 其后按功能增量追加：v9 inbox_items / v10 index_queue / v11 turn_events / v12 dialogs /
// v13 drop_dead_buckets / v14 drop_unused_buckets / v15 drop_file_list。ensureBucket 幂等，
// 既有库跑未应用版本即对齐；v13/v14 只清**空**桶（不静默丢数据），v15 删的是**派生缓存**表。
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
	{Version: 8, Name: "schema", UpLayer: func(tx *bolt.Tx, layer Layer) error {
		for _, b := range bucketsFor(layer) {
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		// 索引回填（A-01）：遍历主表逐行重建二级索引项。写的是**索引桶**（与主表不同桶），
		// 故可与主表 ForEach 共存；空库/新库/无该表的层 → 空转（幂等）。
		for _, table := range []string{"sessions", "turns", "messages", "tasktree", "task_shadow"} {
			if !hasBucket(layer, table) {
				continue
			}
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
	{Version: 9, Name: "inbox", UpLayer: func(tx *bolt.Tx, layer Layer) error {
		if !hasBucket(layer, "inbox_items") {
			return nil
		}
		return ensureBucket(tx, "inbox_items")
	}},
	// v10（resident indexer，P1）：持久化待索引队列（prjusr 级 `index_queue` 表）。
	// 写入方 = filesys（create/write/remove/rename），消费方 = resident indexer（取一批 → 索引 →
	// 回执）。查询模式 = 按 `work_dir` + `state` 过滤、`created_at` 升序；队列量小，
	// **不建二级索引**（全桶过滤后内存排序即可）。
	{Version: 10, Name: "index_queue", UpLayer: func(tx *bolt.Tx, layer Layer) error {
		if !hasBucket(layer, "index_queue") {
			return nil
		}
		return ensureBucket(tx, "index_queue")
	}},
	// v11（MOD-1 M3，2026-10-20）：TurnState 事件日志（prjusr 级 `turn_events` 表）。
	// 主键 `<turn_id>/<seq 12 位>` → 同 turn 顺序扫描即重放；无二级索引（按 turn 前缀扫，
	// 清理按 turns 终态 + 保留天数过滤）。只加桶，不涉及既有表。
	{Version: 11, Name: "turn_events", UpLayer: func(tx *bolt.Tx, layer Layer) error {
		if !hasBucket(layer, "turn_events") {
			return nil
		}
		return ensureBucket(tx, "turn_events")
	}},
	// v12（SESSION-COMP-1 P0，2026-10-24）：会话 → 对话（一树一库）。
	//   - `dialogs`：prjusr 侧的可重建目录（dialog_id/file/title/kind/updated_at）；
	//   - `messages_by_client_msg`：幂等键索引 `(session_id, client_msg_id)`；
	//   - 回填：既有 messages 行按新索引重建索引项（indexPut 幂等；空 client_msg_id 亦入索引，
	//     键尾为主键 → 不冲突）。
	// 会话库（`dialogs/dialog-<hex>.db`）由 `Internal/sessionstore.Migrate` 搬运，不在本迁移内。
	{Version: 12, Name: "dialogs", UpLayer: func(tx *bolt.Tx, layer Layer) error {
		for _, b := range []string{"dialogs", "messages_by_client_msg"} {
			if !hasBucket(layer, b) {
				continue
			}
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		if !hasBucket(layer, "messages") {
			return nil
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
	// v13（2026-10-25 · schema 债务清理）：删除两个**零读写**死桶——
	//   - `mcps`：旧 usr `mcpServers` 集合（MCP 定义已改四级文件化 `capability/mcps/*.json`）；
	//   - `meta`：预留桶，自建桶起从无写入。
	// 仅当桶**为空**时删（历史残留数据保留可查，不静默丢数据）；新建库（v8 已不含这两桶）
	// 不受影响。
	{Version: 13, Name: "drop_dead_buckets", Up: func(tx *bolt.Tx) error {
		for _, name := range []string{"mcps", "meta"} {
			b := tx.Bucket([]byte(name))
			if b == nil || b.Stats().KeyN > 0 {
				continue
			}
			if err := tx.DeleteBucket([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	}},
	// v14（2026-10-25 · 按层建桶）：三层不再同构 —— 删除本层**不使用**的空桶
	// （usr 只剩 config/llms；prj 只剩 config；prjusr 与一树一库各按实际表集）。
	// 只删空桶：非空残留保留可查；后续若该层真写入，Table/索引会 CreateBucketIfNotExists 自建。
	{Version: 14, Name: "drop_unused_buckets", UpLayer: func(tx *bolt.Tx, layer Layer) error {
		keep := make(map[string]bool, len(bucketsFor(layer)))
		for _, b := range bucketsFor(layer) {
			keep[b] = true
		}
		var drop []string
		if err := tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			n := string(name)
			if keep[n] || n == bucketMeta || n == bucketMigrations {
				return nil
			}
			if b.Stats().KeyN == 0 {
				drop = append(drop, n)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, n := range drop {
			if err := tx.DeleteBucket([]byte(n)); err != nil {
				return err
			}
		}
		return nil
	}},
	// v15（2026-10-25 · file_list 退场）：vfts 增量清单改由**插件自持**本地文件
	// （`<workdir>/.chonkpilot/vfts/file_list.json`，D-71）→ prj 库不再有 `file_list` 表。
	// 该表是**派生缓存**（删除后插件首轮同步按全量重建，无数据损失），故这里**含数据**一并删除
	// （v14 只清空桶，非空残留会留下来）。
	{Version: 15, Name: "drop_file_list", Up: func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("file_list")) == nil {
			return nil
		}
		return tx.DeleteBucket([]byte("file_list"))
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
