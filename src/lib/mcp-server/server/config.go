// Package server 是 chonkpilot-mcp-server 的可复用核心：
// 把契约目录（tools/prompts/skills/resources）注册到装配方自建的官方 mcp.Server 上
// （lib 形态：RegisterContracts(srv, root, cfg)，不做容器/注册表/传输层）；
// 工具调用经 callTool spawn chonkpilot-mcp-tools 分类 executor exe（--input 临时文件 + stdout 统一 JSON）。
// 独立 exe 形态提供 stdio / Streamable HTTP（端点 /mcp）/ Windows 服务。
//
// 独立 module 的公开包，可被其他程序（如 chonkpilot-server）import 后内嵌启动。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

// ToolAsyncOverride 是**单个工具**的用户级异步配置覆盖（usr 键 `tool_async` 的一个值；
// 形状见 64-配置项一览 §3）= `{"<工具暴露名>": {"mode": "always|never|auto|manual",
// "threshold": 30, "hard_timeout": 300}}`。
//
//   - mode → 暴露 `_meta.async`（四档：always=仅异步 / never=仅同步 / auto=自动异步+超时 /
//     manual=手动异步）；
//   - threshold（秒）→ 暴露 `_meta.async-threshold`（auto/manual 档的超时转后台点）；
//   - hard_timeout（秒）→ **不透出**，仅作 executor 执行硬杀上限（见 executor.go resolveExecTimeout）；
//   - cancel_on_timeout（秒）→ **不透出**，⑤ 超时自动取消（gateway `doCall` 消费：到超时点直接取消，
//     不等用户裁决；>0 生效、默认 0 = 不取消；用户口径 2026-10-07，见 18 §7 B1/B4）；
//   - touch_files（bool）→ **不透出**，供 gateway 前置打点钩子（§5.4a）判定该工具是否「涉及文件变动」
//     （`false` = 不涉及 → `plugin-history` 直接放行、不打检查点，省 8–9 次 git 进程）。
//
// 取值语义（用户口径，2026-09-27 / 2026-09-28 / 2026-10-07）：
//   - 字段**未设置**（键缺失 / `null` / 空串 / 非数字）→ 该字段不覆盖（维持契约现值/回落全局）；
//   - `*Set` = 该字段是否被显式设置（区分 0/-1 与「未设置」；touch_files 区分 true/false 与「未设置」）；
//   - threshold / hard_timeout 显式 **0 或 -1 = 无上限**（不设点 / 不杀，永远等，用户可取消）；
//   - cancel_on_timeout 显式 **> 0 才生效**（0 / 负数 = 不取消）；
//   - touch_files 未设置 → 由 gateway 按工具来源取缺省（self 内置仅 filesys_run/script_run 涉及，
//     其余内置不涉及；dir 节点 / 第三方保守按涉及）。
type ToolAsyncOverride struct {
	Mode               string `json:"mode"`
	Threshold          int    `json:"threshold"`
	HardTimeout        int    `json:"hard_timeout"`
	TouchFiles         bool   `json:"touch_files"`
	CancelOnTimeout    int    `json:"cancel_on_timeout"`
	ThresholdSet       bool   `json:"-"` // threshold 是否显式设置（0/-1 = 无上限）
	HardTimeoutSet     bool   `json:"-"` // hard_timeout 是否显式设置（0/-1 = 无上限）
	TouchFilesSet      bool   `json:"-"` // touch_files 是否显式设置（区分 false 与「未设置」）
	CancelOnTimeoutSet bool   `json:"-"` // cancel_on_timeout 是否显式设置
}

// UnmarshalJSON 容错解析单个覆盖项：字段缺省 / `null` / 空串 / 非数字 → 视为**未设置**
// （不报错、不影响其它字段）；数字或数值字符串 → 采用并标记 Set。
// 这样 `{"hard_timeout": 0}` 与 `{"hard_timeout": null}` 得以区分（前者 = 无上限）。
func (o *ToolAsyncOverride) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["mode"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			o.Mode = s
		}
	}
	o.Threshold, o.ThresholdSet = optIntField(raw["threshold"])
	o.HardTimeout, o.HardTimeoutSet = optIntField(raw["hard_timeout"])
	o.CancelOnTimeout, o.CancelOnTimeoutSet = optIntField(raw["cancel_on_timeout"])
	o.TouchFiles, o.TouchFilesSet = optBoolField(raw["touch_files"])
	return nil
}

