// pluginconfigstore.go —— 插件配置权威存储的宿主适配器（ConfigStore 端口实现）。
//
// 框架 `host/mgr` 只声明端口；主仓在此把 usr 层 `chonkpilot.db` 的两个桶接上：
//
//	plugin_config  非密配置（key = 插件 id）
//	plugin_secret  密配置（key = 插件 id）
package host

import (
	"github.com/chonkpilot/chonkpilot-data"
	pluginmgr "github.com/lsong98sh/chonkpilot-plugin-framework/mgr"
)

// dataConfigStore 把 usr 层共享库适配为插件配置端口。
//
// **每次操作短开**（D-45）——插件配置读写是低频操作（启动 / 配置页），而插件平台生命周期
// 很长：若常持该库，同一用户开第二个实例时其 usr 库被本进程排他锁住（实测第二实例
// `data-user-config-load` / `execConfig` 全部 `timeout`，前端永不就绪 → 多实例不可用）。
type dataConfigStore struct{}

func (s dataConfigStore) withDB(fn func(db *data.DB) error) error {
	db, release, err := data.OpenSharedLayer(data.UserPath(), data.LayerUsr)
	if err != nil {
		return err
	}
	defer release()
	return fn(db)
}

// openPluginConfigStore 探活 usr 层配置库（失败 → 返回 error，由框架降级为文件态）。
// 探活成功**不持有连接**：连接由各操作短开。
func openPluginConfigStore() (pluginmgr.ConfigStore, func(), error) {
	db, release, err := data.OpenSharedLayer(data.UserPath(), data.LayerUsr)
	if err != nil {
		return nil, nil, err
	}
	_ = db
	release()
	return dataConfigStore{}, nil, nil
}

func (s dataConfigStore) Get(table, id string) (map[string]any, bool, error) {
	var out map[string]any
	var found bool
	err := s.withDB(func(db *data.DB) error {
		rec := data.Record{}
		ok, gerr := db.Table(table).Get(id, &rec)
		if gerr != nil {
			return gerr
		}
		found = ok
		if !ok {
			return nil
		}
		out = map[string]any{}
		for k, v := range rec {
			if k == data.KeyField || k == "updated_at" {
				continue
			}
			out[k] = v
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, found, nil
}

func (s dataConfigStore) Upsert(table, id string, values map[string]any) error {
	return s.withDB(func(db *data.DB) error {
		rec := data.Record{}
		for k, v := range values {
			rec[k] = v
		}
		return db.Table(table).Upsert(id, rec)
	})
}

func (s dataConfigStore) Delete(table, id string) error {
	return s.withDB(func(db *data.DB) error {
		err := db.Table(table).Delete(id)
		if err == data.ErrNotFound {
			return nil
		}
		return err
	})
}
