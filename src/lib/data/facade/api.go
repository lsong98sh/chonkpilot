// Package facade 是 data 层门面的【唯一定义】（接口 + DTO）——单一真相。
//
// 绑定（23-工程与部署拓扑 §7）由**同一份定义**派生，宿主在装配期选择用哪个：
//   - inline 绑定：同进程直接函数调用（`facade/inline`；本期唯一落地绑定）
//   - http 绑定：由生成器读本包 AST 产出 client + server（PoC 已验证，见 TECH-POC/facade）
//   - mq   绑定：现有 data-<domain>-* 消息面（persist 的 MQ handler = 该绑定的落地形态，
//     与 inline 绑定**同源实现**；见 41 G-34）
//
// 约束（契约须按「最差绑定」设计，对齐 23 §7 与 TECH-POC/facade §6）：
//   - 契约只放**可序列化 struct**：不放句柄 / channel / 闭包 / `*DB` 这类跨不过进程的东西
//   - 方法形态 = `Name(req TReq) (TResp, error)`（生成器子集：参数与返回必须是本包内具名 struct）
//   - DTO 一律用**领域字段**，不搬存储结构（存储模型 → 领域模型的翻译在实现侧，见 23 §7）
//   - **订阅面与请求面成对**（23 §7）：变更广播（data-<domain>-refresh）由**实现侧**在写入后
//     发出（inline/http 绑定同源），不塞进请求-响应签名（广播/多订阅放不进 req/resp 形态）
package facade

// API 是 data 门面（= 各域面的组合；域面见下方子接口）。
//
// 覆盖域（阶段 4）：snapshot（试点，41 G-32）· config（第二批）· session / turn / message
// （第三批：会话 CRUD/活动态 + 轮次 + 消息）· tasktree / knowledge / filelist / scenario /
// memory（第四批，本批：任务树 + 知识库文件树 + 全文索引清单 + 场景 + 记忆库）· mcp（第五批：
// MCP server 三级/四级文件化配置）。
// **至此 data 面各域均有域面**（`data-<domain>-*` 全量动作）。
type API interface {
	SnapshotAPI
	ConfigAPI
	SessionAPI
	TurnAPI
	MessageAPI
	TasktreeAPI
	KnowledgeAPI
	FileListAPI
	ScenarioAPI
	MemoryAPI
	McpAPI
}

// TasktreeAPI 是 tasktree 域（任务树：节点列表 / 任务快照 / 幂等落库 / 关闭）门面面。
//
// 写入（Upsert）与关闭（Delete = 逻辑删除）后由实现侧发出既有 `task-deleted`
// （关闭时，61 §3.4 载荷不变）——订阅面不在本请求-响应签名内（23 §7）。
type TasktreeAPI interface {
	// TasktreeList 读某主会话的任务树节点（Mode="init" → 仅 llm/运行中存活节点）。
	TasktreeList(req TasktreeListRequest) (TasktreeListResponse, error)

	// TasktreeTasks 读任务快照列表（按会话 / 主会话过滤；单源 = 任务层权威表）。
	TasktreeTasks(req TasktreeTasksRequest) (TasktreeTasksResponse, error)

	// TasktreeUpsert 节点幂等落库（同 id 覆盖；任务层唯一写入路径）。
	TasktreeUpsert(req TasktreeUpsertRequest) (TasktreeUpsertResponse, error)

	// TasktreeDelete 关闭节点（= 逻辑删除：标记 closed/deleted_at，级联子树；行保留可查）。
	TasktreeDelete(req TasktreeDeleteRequest) (TasktreeDeleteResponse, error)
}

// KnowledgeAPI 是 knowledge 域（知识库 = capability 原语文件树）门面面。
//
// 级别（app / user / project）与路径归属由实现侧解析（kbRootOf 作用域语义）；移动 / 改名
// （Rename / RenameDir）的**作用域四条校验 + 目标已存在即拒**由实现侧执行（G-26），
// 门面不得绕过。本域无变更广播（文件树由调用方按需重列）。
type KnowledgeAPI interface {
	// KnowledgeRoot 解析知识库根（kind = app / user / project；空 = app）。
	KnowledgeRoot(req KnowledgeRootRequest) (KnowledgeRootResponse, error)

	// KnowledgeList 列举目录内容（根目录首次打开由实现侧预置分类目录）。
	KnowledgeList(req KnowledgeListRequest) (KnowledgeListResponse, error)

	// KnowledgeRead 读原语文档（领域形态 + 原文）。
	KnowledgeRead(req KnowledgeReadRequest) (KnowledgeReadResponse, error)

	// KnowledgeSave 保存原语文档（按领域形态序列化回写）。
	KnowledgeSave(req KnowledgeSaveRequest) (KnowledgeSaveResponse, error)

	// KnowledgeCreate 新建原语文档（按类型生成契约模板 + 规范文件名）。
	KnowledgeCreate(req KnowledgeCreateRequest) (KnowledgeCreateResponse, error)

	// KnowledgeDelete 删除原语文档。
	KnowledgeDelete(req KnowledgeDeleteRequest) (KnowledgeDeleteResponse, error)

	// KnowledgeRename 文件改名或跨目录移动（G-26：限同一知识库根内）。
	KnowledgeRename(req KnowledgeRenameRequest) (KnowledgeRenameResponse, error)

	// KnowledgeMkdir 建目录。
	KnowledgeMkdir(req KnowledgeMkdirRequest) (KnowledgeMkdirResponse, error)

	// KnowledgeRmdir 递归删目录。
	KnowledgeRmdir(req KnowledgeRmdirRequest) (KnowledgeRmdirResponse, error)

	// KnowledgeRenameDir 目录改名或跨目录移动（G-26：限同一知识库根内）。
	KnowledgeRenameDir(req KnowledgeRenameDirRequest) (KnowledgeRenameDirResponse, error)
}