// optBoolField 解析可选布尔字段：缺省/null/空串/非布尔 → (false, false)；
// bool 或 "true"/"false" 字符串 → (值, true)。用于区分 `touch_files:false` 与「未设置」。
func optBoolField(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

// optIntField 解析可选整数字段：缺省/null/空串/非数字 → (0, false)；数字（含数值字符串）→ (n, true)。
func optIntField(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, false
		}
		if v, err := strconv.Atoi(s); err == nil {
			return v, true
		}
	}
	return 0, false
}

// toolAsyncModes 是合法 mode 取值（四档；其它值 = 非法 → 丢字段）。
var toolAsyncModes = map[string]bool{"always": true, "never": true, "auto": true, "manual": true}

// Defaults 是注入 tools/call 的默认参数（server 级配置，不依赖 NATS）。
// 客户端显式传入的同名参数优先（覆盖默认）。
type Defaults struct {
	// 递归过滤（file_find / file_manager 的递归遍历与 file_pattern 批量操作）
	SkipDirs    []string `json:"skip_dirs,omitempty"`
	IgnoreFiles []string `json:"ignore_files,omitempty"`
	// file_find 的 grep 限定文本扩展名
	FileExt []string `json:"fileext,omitempty"`
}

// Config 是 mcp-server 执行配置。
// 注意：不承载执行器目录——契约 runtime（exe 型）按契约文件所在目录相对解析（resolveRuntime），
// 解释器走 PATH；契约根由 RegisterContracts 的 root 参数指定（调用方/启动参数决定）。
type Config struct {
	Root           string            `json:"root"`            // 契约扫描根
	TimeoutSec     int               `json:"timeout_sec"`     // tools/call 执行超时（秒）
	MaxConcurrency int               `json:"max_concurrency"` // 并发执行上限
	Interpreters   map[string]string `json:"interpreters"`    // runtime → 解释器绝对路径（spawn executor 时注入 CHONKPILOT_INTERPRETERS，供 script_run 解析）
	ChromePath     string            `json:"chrome_path"`     // 浏览器可执行文件路径（spawn 子进程注入环境变量 CHONK_CHROME）
	// Toolchain 是能力原语 prompt/skill 文本里 {{toolchain.<key>}} 的取值表（key 见
	// ToolchainKeys；装配层 instance-register 后由 usr 配置注入，见 chonkpilot-server
	// loadExecConfig）。空 = 全部按未配置处理（已知 key 替换为空串）。
	Toolchain map[string]string `json:"toolchain"`
	Defaults  Defaults          `json:"defaults"`
	// ToolAsync 是**工具级异步配置覆盖**（usr 键 `tool_async`，装配层读后注入；见
	// chonkpilot-llm/server/server.go loadExecConfig）：key = tools/list 暴露名
	// （内嵌 self 节点 = self_<契约名>；独立 exe = 契约名），value = 四档 mode + 可选
	// threshold / hard_timeout。空表 = 全部按契约现值（默认行为不变）。
	ToolAsync map[string]ToolAsyncOverride `json:"-"`

	// SkipTools（P6 2026-10-19）为 true 时 RegisterContracts 只注册资产（prompts/skills/resources），
	// **不注册工具**：系统级 app 根的可执行工具改由外部进程（chonkpilot-self-tools）经插件桥注册/执行。
	// 仅影响同一 cfg 上的 RegisterContracts 调用；dir 节点扫描经 RegisterContractsWithTools 强制包含工具。
	SkipTools bool `json:"-"`

	// BridgeAddr / BridgeToken（2026-10-19）：插件桥地址与 HTTP token（域/元工具经 plugin/v1.*
	// 取能力）。2026-10-21（L3）起 meta/domain 工具进程内执行，由 toolmeta.Run 直接消费，不再注入子进程。空 = 不连接。
	BridgeAddr  string `json:"-"`
	BridgeToken string `json:"-"`
	// ToolSandbox 是 agentbox 沙箱开关（usr 键 `tool_sandbox`，装配层注入；见
	// chonkpilot-llm/server/server.go applyToolSandbox）。**管理单位 = 被 spawn 的执行体**：
	//   - Executors：内置 executor，key = executor **exe 基名**（见 ExecutorExe* 常量）；
	//   - MCPs：外部 MCP，key = MCP 条目名。
	// value = 是否对该执行体施加限制（越界读写一律拒绝）。**未配置 / false = 不启用
	// 隔离**（缺省与现状一致）。**旧形态（按 executor 类别 core/desktop/browser）读取时迁移**，
	// 见 ParseToolSandbox。
	// 2026-10-21：沙箱改由 gateway 在 spawn 时以进程级 Low IL 施加；本开关暂保留待 L6 迁移。
	ToolSandbox ToolSandboxConfig `json:"-"`

	// mu 保护运行期可被覆盖的字段（SetRuntime：装配层 instance-register 后注入 usr/prj 配置）；
	// limiter 为并发限流器（RegisterContracts 懒建，上限随 MaxConcurrency 运行期调整）。
	mu      sync.RWMutex
	limiter *limiter
}

