// config 表通用读写（12-数据层）：记录形态 {"v": <值>}，一个决策一个 key。
package data

import "encoding/json"

// GetConfig 读 config 表 key 的值（不存在 → ok=false）。
// 值为字符串时原样返回；非字符串（历史数据/数组等）序列化为 JSON 文本返回。
func GetConfig(db *DB, key string) (string, bool) {
	var rec Record
	ok, err := db.Table("config").Get(key, &rec)
	if err != nil || !ok {
		return "", false
	}
	raw, exists := rec["v"]
	if !exists || raw == nil {
		return "", false
	}
	if s, isStr := raw.(string); isStr {
		return s, true
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// SetConfig 写 config 表 key（记录整体为 {"v": value}）。
func SetConfig(db *DB, key, value string) error {
	return db.Table("config").Upsert(key, Record{"v": value})
}

// DeleteConfig 删 config 表 key（不存在视为成功，用于"重置继承"）。
func DeleteConfig(db *DB, key string) error {
	err := db.Table("config").Delete(key)
	if err == ErrNotFound {
		return nil
	}
	return err
}
