// 本文件是 filelist 域（全文索引的**文件清单**：增量清单读写）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：项目级 prj 库 `file_list` 表一行 = 清单键（sha1(绝对路径) 前 16 hex）
// + 文件绝对路径 + 体积 / 修改时间 / 内容 md5 + 该文件在索引集合中的文档 id 列表（JSON 数组）
// + 块数 + 最近索引时间。这些**都不出门面**：
//   - 清单键是**存储主键**（内容无关的稳定 hash）→ 门面按领域标识（`Path`）给出，键由实现侧
//     按同一规则推导，调用方不自行算 hash；
//   - 文档 id 列表是**索引集合侧**的主键（按文件删除用）→ 门面按"该文件的索引文档"这一
//     领域事实给出，不是"JSON 数组列"；
//   - 分页（offset/limit）是**读请求**语义（读最差绑定下必须一次取回可分批）→ 门面收在入参。
//
// 本域**不广播 -refresh**（既有口径：清单消费者只有 vfts 插件自身）——订阅面为空，无请求面外的主题。
package facade

// FileListEntry 是文件清单一行（领域字段）。
type FileListEntry struct {
	// Key 清单键（实现侧按路径推导的稳定标识；调用方原样回传，不自行构造）。
	Key string `json:"key"`
	// Path 文件绝对路径（'/' 分隔，R-11 绝对路径口径）。
	Path string `json:"path"`
	// Size 文件字节数。
	Size int64 `json:"size"`
	// MTime 文件修改时间（RFC3339Nano）。
	MTime string `json:"mtime"`
	// MD5 文件内容 md5（十六进制）。
	MD5 string `json:"md5"`
	// DocIDs 该文件在索引集合中的文档 id 列表（按文件删除用）。
	DocIDs []string `json:"doc_ids"`
	// Chunks 该文件的文档块数。
	Chunks int `json:"chunks"`
	// IndexedAt 最近一次索引时间（RFC3339）。
	IndexedAt string `json:"indexed_at"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// FileListListRequest 是清单读入参（可选按路径前缀过滤 + 分页）。
type FileListListRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// Prefix 路径前缀过滤（空 = 全量）。
	Prefix string `json:"prefix,omitempty"`
	// Offset 跳过条数（0 = 不跳过）。
	Offset int `json:"offset,omitempty"`
	// Limit 返回上限（0 = 不限制）。
	Limit int `json:"limit,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope：供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// FileListListResponse 是清单读出参。
type FileListListResponse struct {
	// List 清单条目（按清单键升序；已按 Prefix / Offset / Limit 处理）。
	List []FileListEntry `json:"list"`
	// Total 过滤后总条数（未分页前）。
	Total int `json:"total"`
}

// FileListPutRequest 是清单单条写入参（按 Key upsert；Key 空 = 由实现侧按路径推导）。
type FileListPutRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Entry 清单条目（Key 可空）。
	Entry FileListEntry `json:"entry"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// FileListPutResponse 是清单单条写出参。
type FileListPutResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// ID 落库条目键。
	ID string `json:"id"`
}

// FileListDeleteRequest 是清单批量删除入参。
type FileListDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Keys 要删除的清单键（空 = 无操作目标，实现侧报错）。
	Keys []string `json:"keys"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// FileListDeleteResponse 是清单批量删除出参。
type FileListDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Deleted 实际删除条数（不存在的键不计）。
	Deleted int `json:"deleted"`
}
