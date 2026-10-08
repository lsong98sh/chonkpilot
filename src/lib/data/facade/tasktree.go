// 本文件是 tasktree 域（任务树：节点列表 / 任务快照 / 幂等落库 / 关闭）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：prjusr 库 `tasktree` 表一行 = 节点键（`node_id`）+ `node_type` +
// 归属（`top_session` / `session_id` / `parent_node_id`）+ 标题 / 状态 / 时间戳 + 任务层扩展
// 字段（`instance_id` / `workdir` / `state` / `args_digest` / `result_digest` / `exec_json` /
// `started_at` / `done_at` / `closed` / `deleted_at`）。这些**都不出门面**：
//   - 「关闭」= **逻辑删除**（`closed` + `deleted_at` 标记，历史保留）→ 领域上是"节点已关闭"
//     这一状态，不是两列存储标记；
//   - 影子桶（`task_shadow*`，P1 回滚开关）与索引桶（`tasktree_by_parent/_by_top`）是**存储
//     实现**（索引无读方）→ 门面只暴露 `Shadow` 开关语义（回滚用），不暴露桶名；
//   - `exec_json` 是**任务层执行态扩展**的承载列（层写入、层回读）→ 门面按原字符串透传
//     （明细结构由任务层定义，门面不解析）；`awaiting`（待裁决）是**领域派生视图**，
//     由实现侧从执行态解析后按领域形态给出。
//
// 字段覆盖 = 调用方的**全部合法需求**：list（按主会话）/ tasks（按会话）/ upsert（幂等落库）/
// delete（关闭 = 逻辑删除 + 级联子树）。
package facade

