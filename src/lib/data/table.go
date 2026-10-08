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

// RFC3339FixedNano 固定 9 位纳秒 RFC3339 时间格式（数据面统一时间戳口径）：同秒内
// 字典序 = 时间序（秒级 RFC3339 / 裁剪尾零的 RFC3339Nano 均不满足）。
// 单一定义在本包；kernel 再导出同名常量供 internal 各域使用（本包不可反向 import
// internal/kernel——kernel 已 import 本包，会构成 import 环）。
const RFC3339FixedNano = "2006-01-02T15:04:05.000000000Z07:00"

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

// Upsert 写入（不存在插入；已存在覆盖）；同事务迁移索引（created_at 变更亦正确）。
// updated_at：rec 未携带时隐式写当前时刻（RFC3339FixedNano 纳秒口径，A-16——秒级 RFC3339
// 与 created_at 的纳秒值混排时同秒内字典序 ≠ 时间序）；调用方已设置则保留不覆盖。
func (t *Table) Upsert(key string, rec Record) error {
	if rec == nil {
		rec = Record{}
	}
	if _, has := rec["updated_at"]; !has {
		rec["updated_at"] = time.Now().UTC().Format(RFC3339FixedNano)
	}
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
// 返回要落库的记录；fn 返回 nil = 放弃本次写入（不建行、不改行，事务原状提交）——供
// 「行须存在」的调用方在 fn 内跳过（如逻辑删除遇行已消失）。同事务迁移索引键：旧索引键
// 在 fn 调用前固化为 (桶, 键) 对，fn 原地改列（含索引列）也不残留旧键。用于把
// Get→改→Upsert 的跨事务竞态收敛为原子操作（避免并发 lost update，A-09/A-17~A-19）。
//
// 语义：**隐式 upsert**（不存在且 fn 返回非 nil → 以 fn 返回值为新行写入），与 Upsert 的
// 「不存在即插入」一致；updated_at **由 fn 自行设置**（本原语不隐式写入，调用方按需写
// RFC3339FixedNano）。
func (t *Table) UpdateIn(key string, fn func(rec Record) Record) error {
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		rec := Record{}
		var oldIdx [][2]string // 旧行索引键 (桶名, 索引键)——fn 可能原地改 rec，须先固化
		if old, ok := decodeRecord(b.Get([]byte(key)), key); ok {
			rec = old
			delete(rec, KeyField) // 主键不落 value
			for _, idx := range IndexesFor(t.name) {
				if ik, ok2 := buildIndexKey(idx, key, old); ok2 {
					oldIdx = append(oldIdx, [2]string{idx.Bucket, ik})
				}
			}
		}
		out := fn(rec)
		if out == nil {
			return nil
		}
		for _, p := range oldIdx {
			if ib := tx.Bucket([]byte(p[0])); ib != nil {
				if err := ib.Delete([]byte(p[1])); err != nil {
					return err
				}
			}
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

// ForEach 单事务内按主键字节序遍历全部行（k = 主键，rec = 解码记录带 _key）：fn 返回错误
// 中止遍历并透传。桶不存在 → 不调用 fn；坏行（JSON 解析失败）跳过——与逐键 Get 的容错
// 口径一致。用于把「ListKeys + 逐键 Get」（每键一个独立 View 事务，万级行 = 万次事务且
// 非一致性快照，A-20）收敛为单事务扫描。
func (t *Table) ForEach(fn func(key string, rec Record) error) error {
	return t.db.b.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			if k == nil {
				return nil
			}
			rec, ok := decodeRecord(v, string(k))
			if !ok {
				return nil
			}
			return fn(string(k), rec)
		})
	})
}

// ReplaceAll 整体替换表内容（**单事务**）：先清空全部现有行（同事务清理索引键；坏行仅
// 物理删除），再写入 recs（键 = 主键；同事务建索引）。用于集合表的「清 + 写」原子化（A-22）：
// 中途失败整体回滚不残缺，清与写之间不存在空表窗口（并发读要么见旧集、要么见新集）。
func (t *Table) ReplaceAll(recs map[string]Record) error {
	return t.db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(t.name))
		if err != nil {
			return err
		}
		type row struct {
			key string
			rec Record // nil = 坏行（无索引键可清）
		}
		var olds []row
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			old, ok := decodeRecord(v, string(k))
			if !ok {
				olds = append(olds, row{key: string(k)})
				continue
			}
			olds = append(olds, row{key: string(k), rec: old})
		}
		for _, r := range olds {
			if r.rec != nil {
				if err := indexDelete(tx, t.name, r.key, r.rec); err != nil {
					return err
				}
			}
			if err := b.Delete([]byte(r.key)); err != nil {
				return err
			}
		}
		for k, rec := range recs {
			if rec == nil {
				rec = Record{}
			}
			if err := indexPut(tx, t.name, k, rec); err != nil {
				return err
			}
			if err := putRecord(b, k, rec); err != nil {
				return err
			}
		}
		return nil
	})
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
