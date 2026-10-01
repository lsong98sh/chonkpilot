// config 域门面实现（`chonkpilot-data/facade` 的 ConfigAPI）：配置 kv 面 + 用户配置。
//
// 位置（23-工程与部署拓扑 §7「门面 = 翻译层」）：本文件是**实现侧**——把「域名 + 领域键」
// 翻译成 config 表键前缀 / 专用表 / 分层读写规则，再落到表访问与视图 helper。
//
// 阶段 4「internal 下沉」：本文件由 `chonkpilot-data/persist` 整体下移至
// `chonkpilot-data/internal/config`（逻辑逐字未改；共享内核改用 internal/kernel）。
//
// 路径解析：`cfgInstBind` 三层顺序 ① 本进程实例视图（MQ 路径既有语义）② 调用方带入的 Scope
// ③ data 组件绑定表（inline 绑定形态：宿主/插件未持实例视图）。
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// 编译期断言：实现完整 config 域门面（缺方法即编译不过）。
var _ facade.ConfigAPI = (*Service)(nil)

// configKVFields 门面 kv 域 → config 表**键前缀**（存储事实，不出门面：调用方只报域名）。
// 与 61-消息一览 §3.1 的域划分一一对应（prj-config 无前缀 / prompt / prj-security）。
var configKVFields = map[string]string{
	facade.DomainPrjConfig:   "",
	facade.DomainPrompt:      "prompt-",
	facade.DomainPrjSecurity: "security-",
}

// openKVDomain 打开某 kv 域所需两层：prj-config 需要 prjusr（个人运行态键），其余域仅 prj。
// prj-config 另做配置键改名一次性迁移（D-04）：prj（团队预设）与 prjusr（本机运行态）两侧都迁。
// prjusr 不可用（临时形态/解析失败）→ 退化为**纯 prj 行为**（与既有 handleConfigKV 口径一致）。
func (s *Service) openKVDomain(domain, instanceID string, scope facade.Scope) (prj, pudb *data.DB, release func(), err error) {
	workDir, dataDir, err := s.CfgInstBind(instanceID, scope)
	if err != nil {
		return nil, nil, nil, err
	}
	prj, relPrj, err := kernel.OpenPrjLayer(workDir, dataDir)
	if err != nil {
		return nil, nil, nil, err
	}
	if domain != facade.DomainPrjConfig {
		return prj, nil, relPrj, nil // 其余 kv 域不使用 prjusr
	}
	s.MigrateConfigKeyRenames(prj)
	pudb, relPu, err := kernel.OpenPrjUsrLayer(dataDir, prj)
	if err != nil {
		return prj, nil, relPrj, nil // prjusr 不可用 → 纯 prj 行为
	}
	s.MigrateConfigKeyRenames(pudb)
	return prj, pudb, func() { relPu(); relPrj() }, nil
}

// kvPrefixOf 取域的存储前缀（未知域 → 报错，避免静默落错位置）。
func kvPrefixOf(domain string) (string, error) {
	prefix, ok := configKVFields[domain]
	if !ok {
		return "", fmt.Errorf("persist: unknown config kv domain %q", domain)
	}
	return prefix, nil
}

// ── 同族 kv 域（prj-config / prompt / prj-security）──────────────

// ConfigKVList 读同族 kv 域全表（prj-config = prj + prjusr 合并，prjusr 覆盖 prj；
// prompt / prj-security = prj 单层；命中前缀的键剥前缀）。
func (s *Service) ConfigKVList(req facade.ConfigKVListRequest) (facade.ConfigKVListResponse, error) {
	prefix, err := kvPrefixOf(req.Domain)
	if err != nil {
		return facade.ConfigKVListResponse{}, err
	}
	prj, pudb, release, err := s.openKVDomain(req.Domain, req.InstanceID, req.Scope)
	if err != nil {
		return facade.ConfigKVListResponse{}, err
	}
	defer release()
	out := configKVList(prj, prefix)
	if pudb != nil {
		for k, v := range configKVList(pudb, prefix) {
			out[k] = v // prjusr（本机）优先覆盖 prj（团队预设）
		}
	}
	return facade.ConfigKVListResponse{List: out}, nil
}