// TaskNode 是任务树节点（领域字段；P2 起 = 任务层权威行）。
//
// 时间与状态字段直接来自任务层写入（门面不改写语义）；`Closed` / `DeletedAt` 表示节点已按
// 逻辑删除关闭（list / tasks 默认不出视图，显式 include_closed 时一并返回）。
//
// DSL-3 展示字段（61 §3.4 增补；缺省为空 → 与既有载荷逐字节等价）：容器节点用
// `LoopCurrent`/`LoopTotal`（进度徽标），作业根节点用 `Steps`（步骤执行记录）/`ReturnKind` 两态。
type TaskNode struct {
	// ID 节点 id（任务层 id；= 存储 `node_id` / `task_id`）。
	ID string `json:"id"`
	// NodeType 节点类别（llm → session；其余 → tool；由 Kind 推导，供视图直取）。
	NodeType string `json:"node_type,omitempty"`
	// TopSession 主会话 id（任务树按它分树）。
	TopSession string `json:"top_session,omitempty"`
	// SessionID 所属会话 id。
	SessionID string `json:"session_id,omitempty"`
	// ParentID 父节点 id（空 = 树根）。
	ParentID string `json:"parent_id,omitempty"`
	// Kind 节点种类（llm / tool / …）。
	Kind string `json:"kind,omitempty"`
	// Title 节点标题（= 任务名 / 用途）。
	Title string `json:"title,omitempty"`
	// Status 节点状态（running / done / error / …；层权威值）。
	Status string `json:"status,omitempty"`
	// ToolCallID 关联的 LLM tool-call id（供按 tool_call_id 定位 / 取消）。
	ToolCallID string `json:"tool_call_id,omitempty"`
	// CreatedAt 创建时间（RFC3339）。
	CreatedAt string `json:"created_at,omitempty"`
	// UpdatedAt 更新时间（RFC3339）。
	UpdatedAt string `json:"updated_at,omitempty"`
	// FinishedAt 结束时间（RFC3339；可选）。
	FinishedAt string `json:"finished_at,omitempty"`
	// State 任务层权威状态（= Status 同值口径；可选）。
	State string `json:"state,omitempty"`
	// InstanceID 实例归属（任务层扩展字段；可选）。
	InstanceID string `json:"instance_id,omitempty"`
	// WorkDir 工作目录（任务层扩展字段；可选）。
	WorkDir string `json:"work_dir,omitempty"`
	// ArgsDigest 入参摘要（任务层扩展字段；可选）。
	ArgsDigest string `json:"args_digest,omitempty"`
	// ResultDigest 结果摘要（任务层扩展字段；可选）。
	ResultDigest string `json:"result_digest,omitempty"`
	// ExecJSON 执行态明细（任务层扩展载体；门面透传原字符串，可选）。
	ExecJSON string `json:"exec_json,omitempty"`
	// StartedAt 开始时间（任务层扩展字段；可选）。
	StartedAt string `json:"started_at,omitempty"`
	// DoneAt 完成时间（任务层扩展字段；可选）。
	DoneAt string `json:"done_at,omitempty"`
	// Closed 是否已关闭（逻辑删除标记）。
	// **nil = 该行未带 closed 字段**（与"带 closed=false"是两件不同的事），故用指针保真
	// ——写入侧（落库）与读出侧（视图行）都据此保持"有键/无键"语义。
	Closed *bool `json:"closed,omitempty"`
	// DeletedAt 关闭时间（逻辑删除标记；可选）。
	DeletedAt string `json:"deleted_at,omitempty"`

	// ── DSL-3 展示字段（61 §3.4 增补；缺省为空 → 与既有载荷逐字节等价）──
	// LoopCurrent 容器当前轮次（1 起；kind=dsl_loop/dsl_parallel）。
	LoopCurrent int `json:"loop_current,omitempty"`
	// LoopTotal 容器总轮数（未知 = 0 缺省）。
	LoopTotal int `json:"loop_total,omitempty"`
	// Steps 步骤执行记录（仅 kind=dsl_job 作业根；扁平、不随迭代进树）。
	Steps []DslStep `json:"steps,omitempty"`
	// Shadow 执行记录节点标记（true = 不进树；本实现记录入 Steps，不产出 shadow 节点）。
	Shadow bool `json:"shadow,omitempty"`
	// ReturnKind `$RETURN` 两态（inline / file）。
	ReturnKind string `json:"return_kind,omitempty"`
	// ReturnInline inline 全文（≤64K）。
	ReturnInline string `json:"return_inline,omitempty"`
	// ReturnFile file 文件名（>64K）。
	ReturnFile string `json:"return_file,omitempty"`
	// ReturnSize file 字节数。
	ReturnSize int `json:"return_size,omitempty"`
}

// DslStep 是 DSL 作业的一次 LLM 步骤执行记录（DSL-3：`dsl_job.steps[]` 元素；前端步骤表格行源）。
type DslStep struct {
	// No 跨迭代累计序号（1 起）。
	No int `json:"no"`
	// Status 执行状态（running / done / error / cancelled）。
	Status string `json:"status,omitempty"`
	// Purpose 该步运行目的（展示名）。
	Purpose string `json:"purpose,omitempty"`
	// ElapsedMs 耗时（毫秒）。
	ElapsedMs int64 `json:"elapsed_ms,omitempty"`
	// CreatedAt 开始时刻（RFC3339）。
	CreatedAt string `json:"created_at,omitempty"`
	// SessionID 该次执行的子会话 id（jobSession-N；供前端查看/新窗口）。
	SessionID string `json:"session_id,omitempty"`
	// StatementID 所属静态语句节点 id（可选；当前实现未产出）。
	StatementID string `json:"statement_id,omitempty"`
}

