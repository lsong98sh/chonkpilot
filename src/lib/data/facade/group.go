// 本文件是 group 域（**群聊落库两表**：参与者名单 + 大轮次投递对象）的【数据传输面】（DTO）。
//
// ── 为什么是两张表（而不是合并一张）──────────────────────────────────
// 设计稿 D44 允许「合并或分开」，本实现**分开**，理由（对齐 12-数据层「一表一实体」既有写法）：
//   - **生命周期不同**：参与者名单每个群聊**一份**、低频写（开群聊时一次性写入，之后偶尔加人）；
//     大轮次投递对象**每轮一条**、随调度高频追加。合并会带来大量空列 + 需按行类型判别。
//   - **主键/查询模式不同**：名单按 `(session_id, agent)` 唯一定位（自然键）；投递对象按
//     `(session_id, round)` 定位（轮次序号），两者都只需**按会话前缀扫描**，索引需求一致但键不同。
//   - **可独立演进**：投递对象后续可加投递状态 / 合并信息，不影响名单。
//   - 既有同层表的先例 = `sessions` / `turns` / `messages` / `memory_extract` 均一表一实体。
//
// 落点 = **prjusr 库**（`<usr 数据根>/data/<project-id>/chonkpilot.db`）——与会话/轮次/消息同层
// （两表都以外键 `session_id` 关联 `kind = group_chat` 的会话行）。
//
// ── 外键（逻辑关联）────────────────────────────────────────────────
// 两表都带 `session_id` 列，指向 `sessions` 表中 `kind = group_chat` 的会话（bbolt 无 DDL 外键，
// 故为**逻辑外键 + 前缀索引键**：主键首段恒为 `session_id`，按前缀 Seek 即该群聊的全部行）。
// 参与者的干活子会话（`work_session`，D23）**不在此二表**——它是一条 sessions 行（`kind =
// work_session` + `parent_id = 群聊会话`），由调度器按参与者名单创建。
package facade

// GroupParticipant 是群聊参与者名单项（**外键 = 群聊会话**；设计稿 D3「名单固定顺序」/ D26）。
type GroupParticipant struct {
	// SessionID 所属群聊会话 id（外键 → `sessions.kind == group_chat` 的行；前缀键首段）。
	SessionID string `json:"session_id"`
	// Agent 参与者（agent 标识 = 场景 agent 名；同一群聊内唯一，`(session_id, agent)` 为逻辑主键）。
	Agent string `json:"agent"`
	// Role 角色 / 职责（D43 注入用：主 agent 与各成员的职责，如「审查员」「测试员」）。
	Role string `json:"role,omitempty"`
	// Seq 名单固定顺序（1 基；**输出字段** = 落库时按入参 List 顺序赋值，读取按它升序 →
	// 「按名单顺序依次叫号」的确定性顺序，D3）。入参携带值被忽略（以 List 顺序为准）。
	Seq int `json:"seq,omitempty"`
}

// GroupRoundTarget 是群聊**每【大轮次】**的投递对象（**大轮次 = 群聊依次叫号的那一轮**，
// 不是参与者子会话内部的轮次；设计稿 D44 ② / D32 / D33）。
type GroupRoundTarget struct {
	// SessionID 所属群聊会话 id（外键 → `sessions.kind == group_chat` 的行；前缀键首段）。
	SessionID string `json:"session_id"`
	// Round 大轮次序号（1 基；同一群聊内唯一，`(session_id, round)` 为逻辑主键）。
	Round int `json:"round"`
	// Agents 本轮的投递对象（= 下一轮合并投递要喂给谁）：各发言人末行 `@` 名单经 D33 过滤后的
	// **并集**（不含 `@` 的公开发言 = 投给全员），按**参与者名单固定顺序**去重保序。
	Agents []string `json:"agents"`
}

// GroupParticipantsLoadRequest 是参与者名单读入参。
type GroupParticipantsLoadRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// SessionID 群聊会话 id（必填）。
	SessionID string `json:"session_id"`
	// Scope 实例数据根（可选；语义同 SessionListRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// GroupParticipantsLoadResponse 是参与者名单读出参（按名单固定顺序升序；无名单 → 空数组）。
type GroupParticipantsLoadResponse struct {
	// List 参与者名单（按 Seq 升序）。
	List []GroupParticipant `json:"list"`
}

// GroupParticipantsSaveRequest 是参与者名单写入参（**整表替换**：以 List 为准 —— 未出现者删除、
// 出现者写入；Seq 按 List 顺序重新赋值）。
type GroupParticipantsSaveRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 群聊会话 id（必填）。
	SessionID string `json:"session_id"`
	// List 目标名单（顺序即固定顺序；Agent 重复项按首现保留）。
	List []GroupParticipant `json:"list"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// GroupParticipantsSaveResponse 是参与者名单写出参。
type GroupParticipantsSaveResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// GroupRoundTargetsLoadRequest 是大轮次投递对象读入参。
type GroupRoundTargetsLoadRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 群聊会话 id（必填）。
	SessionID string `json:"session_id"`
	// Round 指定大轮次（可选；0/缺省 = 该群聊**全部**轮次，按轮次升序）。
	Round int `json:"round,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// GroupRoundTargetsLoadResponse 是大轮次投递对象读出参。
type GroupRoundTargetsLoadResponse struct {
	// List 投递对象行（按轮次升序）。
	List []GroupRoundTarget `json:"list"`
}

// GroupRoundTargetsSaveRequest 是大轮次投递对象写入参（按 `(session_id, round)` 幂等覆盖）。
type GroupRoundTargetsSaveRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// SessionID 群聊会话 id（必填）。
	SessionID string `json:"session_id"`
	// Round 大轮次序号（必填；>= 1）。
	Round int `json:"round"`
	// Agents 本轮投递对象（按参与者名单固定顺序；空数组 = 无人接收）。
	Agents []string `json:"agents"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// GroupRoundTargetsSaveResponse 是大轮次投递对象写出参。
type GroupRoundTargetsSaveResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}
