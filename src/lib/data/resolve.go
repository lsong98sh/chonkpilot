// 配置分层解析（12-数据层）：
//
//	读：prjusr → prj → usr → 系统（资源/常量），首个命中即生效
//	写：只写当前层；删本层 key = 恢复继承上一级
package data

// SystemLayer 是系统级来源标识（无库，取值来自随发布资源/代码常量）。
const SystemLayer Layer = "system"

// SystemDefault 取系统级兜底值（资源/常量）；该 key 无系统默认 → ok=false。
type SystemDefault func(key string) (string, bool)

// Resolver 按 key 做分层覆盖解析；各层 DB 可为 nil（视为不存在，跳过）。
type Resolver struct {
	prjUsr *DB
	prj    *DB
	usr    *DB
	system SystemDefault
}

// NewResolver 构造解析器（system 可为 nil）。
func NewResolver(prjUsr, prj, usr *DB, system SystemDefault) *Resolver {
	return &Resolver{prjUsr: prjUsr, prj: prj, usr: usr, system: system}
}

// Get 按 key 逐层解析，返回 (值, 来源层, ok)。
// 来源层 = SystemLayer 表示命中系统级；ok=false 表示各层与系统默认都没有该 key。
func (r *Resolver) Get(key string) (string, Layer, bool) {
	if r == nil {
		return "", "", false
	}
	for _, l := range []struct {
		layer Layer
		db    *DB
	}{
		{LayerPrjUsr, r.prjUsr},
		{LayerPrj, r.prj},
		{LayerUsr, r.usr},
	} {
		if l.db == nil {
			continue
		}
		if v, ok := GetConfig(l.db, key); ok {
			return v, l.layer, true
		}
	}
	if r.system != nil {
		if v, ok := r.system(key); ok {
			return v, SystemLayer, true
		}
	}
	return "", "", false
}

// DB 返回指定层的库句柄（usr/prj/prjusr）；未知层或未挂载 → nil。
func (r *Resolver) DB(layer Layer) *DB {
	if r == nil {
		return nil
	}
	switch layer {
	case LayerPrjUsr:
		return r.prjUsr
	case LayerPrj:
		return r.prj
	case LayerUsr:
		return r.usr
	}
	return nil
}

// Set 写指定层的 key（只写目标层；层未挂载 → 返回 ErrLayerUnavailable）。
func (r *Resolver) Set(layer Layer, key, value string) error {
	db := r.DB(layer)
	if db == nil {
		return ErrLayerUnavailable
	}
	return SetConfig(db, key, value)
}

// Reset 删除指定层的 key（不存在视为成功，效果 = 恢复继承上一级）。
func (r *Resolver) Reset(layer Layer, key string) error {
	db := r.DB(layer)
	if db == nil {
		return ErrLayerUnavailable
	}
	return DeleteConfig(db, key)
}
