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
//   - 沙箱：**executor 级**（key = 类别 core/desktop/browser，2026-09-26 起）→ 直接写入共享
//     Config（category 全局，无需按提供方归属归一）。
//
// **零新增 MQ 主题**、**不改既有 payload 形状**（取值落在既有 tools/list `_meta` 内）。
package mcpgateway

import (
	"sync"
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

// SandboxConfig 是 dir 节点 / self **共享的执行配置**窄接口（RB-2：依赖倒置，同 ExecSink 手法）：
// gateway 只经它把 executor 级沙箱开关（类别键位 core/desktop/browser）写入执行侧，
// **不依赖具体 mcp-server 类型**。实现方 = 装配方持有的 mcp-server 执行配置
// （方法签名与其 `SetToolSandbox` 一致）。
type SandboxConfig interface {
	SetToolSandbox(eff map[string]bool) bool
}

// toolOverrideState 保存覆盖表（读写锁保护；与 tools/list、doCall 并发读取）。
type toolOverrideState struct {
	mu      sync.RWMutex
	async   map[string]ToolAsyncOverride
	sandbox map[string]bool // key = executor 类别（core/desktop/browser）
}

// SetAsyncOverrides 注入工具级异步覆盖表（key = 暴露名）。nil/空 = 清空（回落契约现值）。
func (g *Gateway) SetAsyncOverrides(m map[string]ToolAsyncOverride) {
	g.tov.mu.Lock()
	g.tov.async = m
	g.tov.mu.Unlock()
}

// SetSandboxOverrides 注入 **executor 级**沙箱开关表（key = executor 类别 core/desktop/browser），
// 并写入共享执行配置（`SandboxConfig.SetToolSandbox`）。
//
// 语义（2026-09-26 决策：沙箱由工具级改为 executor 级）：开关按**工具所属 executor 类别**生效
// （= 契约 `_meta.category`），仅本仓 spawn 的 builtin executor 消费；第三方（spawned/proxied）
// MCP 在第三方进程内执行、无本仓 executor → 本表不施加（其沙箱另见 usr `mcps[].sandbox`，
// 仅 stdio 且须第三方实现 agentbox 消费方）。**旧形态（按工具暴露名）不再生效**。
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

// applySandboxToConfig 把 executor 级开关表写入**共享执行配置**（key 已是 executor 类别，
// 无需按提供方归属归一——类别是全局的，只有本仓 spawn 的 executor 会消费）。
// 无共享执行配置（独立 gateway / 单测）时无执行侧可施加，直接返回。
func (g *Gateway) applySandboxToConfig() {
	cfg := g.params.MCPConfig
	if cfg == nil {
		return
	}
	g.tov.mu.RLock()
	sb := g.tov.sandbox
	g.tov.mu.RUnlock()
	cfg.SetToolSandbox(sb) // nil/空 → 清空（= 全部 executor 不隔离）
}
