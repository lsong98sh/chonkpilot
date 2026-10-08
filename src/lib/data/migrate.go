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

// migrations 内置迁移：**已压平为单条最新版**（2026-10-07；未发布期无旧库，不留增量史）。
// 一条即建出当前全量桶；ensureBucket 幂等，故既有库（schema_version < 7）跑一次即对齐，
// 且**不主动删除**旧库中已存在的历史桶（如 scenarios / permission 残留，其已无任何读写）。
var migrations = []Migration{
	{Version: 7, Name: "schema", Up: func(tx *bolt.Tx) error {
		for _, b := range []string{
			bucketMeta, bucketMigrations, "config",
			"llms", "mcps",
			"sessions", "turns", "messages", "tasktree", "meta",
			"sessions_by_created", "turns_by_session", "messages_by_turn",
			"tool_pair_by_turn", "tool_pair_by_task", "tasktree_by_parent", "tasktree_by_top",
			"task_shadow", "task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_instance",
			"memory_extract",
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
