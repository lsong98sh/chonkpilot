// 跨表写事务（MOD-1 M6）：`data-inbox-commit` 需要在**同一 bbolt 写事务**内
// 落 messages + 置 inbox consumed。Table 的 Insert/Update 各自开事务，无法跨表原子，
// 故新增本事务句柄（只暴露提交路径需要的 Get / Exists / Insert / Put）。
package data

import bolt "go.etcd.io/bbolt"

// Tx 是单次写事务句柄（由 DB.Update 提供；fn 返回 error → 整体回滚）。
type Tx struct{ tx *bolt.Tx }

// Update 在单个 bbolt 写事务内执行 fn（跨表原子写）。
func (db *DB) Update(fn func(tx *Tx) error) error {
	return db.b.Update(func(bt *bolt.Tx) error {
		return fn(&Tx{tx: bt})
	})
}

// Get 读一行（表/桶不存在 → ok=false, nil）；out 非 nil 时回填记录（含 _key）。
func (tx *Tx) Get(table, key string, out *Record) (bool, error) {
	b := tx.tx.Bucket([]byte(table))
	if b == nil {
		return false, nil
	}
	rec, ok := decodeRecord(b.Get([]byte(key)), key)
	if !ok {
		return false, nil
	}
	if out != nil {
		*out = rec
	}
	return true, nil
}

// Exists 判断行是否存在。
func (tx *Tx) Exists(table, key string) (bool, error) {
	return tx.Get(table, key, nil)
}

// Insert 插入（已存在 → ErrExists）；同事务维护索引。
func (tx *Tx) Insert(table, key string, rec Record) error {
	b, err := tx.tx.CreateBucketIfNotExists([]byte(table))
	if err != nil {
		return err
	}
	if b.Get([]byte(key)) != nil {
		return ErrExists
	}
	if rec == nil {
		rec = Record{}
	}
	if err := indexPut(tx.tx, table, key, rec); err != nil {
		return err
	}
	return putRecord(b, key, rec)
}

// Put 写入/覆盖（索引按新记录重建；既有行的旧索引项先删除）。
func (tx *Tx) Put(table, key string, rec Record) error {
	b, err := tx.tx.CreateBucketIfNotExists([]byte(table))
	if err != nil {
		return err
	}
	if old, ok := decodeRecord(b.Get([]byte(key)), key); ok {
		if err := indexDelete(tx.tx, table, key, old); err != nil {
			return err
		}
	}
	if rec == nil {
		rec = Record{}
	}
	if err := indexPut(tx.tx, table, key, rec); err != nil {
		return err
	}
	return putRecord(b, key, rec)
}