// ConfigKVGet 批量按键读（读序 prjusr → prj → usr，仅 prj-config 无前缀域 + 非个人运行态键
// 参与 usr 兜底；prompt- / security- 前缀域不参与）。未配置的键 → 空串（与 load 应答同口径）。
func (s *Service) ConfigKVGet(req facade.ConfigKVGetRequest) (facade.ConfigKVGetResponse, error) {
	prefix, err := kvPrefixOf(req.Domain)
	if err != nil {
		return facade.ConfigKVGetResponse{}, err
	}
	out := make(map[string]string, len(req.Keys))
	// prompt 域的文件化键（summary_prompt）：走三级文件 + 旧键 + 内置默认回落链，且**先于**
	// 数据根解析（实例未登记也要能读 → workDir ""，退化为系统级/内置默认，原口径不变）。
	rest := req.Keys
	if req.Domain == facade.DomainPrompt {
		rest = make([]string, 0, len(req.Keys))
		for _, key := range req.Keys {
			if key == summaryPromptKey {
				out[key] = s.readSummaryPrompt(s.workDirOf(req.InstanceID, req.Scope), req.InstanceID)
				continue
			}
			rest = append(rest, key)
		}
	}
	if len(rest) == 0 {
		return facade.ConfigKVGetResponse{Values: out}, nil
	}
	prj, pudb, release, err := s.openKVDomain(req.Domain, req.InstanceID, req.Scope)
	if err != nil {
		return facade.ConfigKVGetResponse{}, err
	}
	defer release()
	// usr 兜底层惰性打开（仅 prj-config 的无前缀键可能用到；一次调用内复用）。
	var udb *data.DB
	var urel func()
	defer func() {
		if urel != nil {
			urel()
		}
	}()
	for _, key := range rest {
		v := ""
		if pudb != nil && isLocalRuntimeKey(key) {
			v = kernel.ConfigGet(pudb, prefix+key) // prjusr（个人运行态）优先
		}
		if v == "" {
			v = kernel.ConfigGet(prj, prefix+key) // prj（团队预设）
		}
		if v == "" && prefix == "" && !isLocalRuntimeKey(key) && udb == nil {
			udb, urel, _ = s.UsrDB()
		}
		if v == "" && udb != nil {
			v = kernel.ConfigGet(udb, key) // usr（用户全局兜底）
		}
		out[key] = v
	}
	return facade.ConfigKVGetResponse{Values: out}, nil
}

// ConfigKVSet 批量写：prj-config 的个人运行态键落 prjusr（不污染团队共享库），其余落 prj；
// prompt 域的文件化键（summary_prompt）写项目级文件（内容 = 继承值 → 删项目级文件保持继承）。
// 写入成功后**整批只广播 1 条** data-<domain>-refresh（载荷带 `ids` 全组键 + `id` = 首键，
// 61 §3.1）——N 键不再 N 条；单键写仍为 1 条且载荷与改前一致（不写 `ids`）。
func (s *Service) ConfigKVSet(req facade.ConfigKVSetRequest) (facade.ConfigKVSetResponse, error) {
	prefix, err := kvPrefixOf(req.Domain)
	if err != nil {
		return facade.ConfigKVSetResponse{}, err
	}
	if len(req.Entries) == 0 {
		return facade.ConfigKVSetResponse{}, errors.New("persist: config entries required")
	}
	keys := make([]string, 0, len(req.Entries))
	for k := range req.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 写入/广播顺序稳定（可断言、可复现）
	// 文件化键先行（不依赖数据根解析；实例未登记时按 workDir "" 处置，原口径不变）。
	rest := keys
	if req.Domain == facade.DomainPrompt {
		rest = make([]string, 0, len(keys))
		for _, key := range keys {
			if key == summaryPromptKey {
				workDir := s.workDirOf(req.InstanceID, req.Scope)
				if err := s.writeSummaryPrompt(workDir, req.InstanceID, req.Entries[key]); err != nil {
					return facade.ConfigKVSetResponse{}, err
				}
				continue
			}
			rest = append(rest, key)
		}
	}
	if len(rest) > 0 {
		prj, pudb, release, err := s.openKVDomain(req.Domain, req.InstanceID, req.Scope)
		if err != nil {
			return facade.ConfigKVSetResponse{}, err
		}
		defer release()
		for _, key := range rest {
			target := prj
			if pudb != nil && isLocalRuntimeKey(key) {
				target = pudb // 个人运行态只写 prjusr，不污染团队共享库
			}
			if err := kernel.ConfigSet(target, prefix+key, req.Entries[key]); err != nil {
				return facade.ConfigKVSetResponse{}, err
			}
		}
	}
	// 整批一次广播（keys 已排序 → 广播顺序/`id`（首键）稳定可断言）：N 键 = 1 条刷新。
	s.RefreshScopedKeys(req.Domain, req.InstanceID, keys, "save", req.Scope)
	return facade.ConfigKVSetResponse{OK: true}, nil
}

