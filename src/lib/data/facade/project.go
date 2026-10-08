// 本文件是 project 域（项目初始化：工程规格文件 + 目录探测 + 项目级 agent 落文件）的【数据传输面】（DTO）。
//
// 用途：支撑「场景向导」。判断 `<workDir>/.chonkpilot/project_spec.md` 是否存在（= 该项目
// 是否已完成初始化），读写该规格文件，并对工作目录做**只读**探测（语言 / 框架 / 包管理 /
// 构建 / 测试 / lint / 结构），供向导预填与用户确认；另把向导合成的 agent 提示词落为
// **项目级 capability agent 文件**并回引用串（供场景以引用承载子 agent）。
//
// 约束（对齐 23 §7）：门面只放**领域字段**，不交路径规则——规格文件 / agent 文件的落点与探测的
// 忽略规则都留在实现侧。`ProjectProbe` **只读**，不写盘；`ProjectSpecWrite` 仅落
// `<workDir>/.chonkpilot/project_spec.md`；`ProjectAgentWrite` 仅落
// `<workDir>/.chonkpilot/capability/agents/`。本域无变更广播（消费者只有向导自身）。
package facade

// ProjectSpecExistsRequest 是工程规格文件存在性判定入参。
type ProjectSpecExistsRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选；语义同 MemorySaveRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// ProjectSpecExistsResponse 是存在性判定出参。
type ProjectSpecExistsResponse struct {
	// Exists 是否存在。
	Exists bool `json:"exists"`
	// Path 落点（实现侧口径的定位串；调用方原样回传，不自行拼接）。
	Path string `json:"path"`
}

// ProjectSpecReadRequest 是工程规格文件读入参。
type ProjectSpecReadRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// ProjectSpecReadResponse 是工程规格文件读出参。
type ProjectSpecReadResponse struct {
	// Exists 是否存在（false 时 Content 为空，**不是错误**）。
	Exists bool `json:"exists"`
	// Path 落点。
	Path string `json:"path"`
	// Content 全文。
	Content string `json:"content,omitempty"`
}

// ProjectSpecWriteRequest 是工程规格文件写入参（整份覆盖）。
type ProjectSpecWriteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Content 全文。
	Content string `json:"content"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// ProjectSpecWriteResponse 是工程规格文件写出参。
type ProjectSpecWriteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Path 落点。
	Path string `json:"path"`
}

// ProjectAgentWriteRequest 是「写项目级 capability agent 文件」入参（场景向导合成结果落文件）。
type ProjectAgentWriteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Name agent 名称（落盘文件名由实现侧净化）。
	Name string `json:"name"`
	// RoleTag 角色标签（可选）。
	RoleTag string `json:"role_tag,omitempty"`
	// Description agent 描述（可选）。
	Description string `json:"description,omitempty"`
	// Prompt agent 提示词（= 契约文档 [content] 正文）。
	Prompt string `json:"prompt"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// ProjectAgentWriteResponse 是「写项目级 capability agent 文件」出参。
type ProjectAgentWriteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Ref 场景可直接引用的引用串（`${workDir}/.chonkpilot/capability/agents/<名>.agent.md`）。
	Ref string `json:"ref"`
	// Path 落盘绝对路径（斜杠形态）。
	Path string `json:"path"`
}

// ProjectProbeRequest 是工作目录探测入参（只读）。
type ProjectProbeRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// ProjectProbeResponse 是工作目录探测出参（只读；缺失项省略 / 空值）。
type ProjectProbeResponse struct {
	// WorkDir 被探测的工作目录。
	WorkDir string `json:"work_dir"`
	// Empty 空目录（剔除 `.git` / `.chonkpilot` / OS 噪音后无任何文件）。
	Empty bool `json:"empty"`
	// HasCode 是否存在源码文件。
	HasCode bool `json:"has_code"`
	// HasGit 是否存在 `.git` 目录。
	HasGit bool `json:"has_git"`
	// HasReadme 根目录是否存在 README*。
	HasReadme bool `json:"has_readme"`
	// FileCount 文件数（有界统计，剔除忽略集）。
	FileCount int `json:"file_count"`
	// Languages 语言清单（按文件数降序）。
	Languages []string `json:"languages,omitempty"`
	// Frameworks 框架清单（取自依赖清单，尽力而为）。
	Frameworks []string `json:"frameworks,omitempty"`
	// PackageManager 包管理器（尽力而为）。
	PackageManager string `json:"package_manager,omitempty"`
	// BuildTool 构建工具（尽力而为）。
	BuildTool string `json:"build_tool,omitempty"`
	// TestTool 测试工具（尽力而为）。
	TestTool string `json:"test_tool,omitempty"`
	// LintTool 代码风格工具（尽力而为）。
	LintTool string `json:"lint_tool,omitempty"`
	// TopDirs 顶层目录清单（剔除忽略集，升序）。
	TopDirs []string `json:"top_dirs,omitempty"`
}