// DefaultConfig 返回内置默认配置（未指定 --config 时使用）。
func DefaultConfig() *Config {
	return &Config{
		TimeoutSec:     300,
		MaxConcurrency: 16,
		Defaults: Defaults{
			SkipDirs:    []string{".git", ".svn", "node_modules", ".trae", ".chonkpilot", "__pycache__", ".venv", "venv", "build", "dist", ".next", ".nuxt"},
			IgnoreFiles: nil,
			FileExt:     nil,
		},
	}
}

// Apply 用配置 JSON 覆盖默认（浅合并：仅非空字段生效）。
func (c *Config) Apply(raw []byte) error {
	var patch struct {
		Root           *string           `json:"root"`
		TimeoutSec     *int              `json:"timeout_sec"`
		MaxConcurrency *int              `json:"max_concurrency"`
		Interpreters   map[string]string `json:"interpreters"`
		ChromePath     *string           `json:"chrome_path"`
		Toolchain      map[string]string `json:"toolchain"`
		Defaults       *Defaults         `json:"defaults"`
	}
	if err := json.Unmarshal(raw, &patch); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if patch.Root != nil {
		c.Root = *patch.Root
	}
	if patch.TimeoutSec != nil && *patch.TimeoutSec > 0 {
		c.TimeoutSec = *patch.TimeoutSec
	}
	if patch.MaxConcurrency != nil && *patch.MaxConcurrency > 0 {
		c.MaxConcurrency = *patch.MaxConcurrency
		if c.limiter != nil {
			c.limiter.setLimit(*patch.MaxConcurrency)
		}
	}
	if patch.Interpreters != nil {
		c.Interpreters = patch.Interpreters
	}
	if patch.ChromePath != nil && *patch.ChromePath != "" {
		c.ChromePath = *patch.ChromePath
	}
	if patch.Toolchain != nil {
		c.Toolchain = patch.Toolchain
	}
	if patch.Defaults != nil {
		if patch.Defaults.SkipDirs != nil {
			c.Defaults.SkipDirs = patch.Defaults.SkipDirs
		}
		if patch.Defaults.IgnoreFiles != nil {
			c.Defaults.IgnoreFiles = patch.Defaults.IgnoreFiles
		}
		if patch.Defaults.FileExt != nil {
			c.Defaults.FileExt = patch.Defaults.FileExt
		}
	}
	return nil
}

// SetRuntime 运行期覆盖执行配置（装配层在 instance-register 后注入 usr/prj 配置；
// 见 36-配置 §3.1）：仅 >0 / 非空项生效，其余保留原值。
// 加写锁，避免与 tools/call 的读取（defaultsMap/execTimeout/executorEnv）构成 data race。
func (c *Config) SetRuntime(timeoutSec, maxConcurrency int, interpreters map[string]string, chromePath string, skipDirs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if timeoutSec > 0 {
		c.TimeoutSec = timeoutSec
	}
	if maxConcurrency > 0 {
		c.MaxConcurrency = maxConcurrency
		if c.limiter != nil {
			c.limiter.setLimit(maxConcurrency)
		}
	}
	if len(interpreters) > 0 {
		c.Interpreters = interpreters
	}
	if chromePath != "" {
		c.ChromePath = chromePath
	}
	if len(skipDirs) > 0 {
		c.Defaults.SkipDirs = skipDirs
	}
}

