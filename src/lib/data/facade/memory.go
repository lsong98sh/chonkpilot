// 本文件是 memory 域（记忆库 = **会话沉淀的 LLM 记忆**：类别清单 / 类别全文读写 / 删自定义类别）
// 的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：记忆 = **文件树**——用户级 `<用户目录>/用户偏好.md`（唯一用户级、不可
// 配置）+ 项目级 `<workdir>/.chonkpilot/memory/<类别>.md`（预置 8 类 + 用户自定义类）；类别清单
// = 预置集 ∪（项目记忆目录扫描出的自定义 .md）。这些**都不出门面**：
//   - 「记忆」= 领域条目（类别名 / 级别 / 全文 / 预估 token），不是"某个 .md 文件"——
//     换落点（分库 / 换目录 / 换格式）不会把改动漏到调用方；
//   - 级别（user / project）是**领域归属**（用户偏好唯一恒用户级；自定义类别恒项目级），
//     目录与文件名规则在实现侧（23 §7：门面不交路径规则）；
//   - 类别名合法性（文件名字符安全 / 保留设备名 / 长度）与"预置类别不可删"是**领域规则**，
//     由实现侧执行（门面不复制这套校验）。
//
// 订阅面（请求面 + 订阅面成对，23 §7）：`save` / `delete` 后由**实现侧**广播既有
// `data-memory-refresh`（{instance_id, id, op, list}）——不塞进请求-响应签名。
package facade

// MemoryUserCategory 是唯一用户级记忆类别名（「用户偏好」；跨项目、无类别维度）。
// 领域级常量（记忆域与 config 域共用 —— 后者据它决定提示词覆盖文件的写入级别），
// 单源定义于此处，避免同名字面量多处维护。
const MemoryUserCategory = "用户偏好"

// MemoryCategory 是记忆类别条目（领域字段；列表视图用）。
type MemoryCategory struct {
	// Category 类别名（中文，去扩展名）。
	Category string `json:"category"`
	// Level 级别（user / project）。
	Level string `json:"level"`
	// Path 落点（实现侧口径的定位串；调用方原样回传，不自行拼接）。
	Path string `json:"path"`
	// Tokens 全文预估 token（文件缺失 → 0）。
	Tokens int `json:"tokens"`
	// Prompt 该类别的沉淀提示词**有效值**（OP-04：后端按文件读序
	// 项目级 → 用户级 → 系统级磁盘 → embed 内置解析后下发；前缀空 = 无任何提示词文件，
	// 该类不提取）。前端「编辑提示词」据此回填，不再持镜像常量。
	Prompt string `json:"prompt"`
	// PromptOverride 是否存在**用户可编辑的覆盖文件**（项目级 / 用户级；OP-04）。
	// true = 「已自定义」（可「恢复默认」清除覆盖回落系统级/内置）；false = 来源为系统级/内置。
	PromptOverride bool `json:"prompt_override"`
}

// MemoryDoc 是单个记忆条目的全文（领域字段）。
type MemoryDoc struct {
	// Category 类别名。
	Category string `json:"category"`
	// Level 级别（user / project）。
	Level string `json:"level"`
	// Path 落点（调用方原样回传）。
	Path string `json:"path"`
	// Content 全文。
	Content string `json:"content"`
	// Tokens 全文预估 token。
	Tokens int `json:"tokens"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// MemoryListRequest 是类别清单读入参（首次访问由实现侧按开关预置类别文件）。
type MemoryListRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope：供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// MemoryListResponse 是类别清单读出参（预置集 ∪ 自定义集）。
type MemoryListResponse struct {
	// List 类别清单（预置在前、自定义按名称升序在后）。
	List []MemoryCategory `json:"list"`
}

// MemoryGetRequest 是单类全文读入参。
type MemoryGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Category 类别名（必填）。
	Category string `json:"category"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// MemoryGetResponse 是单类全文读出参。
type MemoryGetResponse struct {
	// Doc 记忆条目全文。
	Doc MemoryDoc `json:"doc"`
}

// MemorySaveRequest 是单类全文写入参（写 / 建类别；自定义类别由此创建）。
type MemorySaveRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Category 类别名（必填）。
	Category string `json:"category"`
	// Content 全文。
	Content string `json:"content"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// MemorySaveResponse 是单类全文写出参。
type MemorySaveResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// ID 类别名（= 领域条目 id；对齐既有应答的 id 字段）。
	ID string `json:"id"`
}

// MemoryDeleteRequest 是自定义类别删除入参（预置类别不可删，只可清空全文）。
type MemoryDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Category 类别名（必填）。
	Category string `json:"category"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// MemoryDeleteResponse 是自定义类别删除出参。
type MemoryDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// ID 类别名（= 领域条目 id）。
	ID string `json:"id"`
}

// ── 记忆提取进度（专用表 memory_extract；OP-05/06，2026-10-06）──────────
//
// 存储形状（内核事实）：prjusr bbolt bucket `memory_extract`，一行 = 某 (会话, 类别) 的
// 「最后已**成功**提取的 turn」；主键 = `<session_id>\x00<category>`。这些**不出门面**——
// 「进度」= 领域条目（会话 / 类别 / 最后已提取 turn），换存储（表/键）不把改动漏到调用方。
// 本域**无订阅面**（进度只由 memory 插件读写，不广播 -refresh）。

// MemoryExtractRecord 是某 (会话, 类别) 的记忆提取进度（领域字段）。
type MemoryExtractRecord struct {
	// SessionID 会话 id。
	SessionID string `json:"session_id"`
	// Category 类别名。
	Category string `json:"category"`
	// LastTurnID 最后已成功提取的 turn id（空 = 尚无进度）。
	LastTurnID string `json:"last_turn_id"`
}

// MemoryExtractLoadRequest 是提取进度读入参（Category 空 = 该会话全部类别）。
type MemoryExtractLoadRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Category 类别名（可选；空 = 读该会话全部类别）。
	Category string `json:"category,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// MemoryExtractLoadResponse 是提取进度读出参。
type MemoryExtractLoadResponse struct {
	// List 进度条目（按类别名升序；无进度 → 空）。
	List []MemoryExtractRecord `json:"list"`
}

// MemoryExtractSaveRequest 是提取进度写入参（仅在该类别**成功**写回后调用）。
type MemoryExtractSaveRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Category 类别名（必填）。
	Category string `json:"category"`
	// LastTurnID 最后已成功提取的 turn id（必填）。
	LastTurnID string `json:"last_turn_id"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// MemoryExtractSaveResponse 是提取进度写出参。
type MemoryExtractSaveResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// ID 类别名（= 领域条目 id）。
	ID string `json:"id"`
}

// MemoryExtractDeleteRequest 是提取进度删除入参（Category 空 = 删该会话全部类别；幂等）。
type MemoryExtractDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 会话 id（必填）。
	SessionID string `json:"session_id"`
	// Category 类别名（可选；空 = 删该会话全部类别）。
	Category string `json:"category,omitempty"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// MemoryExtractDeleteResponse 是提取进度删除出参。
type MemoryExtractDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}
