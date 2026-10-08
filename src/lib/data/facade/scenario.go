// 本文件是 scenario 域（场景 = **独立根 `scenarios/`**（与 capability/ 平级）
// `/<场景目录>/` 的场景元素：列举 / 定位 / 保存 / 删除）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：场景 = 级别场景根（app / user / project）下 `<场景目录>/` 目录，内含
// `scenario.json`（名称 / 描述 / 时间 + **子 agent 引用列表**）+ `main.agent.md`（主 agent，固定文件名）。
// 这些**都不出门面**：
//   - 场景 = 领域对象（id / 名称 / 描述 / 级别 / agent 列表 / 时间），不是
//     "目录 + JSON + 若干 md 文件"；
//   - agent = 领域字段（名称 / 描述 / 角色标签 / 是否主 agent / 提示词 / 工具白名单 / LLM 引用
//     / 委派条件），不是".agent.md 分区文本"；**子 agent 唯一形态 = 引用（Ref）**，主 agent 内联；
//   - 系统提示词由 `llm/server` 侧按 [25-MCP与场景分层模型 §3] 三层（全局 / 场景 / agent）
//     **自行拼接**（场景层 = `description` + 团队成员段；主 agent 的 prompt 归 agent 层）；
//   - 级别（app / user / project）是**领域归属**（**三级均可写**；app 级自 2026-09-26 起可编辑，
//     出厂内容由 embed 提供、app 初始化时缺失即物化），门面只承载级别语义，路径解析在实现侧（23 §7：
//     门面不交路径规则）。
//
// agent 的扩展字段（工具白名单 / LLM 引用 / 委派条件）为**可选**（缺省 = 不设白名单 /
// 继承默认 LLM / 无委派条件），故用领域字段承载、空值不输出。
package facade

// ScenarioAgent 是场景内的一个 agent（领域字段；`IsMain` 标识主 agent）。
type ScenarioAgent struct {
	// Name agent 名称。
	Name string `json:"name"`
	// Description agent 描述。
	Description string `json:"description,omitempty"`
	// RoleTag 角色标签（UI 展示用）。
	RoleTag string `json:"roleTag,omitempty"`
	// IsMain 是否主 agent（主 agent 恒列首位；其 prompt 归 **agent 层**，25 §3）。
	IsMain bool `json:"isMain"`
	// Prompt agent 提示词（主 agent 的提示词 = **agent 层**，25 §3；非场景层提示词）。
	Prompt string `json:"prompt,omitempty"`
	// Tools 工具白名单（可选；空 = 不设白名单）。
	Tools string `json:"tools,omitempty"`
	// LLMRef LLM 引用（可选；空 = 继承默认 LLM）。
	LLMRef string `json:"llmRef,omitempty"`
	// DelegateCond 委派条件（可选）。
	DelegateCond string `json:"delegateCond,omitempty"`
	// Ref 引用路径（可选；**非主 agent** 的 agent 以引用形式落盘于 scenario.json.agents，
	// 形如 `${exeDir}/capability/agents/<名>.agent.md`；主 agent 内联 → Ref 空。P4，2026-10-01）。
	Ref string `json:"ref,omitempty"`
}

// Scenario 是场景元素（领域字段）。
type Scenario struct {
	// ID 场景 id（= 场景目录名；调用方指定）。
	ID string `json:"id"`
	// Name 场景名称（空 = 取 ID）。
	Name string `json:"name,omitempty"`
	// Description 场景描述。
	Description string `json:"description,omitempty"`
	// Level 场景级别（app / user / project；列出时由实现侧给出，保存时 = 写入目标级别）。
	Level string `json:"level,omitempty"`
	// Agents agent 列表（主 agent 恒列首位）。
	Agents []ScenarioAgent `json:"agents"`
	// CreatedAt 创建时间（RFC3339；可选）。
	CreatedAt string `json:"createdAt,omitempty"`
	// UpdatedAt 更新时间（RFC3339；可选）。
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// ScenarioListRequest 是场景列举入参（合并三级根；首次由实现侧物化出厂默认场景）。
type ScenarioListRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope：供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// ScenarioListResponse 是场景列举出参（场景 id 全局唯一，跨级不重名）。
type ScenarioListResponse struct {
	// List 场景列表（app → user → project 顺序）。
	List []Scenario `json:"list"`
}

// ScenarioGetRequest 是场景定位入参。
type ScenarioGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// ScenarioID 场景 id（必填）。
	ScenarioID string `json:"scenario_id"`
	// Level 限定级别（空 = 具体级优先：project → user → app）。
	Level string `json:"level,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// ScenarioGetResponse 是场景定位出参。
type ScenarioGetResponse struct {
	// Scenario 场景内容。
	Scenario Scenario `json:"scenario"`
}

// ScenarioSaveRequest 是场景保存入参（按 ID 落目录；级别可为 app / user / project）。
// ⚠️ id 全局唯一（跨级亦然）：id 已存在于**其它**级别 → 实现侧拒绝（无覆盖语义）。
type ScenarioSaveRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Scenario 场景内容（ID 必填）。
	Scenario Scenario `json:"scenario"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// ScenarioSaveResponse 是场景保存出参。
type ScenarioSaveResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// ID 场景 id。
	ID string `json:"id"`
}

// ScenarioDeleteRequest 是场景删除入参（app / user / project 三级均可删）。
type ScenarioDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// ScenarioID 场景 id（必填）。
	ScenarioID string `json:"scenario_id"`
	// Level 指定级别（空 = 具体级优先 project → user → app 查找实际存在的副本）。
	Level string `json:"level,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// ScenarioDeleteResponse 是场景删除出参。
type ScenarioDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}
