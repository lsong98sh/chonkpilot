// servers.list 解析（分组式格式，对齐 26-mcp-gateway）：
//
//	# 注释
//	mcp.alias=core
//	core.url=http://127.0.0.1:5700/core/tools/mcp
//	core.category=core
//	core.description=内置 core 分类
package mcpgateway

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
)

// Server 来源（Origin）：决定是否向该 server 注入内部调用上下文（`_meta["chonkpilot"]`）。
// 内部上下文只注入「本仓自有/内置」来源；第三方（用户定义）一律不注入、也不给 CHONKPILOT_*。
const (
	OriginBuiltin = "builtin" // 本仓自有/内置：内嵌 self/本仓 mcp-server、系统级 config.json mcpServers、三级 capability dir 节点
	OriginUser    = "user"    // 用户定义/第三方（usr mcps 等）
)

// normalizeOrigin 归一来源取值：`builtin`（大小写/空白容忍）→ builtin；其余/空 → user（安全默认）。
// 用于显式取值（servers.list 的 `origin=` 键 / register 载荷）与裸构造的兜底；静态条目缺省由
// parseServersList 显式填 builtin（部署方文件视为本仓自有），动态 register 缺省仍为 user。
func normalizeOrigin(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), OriginBuiltin) {
		return OriginBuiltin
	}
	return OriginUser
}

// ServerEntry 是 servers.list 中一个 server 组。
type ServerEntry struct {
	ID          string            // mcp.alias= 分组 id（唯一，别名）
	Name        string            // 显示名
	Description string            // 可选：server 描述（servers/list 视图/UI 用）
	Category    string            // 分类（供 tools/list 的 _meta.category；UI 分组浏览用）
	Runtime     string            // 可执行/解释器（spawned；单个 token，如 node.exe / C:\Program Files\X\y.exe）
	Args        []string          // 启动参数（spawned；逐个作为 exec 参数传递，不做 shell/引号解析）
	URL         string            // 连接端点（proxied；或 runtime 拉起的连接点）
	Transport   string            // stdio / http / sse（缺省：url 存在 → http[runtime+url=spawn 后连]；仅 runtime → stdio）
	Enabled     bool              // 默认 true
	TimeoutSec  int               // 调用超时（秒），缺省 0 = 取全局
	Namespace   string            // 工具前缀（默认 <id>_；"-" = 禁前缀）
	Aliases     map[string]string // 原名 → 别名
	Env         []string          // K=V
	Headers     map[string]string // K=V
	Cwd         string            // 子进程工作目录
	HotTools    []string          // 高频固定工具
	CBDisabled  bool              // 熔断豁免
	Isolate     *bool             // 按 workdir 隔离连接（nil = 按 transport 推断：stdio→true / http|sse→false）
	Scope       string            // 归属域："" = global（servers.list/静态缺省）；servers/register 载荷 scope（instance id uuid）
	Origin      string            // 来源：builtin（本仓自有/内置）/ user（用户定义/第三方）；空 = user（安全默认；`servers.list` 解析器显式填 builtin，见 parseServersList）
	// Sandbox 是 agentbox 沙箱开关（usr `mcps` 条目的 `sandbox` 键；register 载荷 mcp_server.sandbox）：
	// nil/未设置 = **不隔离**（与引入前行为一致）；true = 对该上游 spawn 生效。
	// **与 Isolate 语义不同、互不替代**：Isolate = 连接池按 (instance, workdir) 隔离；
	// Sandbox = 把「可读 / 可写目录（递归）」策略下发到 spawn 出的上游进程。
	// **仅 stdio 传输生效**（http/sse 不隔离，见 14-安全域-agentbox）。
	Sandbox *bool `json:"sandbox,omitempty"`
	// SandboxDirs 是该 server 的 agentbox 允许目录快照（prj `security-*` → {dir, writable}），
	// 由装配层注入；仅 Sandbox=true + 传输 stdio + 非空时随 spawn 环境变量下发。
	SandboxDirs []agentbox.Rule `json:"sandbox_dirs,omitempty"`
}

// IsBuiltin 是否本仓自有/内置来源——**唯一**决定是否注入内部上下文 `_meta["chonkpilot"]` 的条件；
// 空/未知来源按 user 处理（安全默认：不注入）。
func (e ServerEntry) IsBuiltin() bool { return normalizeOrigin(e.Origin) == OriginBuiltin }

// TransportName 返回归一传输名：显式 transport 优先；缺省按连接点推断（url → http；仅 runtime → stdio）。
// buildConn 与隔离推断共用同一判据（单一来源）。
func (e ServerEntry) TransportName() string {
	if t := strings.TrimSpace(e.Transport); t != "" {
		return t
	}
	if strings.TrimSpace(e.URL) != "" {
		return "http" // 仅 url = proxied；runtime+url = spawn 后按 url 连
	}
	return "stdio" // 仅 runtime
}

// IsolateEnabled 判定该 server 是否**按 workdir 隔离**连接（同 server 不同项目各持独立管道/子进程）：
// 显式 Isolate 优先；缺省按 transport 推断——stdio 只有一条管道、无法并发处理（共享会互相阻塞），
// 故默认隔离 true；http/sse 可多连接并发，默认 false（共享单连接，与既有行为一致）。
func (e ServerEntry) IsolateEnabled() bool {
	if e.Isolate != nil {
		return *e.Isolate
	}
	return e.TransportName() == "stdio"
}