// SetToolchain 运行期覆盖 prompt 占位符 {{toolchain.<key>}} 的取值表（装配层在
// instance-register 后由 usr 配置注入；key 见 ToolchainKeys）。nil/空表 = 不覆盖（保留原值）。
// 加写锁，避免与 prompts/get 渲染读取（toolchainVars）构成 data race。
func (c *Config) SetToolchain(vars map[string]string) {
	if len(vars) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Toolchain = vars
}

// toolchainVars 返回占位符取值表副本（读锁保护；未配置 → nil，等价"全部未配置"）。
func (c *Config) toolchainVars() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.Toolchain) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.Toolchain))
	for k, v := range c.Toolchain {
		out[k] = v
	}
	return out
}

// SetToolAsync 运行期覆盖工具级异步配置（装配层读 usr 键 `tool_async` 后注入，见
// chonkpilot-llm/server/server.go loadExecConfig）。传入 nil/空表 = **清空全部覆盖**
// （恢复契约现值；前端「恢复默认」= 删该工具键项 → 覆盖表收敛为无该项）。
// 与 SetToolchain 的「空值不覆盖」语义**不同**：本表是整体替换，删除即回落契约。
// 归一化（非法 mode / 非正 threshold·hard_timeout / 空键）在此完成并记日志（见 normalizeToolAsync）。
// 返回**有效覆盖表是否变化**（调用方据此决定是否触发契约重注册热生效，避免无谓抖动）。
// 加写锁，避免与 buildTool（暴露 _meta）/ callTool（读硬上限）读取构成 data race。
func (c *Config) SetToolAsync(raw map[string]ToolAsyncOverride) bool {
	next := NormalizeToolAsync(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	if sameToolAsync(c.ToolAsync, next) {
		return false
	}
	c.ToolAsync = next
	return true
}

// NormalizeToolAsync 归一覆盖表（boundary：非法 mode → **忽略该字段**并记日志，
// 不整体拒绝、不影响其它键项）：
//   - key 去空白；空键 → 丢整项（无法定位工具）；
//   - mode 去空白并小写；空 = 不覆盖；非法取值 → 丢 mode 字段；
//   - threshold / hard_timeout：**显式设置即保留**（含 0 / -1 = **无上限**；`Set` 标记区分
//     它与「未设置」）；未设置（键缺失/null/空串/非数字）→ 不覆盖该字段；
//   - touch_files：**显式设置即保留**（true = 涉及 / false = 不涉及；`TouchFilesSet` 区分它与「未设置」）；
//   - cancel_on_timeout：**显式设置即保留**（含 0；`CancelOnTimeoutSet` 区分它与「未设置」；仅 > 0 生效）；
//   - 五项皆未设置 → 丢整项；结果为空 → nil（= 无覆盖，便于比较）。
//
// 注：配置引用的工具不存在（孤儿键）**不报错**——查表时自然不命中（调用点不校验工具面，
// 因为第三方/目录节点工具与内嵌 mcp-server 的工具面不必一致）。
//
// 兼容：直接构造的 `ToolAsyncOverride`（不走 JSON）未置 `Set` 标记时，按「非零值 = 已设置」
// 兜底（正数覆盖照旧生效）；
//
// 导出（2026-09-19，I-82）：装配层（chonkpilot-llm/server）解析 usr `tool_async` 后需把**同一份
// 归一结果**下发给 gateway（工具级覆盖下沉），避免两处各归一出现口径差。
func NormalizeToolAsync(raw map[string]ToolAsyncOverride) map[string]ToolAsyncOverride {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]ToolAsyncOverride, len(raw))
	for name, ov := range raw {
		name = strings.TrimSpace(name)
		if name == "" {
			log.Printf("[mcp-server] tool_async: 忽略空工具名项")
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(ov.Mode))
		if mode != "" && !toolAsyncModes[mode] {
			log.Printf("[mcp-server] tool_async[%s]: 非法 mode=%q（取值 always/never/auto/manual），忽略该字段", name, ov.Mode)
			mode = ""
		}
		eff := ToolAsyncOverride{Mode: mode}
		if ov.ThresholdSet || ov.Threshold != 0 {
			eff.Threshold = ov.Threshold
			eff.ThresholdSet = true
		}
		if ov.HardTimeoutSet || ov.HardTimeout != 0 {
			eff.HardTimeout = ov.HardTimeout
			eff.HardTimeoutSet = true
		}
		if ov.TouchFilesSet || ov.TouchFiles {
			eff.TouchFiles = ov.TouchFiles
			eff.TouchFilesSet = true
		}
		if ov.CancelOnTimeoutSet || ov.CancelOnTimeout != 0 {
			eff.CancelOnTimeout = ov.CancelOnTimeout
			eff.CancelOnTimeoutSet = true
		}
		if eff.Mode == "" && !eff.ThresholdSet && !eff.HardTimeoutSet && !eff.TouchFilesSet && !eff.CancelOnTimeoutSet {
			continue // 无有效覆盖 → 不登记（等价未配置）
		}
		out[name] = eff
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sameToolAsync 比较两张覆盖表是否等价（ToolAsyncOverride 字段全为可比值，无需 reflect）。
func sameToolAsync(a, b map[string]ToolAsyncOverride) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// toolAsyncOverride 取某工具的有效覆盖（读锁保护）。
// name = **契约名**（buildTool 的原语名 / ToolDoc.Name）；查找顺序：
// 历史暴露名 `<self_><契约名>`（P0a 前的 UI 存量键）→ 契约名精确匹配（P0a 后的现键）。
// 未配置（含孤儿键/第三方工具键）→ ok=false（调用点维持契约现值）。
func (c *Config) toolAsyncOverride(name string) (ToolAsyncOverride, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.ToolAsync) == 0 {
		return ToolAsyncOverride{}, false
	}
	if ov, ok := c.ToolAsync["self_"+name]; ok {
		return ov, true
	}
	ov, ok := c.ToolAsync[name]
	return ov, ok
}

