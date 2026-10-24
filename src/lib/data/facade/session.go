// 本文件是 session 域（会话 CRUD / 活动态 / 幂等建会话）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：sessions 表一条记录 = 标题 / parent_id / created_at / updated_at
// 五列 + `history`（**塞在字符串里的消息数组 JSON**）+ `snapshot_turn`。
// 23 §7 · G-33 明确把 `history` / `snapshot_turn` 列为**消息面/存储形状**，故本 DTO 用领域
// 形态承载：
//   - 会话 = id / 标题 / 父会话 / 创建与更新时间 / 最近活动 + **快照**（messages + turn，
//     复用试点批 facade.Snapshot 的领域形态：消息已解析为结构化数组，不是 JSON 字符串）。
//
// 换存储（列名 / 分表 / 独立库）不会把改动漏到调用方。
//
// 字段覆盖 = 调用方的**全部合法需求**：列表 / 详情 / 最新 / 改名 / 删 / 活动态读写 / 幂等建 /
// 生命周期更新 / 子聊天列举。
package facade

// 会话类型标识（`sessions.kind`；单聊方案 D23 / D28，2026-10-15；群聊方案 D23/D25）：
//   - ""（空）        = 普通顶层会话；
//   - SessionKindSubChat     = 子聊天（`subchat_open` 创建；人机一对一）；
//   - SessionKindGroupChat   = 群聊（同一「子聊天」体系，N 个 agent，人只旁观）；
//   - SessionKindWorkSession = 参与者的「干活子会话」（**群聊专属**：每个参与者一个、跨轮复用，
//     工具交互全在里面；群聊方案 D12 / D23）；
//   - SessionKindSubSession  = `dsl_run` 的 LLM 步骤派生的子会话（现行委派路径）。
//
// 不变量（单聊方案 §3.2）：`kind ∈ {sub_chat, sub_session, group_chat, work_session}` ⟺
// `parent_id != ""`；`kind ∈ {sub_chat, group_chat}` ⟹ 才有 agent / scenario_id / llm / lifecycle。
//
// **`work_session` 与 `sub_session` 不是同物**（群聊方案 §5 术语提示）：前者 = agent 的私有工作区
// （`parent_id` = **群聊会话**），后者 = dsl_run 的 LLM 步骤派生（走任务执行树）；
// `work_session` **不进**子聊天清单（`SessionSubChats*`）——它没有「入口/状态条」视图。
const (
	SessionKindSubChat     = "sub_chat"
	SessionKindGroupChat   = "group_chat"
	SessionKindWorkSession = "work_session"
	SessionKindSubSession  = "sub_session"
)

// 子聊天生命周期（`sessions.lifecycle`，三态；单聊方案 §3.4.4 / D24）——**仅子聊天**有意义：
//   - SessionLifecycleChatting = 进行中（`subchat_open` 建会话时初值）；
//   - SessionLifecycleReady    = 子 agent 判断该结束（建议态，**非终态**，可逆回 chatting）；
//   - SessionLifecycleFinished = 已结束（**用户拍板**后唯一终态）。
const (
	SessionLifecycleChatting = "chatting"
	SessionLifecycleReady    = "ready"
	SessionLifecycleFinished = "finished"
)