// SandboxPolicyJSON 返回 spawn 上游进程时注入的 agentbox 策略 JSON（`""` = **不注入**）。
// 生效条件（三者同时满足）：显式 Sandbox=true + 传输为 stdio + 允许目录非空。
//
//   - **仅 stdio**：http/sse 上游不经我们 spawn（或子进程与连接解耦），不施加隔离（用户口径）；
//   - **空目录不注入**：上游进程非本仓实现、无法验证其 agentbox 行为；空允许集若被严格实现
//     会成为「全拒」→ 宁可不下发（由调用方记 warn）。
//
// 注：上游进程是否真正据此限制文件访问，取决于该进程是否实现 agentbox 消费方
// （本仓 executor 已实现；第三方进程仅「透传」，不构成强制）。
func (e ServerEntry) SandboxPolicyJSON() string {
	if e.Sandbox == nil || !*e.Sandbox {
		return ""
	}
	if e.TransportName() != "stdio" {
		return ""
	}
	if len(e.SandboxDirs) == 0 {
		return ""
	}
	b, err := json.Marshal(e.SandboxDirs)
	if err != nil {
		return ""
	}
	return string(b)
}

// defaultServersList 已移除（2026-09-07 收敛）：能力源 = 装配方 MCPServer 参数传入，
// 不再存在"对接本机 chonkpilot-mcp-server 的隐式默认"——内嵌形态（chonkpilot-server）
// 与独立 exe（chonkpilot-gateway）都**无回退**；无源 = 空列表（不报错，工具面为空但不 fatal）。

// loadServersList 已移除（RB-2，2026-09-21）：gateway lib **不读「源」**——读 servers.list 由
// 面层（exe main / 装配方）完成后经 `Params.Servers` 传入，gateway 只接成品（解析可留 lib，
// 见 ParseServersList）。

// ParseServersList 解析分组式 servers.list 文本（导出：供 chonkpilot-server 等复用/测试）。
func ParseServersList(data []byte) ([]ServerEntry, error) {
	return parseServersList(data)
}

// parseServersList 解析分组式文本。
func parseServersList(data []byte) ([]ServerEntry, error) {
	var entries []ServerEntry
	cur := -1
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue // 非法行忽略 + 由调用方 warn
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "mcp.alias" { // 分组标记（硬切：旧 tool= 不再解析）
			if val == "" {
				return nil, fmt.Errorf("line %d: mcp.alias= id empty", i+1)
			}
			// 静态来源（部署方文件）缺省 = builtin（本仓自有/内置）；第三方条目须显式 `origin=user`。
			entries = append(entries, ServerEntry{ID: val, Enabled: true, Aliases: map[string]string{}, Headers: map[string]string{}, Origin: OriginBuiltin})
			cur = len(entries) - 1
			continue
		}
		if cur < 0 {
			continue // 字段在 tool= 之前 → 忽略
		}
		e := &entries[cur]
		// key = <id>.<field>，须带本组前缀
		if !strings.HasPrefix(key, e.ID+".") {
			continue
		}
		field := strings.TrimPrefix(key, e.ID+".")
		switch field {
		case "name":
			e.Name = val
		case "description":
			e.Description = val
		case "category":
			e.Category = val
		case "runtime":
			e.Runtime = expandVars(val)
		case "args":
			// 逗号分隔（与 env 同风格）；每项为**完整**一个 exec 参数（含空格不再二次切分）。
			for _, a := range splitComma(val) {
				e.Args = append(e.Args, expandVars(a))
			}
		case "url":
			e.URL = expandVars(val)
		case "transport":
			e.Transport = val
		case "enabled":
			e.Enabled = val == "true" || val == "1"
		case "timeout":
			fmt.Sscanf(val, "%d", &e.TimeoutSec)
		case "namespace":
			e.Namespace = val
		case "alias":
			kv := strings.SplitN(val, "=", 2)
			if len(kv) == 2 {
				e.Aliases[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		case "env":
			e.Env = splitKVList(val)
		case "headers":
			e.Headers = splitHeaders(val)
		case "cwd":
			e.Cwd = val
		case "hot_tools":
			e.HotTools = splitComma(val)
		case "isolate":
			e.Isolate = boolPtr(val == "true" || val == "1")
		case "circuit_breaker":
			e.CBDisabled = val == "disable"
		case "origin":
			e.Origin = normalizeOrigin(val)
		}
	}
	return entries, nil
}

// boolPtr 返回布尔字面量的指针（ServerEntry.Isolate 三态：nil = 未设置 → 按 transport 推断）。
func boolPtr(b bool) *bool { return &b }

// expandVars 展开 %VAR% 与 ${VAR}（环境变量）。
func expandVars(s string) string {
	s = os.Expand(s, func(k string) string { return os.Getenv(k) })
	for {
		start := strings.Index(s, "%")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+1:], "%")
		if end < 0 {
			break
		}
		key := s[start+1 : start+1+end]
		s = s[:start] + os.Getenv(key) + s[start+2+end:]
	}
	return s
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitKVList(s string) []string {
	return splitComma(s)
}

func splitHeaders(s string) map[string]string {
	m := map[string]string{}
	for _, p := range splitComma(s) {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return m
}