// Task 是任务快照（tasks 视图：会话区展示任务列表用，字段为**展示口径**的领域形态）。
type Task struct {
	// ID 任务 id（= 节点 id）。
	ID string `json:"id"`
	// Status 任务状态（层权威）。
	Status string `json:"status,omitempty"`
	// Kind 任务种类（llm / tool / …）。
	Kind string `json:"kind,omitempty"`
	// SessionID 所属会话 id。
	SessionID string `json:"session_id,omitempty"`
	// TopSession 主会话 id。
	TopSession string `json:"top_session,omitempty"`
	// Name 任务名（= 节点标题）。
	Name string `json:"name,omitempty"`
	// ToolCallID 关联的 LLM tool-call id（可选）。
	ToolCallID string `json:"tool_call_id,omitempty"`
	// State 任务层权威状态（可选）。
	State string `json:"state,omitempty"`
	// ExecJSON 执行态明细（可选；门面透传原字符串）。
	ExecJSON string `json:"exec_json,omitempty"`
	// StartedAt 开始时间（= 节点创建时间）。
	StartedAt string `json:"started_at,omitempty"`
	// Awaiting 待裁决明细（仅状态为 awaiting 且执行态携带选项时非 nil）。
	Awaiting map[string]any `json:"awaiting,omitempty"`

	// ── DSL-3 展示字段（口径同 TaskNode；缺省为空 → 与既有载荷逐字节等价）──
	LoopCurrent  int       `json:"loop_current,omitempty"`
	LoopTotal    int       `json:"loop_total,omitempty"`
	Steps        []DslStep `json:"steps,omitempty"`
	Shadow       bool      `json:"shadow,omitempty"`
	ReturnKind   string    `json:"return_kind,omitempty"`
	ReturnInline string    `json:"return_inline,omitempty"`
	ReturnFile   string    `json:"return_file,omitempty"`
	ReturnSize   int       `json:"return_size,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// TasktreeListRequest 是任务树节点列表读入参（按主会话分树）。
type TasktreeListRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// TopSession 主会话 id（必填；空 → 由实现侧按请求 id 兜底）。
	TopSession string `json:"top_session,omitempty"`
	// Mode 视图模式（"init" = 仅 llm/运行中存活节点）。
	Mode string `json:"mode,omitempty"`
	// IncludeClosed 是否包含已关闭节点（缺省 = 不出视图）。
	IncludeClosed bool `json:"include_closed,omitempty"`
	// Shadow 是否走影子域（P1 回滚开关；缺省 = 权威表）。
	Shadow bool `json:"shadow,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope：供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// TasktreeListResponse 是任务树节点列表读出参。
type TasktreeListResponse struct {
	// Nodes 节点列表（按创建时间升序）。
	Nodes []TaskNode `json:"nodes"`
}

// TasktreeTasksRequest 是任务快照列表读入参（按会话 / 主会话过滤）。
type TasktreeTasksRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（可选）。
	SessionID string `json:"session_id,omitempty"`
	// TopSession 主会话 id（可选）。
	TopSession string `json:"top_session,omitempty"`
	// IncludeClosed 是否包含已关闭节点（缺省 = 不出视图）。
	IncludeClosed bool `json:"include_closed,omitempty"`
	// Shadow 是否走影子域（P1 回滚开关）。
	Shadow bool `json:"shadow,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// TasktreeTasksResponse 是任务快照列表读出参。
type TasktreeTasksResponse struct {
	// List 任务快照列表（按创建时间升序）。
	List []Task `json:"list"`
}

// TasktreeUpsertRequest 是节点幂等落库入参（同 id 覆盖；任务层唯一写入路径）。
type TasktreeUpsertRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Node 节点领域字段（ID 必填；NodeType 由实现侧按 Kind 推导，调用方无需自报）。
	Node TaskNode `json:"node"`
	// Shadow 是否走影子域（P1 回滚开关）。
	Shadow bool `json:"shadow,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// TasktreeUpsertResponse 是节点幂等落库出参。
type TasktreeUpsertResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// TasktreeDeleteRequest 是节点关闭（逻辑删除，级联子树）入参。
type TasktreeDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// NodeID 要关闭的节点 id（必填）。
	NodeID string `json:"node_id"`
	// Shadow 是否走影子域（P1 回滚开关；影子路径保持物理删除且不广播）。
	Shadow bool `json:"shadow,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// TasktreeDeleteResponse 是节点关闭出参（节点不存在 → OK=true，幂等）。
type TasktreeDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}
