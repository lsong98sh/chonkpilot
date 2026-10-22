// 工具级覆盖的 **gateway 侧统一施加**（I-82 修复，2026-09-19）：
//
// 背景：usr 键 `tool_async` / `tool_sandbox` 原先只作用于**内嵌 mcp-server 注册的工具**
// （chonkpilot-llm/server loadExecConfig → mcp-server Config.SetToolAsync/SetToolSandbox，
// 查表按契约名）。而前端工具页（SettingsToolAsyncPage / SettingsToolSandboxPage）列出的是
// **tools/list 的暴露名全量**（self_<契约名> / <dir节点名>_<原名> / <第三方名>_<原名>）并可配
// → dir 节点工具与第三方工具写下的是**孤儿键**（不报错也不生效）。
//
// 本文件把覆盖下沉到 gateway：装配方（chonkpilot-llm/server）读 usr 配置后经
// SetAsyncOverrides / SetSandboxOverrides 注入**已归一**的覆盖表，gateway 在
//   - 构造 tools/list 的 `_meta`（handleToolsList）时按**暴露名**再应用一次（async / async-threshold）；
//   - doCall 的异步判定处按暴露名应用（mode / threshold）；
//   - 沙箱：**被 spawn 执行体级**（2026-10-12 起；executors 按 executor exe 基名 / mcps 按 MCP 条目名）
//     → 内置 executor 写**共享 Config**（由 mcp-server 每次 spawn 读，即时生效）；
//     外部 MCP 由本网关在 stdio spawn 时按 entry 名查本表 → 开关**变化**时主动作废受影响连接（I-178）。
//
// **零新增 MQ 主题**、**不改既有 payload 形状**（取值落在既有 tools/list `_meta` 内）。
package mcpgateway

import (
	"context"
	"strings"
	"sync"
	"time"
)

// ToolAsyncOverride 是工具级异步覆盖（mode + threshold + touch_files），与 mcp-server 契约侧同形但
// **归 gateway 所有**（RB-2：gateway lib 不依赖 chonkpilot-mcp-server 包；装配方负责从其类型转换）。
// 未设置 = 该字段不覆盖（维持契约现值）；未配置的工具 = 无覆盖。`hard_timeout` 属执行硬上限
// （仅 executor 消费、不透出到 tools/list），不在 gateway 面。
//
// `ThresholdSet` = threshold 是否**显式设置**（区分「未设置」与「0/-1 = 无上限/无阈值」，
// 用户口径 2026-09-27）。
//
// `TouchFiles` / `TouchFilesSet` = 该工具是否**涉及文件变动**（用户口径 2026-09-28）：显式设置优先，
// 未设置 → 由 `resolveTouchFiles` 按工具来源取缺省（self 内置仅 filesys_run/script_run 涉及；
// dir 节点 / 第三方 / 无法判定 → 保守按涉及）。取值经前置打点钩子 payload `touch_files` 下发
// （见 mcpgateway.runPreHooks），**不进 tools/list `_meta`**。
//
// `CancelOnTimeout` / `CancelOnTimeoutSet` = **超时自动取消**（配置⑤，用户口径 2026-10-07）：
// 工具到超时点时**直接取消**（不等用户裁决），秒数 > 0 生效、默认 0 = 不取消（不配置时行为逐字节
// 不变）；见 mcpgateway.doCall 的 `timer.C` 分支。**不进 tools/list `_meta`**（仅 gateway 消费）。
type ToolAsyncOverride struct {
	Mode               string `json:"mode"`
	Threshold          int    `json:"threshold"`
	ThresholdSet       bool   `json:"-"`
	TouchFiles         bool   `json:"touch_files"`
	TouchFilesSet      bool   `json:"-"`
	CancelOnTimeout    int    `json:"cancel_on_timeout"`
	CancelOnTimeoutSet bool   `json:"-"`
}

// toolOverrideState 保存覆盖表（读写锁保护；与 tools/list、doCall 并发读取）。
type toolOverrideState struct {
	mu sync.RWMutex
	// async = 工具级异步覆盖（key = 暴露名）。
	async map[string]ToolAsyncOverride
	// sandboxExecutors = 内置 executor 沙箱开关（key = executor exe 基名）；
	// sandboxMCPs = 外部 MCP 沙箱开关（key = MCP 条目名）。
	sandboxExecutors map[string]bool
	sandboxMCPs      map[string]bool
}