// ConfigKVDelete 批量删（prj 与 prjusr 两层同删 = 重置即恢复继承；文件化键删项目级文件）。
// 逐键后广播 data-<domain>-refresh（与 MQ 路径同粒度：一次删一条）。
func (s *Service) ConfigKVDelete(req facade.ConfigKVDeleteRequest) (facade.ConfigKVDeleteResponse, error) {
	prefix, err := kvPrefixOf(req.Domain)
	if err != nil {
		return facade.ConfigKVDeleteResponse{}, err
	}
	keys := append([]string{}, req.Keys...)
	sort.Strings(keys)
	rest := keys
	if req.Domain == facade.DomainPrompt {
		rest = make([]string, 0, len(keys))
		for _, key := range keys {
			if key == summaryPromptKey {
				if p := summaryPromptFile(s.workDirOf(req.InstanceID, req.Scope)); p != "" {
					if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
						return facade.ConfigKVDeleteResponse{}, err
					}
				}
				continue
			}
			rest = append(rest, key)
		}
	}
	if len(rest) > 0 {
		prj, pudb, release, err := s.openKVDomain(req.Domain, req.InstanceID, req.Scope)
		if err != nil {
			return facade.ConfigKVDeleteResponse{}, err
		}
		defer release()
		for _, key := range rest {
			kernel.ConfigDelete(prj, prefix+key)
			if pudb != nil {
				kernel.ConfigDelete(pudb, prefix+key) // 两层同删：重置 = 恢复继承
			}
		}
	}
	for _, key := range keys {
		s.RefreshScoped(req.Domain, req.InstanceID, key, "delete", req.Scope)
	}
	return facade.ConfigKVDeleteResponse{OK: true}, nil
}

// legacyConfigValue 读实例 prj 层某 config 键的原始值（**兼容旧数据**用：如 summary_prompt
// 的旧 prj config key）；实例不可解析 / 库打不开 → ""。句柄用完即释（不占长驻连接缓存）。
func (s *Service) legacyConfigValue(instanceID, key string) string {
	workDir, dataDir, err := s.CfgInstBind(instanceID, facade.Scope{})
	if err != nil {
		return ""
	}
	prj, rel, err := kernel.OpenPrjLayer(workDir, dataDir)
	if err != nil {
		return ""
	}
	defer rel()
	return kernel.ConfigGet(prj, key)
}

// workDirOf 取实例工作目录（解析失败 → 空串：prompt 文件化键退化为系统级/默认）。
func (s *Service) workDirOf(instanceID string, scope facade.Scope) string {
	workDir, _, err := s.CfgInstBind(instanceID, scope)
	if err != nil {
		return ""
	}
	return workDir
}

// ── 用户配置（user-config 域）────────────────────────────────

