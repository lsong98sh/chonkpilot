// 数据初始化（seed，对齐 12-数据层）：**仅 usr 层**写默认值（幂等，已存在不覆盖）。
// 系统级默认 = 代码常量/随发布资源，不落库；prj/prjusr 无 seed（键缺省即沿 fallback 继承）。
package data

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

const bucketConfig = "config"

// seedDefaults 按层 seed（迁移后同一事务内执行）。写入形态与 config 表一致：{"v": <值>}。
func seedDefaults(tx *bolt.Tx, layer Layer) error {
	b := tx.Bucket([]byte(bucketConfig))
	if b == nil {
		return nil
	}
	for k, v := range seedMap(layer) {
		if b.Get([]byte(k)) != nil {
			continue // 已存在不覆盖
		}
		raw, err := json.Marshal(Record{"v": v})
		if err != nil {
			return err
		}
		if err := b.Put([]byte(k), raw); err != nil {
			return err
		}
	}
	return nil
}

// seedMap 返回该层的默认配置（key → 字符串值）。
//
// v6：**三层均不预写任何值**——默认值一律来自系统常量/随发布资源，缺 key 即沿 fallback
// 继承（12-数据层）。若预写 theme 之类用户级值，会把"继承系统默认"变成
// "用户已显式覆盖"，破坏 fallback 语义与"重置继承"能力。
func seedMap(layer Layer) map[string]string {
	return nil
}
