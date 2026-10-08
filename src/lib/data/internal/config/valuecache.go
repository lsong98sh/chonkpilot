// config 表进程内值缓存（D-45：prj/usr 层短开后的高频读吸收；12-数据层 §5.4）。
//
// 背景：D-45 使 prj/usr 层连接按调用短开（bbolt 单文件排他锁——长持会锁死 GUI × CLI
// 对同一项目库的并发访问），config 门面读路径随之配「值缓存」：缓存**只存 config 表
// 平铺值**（不持库句柄），读前 stat db 文件 mtime+size 判失效。
//
// 失效口径：
//   - 本进程写：门面写路径（ConfigKVSet/ConfigKVDelete/UserConfigSet/UserConfigDelete）
//     写后**主动失效**（defer invalidate，错误路径的部分写入也覆盖）；
//   - 跨进程写（D-45 核心场景：CLI 改 prj 库 → GUI 读）：读前 stat 发现 mtime/size
//     变化 → 失效重读。stamp 取**读表之前**的 stat（读表发生在 stamp 之后 → 读到的值
//     ≥ stamp 时刻状态；「stamp 相同」即「文件自 stamp 起未变」→ 命中必新鲜）。
//
// 范围：仅 config 表 kv 值；llms 集合表不缓存（低频、整体替换写）。缓存条目的 vals
// 不可变（失效 = 替换整条），并发读无需二次加锁。
package config

import (
	"os"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// dbStamp 是 db 文件变化判据（mtime+size 双判据：mtime 主、size 补精度）。
type dbStamp struct {
	modTime time.Time
	size    int64
}

// dbValues 是缓存条目（vals 只读共享：失效 = 替换整条，不原位改写）。
type dbValues struct {
	stamp dbStamp
	vals  map[string]string // config 表 key → v（kernel.Sval 收敛后的字符串）
}

var (
	valueCacheMu sync.Mutex
	valueCache   = map[string]*dbValues{} // key = db 文件绝对路径
)

// invalidateConfigValues 丢弃某 db 文件的 config 值缓存（写路径调用；下次读重读）。
// 幂等：路径不在缓存时为空操作。
func invalidateConfigValues(path string) {
	valueCacheMu.Lock()
	delete(valueCache, path)
	valueCacheMu.Unlock()
}

// cachedConfigValues 读 db 的 config 表平铺值（mtime+size 失效的进程内缓存）。
// stat 失败（文件刚删/重建中）→ 直读且不入缓存（下次读自然重建）。
func cachedConfigValues(db *data.DB) map[string]string {
	path := db.Path()
	if st, err := os.Stat(path); err == nil {
		stamp := dbStamp{modTime: st.ModTime(), size: st.Size()}
		valueCacheMu.Lock()
		ent, ok := valueCache[path]
		valueCacheMu.Unlock()
		if ok && ent.stamp == stamp {
			return ent.vals
		}
		vals := readConfigTable(db)
		valueCacheMu.Lock()
		valueCache[path] = &dbValues{stamp: stamp, vals: vals}
		valueCacheMu.Unlock()
		return vals
	}
	return readConfigTable(db)
}

// readConfigTable 直读 config 表全表（key → v；v 收敛口径与 kernel.ConfigGet/configKVList
// 一致 = kernel.Sval(rec["v"])）。
func readConfigTable(db *data.DB) map[string]string {
	out := map[string]string{}
	keys, err := db.Table("config").ListKeys()
	if err != nil {
		return out
	}
	for _, k := range keys {
		var rec data.Record
		if ok, _ := db.Table("config").Get(k, &rec); ok {
			out[k] = kernel.Sval(rec["v"])
		}
	}
	return out
}
