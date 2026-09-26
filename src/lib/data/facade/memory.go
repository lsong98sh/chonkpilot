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
