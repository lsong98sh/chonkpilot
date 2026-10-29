// 建桶（对齐 12-数据层）：打开库时按层建该层实际使用的桶，缺失即建；**无迁移体系**
// （2026-10-25 决策：开发期不需要版本迁移，库结构 = `bucketsFor` 直建）。
//
// 表/索引的写入口本身仍走 CreateBucketIfNotExists（Table/索引），故运行期新增表也能自愈。
package data

import (
	bolt "go.etcd.io/bbolt"
)

// personalBuckets 是 prjusr（个人运行态）实际使用的桶（含二级索引）。
//
// 2026-10-28（SESSION-DB-2）：prjusr `chonkpilot.db` = **宿主/server 独占**（配置 + UI 运行态 +
// 对话目录 `dialogs`）；会话正文归**一树一库** `dialogs/dialog-<hex>.db`（Session 独占）。
// 会话内容桶（`turns` / `messages` / `tasktree` / `inbox_items` 等）仍保留在此，**仅供存量库
// 兼容读取**（旧布局已退场，新写入只落对话库，见 `shardBuckets`）。
var personalBuckets = []string{
	"sessions", "turns", "messages", "tasktree", "task_shadow",
	"memory_extract", "group_participants", "group_round_targets",
	"inbox_items", "turn_events", "dialogs",
	"sessions_by_top", "turns_by_session",
	"messages_by_session", "messages_by_turn", "messages_by_client_msg",
	"tasktree_by_parent", "tasktree_by_top", "tasktree_by_session",
	"task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_session",
}

// shardBuckets 是一树一库（`dialogs/dialog-<hex>.db`，**Session 独占**）实际使用的桶。
//
// 只含会话正文域：`config`（配置归宿主库）与 `dialogs`（对话目录归宿主库）**不得**出现在
// 对话库内——两个 owner 的边界由桶集合强制（SESSION-DB-2）。
var shardBuckets = []string{
	"sessions", "turns", "messages", "tasktree", "task_shadow",
	"inbox_items", "turn_events",
	"sessions_by_top", "turns_by_session",
	"messages_by_session", "messages_by_turn", "messages_by_client_msg",
	"tasktree_by_parent", "tasktree_by_top", "tasktree_by_session",
	"task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_session",
}

// bucketsFor 返回该层应建的桶（按层实际使用）。
// 层标识为空（`Open` 未指定层：通用工具 / 测试）→ 返回**并集**。
func bucketsFor(layer Layer) []string {
	base := []string{"config"}
	switch layer {
	case LayerUsr:
		return append(base, "llms")
	case LayerPrj:
		return base // 项目级只剩 config
	case LayerPrjUsr:
		return append(base, personalBuckets...)
	case LayerDialog:
		// 对话库 = Session 独占：不建 `config` / `dialogs`（宿主库专属）。
		return append([]string{}, shardBuckets...)
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

// ensureLayerBuckets 在打开库的同一事务内建齐该层桶（幂等）。
func ensureLayerBuckets(tx *bolt.Tx, layer Layer) error {
	for _, b := range bucketsFor(layer) {
		if err := ensureBucket(tx, b); err != nil {
			return err
		}
	}
	return nil
}