// SetAsyncOverrides 注入工具级异步覆盖表（key = 暴露名）。nil/空 = 清空（回落契约现值）。
func (g *Gateway) SetAsyncOverrides(m map[string]ToolAsyncOverride) {
	g.tov.mu.Lock()
	g.tov.async = m
	g.tov.mu.Unlock()
}

// SetSandboxOverrides 注入 agentbox 沙箱开关（**管理单位 = 被 spawn 的执行体**，
// 2026-10-12 由 executor 类别重整）。
//
//   - executors：内置 executor 开关（key = executor exe 基名）；
//   - mcps：外部 MCP 开关（key = MCP 条目名，与 gateway stdio spawn 的 entry.Name 对应）。
//
// 语义：内置 executor 由执行侧（mcp-server 执行配置 / core-mcp-server）按其键位消费，
// **装配方同时写入该配置**（L5：gateway 不再持有执行配置/能力源，只保留外部 MCP 连接侧）；
// 外部 MCP 由本网关在 **stdio spawn 时按 entry.Name 查本表 mcps** 决定是否注入
// `CHONKPILOT_SANDBOX`（允许目录仍来自 prj `security-*`，随 entry 快照）。**旧形态
// （按 executor 类别 / usr `mcps[].sandbox`）读取时迁移（装配层 / 本网关），不再作开关来源**。
func (g *Gateway) SetSandboxOverrides(executors, mcps map[string]bool) {
	g.tov.mu.Lock()
	old := g.tov.sandboxMCPs
	g.tov.sandboxExecutors = executors
	g.tov.sandboxMCPs = mcps
	g.tov.mu.Unlock()
	// I-178：外部 MCP 的沙箱以**长连接子进程**为单位（sandboxOn 虽为实时回调，但仅在 spawn/重连时
	// 读取）→ 开关**变化**时须主动作废受影响 MCP 的连接，下次调用即按新开关重连（即时生效）。
	if changed := boolMapDiff(old, mcps); len(changed) > 0 {
		go g.reconnectMCPsForSandbox(changed)
	}
}

// sandboxReconnectTimeout 是开关变化触发的外部 MCP 重连（Invalidate）单次上限（防握手无界阻塞）。
const sandboxReconnectTimeout = 30 * time.Second