// ─── 工具沙箱开关（决策 42 §2 (104)/(109)）────────────────

// 内置 executor 产物名（= 开关在 `executors` 下的单位键；与 src/mcp-tools 产物一致）。
// 2026-10-21（GATEWAY-2/L1）：DSL 退役 —— 删 core / desktop / browser / dsl 四个 executor；
// desktop/browser 改为常驻插件（非 executor，不在本表）。
// 2026-10-25（PLUGIN-SPLIT-1）：file 域 11 工具随 file 插件迁出主仓 → 删 tools-executor。
const (
	ExecutorExeScript = "chonkpilot-script-executor.exe"
	ExecutorExeWeb    = "chonkpilot-web-executor.exe"
)

// ToolSandboxConfig 是 agentbox 沙箱开关（usr 键 `tool_sandbox`，**管理单位 = 被 spawn 的执行体**）：
//   - Executors：内置 executor，key = executor **exe 基名**（chonkpilot-*-executor.exe）；
//   - MCPs：外部 MCP（gateway stdio spawn），key = MCP 条目名。
//
// value = 是否对该执行体施加 agentbox 限制。**未配置 / false = 不启用隔离**（默认兼容）。
type ToolSandboxConfig struct {
	Executors map[string]bool `json:"executors"`
	MCPs      map[string]bool `json:"mcps"`
}

// legacySandboxCategoryMap 把**旧形状**（按 executor 类别 core/desktop/browser，2026-09-26 形态）
// 迁移到**新形状**的单位键（exe 基名）。2026-10-21（GATEWAY-2/L1）DSL 退役后：
// `desktop` / `browser` 已改为插件（非 executor），迁移为空；
// 2026-10-25（PLUGIN-SPLIT-1）`core`（= file 域）随 file 插件迁出主仓 → 亦迁移为空
// （旧配置里这些项被忽略，插件自身不接 gateway、不受 agentbox 开关约束）。
var legacySandboxCategoryMap = map[string][]string{}