// FileListAPI 是 filelist 域（全文索引的文件清单：增量清单读写）门面面。
//
// 本域无订阅面（清单消费者只有索引插件自身，不广播 -refresh）。
type FileListAPI interface {
	// FileListList 读清单（可选按路径前缀过滤 + 分页）。
	FileListList(req FileListListRequest) (FileListListResponse, error)

	// FileListPut 单条 upsert（按 Key；Key 空 = 实现侧按路径推导）。
	FileListPut(req FileListPutRequest) (FileListPutResponse, error)

	// FileListDelete 按 Key 批量删除（返回实际删除条数）。
	FileListDelete(req FileListDeleteRequest) (FileListDeleteResponse, error)
}

// ScenarioAPI 是 scenario 域（场景：列举 / 定位 / 保存 / 删除）门面面。
//
// 写入（Save / Delete）后由实现侧广播既有 `data-scenario-refresh` —— 订阅面
// 不在本请求-响应签名内（23 §7）。
type ScenarioAPI interface {
	// ScenarioList 列举场景（合并三级根；首次由实现侧物化 app 级出厂场景）。
	ScenarioList(req ScenarioListRequest) (ScenarioListResponse, error)

	// ScenarioGet 按 id 定位场景（Level 空 = 具体级优先 project → user → app）。
	ScenarioGet(req ScenarioGetRequest) (ScenarioGetResponse, error)

	// ScenarioSave 保存场景（级别可为 user / project / app：app 级可编辑）。
	ScenarioSave(req ScenarioSaveRequest) (ScenarioSaveResponse, error)

	// ScenarioDelete 删除场景（Level 空 = 具体级优先查找副本）。
	ScenarioDelete(req ScenarioDeleteRequest) (ScenarioDeleteResponse, error)
}

// MemoryAPI 是 memory 域（记忆库：类别清单 / 全文读写 / 删自定义类别）门面面。
//
// 写入（Save / Delete）后由实现侧广播既有 `data-memory-refresh` —— 订阅面不在本
// 请求-响应签名内（23 §7）。
type MemoryAPI interface {
	// MemoryList 读类别清单（预置集 ∪ 自定义集；首次访问由实现侧按开关预置类别文件）。
	MemoryList(req MemoryListRequest) (MemoryListResponse, error)

	// MemoryGet 读单类全文（自定义类别须已存在）。
	MemoryGet(req MemoryGetRequest) (MemoryGetResponse, error)

	// MemorySave 写单类全文（写 / 建类别）。
	MemorySave(req MemorySaveRequest) (MemorySaveResponse, error)

	// MemoryDelete 删自定义类别（预置类别不可删，报错）。
	MemoryDelete(req MemoryDeleteRequest) (MemoryDeleteResponse, error)
}

// SnapshotAPI 是 snapshot 域（会话快照）门面面。
type SnapshotAPI interface {
	// SnapshotGet 读会话快照（无快照 / 无记录 → Found=false，**不是错误**）。
	SnapshotGet(req SnapshotGetRequest) (SnapshotGetResponse, error)

	// SnapshotSet 写会话快照（覆盖 messages + turn；保留会话记录的其他字段）。
	SnapshotSet(req SnapshotSetRequest) (SnapshotSetResponse, error)
}

