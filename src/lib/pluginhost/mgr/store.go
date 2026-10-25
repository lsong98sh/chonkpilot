// store.go —— 插件配置权威存储端口（ConfigStore）。
//
// 框架不持有数据库实现：由宿主经 `Options.ConfigStore` 注入（例如 usr 层
// `chonkpilot.db` 的 bbolt 适配器）；未注入或打开失败 → 降级为纯文件态
// （`<DataDir>/plugins/<id>/config.json|secrets.json`）。
package mgr

// ConfigStore 是插件配置的权威存储端口。
//
// table = 表/桶名（`plugin_config` / `plugin_secret`），id = 插件 id。
// Get 未命中返回 (nil, false, nil)；Delete 对不存在的行必须视为成功（幂等）。
type ConfigStore interface {
	Get(table, id string) (map[string]any, bool, error)
	Upsert(table, id string, values map[string]any) error
	Delete(table, id string) error
}

// ConfigStoreOpener 打开存储端口；release 可为 nil（无需关闭时）。
type ConfigStoreOpener func() (ConfigStore, func(), error)