// ParseToolSandbox 解析 usr `tool_sandbox` 值（JSON 对象字符串），**兼容新旧两种形状**：
//   - 新形状：`{"executors":{"<exe基名>":bool},"mcps":{"<mcp名>":bool}}`；
//   - 旧形状：`{"core":bool,"desktop":bool,"browser":bool}`（2026-09-26 executor 类别形态）→
//     **读取时迁移**（见 legacySandboxCategoryMap），不报错、不停机；
//   - 空串 → 空配置（= 全部不隔离）；非法 JSON → 返回错误（调用方保留现值、不误清空）。
func ParseToolSandbox(raw string) (ToolSandboxConfig, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ToolSandboxConfig{}, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return ToolSandboxConfig{}, fmt.Errorf("tool_sandbox: JSON 解析失败: %w", err)
	}
	if _, ok := probe["executors"]; ok {
		return decodeNewToolSandbox([]byte(s))
	}
	if _, ok := probe["mcps"]; ok {
		return decodeNewToolSandbox([]byte(s))
	}
	// 旧形状 → 迁移为单位键（未知键按旧—旧形态忽略）。
	var legacy map[string]bool
	if err := json.Unmarshal([]byte(s), &legacy); err != nil {
		return ToolSandboxConfig{}, err
	}
	ex := map[string]bool{}
	for cat, on := range legacy {
		for _, exe := range legacySandboxCategoryMap[strings.ToLower(strings.TrimSpace(cat))] {
			ex[exe] = on
		}
	}
	return ToolSandboxConfig{Executors: ex}, nil
}

