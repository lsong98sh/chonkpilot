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
//   - 沙箱：按提供方归属归一到 mcp-server 契约名后写入共享 Config（仅 builtin：self / dir 节点）。
//
// **零新增 MQ 主题**、**不改既有 payload 形状**（取值落在既有 tools/list `_meta` 内）。
package mcpgateway

import (
	"sync"
)

// ToolAsyncOverride 是工具级异步覆盖（mode + threshold），与 mcp-server 契约侧同形但**归
// gateway 所有**（RB-2：gateway lib 不依赖 chonkpilot-mcp-server 包；装配方负责从其类型转换）。
// 零值 = 该字段不覆盖（维持契约现值）；未配置的工具 = 无覆盖。`hard_timeout` 属执行硬上限
// （仅 executor 消费、不透出到 tools/list），不在 gateway 面。
type ToolAsyncOverride struct {
	Mode      string `json:"mode"`
	Threshold int    `json:"threshold"`
}

// SandboxConfig 是 dir 节点 / self **共享的执行配置**窄接口（RB-2：依赖倒置，同 ExecSink 手法）：
// gateway 只经它把工具级沙箱开关（契约名键位）写入执行侧，**不依赖具体 mcp-server 类型**。
// 实现方 = 装配方持有的 mcp-server 执行配置（方法签名与其 `SetToolSandbox` 一致）。
type SandboxConfig interface {
	SetToolSandbox(eff map[string]bool) bool
}

// toolOverrideState 保存覆盖表（读写锁保护；与 tools/list、doCall 并发读取）。
type toolOverrideState struct {
	mu      sync.RWMutex
	async   map[string]ToolAsyncOverride
	sandbox map[string]bool
}

// SetAsyncOverrides 注入工具级异步覆盖表（key = 暴露名）。nil/空 = 清空（回落契约现值）。
func (g *Gateway) SetAsyncOverrides(m map[string]ToolAsyncOverride) {
	g.tov.mu.Lock()
	g.tov.async = m
	g.tov.mu.Unlock()
}

// SetSandboxOverrides 注入工具级沙箱开关表（key = 暴露名），并**同时重算**执行侧 Config 的
// 契约名开关表（仅 builtin 提供方：self / dir 节点；第三方无 executor 可施加，跳过）。
func (g *Gateway) SetSandboxOverrides(m map[string]bool) {
	g.tov.mu.Lock()
	g.tov.sandbox = m
	g.tov.mu.Unlock()
	g.applySandboxToConfig()
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

// sandboxEnabled 取某暴露名的沙箱开关（未配置 → false）。
func (g *Gateway) sandboxEnabled(exposed string) bool {
	g.tov.mu.RLock()
	defer g.tov.mu.RUnlock()
	return g.tov.sandbox[exposed]
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

// applySandboxToConfig 把暴露名开关表归一到 mcp-server 执行配置的契约名键位：
//
//   - 仅 **builtin in-memory 提供方**（self 节点 / dir 目录节点——执行经本仓 spawn 的
//     chonkpilot-mcp-tools executor）可施加 agentbox；第三方（spawned/proxied）工具在第三方
//     进程内执行，无法注入，跳过（server 级 `mcps[].sandbox` 另论）；
//   - self 节点 handler 的契约名查找 = `self_<契约名>`（SandboxPolicyFor 首选键），故写该键；
//   - dir 节点 handler 与 self **共用同一份 Config**（Params.MCPConfig），契约名空间相同 →
//     若某契约名同时存在于 self 与 dir，则无法用单个键位区分（SandboxPolicyFor 只接受契约名）
//     → **跳过该 dir 契约**（保守：宁可不隔离，不误隔离 self；已在报告中列为共名限制）；
//   - 其余 dir 契约写 `<契约名>`（self 无同名契约 → 不会误命中 self）。
func (g *Gateway) applySandboxToConfig() {
	cfg := g.params.MCPConfig
	if cfg == nil {
		return // 无共享执行配置（独立 gateway/单测）：无执行侧可施加
	}
	g.tov.mu.RLock()
	sb := g.tov.sandbox
	g.tov.mu.RUnlock()

	selfContracts := map[string]bool{}
	dirOn := []string{}
	eff := map[string]bool{}
	for _, rt := range g.reg.routesAll() {
		ps, ok := g.reg.provider(rt.Provider)
		if !ok || ps.prov == nil {
			continue
		}
		mnp, ok := ps.prov.(*memNodeProvider)
		if !ok || !mnp.inject {
			continue // 非 builtin in-memory（第三方）→ 无本仓 executor，跳过
		}
		isDir := ps.entry != nil && ps.entry.Category == "dir"
		if !isDir {
			// self（或其它 builtin memNode）：先收集契约名空间，稍后按开关写 self_ 键
			selfContracts[rt.Original] = true
			continue
		}
		if sb[rt.Name] {
			dirOn = append(dirOn, rt.Original)
		}
	}
	// self：按暴露名命中 → 写 self_<契约名>（仅写 true；未开关不写键，默认不隔离）
	for _, rt := range g.reg.routesAll() {
		ps, ok := g.reg.provider(rt.Provider)
		if !ok || ps.prov == nil {
			continue
		}
		mnp, ok := ps.prov.(*memNodeProvider)
		if !ok || !mnp.inject || (ps.entry != nil && ps.entry.Category == "dir") {
			continue
		}
		if sb[rt.Name] {
			eff["self_"+rt.Original] = true
		}
	}
	// dir：跳过与 self 共名的契约（无法区分，保守不隔离）
	for _, contract := range dirOn {
		if selfContracts[contract] {
			continue
		}
		eff[contract] = true
	}
	cfg.SetToolSandbox(eff)
}