// UserConfigView 读 usr 主库视图（有任一配置 → 单条整体对象；否则空列表）。
// 与 UserConfigGet 的差别：视图 = **usr 主库**（"usr 是否已有配置"语义，前端导出/备份数据源），
// Get = 叠加项目层覆盖后的**有效值**。
func (s *Service) UserConfigView(_ facade.UserConfigViewRequest) (facade.UserConfigViewResponse, error) {
	db, release, err := s.UsrDB()
	if err != nil {
		return facade.UserConfigViewResponse{}, err
	}
	defer release()
	s.MigrateLegacyUserConfig(db)
	s.MigrateConfigKeyRenames(db)
	return facade.UserConfigViewResponse{List: s.userConfigList(db)}, nil
}

// UserConfigGet 读合并后有效值（usr 基线 + 项目层可继承键 defaultLLM/defaultScenario 覆盖；
// 缺失键补系统默认）。实例不可解析（无 instance_id / 未登记）→ 仅 usr 基线。
func (s *Service) UserConfigGet(req facade.UserConfigGetRequest) (facade.UserConfigGetResponse, error) {
	db, release, err := s.UsrDB()
	if err != nil {
		return facade.UserConfigGetResponse{}, err
	}
	defer release()
	s.MigrateLegacyUserConfig(db)
	s.MigrateConfigKeyRenames(db)
	view := readUserConfig(db)
	return facade.UserConfigGetResponse{Config: s.applyProjectOverrides(view, req.InstanceID, req.Scope)}, nil
}

// UserConfigSet 增量写（载荷里出现的键才写；集合键整体替换专用表）。
func (s *Service) UserConfigSet(req facade.UserConfigSetRequest) (facade.UserConfigSetResponse, error) {
	db, release, err := s.UsrDB()
	if err != nil {
		return facade.UserConfigSetResponse{}, err
	}
	defer release()
	s.MigrateLegacyUserConfig(db)
	s.MigrateConfigKeyRenames(db)
	if err := saveUserConfig(db, req.Entries); err != nil {
		return facade.UserConfigSetResponse{}, err
	}
	s.Refresh("user-config", req.InstanceID, LegacyUserConfigKey, "save")
	// 跨窗口即时同步（61 §3.1 · 24 §6.5 · WIN-021）：写入成功后广播**不带 instance_id** 的
	// data-user-config-changed（所有窗口收到）→ theme/locale 即时跟随。
	s.UserConfigChanged(changedUserConfig(readUserConfig(db), savedUserConfigKeys(req.Entries)))
	return facade.UserConfigSetResponse{OK: true, ID: LegacyUserConfigKey}, nil
}

