// configdb.go —— 插件配置落库（PLUGIN-1）。
//
// 权威存储由宿主经 ConfigStore 端口注入（主仓实现 = usr 层 `chonkpilot.db` 的两个桶，
// bbolt，与用户配置同库不同表）：
//
//	plugin_config  非密配置：key = <plugin-id>，value = 字段表
//	plugin_secret  密配置：  key = <plugin-id>，value = 字段表（页面只回掩码）
//
// `<dataDir>/plugins/<id>/config.json|secrets.json` 降级为**运行时快照**（插件进程读取），
// 由宿主在 spawn / 保存配置时从库里物化，不再是权威源。
package mgr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

const (
	pluginConfigTable = "plugin_config"
	pluginSecretTable = "plugin_secret"

	// storeKeyField 是存储层保留的键列（读侧剔除，不进入插件配置）。
	storeKeyField = "_key"
)

// readConfigRow 读一条插件配置（行不存在 → 空 map）。
func readConfigRow(store ConfigStore, table, id string) (map[string]any, error) {
	out := map[string]any{}
	if store == nil {
		return out, nil
	}
	rec, ok, err := store.Get(table, id)
	if err != nil {
		return out, fmt.Errorf("读取 %s(%s): %w", table, id, err)
	}
	if !ok {
		return out, nil
	}
	for k, v := range rec {
		switch k {
		case storeKeyField, "updated_at":
			continue
		}
		out[k] = v
	}
	return out, nil
}

// writeConfigRow 覆盖写一条插件配置（upsert；空 map 也保留行，表示「显式空」）。
func writeConfigRow(store ConfigStore, table, id string, values map[string]any) error {
	if store == nil {
		return nil
	}
	rec := map[string]any{}
	for k, v := range values {
		rec[k] = v
	}
	if err := store.Upsert(table, id, rec); err != nil {
		return fmt.Errorf("写入 %s(%s): %w", table, id, err)
	}
	return nil
}

// deleteConfigRow 删除一条插件配置（不存在视为成功）。
func deleteConfigRow(store ConfigStore, table, id string) error {
	if store == nil {
		return nil
	}
	return store.Delete(table, id)
}

// materializeConfig 把库里的配置物化成插件可读的运行时快照文件。
//
// 只在有变化时写（内容比较），避免每次 spawn 都刷盘；文件权限：config 0644 / secrets 0600。
func (m *Manager) materializeConfig(id string) error {
	config, secrets, err := m.readConfig(id)
	if err != nil {
		return err
	}
	dir := m.pluginDataDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSONAtomicIfChanged(filepath.Join(dir, "config.json"), config, 0o644); err != nil {
		return err
	}
	return writeJSONAtomicIfChanged(filepath.Join(dir, "secrets.json"), secrets, 0o600)
}

// readConfig 读权威配置（端口）；端口不可用/空行时回落运行时快照，并在首次命中时回灌入库。
func (m *Manager) readConfig(id string) (map[string]any, map[string]any, error) {
	config, secrets := map[string]any{}, map[string]any{}
	if m.cfgStore != nil {
		var err error
		config, err = readConfigRow(m.cfgStore, pluginConfigTable, id)
		if err != nil {
			return nil, nil, err
		}
		secrets, err = readConfigRow(m.cfgStore, pluginSecretTable, id)
		if err != nil {
			return nil, nil, err
		}
		if len(config) > 0 || len(secrets) > 0 {
			return config, secrets, nil
		}
		// 回灌：历史文件态 → 首次读取时迁移入库（一次性）。
		dir := m.pluginDataDir(id)
		mergeFile(config, filepath.Join(dir, "config.json"))
		mergeFile(secrets, filepath.Join(dir, "secrets.json"))
		if len(config) > 0 || len(secrets) > 0 {
			_ = writeConfigRow(m.cfgStore, pluginConfigTable, id, config)
			_ = writeConfigRow(m.cfgStore, pluginSecretTable, id, secrets)
			m.logf("plugin-config migrated id=%s (config=%d secret=%d)", id, len(config), len(secrets))
		}
		return config, secrets, nil
	}
	dir := m.pluginDataDir(id)
	mergeFile(config, filepath.Join(dir, "config.json"))
	mergeFile(secrets, filepath.Join(dir, "secrets.json"))
	return config, secrets, nil
}

// writeJSONAtomicIfChanged 内容等价则跳过写盘（快照物化用）。
func writeJSONAtomicIfChanged(path string, v any, perm os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil {
		var cur any
		if json.Unmarshal(old, &cur) == nil && reflect.DeepEqual(cur, v) {
			return nil
		}
	}
	return writeJSONAtomic(path, v, perm)
}
