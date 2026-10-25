// pluginconfigstore.go —— 插件配置权威存储的宿主适配器（ConfigStore 端口实现）。
//
// 框架 `host/mgr` 只声明端口；主仓在此把 usr 层 `chonkpilot.db` 的两个桶接上：
//
//	plugin_config  非密配置（key = 插件 id）
//	plugin_secret  密配置（key = 插件 id）
package host

import (
	"github.com/chonkpilot/chonkpilot-data"
	pluginmgr "github.com/lsong98sh/chonkpilot-plugin-framework/host/mgr"
)

// dataConfigStore 把 usr 层共享库适配为插件配置端口。
type dataConfigStore struct{ db *data.DB }

// openPluginConfigStore 打开 usr 层配置库（失败 → 返回 error，由框架降级为文件态）。
func openPluginConfigStore() (pluginmgr.ConfigStore, func(), error) {
	db, release, err := data.OpenSharedLayer(data.UserPath(), data.LayerUsr)
	if err != nil {
		return nil, nil, err
	}
	return dataConfigStore{db: db}, release, nil
}

func (s dataConfigStore) Get(table, id string) (map[string]any, bool, error) {
	rec := data.Record{}
	ok, err := s.db.Table(table).Get(id, &rec)
	if err != nil || !ok {
		return nil, false, err
	}
	out := map[string]any{}
	for k, v := range rec {
		if k == data.KeyField || k == "updated_at" {
			continue
		}
		out[k] = v
	}
	return out, true, nil
}

func (s dataConfigStore) Upsert(table, id string, values map[string]any) error {
	rec := data.Record{}
	for k, v := range values {
		rec[k] = v
	}
	return s.db.Table(table).Upsert(id, rec)
}

func (s dataConfigStore) Delete(table, id string) error {
	err := s.db.Table(table).Delete(id)
	if err == data.ErrNotFound {
		return nil
	}
	return err
}
