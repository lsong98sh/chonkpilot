// 通用 Table DAO（类 Hibernate，对齐 12-数据层）：增删改查 + 通用 Query。
package data

import (
	"bytes"
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

// Insert 插入（已存在 → ErrExists）；同事务维护索引。
func (t *Table) Insert(key string, rec Record) error {
	if rec == nil {
		rec = Record{}
	}
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		if b.Get([]byte(key)) != nil {
			return ErrExists
		}
		if err := indexPut(tx, t.name, key, rec); err != nil {
			return err
		}
		return putRecord(b, key, rec)
	})
}

// Update 覆盖更新（不存在 → ErrNotFound）；同事务迁移索引（删旧键、插新键）。
func (t *Table) Update(key string, rec Record) error {
	if rec == nil {
		rec = Record{}
	}
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		if b.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		if old, ok := decodeRecord(b.Get([]byte(key)), key); ok {
			if err := indexDelete(tx, t.name, key, old); err != nil {
				return err
			}
		}
		if err := indexPut(tx, t.name, key, rec); err != nil {
			return err
		}
		return putRecord(b, key, rec)
	})
}

// Upsert 写入（不存在插入；已存在覆盖；写 updated_at）；同事务迁移索引（created_at 变更亦正确）。
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
		// 先删旧索引键（键可能因列值变更而迁移），再写主行与新索引键。
		if old, ok := decodeRecord(b.Get([]byte(key)), key); ok {
			if err := indexDelete(tx, t.name, key, old); err != nil {
				return err
			}
		}
		if err := indexPut(tx, t.name, key, rec); err != nil {
			return err
		}
		return putRecord(b, key, rec)
	})
}

// UpdateIn 在**单次事务内**完成「读-改-写」(RMW)：fn 接收当前行（不存在 → 空记录），
// 返回要落库的记录；同事务迁移索引键（旧行存在则先删旧索引键、再写新）。用于把
// Get→改→Upsert 的跨事务竞态收敛为原子操作（避免并发 lost update，A-09）。
//
// 语义：**隐式 upsert**（不存在 → 以 fn 返回值为新行写入），与 Upsert 的「不存在即插入」一致；
// updated_at **由 fn 自行设置**（本原语不隐式写入，调用方按需写 RFC3339FixedNano）。
func (t *Table) UpdateIn(key string, fn func(rec Record) Record) error {
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		rec := Record{}
		if old, ok := decodeRecord(b.Get([]byte(key)), key); ok {
			if err := indexDelete(tx, t.name, key, old); err != nil {
				return err
			}
			rec = old
			delete(rec, KeyField) // 主键不落 value
		}
		out := fn(rec)
		if out == nil {
			out = Record{}
		}
		if err := indexPut(tx, t.name, key, out); err != nil {
			return err
		}
		return putRecord(b, key, out)
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

// Delete 删除（不存在 → ErrNotFound）；同事务清理索引键。
func (t *Table) Delete(key string) error {
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		raw := []byte(nil)
		if b != nil {
			raw = b.Get([]byte(key))
		}
		if raw == nil {
			return ErrNotFound
		}
		if old, ok := decodeRecord(raw, key); ok {
			if err := indexDelete(tx, t.name, key, old); err != nil {
				return err
			}
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

// ListPrefix 按主键前缀顺序返回主键列表（Seek 起扫，遇非前缀即止）。
// 供「主键本身即复合键」的表（memory_extract / config）做前缀读；仅返回键，值由调用方按需 Get。
// prefix 为空 → 等价 ListKeys（全部主键）。
func (t *Table) ListPrefix(prefix string) ([]string, error) {
	var keys []string
	p := []byte(prefix)
	err := t.db.b.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.Seek(p); k != nil && bytes.HasPrefix(k, p); k, _ = c.Next() {
			keys = append(keys, string(k))
		}
		return nil
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