// boolMapDiff 返回 a→b 之间**值发生变化**的键（含新增 / 移除 / true↔false 翻转）。
func boolMapDiff(a, b map[string]bool) []string {
	var out []string
	for k, v := range b {
		if av, ok := a[k]; !ok || av != v {
			out = append(out, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// reconnectMCPsForSandbox 对沙箱开关变化的外部 MCP（**stdio 且自持子进程**）作废连接并重连：
// sandboxOn 为实时回调，故 respawn 即按新开关注入 `CHONKPILOT_SANDBOX`（I-178）。
// 仅 stdio 传输才受 agentbox 约束（http/sse 无 spawn 子进程）→ 其余跳过。加载失败只记日志、不阻断。
func (g *Gateway) reconnectMCPsForSandbox(names []string) {
	if g.reg == nil {
		return
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.TrimSpace(n)] = true
	}
	for _, key := range g.reg.providerKeys() {
		ps, ok := g.reg.provider(key)
		if !ok || ps == nil || ps.entry == nil || ps.prov == nil {
			continue
		}
		if ps.entry.TransportName() != "stdio" || !want[ps.entry.SandboxName()] {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), sandboxReconnectTimeout)
		rebuilt, err := ps.prov.Invalidate(ctx)
		cancel()
		if g.logf != nil {
			if err != nil {
				g.logf("[gateway] sandbox 开关变化：重连外部 MCP %q 失败：%v", ps.entry.SandboxName(), err)
			} else {
				g.logf("[gateway] sandbox 开关变化：重连外部 MCP %q（rebuilt=%v）", ps.entry.SandboxName(), rebuilt)
			}
		}
	}
}

// sandboxMCPOn 报告某外部 MCP（条目名）是否开启 agentbox 沙箱（读锁保护）。供 provider 在
// stdio spawn 时按 entry.Name 查询（见 provider.go buildConn）。
func (g *Gateway) sandboxMCPOn(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	g.tov.mu.RLock()
	defer g.tov.mu.RUnlock()
	return g.tov.sandboxMCPs[name]
}

// sandboxOnEntry 是 provider 侧查询回调（sandboxOnFunc）：按 entry 名查 usr `tool_sandbox.mcps`。
func (g *Gateway) sandboxOnEntry(entry *ServerEntry) bool {
	if entry == nil {
		return false
	}
	return g.sandboxMCPOn(entry.SandboxName())
}

// mergeLegacySandboxMCP 是**读取时迁移**：把旧 usr `mcps[].sandbox=true`（经 ServerEntry.Sandbox
// 透传，已废弃为开关来源）并入 tool_sandbox.mcps，使新模型下仍生效（新写入一律走 tool_sandbox.mcps）。
// 已显式配置该名 → 不覆盖（tool_sandbox 优先）。
func (g *Gateway) mergeLegacySandboxMCP(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	g.tov.mu.Lock()
	if g.tov.sandboxMCPs == nil {
		g.tov.sandboxMCPs = map[string]bool{}
	}
	if _, ok := g.tov.sandboxMCPs[name]; !ok {
		g.tov.sandboxMCPs[name] = true
	}
	g.tov.mu.Unlock()
}

// asyncOverride 取某暴露名的异步覆盖（未配置 → ok=false）。
func (g *Gateway) asyncOverride(exposed string) (ToolAsyncOverride, bool) {
	g.tov.mu.RLock()
	defer g.tov.mu.RUnlock()
	if len(g.tov.async) == 0 {
		return ToolAsyncOverride{}, false
	}
	ov, ok := g.tov.async[exposed]
	return ov, ok
}

// selfTouchFiles 是 **self 节点内置工具**中「涉及文件变动」的契约白名单（用户口径 2026-09-28）：
// `filesys_run` / `script_run` 会实际写盘。`browser_run` **不列入**（C-25）：它只在**显式传入**
// `fail_shot`/`dom_file`/`console_file` 或脚本内 SHT/DOM/DBG 重定向时才落盘，缺省（不带这些参数）
// 不写盘；按用户口径归「不涉及」——标错只让检查点粒度变粗（轮末补点仍在、git diff 校验仍生效），
// 不丢安全。其余内置工具（file_find/file_read/file_diff/web_fetch/desktop_run 等）同按既有口径处理。
var selfTouchFiles = map[string]bool{
	"filesys_run": true,
	"script_run":  true,
}

// resolveTouchFiles 解析某工具是否「涉及文件变动」（前置打点钩子的判定值；用户口径 2026-09-28）：
//
//  1. usr `tool_async` 显式 `touch_files`（`TouchFilesSet`）→ 直接采用；
//  2. 缺省按工具来源：
//     - **self 节点内置工具**（`entry.ID == "self"` 且 `Origin == builtin`）→ 白名单
//     `selfTouchFiles`（filesys_run / script_run 涉及，其余不涉及）；
//     - **dir 节点 / 第三方 / 无法判定**（entry 为空等）→ **保守按涉及**（不丢安全）。
//
// 标错只会让检查点粒度变粗（轮末补点仍在、`git diff` 一致性校验仍生效），不丢安全。
func (g *Gateway) resolveTouchFiles(exposed string, original string, entry *ServerEntry) bool {
	if ov, ok := g.asyncOverride(exposed); ok && ov.TouchFilesSet {
		return ov.TouchFiles
	}
	if entry != nil && entry.ID == "self" && normalizeOrigin(entry.Origin) == OriginBuiltin {
		return selfTouchFiles[original]
	}
	return true
}

// applyAsyncOverrideMeta 把异步覆盖写入 tools/list 的 `_meta` 副本（语义与
// chonkpilot-mcp-server/server.applyToolAsyncOverride 一致，保证 self 节点「已经过一次覆盖」
// 的 _meta 再应用同值时不产生差异）：
//   - mode=auto：契约/来源已显式声明 async → 压回 "auto"，否则维持「auto 不显式透出」；
//   - mode=never/always/manual：显式写入；
//   - threshold>0 → async-threshold（float64，与 in-memory 传输解码后的 JSON 数字同型）；
//   - hard_timeout **不写**（执行硬上限，仅 executor 消费，非 gateway 面）。
func applyAsyncOverrideMeta(meta map[string]any, ov ToolAsyncOverride) {
	switch {
	case ov.Mode == "auto":
		if _, ok := meta["async"]; ok {
			meta["async"] = "auto"
		} else {
			delete(meta, "async")
		}
	case ov.Mode != "":
		meta["async"] = ov.Mode
	}
	if ov.Threshold > 0 {
		meta["async-threshold"] = float64(ov.Threshold)
	}
}
