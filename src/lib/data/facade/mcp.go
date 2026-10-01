// 本文件是 mcp 域（MCP server 配置 = **四级 `<级别>/capability/mcps/<名>.json` 文件**：
// 列举 / 定位 / 保存 / 删除）的【数据传输面】（DTO）。
//
// ── 为什么是「领域字段」而不是存储结构 ───────────────────────────────
// 存储形状（内核事实）：一个 MCP server = 某级 capability 根下 `mcps/<名>.json` 文件
// （文件名 = server 名 + .json；正文 = 字段 JSON 对象）。这些**都不出门面**：
//   - server = 领域字段（名称 / 连接点 / 运行参数 / 环境 / 工具面等），不是"目录 + JSON 文件"；
//   - 级别（app / user / project / prjusr）是**领域归属**（四级均可写）；路径解析在实现侧
//     （23 §7：门面不交路径规则）。
//
// ── 同名跨级语义（25-MCP与场景分层模型 §4）──────────────────────────
// 与场景不同：MCP server **同名跨级 = 最具体级优先、整条覆盖**（prjusr > project > user >
// app）；合并视图由实现侧组装（McpList 返回生效定义，Level = 命中的最具体级）。
//
// ── 兼容旧配置 ───────────────────────────────────────────────────
// 本域**只**承载文件化配置；旧 usr KV `mcpServers`（专用表 `mcps`）的兼容回落由消费方
// （gateway provider）叠加，不在本域（新写入一律走文件）。
package facade

// McpServer 是一个 MCP server 定义（领域字段；对齐既有 servers.list / 前端表单 / gateway
// ServerEntry 字段名）。
type McpServer struct {
	// Name server 名（必填；同时 = 文件名）。
	Name string `json:"name"`
	// Level 级别（app / user / project / prjusr；列出时 = 命中的最具体级，保存时 = 写入目标级）。
	Level string `json:"level,omitempty"`
	// Runtime 可执行/解释器（spawned；单个 token）。
	Runtime string `json:"runtime,omitempty"`
	// Args 启动参数（spawned；逐个 exec 参数，不做 shell/引号解析）。
	Args []string `json:"args,omitempty"`
	// URL 连接端点（proxied；或 runtime 拉起的连接点）。
	URL string `json:"url,omitempty"`
	// Enabled 是否启用（缺省 false）。
	Enabled bool `json:"enabled"`
	// Description 描述。
	Description string `json:"description,omitempty"`
	// Transport 传输方式（stdio / http / sse；空 = 按连接点推断）。
	Transport string `json:"transport,omitempty"`
	// Category 分类。
	Category string `json:"category,omitempty"`
	// Namespace 工具前缀（默认 <名>_；"-" = 禁前缀）。
	Namespace string `json:"namespace,omitempty"`
	// Env 环境变量（K=V）。
	Env []string `json:"env,omitempty"`
	// Headers 请求头（K=V）。
	Headers map[string]string `json:"headers,omitempty"`
	// Cwd 子进程工作目录。
	Cwd string `json:"cwd,omitempty"`
	// HotTools 高频固定工具（原生名列表；"*" = 全部 hot）。
	HotTools []string `json:"hot_tools,omitempty"`
	// Timeout 调用超时（秒；0 = 全局）。
	Timeout int `json:"timeout,omitempty"`
	// Isolate 按 (实例, workdir) 隔离连接（三态：nil = 未设置 → gateway 按 transport 推断）。
	Isolate *bool `json:"isolate,omitempty"`
	// Sandbox agentbox 沙箱开关（三态：nil/未设置 = 不隔离；仅 stdio 的 spawn 生效）。
	Sandbox *bool `json:"sandbox,omitempty"`
}

// ── 请求 / 响应 ────────────────────────────────────────────────────

// McpListRequest 是 MCP server 列举入参（四级根合并；返回生效定义）。
type McpListRequest struct {
	// InstanceID 实例 id（数据根由门面按它解析，调用方不构造路径，见 23 §7）。
	InstanceID string `json:"instance_id,omitempty"`
	// Scope 实例数据根（可选；语义同 SnapshotGetRequest.Scope）。
	Scope Scope `json:"scope,omitempty"`
}

// McpListResponse 是 MCP server 列举出参（同名跨级只保留最具体级的一条；Level = 命中级别）。
type McpListResponse struct {
	// List 生效定义列表（app → user → project → prjusr 合并后按名排序）。
	List []McpServer `json:"list"`
}

// McpGetRequest 是 MCP server 定位入参。
type McpGetRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Name server 名（必填）。
	Name string `json:"name"`
	// Level 限定级别（空 = 具体级优先 prjusr → project → user → app）。
	Level string `json:"level,omitempty"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// McpGetResponse 是 MCP server 定位出参。
type McpGetResponse struct {
	// Server 定义（Level = 命中的级别）。
	Server McpServer `json:"server"`
}

// McpSaveRequest 是 MCP server 保存入参（按 Server.Level 落对应级文件；Level 空 = 用户级）。
type McpSaveRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Server 定义（Name 必填；Level = 写入目标级别，空 = 用户级）。
	Server McpServer `json:"server"`
	// OldName 改名前的 server 名（可选：非空且与新名/级别不同 → 先删旧文件，避免残留）。
	OldName string `json:"old_name,omitempty"`
	// OldLevel 改名前的级别（可选）。
	OldLevel string `json:"old_level,omitempty"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// McpSaveResponse 是 MCP server 保存出参。
type McpSaveResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
	// Name server 名。
	Name string `json:"name"`
}

// McpDeleteRequest 是 MCP server 删除入参。
type McpDeleteRequest struct {
	// InstanceID 实例 id。
	InstanceID string `json:"instance_id,omitempty"`
	// Name server 名（必填）。
	Name string `json:"name"`
	// Level 指定级别（空 = 具体级优先：删命中的最具体级副本）。
	Level string `json:"level,omitempty"`
	// Scope 实例数据根（可选）。
	Scope Scope `json:"scope,omitempty"`
}

// McpDeleteResponse 是 MCP server 删除出参。
type McpDeleteResponse struct {
	// OK 是否成功（失败走 error）。
	OK bool `json:"ok"`
}

// McpAPI 是 mcp 域（MCP server 配置：列举 / 定位 / 保存 / 删除）门面面。
//
// 覆写后由实现侧广播既有 `data-mcp-refresh` —— 订阅面不在本请求-响应签名内（23 §7）。
type McpAPI interface {
	// McpList 列举生效 MCP server（四级合并；同名最具体级优先、整条覆盖）。
	McpList(req McpListRequest) (McpListResponse, error)

	// McpGet 按名定位（Level 空 = 具体级优先 prjusr → project → user → app）。
	McpGet(req McpGetRequest) (McpGetResponse, error)

	// McpSave 保存（按 Server.Level 落对应级 `<名>.json`；OldName 非空且改名 → 先删旧文件）。
	McpSave(req McpSaveRequest) (McpSaveResponse, error)

	// McpDelete 删除（Level 空 = 具体级优先删命中的最具体级副本）。
	McpDelete(req McpDeleteRequest) (McpDeleteResponse, error)
}