// Session 是会话（领域字段；对话粒度 = 会话 → 轮次 → 消息，见 session/turn 两域）。
type Session struct {
	// ID 会话 id。
	ID string `json:"id"`
	// DialogID 所属对话 id（会话存储为一树一库；列表/详情带出，供后续调用回填）。
	// **仅非空才写键** → 旧布局会话行载荷逐字节不变。
	DialogID string `json:"dialog_id,omitempty"`
	// Title 会话标题。
	Title string `json:"title,omitempty"`
	// ParentID 父会话 id（空 = 顶层会话；子会话仅顶层会话进列表/最新）。
	ParentID string `json:"parent_id,omitempty"`
	// CreatedAt 创建时间（RFC3339）。
	CreatedAt string `json:"created_at,omitempty"`
	// UpdatedAt 更新时间（RFC3339）。
	UpdatedAt string `json:"updated_at,omitempty"`
	// LastActivity 最近活动时间（可选；空 = 取 UpdatedAt，与既有"活动排序"口径一致）。
	LastActivity string `json:"last_activity,omitempty"`
	// Kind 会话类型标识（空 = 普通顶层会话；取值见 SessionKind* 常量）。**仅非空才落库**，
	// 故普通顶层会话行形状与既有逐字节一致。
	// **读侧回退口径**：行缺该键（历史数据）且 ParentID 非空 ⇒ 视为 SessionKindSubSession。
	Kind string `json:"kind,omitempty"`
	// Lifecycle 子聊天生命周期（取值见 SessionLifecycle* 常量；仅子聊天有）。
	Lifecycle string `json:"lifecycle,omitempty"`
	// Agent 固定角色名（子 agent 定义引用；仅子聊天有）。
	Agent string `json:"agent,omitempty"`
	// ScenarioID 固定场景（目录名 / key；仅子聊天有）。
	ScenarioID string `json:"scenario_id,omitempty"`
	// LLM 固定 LLM（provider name；仅子聊天有）。
	LLM string `json:"llm,omitempty"`
	// Purpose 子聊天的**运行目的 / 展示名**（= gateway 注入的 `tool_call_display_name`，由
	// `subchat_open` handler 经注入通道读得并落库；单聊方案 §5.4）。仅子聊天有；**仅非空才落库**。
	Purpose string `json:"purpose,omitempty"`
	// Goal / Playbook / Convergence 是**群聊**的「本次目标 / 剧本（推进规则）/ 收敛与退出条件」
	// （群聊方案 §18.2 / D43 / D51 / D52；SUBCHAT-3 阶段 F）。由主 agent 在 `subchat_open` 开群聊时
	// 写入，**每轮注入给全体参与者（含主 agent）**。仅群聊有；**仅非空才落库**（普通会话 / 单聊行
	// 形状零变化）。与 Purpose 同层（会话行的可选固定参数）。
	Goal        string `json:"goal,omitempty"`
	Playbook    string `json:"playbook,omitempty"`
	Convergence string `json:"convergence,omitempty"`
	// Snapshot 该会话的快照（领域形态：messages + turn）。
	// **nil = 该会话从未写过快照**（存储侧无 history/snapshot_turn 两列）——
	// 与"有快照但为空"（非 nil 且 Messages 为空）是两件不同的事，故用指针保真。
	Snapshot *Snapshot `json:"snapshot,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// SessionListRequest 是会话列表读入参。
type SessionListRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope：供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionListResponse 是会话列表读出参（仅顶层会话，按最近活动降序）。
type SessionListResponse struct {
	// List 会话列表。
	List []Session `json:"list"`
}

// SessionGetRequest 是会话详情读入参。
type SessionGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（非空 → 读对话库；空 → 旧布局）。
	DialogID string `json:"dialog_id,omitempty"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionGetResponse 是会话详情读出参（会话不存在 → Found=false，**不是错误**）。
type SessionGetResponse struct {
	// Found 是否存在该会话。
	Found bool `json:"found"`
	// Session 会话内容（Found=false 时无意义）。
	Session Session `json:"session"`
}

// SessionLatestRequest 是最近活动顶层会话读入参。
type SessionLatestRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionLatestResponse 是最近活动顶层会话读出参（无会话 → 空串）。
type SessionLatestResponse struct {
	// SessionID 最近活动顶层会话 id（无 → 空串）。
	SessionID string `json:"session_id"`
	// DialogID 该会话所在对话 id（shard=on 时回带；旧布局为空 → 不写键）。
	DialogID string `json:"dialog_id,omitempty"`
}

// SessionTitleRequest 是会话改名入参。
type SessionTitleRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（非空 → 改对话库；主 session 改名会同步 `dialogs.title`）。
	DialogID string `json:"dialog_id,omitempty"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Title 新标题。
	Title string `json:"title"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionTitleResponse 是会话改名出参。
type SessionTitleResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// SessionDeleteRequest 是会话删除入参（连带删除该会话的轮次与消息；活动会话一并清活动态）。
type SessionDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（非空 → 删对话库内会话；**主 session → 整对话删除**）。
	DialogID string `json:"dialog_id,omitempty"`
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionDeleteResponse 是会话删除出参（会话不存在 → OK=true，幂等）。
type SessionDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// SessionForkRequest 是对派生入参（SESSION-COMP-1 P4；`data-session-fork`）。
type SessionForkRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 源对话 id（必填）。
	DialogID string `json:"dialog_id"`
	// Mode 派生档位："" → full；"context" = 只带主会话 + 快照。
	Mode string `json:"mode,omitempty"`
	// Title 新对话标题（空 → 源主会话标题）。
	Title string `json:"title,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionForkResponse 是派生结果（新 dialog_id 与主 session_id 均由实现生成）。
type SessionForkResponse struct {
	// OK 是否成功。
	OK bool `json:"ok"`
	// DialogID 新对话 id。
	DialogID string `json:"dialog_id"`
	// SessionID 新主 session id。
	SessionID string `json:"session_id"`
}

// SessionActiveSetRequest 是「当前活动会话」写入参。
type SessionActiveSetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（61 §0.6 契约位；活动态存 prj 配置，本方法不消费）。
	DialogID string `json:"dialog_id,omitempty"`
	// SessionID 活动会话 id。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionActiveSetResponse 是「当前活动会话」写出参。
type SessionActiveSetResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// SessionActiveGetRequest 是「当前活动会话」读入参。
type SessionActiveGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionActiveGetResponse 是「当前活动会话」读出参。
type SessionActiveGetResponse struct {
	// SessionID 活动会话 id（未设置 / 已失效 → 回落最近活动顶层会话；无会话 → 空串）。
	SessionID string `json:"session_id"`
}

// SessionEnsureRequest 是幂等建会话入参（已存在不覆盖）。
type SessionEnsureRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（SESSION-COMP-1 P3：非空 → 写对话库的 `sessions` 表；空 → 旧布局 prjusr）。
	DialogID string `json:"dialog_id,omitempty"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// ParentSessionID 父会话 id（可选；非空 = 子会话）。
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
	// Kind 是会话类型标识（单聊方案 D23 / D25，2026-10-15 新增；空 = 普通顶层会话）：
	//   - "sub_chat"    子聊天（subchat_open 创建；人机一对一）
	//   - "group_chat"  群聊（多 agent，人只旁观）
	//   - "sub_session" dsl_run 的 LLM 步骤派生的子会话（现行路径）
	Kind string `json:"kind,omitempty"`
	// Lifecycle 是**子聊天**生命周期（仅 kind ∈ {sub_chat, group_chat} 有意义；三态）：
	// "chatting" 进行中 / "ready" 子 agent 判断该结束 / "finished" 已结束。
	Lifecycle string `json:"lifecycle,omitempty"`
	// Agent 是固定角色名（子 agent 定义引用；仅子聊天有）。
	Agent string `json:"agent,omitempty"`
	// ScenarioID 是固定场景（目录名 / key；仅子聊天有）。
	ScenarioID string `json:"scenario_id,omitempty"`
	// LLM 是固定 LLM（仅子聊天有）。
	LLM string `json:"llm,omitempty"`
	// Purpose 是子聊天的运行目的 / 展示名（= gateway 注入的 `tool_call_display_name`；单聊方案
	// §5.4）。**仅非空才写键**（普通会话行形状零变化）。
	Purpose string `json:"purpose,omitempty"`
	// Goal / Playbook / Convergence 是**群聊**的「本次目标 / 剧本 / 收敛与退出条件」（群聊方案
	// §18.2 / D43 / D51 / D52；SUBCHAT-3 阶段 F）：主 agent 开群聊时写入，每轮注入给全体参与者。
	// **仅非空才写键**（普通会话 / 单聊行形状零变化）。仅建会话时写入（幂等建 → 已存在不覆盖）。
	Goal        string `json:"goal,omitempty"`
	Playbook    string `json:"playbook,omitempty"`
	Convergence string `json:"convergence,omitempty"`
}

