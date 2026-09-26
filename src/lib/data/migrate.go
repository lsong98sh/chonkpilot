// Schema 与迁移（对齐 12-数据层）：_meta.schema_version / _migrations。
// v6 起三层（usr/prj/prjusr）**同构**：迁移不再按层分支，全部桶在每层都建（空桶无妨）。
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

// migrations 内置迁移（v1 基础桶 / v3 会话桶组 / v4 专用表 v6）。
// 历史版本（v1/v3）保留原 Up 语义以保证既有库版本推进；v6 起不再有层条件。
// v2（usr recent 桶）已于 2026-09-11 摘除（无读写死桶，见 42-决策记录 G7）。
var migrations = []Migration{
	{Version: 1, Name: "init base buckets", Up: func(tx *bolt.Tx) error {
		for _, b := range []string{bucketMeta, bucketMigrations, "config"} {
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		return nil
	}},
	{Version: 3, Name: "session buckets", Up: func(tx *bolt.Tx) error {
		for _, b := range []string{
			"sessions", "turns", "messages", "config", "scenarios", "permission", "tasktree", "meta",
			"sessions_by_created", "turns_by_session", "messages_by_turn",
			"tool_pair_by_turn", "tool_pair_by_task", "tasktree_by_parent", "tasktree_by_top",
		} {
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		return nil
	}},
	// v4：专用表（LLM / MCP 定义，v6 起定义只存 usr 层；其它层建空桶保持同构）。
	{Version: 4, Name: "llms and mcps tables", Up: func(tx *bolt.Tx) error {
		for _, b := range []string{"llms", "mcps"} {
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		return nil
	}},
	// v5：任务层影子域（21 任务层设计方案 §9.1 P1）——影子表 + 索引桶（按 top_session /
	// parent_id / instance_id；照 tasktree / tasktree_by_parent / tasktree_by_top 既有写法）。
	// **不改动** tasktree 表与 data-tasktree-* 默认语义：影子读写经 `shadow: true` 显式路由。
	{Version: 5, Name: "task shadow buckets", Up: func(tx *bolt.Tx) error {
		for _, b := range []string{
			"task_shadow", "task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_instance",
		} {
			if err := ensureBucket(tx, b); err != nil {
				return err
			}
		}
		return nil
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
