// 本文件是 knowledge 域（知识库 = capability 原语文件树：根解析 / 目录列举 / 契约文档读写 /
// 增删改名 / 建删目录）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：三级 capability 根下的**文件系统**——分类目录名（tools/skills/
// prompts/resources）、文件命名规范（`<名>.<token>.md`）、契约分区文本（`# 标题` + `[meta]`
// + `[description]` + `[parameters]|[arguments]` + `[content]`）。这些**都不出门面**：
//   - 调用方只报**域名/类别**（工具 / 技能 / 提示词 / 资源）与**条目名**，不拼 `.<token>.md`；
//   - 文档以**领域形态**给出（标题 / 元信息 / 描述 / 参数段 / 正文），不是分区文本；
//   - 级别（app / user / project）与路径归属由实现侧解析（`kbRootOf` 作用域语义），
//     门面按调用方给的 `Dir` / `Path` 原样转交实现侧校验（**不绕过** G-26 的移动校验）。
//
// ⚠️ 移动/改名（`rename` / `rename-dir`）：`NewName` 含路径分隔符 = 跨目录移动，作用域四条
// 校验（逐段净化、严格落在源根内、归属复检同根、目标非源自身或子路径）+ 目标已存在即拒
// **全部由实现侧执行**（G-26）；门面只承载领域字段，不得绕过。
package facade

// KnowledgeDir 是知识库目录项（领域字段）。
type KnowledgeDir struct {
	// Name 目录名。
	Name string `json:"name"`
	// Path 目录位置（实现侧口径的定位串；调用方原样回传，不自行拼接）。
	Path string `json:"path"`
}

// KnowledgeFile 是知识库文件项（领域字段；列表视图用）。
type KnowledgeFile struct {
	// Name 文件名。
	Name string `json:"name"`
	// Path 文件位置（调用方原样回传，不自行拼接）。
	Path string `json:"path"`
	// Type 原语类型（tool / skill / prompt / resource / file）。
	Type string `json:"type"`
	// Description 描述首行预览（截断）。
	Description string `json:"description"`
	// Modified 最近修改时间（RFC3339）。
	Modified string `json:"modified"`
}

// KnowledgeDoc 是知识库文档的**领域形态**（标题 / 元信息 / 描述 / 参数段 / 正文）。
type KnowledgeDoc struct {
	// Title 标题。
	Title string `json:"title"`
	// Meta 元信息（键 → 值；按类型含 runtime/entry/args/output、arguments、uri/mimetype 等）。
	Meta map[string]string `json:"meta"`
	// Description 描述。
	Description string `json:"description"`
	// Parameters 参数段（YAML 文本；段名见 ParamsSection）。
	Parameters string `json:"parameters"`
	// Content 正文。
	Content string `json:"content"`
	// ParamsSection 参数段分区名（`[parameters]` / `[arguments]`；空 = 默认 `[parameters]`）。
	ParamsSection string `json:"params_section,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// KnowledgeRootRequest 是知识库根解析入参（kind = app / user / project）。
type KnowledgeRootRequest struct {
	// InstanceID 实例 id（kind=project 时用于解析项目根；数据根由门面解析，调用方不构造路径）。
	InstanceID string `json:"instance_id,omitempty"`
	// Kind 级别（空 = app）。
	Kind string `json:"kind,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeRootResponse 是知识库根解析出参。
type KnowledgeRootResponse struct {
	// Root 根位置（调用方原样回传，不自行拼接）。
	Root string `json:"root"`
	// Kind 级别（app / user / project；实现侧归一后的值）。
	Kind string `json:"kind"`
}

// KnowledgeListRequest 是目录列举入参（根目录首次打开由实现侧预置分类目录）。
type KnowledgeListRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Dir 目录位置（空 / 相对 = project 根；实现侧按 kbRootOf 解析归属）。
	Dir string `json:"dir,omitempty"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeListResponse 是目录列举出参。
type KnowledgeListResponse struct {
	// Dir 当前目录（根相对）。
	Dir string `json:"dir"`
	// Dirs 子目录列表。
	Dirs []KnowledgeDir `json:"dirs"`
	// Files 文件列表（按名称升序；隐藏项与非 .md 项不出）。
	Files []KnowledgeFile `json:"files"`
}

// KnowledgeReadRequest 是文档读入参。
type KnowledgeReadRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Path 文件位置（实现侧按 kbRootOf 解析归属）。
	Path string `json:"path"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeReadResponse 是文档读出参（领域形态 + 原文）。
type KnowledgeReadResponse struct {
	// Source 原文（供"原文视图"直接展示）。
	Source string `json:"source"`
	// Doc 文档领域形态（供表单编辑）。
	Doc KnowledgeDoc `json:"doc"`
}

// KnowledgeSaveRequest 是文档写入参（按领域形态序列化回写）。
type KnowledgeSaveRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Path 文件位置（必填）。
	Path string `json:"path"`
	// Doc 文档领域形态。
	Doc KnowledgeDoc `json:"doc"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeSaveResponse 是文档写出参。
type KnowledgeSaveResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// KnowledgeCreateRequest 是新建文档入参（实现侧按类型生成契约模板 + 规范文件名）。
type KnowledgeCreateRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Dir 目标目录（空 / 相对 = project 根）。
	Dir string `json:"dir,omitempty"`
	// Type 原语类型（tool / skill / prompt / resource；接受单数 token 或复数目录名）。
	Type string `json:"type"`
	// Name 条目名（不带类型后缀 / .md；实现侧规范化）。
	Name string `json:"name"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeCreateResponse 是新建文档出参。
type KnowledgeCreateResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Path 新建文件位置。
	Path string `json:"path"`
}

// KnowledgeDeleteRequest 是删除文档入参。
type KnowledgeDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Path 文件位置。
	Path string `json:"path"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeDeleteResponse 是删除文档出参。
type KnowledgeDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// KnowledgeRenameRequest 是文件改名 / 跨目录移动入参（G-26：范围限同一知识库根内）。
type KnowledgeRenameRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Path 源文件位置。
	Path string `json:"path"`
	// NewName 新名（无路径分隔符 = 同目录改名；含分隔符 = 跨目录移动）。
	NewName string `json:"new_name"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeRenameResponse 是文件改名 / 移动出参。
type KnowledgeRenameResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// KnowledgeMkdirRequest 是建目录入参。
type KnowledgeMkdirRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Parent 父目录（空 / 相对 = project 根）。
	Parent string `json:"parent,omitempty"`
	// Name 目录名（实现侧净化）。
	Name string `json:"name"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeMkdirResponse 是建目录出参。
type KnowledgeMkdirResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Path 新目录位置。
	Path string `json:"path"`
}

// KnowledgeRmdirRequest 是删目录入参（递归）。
type KnowledgeRmdirRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Path 目录位置。
	Path string `json:"path"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeRmdirResponse 是删目录出参。
type KnowledgeRmdirResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// KnowledgeRenameDirRequest 是目录改名 / 跨目录移动入参（G-26：范围限同一知识库根内）。
type KnowledgeRenameDirRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Path 源目录位置。
	Path string `json:"path"`
	// NewName 新名（无路径分隔符 = 同目录改名；含分隔符 = 跨目录移动）。
	NewName string `json:"new_name"`
	// Scope 实例数据根（可选；语义同上）。
	Scope Scope `json:"scope,omitempty"`
}

// KnowledgeRenameDirResponse 是目录改名 / 移动出参。
type KnowledgeRenameDirResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}