// ConfigAPI 是 config 域（配置 kv 面 + 用户配置）门面面。
//
// 覆盖（读 / 写 / 删齐备，粗粒度批量 —— 按最差绑定 http 设计，不逐键往返）：
//   - 同族 kv 域：prj-config（prj + 本机 prjusr 两层 + usr 兜底）/ prompt / prj-security
//   - 用户配置：usr 主库视图 / 合并后有效值 / 增量写 / 删（逐键或整份）
//
// 写入（Set / Delete）后由实现侧广播变更（data-<domain>-refresh；user-config 另兼容
// config-refresh）——订阅面不在本请求-响应签名内（23 §7）。
type ConfigAPI interface {
	// ConfigKVList 读同族 kv 域全表（平铺 键 → 值字符串）。
	ConfigKVList(req ConfigKVListRequest) (ConfigKVListResponse, error)

	// ConfigKVGet 批量按键读（读序 prjusr → prj → usr，各域按自身规则）。
	ConfigKVGet(req ConfigKVGetRequest) (ConfigKVGetResponse, error)

	// ConfigKVSet 批量写（prj-config 的个人运行态键落 prjusr，其余落 prj）。
	ConfigKVSet(req ConfigKVSetRequest) (ConfigKVSetResponse, error)

	// ConfigKVDelete 批量删（按域规则在各层同删 = 恢复继承）。
	ConfigKVDelete(req ConfigKVDeleteRequest) (ConfigKVDeleteResponse, error)

	// UserConfigView 读 usr 主库视图（"usr 是否已有配置"语义；不叠加项目层）。
	UserConfigView(req UserConfigViewRequest) (UserConfigViewResponse, error)

	// UserConfigGet 读合并后有效值（usr 基线 + 项目层可继承键覆盖 + 系统默认补齐）。
	UserConfigGet(req UserConfigGetRequest) (UserConfigGetResponse, error)

	// UserConfigSet 增量写用户配置（载荷里出现的键才写）。
	UserConfigSet(req UserConfigSetRequest) (UserConfigSetResponse, error)

	// UserConfigDelete 删用户配置（Keys 空 = 清空整份，回落默认/继承）。
	UserConfigDelete(req UserConfigDeleteRequest) (UserConfigDeleteResponse, error)
}

// SessionAPI 是 session 域（会话：列表/详情/最新/改名/删除/活动态/幂等建）门面面。
//
// 覆盖 `data-session-*` 的会话 CRUD 与查询动作（61 §3.2）；写入后由实现侧广播既有
// `session-new` 事件（幂等建会话首次落库）——订阅面不在本请求-响应签名内（23 §7）。
type SessionAPI interface {
	// SessionList 读顶层会话列表（按最近活动降序）。
	SessionList(req SessionListRequest) (SessionListResponse, error)

	// SessionGet 读单个会话详情（不存在 → Found=false，**不是错误**）。
	SessionGet(req SessionGetRequest) (SessionGetResponse, error)

	// SessionLatest 读最近活动顶层会话 id（无会话 → 空串）。
	SessionLatest(req SessionLatestRequest) (SessionLatestResponse, error)

	// SessionTitle 改会话标题（刷新更新时间）。
	SessionTitle(req SessionTitleRequest) (SessionTitleResponse, error)

	// SessionDelete 删会话及其轮次/消息（会话不存在 → OK=true，幂等；活动会话一并清活动态）。
	SessionDelete(req SessionDeleteRequest) (SessionDeleteResponse, error)

	// SessionActiveSet 写「当前活动会话」。
	SessionActiveSet(req SessionActiveSetRequest) (SessionActiveSetResponse, error)

	// SessionActiveGet 读「当前活动会话」（失效 → 回落最近活动顶层会话）。
	SessionActiveGet(req SessionActiveGetRequest) (SessionActiveGetResponse, error)

	// SessionEnsure 幂等建会话（已存在不覆盖；**首次**落库后广播 session-new）。
	SessionEnsure(req SessionEnsureRequest) (SessionEnsureResponse, error)
}

// TurnAPI 是 turn 域（轮次：历史分页/幂等建/摘要/终态/遗留清理）门面面。
type TurnAPI interface {
	// TurnHistory 读会话历史分页（轮次 + 消息视图 + 是否还有更早）。
	TurnHistory(req TurnHistoryRequest) (TurnHistoryResponse, error)

	// TurnEnsure 幂等建轮次（已存在复用，不覆盖）。
	TurnEnsure(req TurnEnsureRequest) (TurnEnsureResponse, error)

	// TurnSetSummary 写轮次摘要（压缩回写；有值 = 该轮已被压缩）。
	TurnSetSummary(req TurnSetSummaryRequest) (TurnSetSummaryResponse, error)

	// TurnComplete 写轮次终态（done/error/interrupted + 结束原因；保留原字段）。
	TurnComplete(req TurnCompleteRequest) (TurnCompleteResponse, error)

	// TurnCleanupStale 把遗留 running 轮次标 interrupted（启动清理；返回处理条数）。
	TurnCleanupStale(req TurnCleanupStaleRequest) (TurnCleanupStaleResponse, error)
}

// MessageAPI 是 message 域（消息：落库/重建/上下文/取正文）门面面。
type MessageAPI interface {
	// MessageAppend 落一条消息（role=tool 的正文由实现侧规整为工具载荷；brief 缺省时尽力生成）。
	MessageAppend(req MessageAppendRequest) (MessageAppendResponse, error)

	// MessageLoad 读某轮次全部消息（时间升序，供 LLM 会话重建）。
	MessageLoad(req MessageLoadRequest) (MessageLoadResponse, error)

	// MessageContext 组装会话上下文（include_snapshot 且快照非空 → 快照前缀 + 其后轮次）。
	MessageContext(req MessageContextRequest) (MessageContextResponse, error)

	// MessageContent 按 key 批量取正文（未命中的 key 省略）。
	MessageContent(req MessageContentRequest) (MessageContentResponse, error)
}