// SessionEnsureResponse 是幂等建会话出参。
type SessionEnsureResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// DialogID 会话所在对话 id（P3 过渡期：根会话缺 `dialog_id` 时由服务端确定性建对话并回带，
	// 调用方据此回填后续调用的 `dialog_id`；旧布局调用不带该键 → 载荷逐字节不变）。
	DialogID string `json:"dialog_id,omitempty"`
}

// SessionLifecycleSetRequest 是子聊天生命周期写入参（单聊方案 §3.4.4：`chatting` → `ready` →
// `finished`；`ready` 可逆回 `chatting`）。
type SessionLifecycleSetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（非空 → 写对话库；空 + 一树一库生效 → 跨对话库定位该会话）。
	DialogID string `json:"dialog_id,omitempty"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Lifecycle 目标生命周期（必填；取值见 SessionLifecycle* 常量，非法值报错）。
	Lifecycle string `json:"lifecycle"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionLifecycleSetResponse 是子聊天生命周期写出参。
type SessionLifecycleSetResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// SessionSubChatsRequest 是按父会话列举子聊天入参（单聊方案 §3.4.4 / §3.4.6：**不改**
// data-session-list 的"只看顶层"语义，子会话域用自己的列举面）。
type SessionSubChatsRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（非空 → 只查该对话库；空 + 一树一库生效 → 跨对话库扫描）。
	DialogID string `json:"dialog_id,omitempty"`
	// ParentSessionID 父会话 id（必填；返回挂在它下面的子聊天）。
	ParentSessionID string `json:"parent_session_id"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionSubChatsResponse 是按父会话列举子聊天出参。
type SessionSubChatsResponse struct {
	// List 该父会话下的子聊天（`kind ∈ {sub_chat, group_chat}`，**含已结束**，按创建时间升序）。
	// 只挂"子聊天"——`sub_session`（dsl_run 的 LLM 步骤派生的子会话）仍走任务执行树，不在此列。
	List []Session `json:"list"`
}

// SessionSubChatsAllRequest 是**全局**列举子聊天入参（单聊方案 §3.4.7 启动提醒：跨会话枚举）。
type SessionSubChatsAllRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// DialogID 对话 id（非空 → 只查该对话库；空 + 一树一库生效 → 跨对话库扫描）。
	DialogID string `json:"dialog_id,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// SessionSubChatsAllResponse 是全局列举子聊天出参。
type SessionSubChatsAllResponse struct {
	// List **全部**子聊天（`kind ∈ {sub_chat, group_chat}`，**含已结束**，按创建时间升序）——
	// "未结束"过滤（`lifecycle ∈ {chatting, ready}`）由消费方自做（状态条 / 启动提醒各取所需）。
	List []Session `json:"list"`
}
