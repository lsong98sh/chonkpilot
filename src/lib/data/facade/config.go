// 本文件是 config 域（配置 kv 面 + 用户配置）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：config 表一条记录 = `{"v": "<字符串>"}`（一个决策一个 key，键上带
// 域前缀 `prompt-` / `security-`）；用户配置 = 逐 key 标量 + llms/mcps 两张专用表
// （主键 = 数组序号）+ 迁移期的 legacy 整块 `user_config`。这些**都不出门面**：
//   - 值形态 = 领域字符串（配置项取值），不是 `{"v": …}` 记录；
//   - 域前缀是存储隔离手段，调用方只报**域名**（prj-config / prompt / prj-security）；
//   - 用户配置 = 领域键对象（theme / locale / llms / mcpServers / defaultLLM / …），
//     不是「专用表 + 序号主键 + 自由键」。
//
// 于是换存储（分表 / 换前缀 / 换库）不会把改动漏到每个调用方。
//
// 字段覆盖 = 调用方的**全部合法需求**：list / 批量按键读 / 批量写 / 批量删，各域齐备
// （41 G-32 实证：覆盖不全 = 逼调用方绕路直读库）。
package facade

// 配置 kv 域标识（域 = 调用方视角的逻辑分区；存储前缀在实现侧，不出门面）。
const (
	// DomainPrjConfig 项目配置域（团队共享配置 + 本机个人运行态两层）。
	DomainPrjConfig = "prj-config"
	// DomainPrompt 提示词域（配置表键 + 文件化 capability 提示词，见实现侧）。
	DomainPrompt = "prompt"
	// DomainPrjSecurity 项目安全域（信任目录等，agentbox 允许目录来源）。
	DomainPrjSecurity = "prj-security"
)

// ── 同族 kv 域（prj-config / prompt / prj-security）──────────────

// ConfigKVListRequest 是 kv 域全表读入参。
type ConfigKVListRequest struct {
	// Domain 域（DomainPrjConfig / DomainPrompt / DomainPrjSecurity）。
	Domain string `json:"domain"`
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope：供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// ConfigKVListResponse 是 kv 域全表读出参（平铺 键 → 值字符串；已剥域前缀）。
type ConfigKVListResponse struct {
	// List 全表（键 → 值；prj-config 为 prj + prjusr 合并、prjusr 覆盖）。
	List map[string]string `json:"list"`
}

// ConfigKVGetRequest 是 kv 域按键读入参（**批量**：单次调用粒度按最差绑定 http 设计，见 23 §7）。
type ConfigKVGetRequest struct {
	// Domain 域。
	Domain string `json:"domain"`
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Keys 要读的键（域内键名，不含存储前缀）。
	Keys []string `json:"keys"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// ConfigKVGetResponse 是 kv 域按键读出参。
type ConfigKVGetResponse struct {
	// Values 键 → 值（未配置的键 → 空串，与既有 load 应答同口径：不区分"未配置"与"显式空"）。
	Values map[string]string `json:"values"`
}

// ConfigKVSetRequest 是 kv 域批量写入参。
type ConfigKVSetRequest struct {
	// Domain 域。
	Domain string `json:"domain"`
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Entries 要写的键 → 值（值形态 = 领域字符串）。
	Entries map[string]string `json:"entries"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// ConfigKVSetResponse 是 kv 域批量写出参。
type ConfigKVSetResponse struct {
	// OK 写入是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// ConfigKVDeleteRequest 是 kv 域批量删除入参。
type ConfigKVDeleteRequest struct {
	// Domain 域。
	Domain string `json:"domain"`
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Keys 要删的键（按域规则在相应层删除；不存在视为成功 = 恢复继承）。
	Keys []string `json:"keys"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// ConfigKVDeleteResponse 是 kv 域批量删除出参。
type ConfigKVDeleteResponse struct {
	// OK 删除是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// ── 用户配置（user-config 域）─────────────────────────────────────

// UserConfigViewRequest 是 usr 主库视图读入参（"usr 是否已有配置"语义；不叠加项目层）。
type UserConfigViewRequest struct{}

// UserConfigViewResponse 是 usr 主库视图读出参（有任一配置 → 单条整体对象；否则空列表）。
type UserConfigViewResponse struct {
	// List 视图列表（0 或 1 条；元素 = 领域键对象 + id）。
	List []map[string]any `json:"list"`
}

// UserConfigMCPsRequest 是 usr `mcps` 专用表原文读入参。
type UserConfigMCPsRequest struct{}

// UserConfigMCPsResponse 是 usr `mcps` 条目读出参（**只读、不触发迁移**；条目顺序 = 主键序号序）。
type UserConfigMCPsResponse struct {
	// List 条目数组（mcps 记录原文；未配置 → 空数组）。
	List []map[string]any `json:"list"`
}

// UserConfigGetRequest 是用户配置（合并后有效值）读入参。
type UserConfigGetRequest struct {
	// InstanceID 实例 id（用于叠加项目层可继承键；空/不可解析 → 仅 usr 基线）。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// UserConfigGetResponse 是用户配置（合并后有效值）读出参。
type UserConfigGetResponse struct {
	// Config 领域键对象（usr 基线 + 项目层可继承键覆盖；缺失键已补系统默认）。
	Config map[string]any `json:"config"`
}

// UserConfigSetRequest 是用户配置增量写入参。
type UserConfigSetRequest struct {
	// Entries 要写的领域键 → 值（载荷里出现的键才写；集合键整体替换）。
	Entries map[string]any `json:"entries"`
	// InstanceID 实例 id（仅用于变更广播的归属字段，61 §0：payload 必带 instance_id）。
	InstanceID string `json:"instance_id,omitempty"`
}

// UserConfigSetResponse 是用户配置增量写出参。
type UserConfigSetResponse struct {
	// OK 写入是否成功（失败走 error）。
	OK bool `json:"ok"`
	// ID 域内对象 id（无 key 的整块写入；对齐既有应答的 id 字段）。
	ID string `json:"id,omitempty"`
}

// UserConfigDeleteRequest 是用户配置删除入参。
type UserConfigDeleteRequest struct {
	// Keys 要删的键（空 = 清空整份用户配置，回落默认/继承）。
	Keys []string `json:"keys,omitempty"`
	// InstanceID 实例 id（仅用于变更广播的归属字段）。
	InstanceID string `json:"instance_id,omitempty"`
}

// UserConfigDeleteResponse 是用户配置删除出参。
type UserConfigDeleteResponse struct {
	// OK 删除是否成功（失败走 error）。
	OK bool `json:"ok"`
}
