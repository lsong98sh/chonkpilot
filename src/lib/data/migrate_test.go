// 迁移测试：v13 死桶清理（`mcps` / `meta` 零读写，新建库不再建、旧库空桶删除）。
package data

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// TestDeadBucketsDropped：新建库不含 `mcps` / `meta`（v8 清单移除 + v13 兜底删除）。
func TestDeadBucketsDropped(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "dead.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.b.View(func(tx *bolt.Tx) error {
		for _, name := range []string{"mcps", "meta"} {
			if tx.Bucket([]byte(name)) != nil {
				t.Errorf("死桶 %q 不应存在", name)
			}
		}
		for _, name := range []string{"config", "llms", "inbox_items", "dialogs"} {
			if tx.Bucket([]byte(name)) == nil {
				t.Errorf("桶 %q 应存在", name)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("view: %v", err)
	}
}

// TestDropDeadBucketsKeepsData：旧库若这两个桶**非空**（历史残留）→ v13 保留不删。
func TestDropDeadBucketsKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 手工造一个非空 `mcps` 桶 + 回退 schema_version 到 12，模拟旧库。
	if err := db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("mcps"))
		if err != nil {
			return err
		}
		if err := b.Put([]byte("k"), []byte(`{"v":1}`)); err != nil {
			return err
		}
		return tx.Bucket([]byte(bucketMeta)).Put([]byte("schema_version"), []byte("12"))
	}); err != nil {
		db.Close()
		t.Fatalf("seed legacy: %v", err)
	}
	db.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	if err := again.b.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("mcps"))
		if b == nil {
			t.Errorf("非空 mcps 桶不应被删除")
			return nil
		}
		if b.Get([]byte("k")) == nil {
			t.Errorf("mcps 既有数据不得丢失")
		}
		return nil
	}); err != nil {
		t.Fatalf("view: %v", err)
	}
}
