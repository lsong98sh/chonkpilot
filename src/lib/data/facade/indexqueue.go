// INDEX QUEUE（resident indexer 的持久化待索引队列）数据面契约：prjusr 级 `index_queue` 表。
//
// 写入方 = filesys（create / write / remove / rename），消费方 = resident indexer
// （取一批 → 索引 → 回执）。落 **prjusr 库**（与会话同层），表内按 `work_dir` 区分项目。
//
// **仅 in-process 使用，暂无 MQ 绑定**：本域只做 inline 门面（同进程函数调用），
// 不新增 MQ 主题 / msgkeys；加入 MQ 绑定须先更新 `docs/spec/60-reference/61-消息一览.md`
// 并经用户确认。
//
// 消费语义：`Take` **不修改状态**（至少一次；indexer 单实例消费），索引完成后由 `Ack`
// 回执（成功 → done；失败 → attempts+1，累计 3 次转 failed）。
//
// **扇出（consumer 维度）**：同一文件变更对每个 resident 索引引擎各有一行（consumer =
// vfts / codegraph），各自独立 take / ack / pending，互不影响。consumer 为空的行是
// legacy 行（旧版本写入，无 consumer 概念）——take 时任意 consumer 均可见，ack 时也
// 允许任意 consumer 回执。
package facade

// IndexQueueItem 是待索引队列一行（领域字段）。
type IndexQueueItem struct {
	// ID 队列行主键（实现侧生成）。
	ID string `json:"id"`
	// WorkDir 项目工作目录（归一为 '/' 分隔）。
	WorkDir string `json:"work_dir"`
	// Path 待索引文件路径（归一为 '/' 分隔）。
	Path string `json:"path"`
	// Consumer 消费方索引引擎标识（vfts / codegraph；空 = legacy 行）。
	Consumer string `json:"consumer,omitempty"`
	// Operation 变更类型：create | write | remove | rename。
	Operation string `json:"operation"`
	// State 行状态：pending | done | failed。
	State string `json:"state"`
	// Attempts 已失败次数（Ack 失败累加）。
	Attempts int `json:"attempts"`
	// LastError 最近一次失败原因（仅失败时写入）。
	LastError string `json:"last_error,omitempty"`
	// CreatedAt 入队时间（RFC3339FixedNano，字典序 = 时间序）。
	CreatedAt string `json:"created_at,omitempty"`
	// UpdatedAt 最近更新时间（RFC3339FixedNano）。
	UpdatedAt string `json:"updated_at,omitempty"`
}

// IndexQueueAppendRequest 写入一条待索引变更。
type IndexQueueAppendRequest struct {
	// WorkDir 项目工作目录。
	WorkDir string `json:"work_dir"`
	// Path 待索引文件路径（'/' 分隔；实现侧归一 '\\' → '/'）。
	Path string `json:"path"`
	// Operation 变更类型：create | write | remove | rename。
	Operation string `json:"operation"`
	// Consumers 扇出的消费方列表（空 = 旧行为，单行 consumer=""）。
	Consumers []string `json:"consumers,omitempty"`
	// Scope 实例数据根（非空 = 供本进程尚未登记该实例时自登记）。
	Scope Scope `json:"scope,omitempty"`
}

// IndexQueueAppendResponse 返回落库 id（去重命中时返回既有 id）。
type IndexQueueAppendResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// ID 队列行主键（= IDs[0]，兼容单 consumer 调用）。
	ID string `json:"id,omitempty"`
	// IDs 本次入队命中的全部队列行主键（按 Consumers 顺序）。
	IDs []string `json:"ids,omitempty"`
}

// IndexQueueTakeRequest 取一批待索引行（**不修改状态**）。
type IndexQueueTakeRequest struct {
	// WorkDir 项目工作目录。
	WorkDir string `json:"work_dir"`
	// Consumer 消费方标识（空 = 不按 consumer 过滤；非空 = 匹配本 consumer 或 legacy 空行）。
	Consumer string `json:"consumer,omitempty"`
	// Limit 返回上限（0 = 默认 100）。
	Limit int `json:"limit,omitempty"`
	// Scope 实例数据根（语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// IndexQueueTakeResponse 返回待索引行（按 created_at 升序）。
type IndexQueueTakeResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Items 取到的行。
	Items []IndexQueueItem `json:"items"`
}