// decodeNewToolSandbox 解析新形状 `{"executors":{...},"mcps":{...}}`。
func decodeNewToolSandbox(data []byte) (ToolSandboxConfig, error) {
	var v struct {
		Executors map[string]bool `json:"executors"`
		MCPs      map[string]bool `json:"mcps"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return ToolSandboxConfig{}, err
	}
	return ToolSandboxConfig{Executors: v.Executors, MCPs: v.MCPs}, nil
}

// SetToolSandbox 运行期覆盖 agentbox 沙箱开关（装配层读 usr 键 `tool_sandbox` 后注入）。
// executors = 内置 executor 开关（key = exe 基名）；mcps = 外部 MCP 开关（key = MCP 名）。
// 传入空 = 清空（= 全部执行体不隔离，默认兼容）；返回开关表是否变化。
// 归一化：key 去空白，空键丢弃；显式 false 项保留（语义等同缺省）。
func (c *Config) SetToolSandbox(executors, mcps map[string]bool) bool {
	next := ToolSandboxConfig{
		Executors: normalizeSandboxMap(executors, "executor"),
		MCPs:      normalizeSandboxMap(mcps, "mcp"),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sameToolSandbox(c.ToolSandbox, next) {
		return false
	}
	c.ToolSandbox = next
	return true
}

// normalizeSandboxMap 归一单张开关表：key 去空白，空键丢弃并记日志；空表 → nil。
func normalizeSandboxMap(raw map[string]bool, kind string) map[string]bool {
	out := map[string]bool{}
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			log.Printf("[mcp-server] tool_sandbox: 忽略空%s项", kind)
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sameToolSandbox 比较两张开关表是否等价（executors + mcps 两张子表）。
func sameToolSandbox(a, b ToolSandboxConfig) bool {
	return sameBoolMap(a.Executors, b.Executors) && sameBoolMap(a.MCPs, b.MCPs)
}

// sameBoolMap 比较两张 map[string]bool 是否等价。
func sameBoolMap(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// SetContext 已移除（决策 R-11 二次升级）：实例上下文不再经 server 级配置注入 tool arguments，
// 改为每次调用经协议 _meta（CallContext）从调用链透传，见 server.go/callContextFromMeta。

// execTimeout 返回 tools/call 执行超时秒数（读锁保护，运行期可被覆盖）。
func (c *Config) execTimeout() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.TimeoutSec
}

// chromeEnv 返回 spawn 子进程需注入的 CHONK_CHROME 值（空 = 不注入；读锁保护）。
func (c *Config) chromeEnv() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ChromePath
}

// interpretersJSON 返回 runtime → 解释器绝对路径的 JSON 串（空 = 不注入 CHONKPILOT_INTERPRETERS）。
func (c *Config) interpretersJSON() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.Interpreters) == 0 {
		return ""
	}
	b, err := json.Marshal(c.Interpreters)
	if err != nil {
		return ""
	}
	return string(b)
}

// defaultsMap 把默认配置转为注入 tools/call 的参数 map（仅非空）。
// 说明（R-11 二次升级）：不再注入 _workdir/_datadir/_instance/_interpreters——实例上下文经
// 调用上下文（协议 _meta）传递，解释器经 spawn executor 的子进程环境 CHONKPILOT_INTERPRETERS 传递。
func (c *Config) defaultsMap() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := map[string]any{}
	if len(c.Defaults.SkipDirs) > 0 {
		m["skip_dirs"] = c.Defaults.SkipDirs
	}
	if len(c.Defaults.IgnoreFiles) > 0 {
		m["ignore_files"] = c.Defaults.IgnoreFiles
	}
	if len(c.Defaults.FileExt) > 0 {
		m["fileext"] = c.Defaults.FileExt
	}
	return m
}

// executorEnv 组装 spawn executor 的子进程环境：继承宿主环境（剔除残留的 CHONKPILOT_* 上下文与
// 宿主 CHONK_CHROME，避免泄漏第三方/上层上下文或与本次注入形成重复键）→ 注入本次调用的
// CHONKPILOT_INSTANCE/WORKDIR/DATADIR + 配置型 CHONKPILOT_INTERPRETERS / CHONK_CHROME。
// **仅内部 executor 进程可见**（第三方 MCP server 不经我们 spawn，拿不到）。
//
// 2026-10-21（L3）：meta/domain 控制面工具已进程内执行（toolmeta），其会话/轮次/call id /
// purpose / 任务树 / 插件桥地址令牌不再需要注入子进程，故只保留 script/web 两个 executor
// 所需内容（instance/workdir/datadir/解释器/chrome）。
func (c *Config) executorEnv(cx CallContext) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+8)
	// chrome = 本次将注入的 CHONK_CHROME（TrimSpace 后为 "" 则不注入）。剔除判据与注入判据统一用
	// TrimSpace：避免纯空白 ChromePath「剔了旧值却不回注」的破坏性不对称。仅当会回注同名键时才剔除
	// 宿主旧值；否则保留宿主继承，兑现文档承诺的「设 CHONK_CHROME 手动覆盖」。
	chrome := strings.TrimSpace(c.chromeEnv())
	for _, kv := range base {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		// 剔除宿主残留：CHONKPILOT_* 上下文（大小写不敏感，Windows 环境变量名不区分大小写，避免
		// 重复键）；CHONK_CHROME 仅当本次会回注同名键时剔除（否则形成重复键 / 透传宿主旧值）。
		if strings.HasPrefix(strings.ToUpper(name), "CHONKPILOT_") {
			continue
		}
		if chrome != "" && strings.EqualFold(name, "CHONK_CHROME") {
			continue
		}
		out = append(out, kv)
	}
	add := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			out = append(out, k+"="+v)
		}
	}
	add("CHONKPILOT_INSTANCE", cx.InstanceID)
	add("CHONKPILOT_WORKDIR", cx.WorkDir)
	add("CHONKPILOT_DATADIR", cx.DataDir)
	add("CHONKPILOT_INTERPRETERS", c.interpretersJSON())
	add("CHONK_CHROME", chrome)
	return out
}

// bridge 返回插件桥地址 / HTTP token（读锁保护；进程内 meta/domain 执行使用）。
func (c *Config) bridge() (addr, token string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.BridgeAddr, c.BridgeToken
}

// SetBridge 注入插件桥地址 / HTTP token（进程内 meta/domain 工具经 plugin/v1/* 取能力）。
func (c *Config) SetBridge(addr, token string) {
	c.mu.Lock()
	c.BridgeAddr, c.BridgeToken = addr, token
	c.mu.Unlock()
}

// RuntimeConfig 是可在运行期从宿主推送给 core-mcp-server 的执行配置快照
// （2026-10-19：core 独立进程后，usr/prj 配置由 server 经 /internal/runtime 推送）。
type RuntimeConfig struct {
	TimeoutSec     int                          `json:"timeout_sec"`
	MaxConcurrency int                          `json:"max_concurrency"`
	Interpreters   map[string]string            `json:"interpreters,omitempty"`
	ChromePath     string                       `json:"chrome_path,omitempty"`
	SkipDirs       []string                     `json:"skip_dirs,omitempty"`
	ToolAsync      map[string]ToolAsyncOverride `json:"tool_async,omitempty"`
	ToolSandbox    ToolSandboxConfig            `json:"tool_sandbox,omitempty"`
}

// ApplyRuntime 应用运行期配置快照（core-mcp-server /internal/runtime）。
func (c *Config) ApplyRuntime(rc RuntimeConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rc.TimeoutSec > 0 {
		c.TimeoutSec = rc.TimeoutSec
	}
	if rc.MaxConcurrency > 0 {
		c.MaxConcurrency = rc.MaxConcurrency
		if c.limiter != nil {
			c.limiter.setLimit(rc.MaxConcurrency)
		}
	}
	if rc.Interpreters != nil {
		c.Interpreters = rc.Interpreters
	}
	if rc.ChromePath != "" {
		c.ChromePath = rc.ChromePath
	}
	if len(rc.SkipDirs) > 0 {
		c.Defaults.SkipDirs = rc.SkipDirs
	}
	if rc.ToolAsync != nil {
		c.ToolAsync = rc.ToolAsync
	}
	c.ToolSandbox = rc.ToolSandbox
}

// RuntimeSnapshot 返回当前运行期配置快照（server 推送用；读锁保护）。
func (c *Config) RuntimeSnapshot() RuntimeConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return RuntimeConfig{
		TimeoutSec:     c.TimeoutSec,
		MaxConcurrency: c.MaxConcurrency,
		Interpreters:   c.Interpreters,
		ChromePath:     c.ChromePath,
		SkipDirs:       c.Defaults.SkipDirs,
		ToolAsync:      c.ToolAsync,
		ToolSandbox:    c.ToolSandbox,
	}
}

// ─── 并发限流器（运行期可调整上限）────────────────────

// limiter 是 tools/call 的并发限流器：上限来自 Config.MaxConcurrency，可被 prj
// max_concurrency 运行期覆盖（SetRuntime/Apply → setLimit）。
// 实现 = 互斥量保护的**在跑计数** + 通知通道（释放/换限时 close 并重建以广播唤醒等待者），
// 故 acquire 可经 select 感知请求 ctx 的取消/超时（排队阶段即被取消，B3）。
type limiter struct {
	mu    sync.Mutex
	limit int
	cur   int           // 当前在跑数（受 mu 保护）
	ch    chan struct{} // 名额可能释放的通知信号（close 即广播，随后重建）
}

// newLimiter 构造限流器（limit <= 0 → 1，避免零容量阻塞）。
func newLimiter(limit int) *limiter {
	if limit <= 0 {
		limit = 1
	}
	return &limiter{limit: limit, ch: make(chan struct{})}
}

// ensureLimiter 懒建并返回限流器（RegisterContracts 调用；上限取当前 MaxConcurrency）。
func (c *Config) ensureLimiter() *limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limiter == nil {
		c.limiter = newLimiter(c.MaxConcurrency)
	}
	return c.limiter
}

// acquire 占用一个并发名额（达上限则等待）；请求 ctx 取消/超时 → 立即返回错误（不占名额）。
func (l *limiter) acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		if l.cur < l.limit {
			l.cur++
			l.mu.Unlock()
			return nil
		}
		ch := l.ch
		l.mu.Unlock()
		select {
		case <-ch: // 名额释放 / 上限调整 → 重试
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// release 释放一个并发名额（未持有时为安全空操作），并广播唤醒等待者。
func (l *limiter) release() {
	l.mu.Lock()
	if l.cur > 0 {
		l.cur--
	}
	l.notifyLocked()
	l.mu.Unlock()
}

// setLimit 调整并发上限（<=0 忽略）；在跑计数不变，仅改可并发额度并广播唤醒等待者。
// 相对于旧实现的「len(sem) 记账 + drain」：不再有换限期间的计数漂移（在跑数以独立计数为准）。
func (l *limiter) setLimit(n int) {
	if n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if n == l.limit {
		return
	}
	l.limit = n
	l.notifyLocked()
}

// notifyLocked 广播「名额可能释放」：close 当前通知通道并立即重建（调用方须持 mu）。
func (l *limiter) notifyLocked() {
	close(l.ch)
	l.ch = make(chan struct{})
}
