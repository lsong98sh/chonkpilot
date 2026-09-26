// 通用 Table DAO（类 Hibernate，对齐 12-数据层）：增删改查 + 通用 Query。
package data

import (
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Record 是表行（bucket key → JSON value）。
type Record map[string]any

// ErrExists 插入已存在的主键。
var ErrExists = errors.New("data: key already exists")

// ErrNotFound 更新/删除不存在的主键。
var ErrNotFound = errors.New("data: key not found")

// ErrLayerUnavailable 目标层未挂载（Resolver 对该层读写失败）。
var ErrLayerUnavailable = errors.New("data: layer unavailable")

// KeyField 是 Query 返回记录时附加的桶主键字段名（游标续查也依赖它）。
const KeyField = "_key"

// Table 是表（桶）DAO。
type Table struct {
	db   *DB
	name string
}

// Name 返回表名。
func (t *Table) Name() string { return t.name }

// Insert 插入（已存在 → ErrExists）。
func (t *Table) Insert(key string, rec Record) error {
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		if b.Get([]byte(key)) != nil {
			return ErrExists
		}
		return putRecord(b, key, rec)
	})
}

// Update 覆盖更新（不存在 → ErrNotFound）。
func (t *Table) Update(key string, rec Record) error {
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		if b.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		return putRecord(b, key, rec)
	})
}

// Upsert 写入（不存在插入；已存在覆盖；写 updated_at）。
func (t *Table) Upsert(key string, rec Record) error {
	if rec == nil {
		rec = Record{}
	}
	rec["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		return putRecord(b, key, rec)
	})
}

// Get 按主键读（不存在 → ok=false）。
func (t *Table) Get(key string, out *Record) (bool, error) {
	var found bool
	err := t.db.b.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v == nil {
			return nil
		}
		rec := Record{}
		if err := json.Unmarshal(v, &rec); err != nil {
			return err
		}
		if rec == nil {
			rec = Record{} // 记录值为 JSON null 时 Unmarshal 得 nil map，防御 rec[KeyField]=key panic
		}
		rec[KeyField] = key
		*out = rec
		found = true
		return nil
	})
	return found, err
}

// Delete 删除（不存在 → ErrNotFound）。
func (t *Table) Delete(key string) error {
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		if b == nil || b.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		return b.Delete([]byte(key))
	})
}

// ListKeys 返回主键列表。
func (t *Table) ListKeys() ([]string, error) {
	var keys []string
	err := t.db.b.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	return keys, err
}

// putRecord 序列化并写入桶。
func putRecord(b *bolt.Bucket, key string, rec Record) error {
	if rec == nil {
		rec = Record{}
	}
	delete(rec, KeyField) // 主键不落 value
	v, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return b.Put([]byte(key), v)
}