// UserConfigDelete 删用户配置：Keys 空（或显式 legacy 整块键）→ 清空整份（回落默认/继承）；
// 逐键 → 删该键（未知键**明确报错**，禁止兜底清空整份 —— P0：未知键曾把 theme/locale/llms
// /超时一并清掉）。
func (s *Service) UserConfigDelete(req facade.UserConfigDeleteRequest) (facade.UserConfigDeleteResponse, error) {
	db, release, err := s.UsrDB()
	if err != nil {
		return facade.UserConfigDeleteResponse{}, err
	}
	defer release()
	s.MigrateLegacyUserConfig(db)
	s.MigrateConfigKeyRenames(db)
	changed := make([]string, 0, len(req.Keys))
	for _, key := range req.Keys {
		if newKey, renamed := configKeyRenames[key]; renamed {
			key = newKey
		}
		if key == LegacyUserConfigKey {
			continue // 显式 legacy 整块键 → 按"清空整份"处置（原行为，见下）
		}
		if _, known := userConfigKeyKinds[key]; known {
			if err := data.DeleteConfig(db, key); err != nil {
				return facade.UserConfigDeleteResponse{}, err
			}
			s.Refresh("user-config", req.InstanceID, key, "delete")
			changed = append(changed, key)
			continue
		}
		if userConfigFreeKeys[key] {
			if err := data.DeleteConfig(db, key); err != nil {
				return facade.UserConfigDeleteResponse{}, err
			}
			s.Refresh("user-config", req.InstanceID, key, "delete")
			changed = append(changed, key)
			continue
		}
		if table, isCollection := collectionKeys[key]; isCollection {
			if err := clearCollection(db, table); err != nil {
				return facade.UserConfigDeleteResponse{}, err
			}
			s.Refresh("user-config", req.InstanceID, key, "delete")
			changed = append(changed, key)
			continue
		}
		// 未知键：只报错，绝不回落「清空整份用户配置」。
		return facade.UserConfigDeleteResponse{}, fmt.Errorf("unknown config key: %s", key)
	}
	clearAll := len(req.Keys) == 0 || onlyLegacyKeys(req.Keys)
	if clearAll {
		if err := deleteUserConfig(db); err != nil {
			return facade.UserConfigDeleteResponse{}, err
		}
		s.Refresh("user-config", req.InstanceID, LegacyUserConfigKey, "delete")
		changed = allUserConfigKeys() // 清空整份 = 全部 usr 配置键都变
	}
	// 跨窗口即时同步（61 §3.1）：删除成功后同样广播（清空/重置也须让其它窗口即时跟随，
	// 幂等）；载荷取**删除后**的有效值（已回落系统默认）。
	s.UserConfigChanged(changedUserConfig(readUserConfig(db), changed))
	return facade.UserConfigDeleteResponse{OK: true}, nil
}

// onlyLegacyKeys 判定删除请求是否只含 legacy 整块键（等价于"无 key"= 清空整份）。
func onlyLegacyKeys(keys []string) bool {
	for _, k := range keys {
		if k != LegacyUserConfigKey {
			return false
		}
	}
	return len(keys) > 0
}

// ── usr 配置下行广播载荷（data-user-config-changed；61 §3.1）──────

// savedUserConfigKeys 取 save 载荷里**实际落库**的 usr 配置键（集合键 / 标量键 / 自由键；
// 其余键被 saveUserConfig 静默丢弃 → 不列入）。顺序稳定（可断言）。
func savedUserConfigKeys(entries map[string]any) []string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		if k == "id" {
			continue
		}
		if _, ok := collectionKeys[k]; ok {
			keys = append(keys, k)
			continue
		}
		if _, ok := userConfigKeyKinds[k]; ok {
			keys = append(keys, k)
			continue
		}
		if userConfigFreeKeys[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// allUserConfigKeys 全部 usr 配置键（清空整份时全部变更）。
func allUserConfigKeys() []string {
	keys := make([]string, 0, len(userConfigKeyKinds)+len(userConfigFreeKeys)+len(collectionKeys))
	for k := range userConfigKeyKinds {
		keys = append(keys, k)
	}
	for k := range userConfigFreeKeys {
		keys = append(keys, k)
	}
	for k := range collectionKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// changedUserConfig 取受影响键在**变更后的有效值**（view = readUserConfig 组装：标量带系统
// 默认、集合为最新数组、自由键存在才带；重键天然去重）→ 即 61 §3.1 的 payload `{data:{…}}`
// （**与 save 同形**，前端复用同一应用逻辑）。键不在 view（如刚删的自由键）→ 带空串占位。
func changedUserConfig(view map[string]any, keys []string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := view[k]; ok {
			out[k] = v
			continue
		}
		out[k] = ""
	}
	return out
}

// ── config 表通用读写（记录形态 {"v": ...}）──────────

// configKVList 返回域配置平铺 map（prefix 非空仅含该前缀条目且 key 剥前缀；value = v 字符串）。
func configKVList(db *data.DB, prefix string) map[string]string {
	out := map[string]string{}
	keys, _ := db.Table("config").ListKeys()
	for _, k := range keys {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		var rec data.Record
		if ok, _ := db.Table("config").Get(k, &rec); ok {
			out[strings.TrimPrefix(k, prefix)] = kernel.Sval(rec["v"])
		}
	}
	return out
}