// IndexQueueAckRequest 回执一批已消费行。
type IndexQueueAckRequest struct {
	// Consumer 消费方标识（空 = 不按 consumer 过滤；非空 = 只回执本 consumer 或 legacy 空行）。
	Consumer string `json:"consumer,omitempty"`
	// IDs 队列行主键（不存在的 id 跳过，不报整体失败）。
	IDs []string `json:"ids"`
	// Failed 本批是否索引失败。
	Failed bool `json:"failed"`
	// Error 失败原因（Failed=true 时写入 last_error）。
	Error string `json:"error,omitempty"`
	// Scope 实例数据根（语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// IndexQueueAckResponse 返回实际更新行数。
type IndexQueueAckResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Updated 实际更新的行数（不存在的 id 不计）。
	Updated int `json:"updated"`
}

// IndexQueuePendingRequest 统计某项目的待索引行数。
type IndexQueuePendingRequest struct {
	// WorkDir 项目工作目录。
	WorkDir string `json:"work_dir"`
	// Consumer 消费方标识（空 = 不按 consumer 过滤；非空 = 统计本 consumer 或 legacy 空行）。
	Consumer string `json:"consumer,omitempty"`
	// Scope 实例数据根（语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// IndexQueuePendingResponse 返回待索引（state=pending）行数。
type IndexQueuePendingResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Pending 待索引行数。
	Pending int `json:"pending"`
}

// IndexQueueCompactRequest 清理过期已完成行。
type IndexQueueCompactRequest struct {
	// KeepDoneSeconds done 行保留秒数（0 = 默认 3600）。
	KeepDoneSeconds int `json:"keep_done_seconds,omitempty"`
	// Scope 实例数据根（语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// IndexQueueCompactResponse 返回删除行数。
type IndexQueueCompactResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Removed 实际删除的过期 done 行数。
	Removed int `json:"removed"`
}

// IndexQueueAPI 是 indexqueue 域（resident indexer 待索引队列）门面面。
//
// 本域**无变更广播**（队列消费方只有 resident indexer 自身）；仅 in-process 使用，
// 暂无 MQ 绑定（见文件头）。
type IndexQueueAPI interface {
	// IndexQueueAppend 入队一条变更，按 Consumers 扇出；同 (work_dir, path, consumer) 已有
	// pending 行 → 更新 operation（保留原 created_at），不产生重复行。Consumers 为空 = 旧行为
	// （单行 consumer=""）。
	IndexQueueAppend(req IndexQueueAppendRequest) (IndexQueueAppendResponse, error)

	// IndexQueueTake 取一批 pending 行（按 created_at 升序；不修改状态，至少一次语义）。
	// Consumer 非空 → 只取本 consumer 的行与 legacy 空行；为空 → 不过滤。
	IndexQueueTake(req IndexQueueTakeRequest) (IndexQueueTakeResponse, error)

	// IndexQueueAck 回执：成功 → done；失败 → attempts+1，累计 3 次转 failed。
	// Consumer 非空 → 只回执本 consumer 的行与 legacy 空行，其余 id 跳过。
	IndexQueueAck(req IndexQueueAckRequest) (IndexQueueAckResponse, error)

	// IndexQueuePending 统计某 work_dir 的 pending 行数（Consumer 过滤语义同 Take）。
	IndexQueuePending(req IndexQueuePendingRequest) (IndexQueuePendingResponse, error)

	// IndexQueueCompact 删除过期（updated_at 早于 now-KeepDoneSeconds）的 done 行；
	// failed 不删。
	IndexQueueCompact(req IndexQueueCompactRequest) (IndexQueueCompactResponse, error)
}
