// Package server 是 chonkpilot-mcp-server 的可复用核心：
// 把契约目录（tools/prompts/skills/resources）注册到装配方自建的官方 mcp.Server 上
// （lib 形态：RegisterContracts(srv, root, cfg)，不做容器/注册表/传输层）；
// 工具调用经 callTool spawn chonkpilot-mcp-tools 分类 executor exe（--input 临时文件 + stdout 统一 JSON）。
// 独立 exe 形态提供 stdio / Streamable HTTP（端点 /mcp）/ Windows 服务。
//
// 独立 module 的公开包，可被其他程序（如 chonkpilot-server）import 后内嵌启动。
package server

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
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

	// SecurityDirs 是 agentbox 沙箱的**允许目录集**（prj `security-*`：一条 = {dir, writable}，
	// 语义 = 「可读 / 可写目录」且**递归**），装配层（chonkpilot-llm/server loadExecConfig）
	// 按实例读取后注入。空表 = 无允许目录（仅在 ToolSandbox 开启时才有意义：空集 = 全拒）。
	SecurityDirs []agentbox.Rule `json:"-"`
	// ToolSandbox 是**executor 级沙箱开关**（usr 键 `tool_sandbox`，装配层注入；见
	// chonkpilot-llm/server/server.go loadExecConfig）：key = executor 类别（契约
	// `_meta.category`：core / desktop / browser），value = 是否对该 executor 的**全部工具**
	// 施加 agentbox 限制（越界读写一律拒绝）。**未配置 / false = 不启用隔离**
	// （缺省与现状行为一致，不影响既有用户）。**旧形态（按工具暴露名）不再生效**。
	ToolSandbox map[string]bool `json:"-"`

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
// name = **契约名**（buildTool 的原语名 / ToolDoc.Name）；查找顺序：暴露名形态
// （内嵌 self 节点 = self_<契约名>，即 UI 工具页写入的键）→ 契约名精确匹配。
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

// ─── agentbox 沙箱（决策 42 §2 (104)/(109)）────────────────

// SetSecurityDirs 运行期覆盖 agentbox 允许目录集（装配层按实例读 prj `security-*` 后注入）。
// 传入 nil/空 → 清空（= 无允许目录；仅在 executor 级开关开启时才生效）。
// 加写锁，避免与 tools/call 的 SandboxPolicyFor 读取构成 data race。
func (c *Config) SetSecurityDirs(rules []agentbox.Rule) {
	next := append([]agentbox.Rule{}, rules...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if sameRules(c.SecurityDirs, next) {
		return
	}
	c.SecurityDirs = next
}

// SetToolSandbox 运行期覆盖 **executor 级沙箱开关**（装配层读 usr 键 `tool_sandbox` 后注入）。
// key = executor 类别（core / desktop / browser）；传入 nil/空表 = 清空（= 全部 executor 不隔离，
// 默认兼容）；返回开关表是否变化。归一化：key 去空白，空键丢弃；显式 false 项保留（语义等同缺省）。
func (c *Config) SetToolSandbox(raw map[string]bool) bool {
	next := map[string]bool{}
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			log.Printf("[mcp-server] tool_sandbox: 忽略空类别项")
			continue
		}
		next[k] = v
	}
	if len(next) == 0 {
		next = nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sameToolSandbox(c.ToolSandbox, next) {
		return false
	}
	c.ToolSandbox = next
	return true
}

// SandboxPolicyFor 返回该 **executor 类别** 应注入的 agentbox 策略 JSON（`""` = **不注入**，
// 即不启用隔离）。category = 工具契约 `_meta.category`（core / desktop / browser，= 执行器类别；
// 空串 / 未知类别 → 不命中）。开关未配置 / 显式 false → 不启用。
// 策略内容 = SecurityDirs 的 JSON（可为空数组 = 空允许集 → 执行器侧全拒的严格语义）。
// 读锁保护（与 SetSecurityDirs/SetToolSandbox 互斥）。
func (c *Config) SandboxPolicyFor(category string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.ToolSandbox) == 0 {
		return ""
	}
	on, ok := c.ToolSandbox[category]
	if !ok || !on {
		return ""
	}
	b, err := json.Marshal(c.SecurityDirs)
	if err != nil {
		return ""
	}
	if c.SecurityDirs == nil {
		return "[]" // 无允许目录 → 空集（执行器侧解释为全拒）
	}
	return string(b)
}

// sameRules 比较两张允许目录表是否等价（顺序敏感：配置面保序）。
func sameRules(a, b []agentbox.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameToolSandbox 比较两张 executor 开关表是否等价。
func sameToolSandbox(a, b map[string]bool) bool {
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

// executorEnv 组装 spawn executor 的子进程环境：继承宿主环境（剔除残留的 CHONKPILOT_*，避免泄漏
// 第三方/上层上下文）→ 注入本次调用的 CHONKPILOT_INSTANCE/WORKDIR/DATADIR + 配置型
// CHONKPILOT_INTERPRETERS / CHONK_CHROME，以及**按 executor 开关**的 agentbox 策略
// CHONKPILOT_SANDBOX（sandboxPolicy 为空串 = 不注入 = 不启用隔离）。**仅内部 executor
// 进程可见**（第三方 MCP server 不经我们 spawn，拿不到）。
func (c *Config) executorEnv(cx CallContext, sandboxPolicy string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+7)
	for _, kv := range base {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if strings.HasPrefix(name, "CHONKPILOT_") {
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
	add("CHONK_CHROME", c.chromeEnv())
	add(agentbox.EnvSandbox, sandboxPolicy)
	return out
}

// ─── 并发限流器（运行期可调整上限）────────────────────

// limiter 是 tools/call 的并发限流器：上限来自 Config.MaxConcurrency，可被 prj
// max_concurrency 运行期覆盖（SetRuntime/Apply → setLimit）。
type limiter struct {
	mu    sync.Mutex
	cond  *sync.Cond
	limit int
	busy  int
}

// newLimiter 构造限流器（limit <= 0 → 1，避免零容量阻塞）。
func newLimiter(limit int) *limiter {
	if limit <= 0 {
		limit = 1
	}
	l := &limiter{limit: limit}
	l.cond = sync.NewCond(&l.mu)
	return l
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

// acquire 占用一个并发名额（达上限则等待）。
func (l *limiter) acquire() {
	l.mu.Lock()
	for l.busy >= l.limit {
		l.cond.Wait()
	}
	l.busy++
	l.mu.Unlock()
}

// release 释放一个并发名额。
func (l *limiter) release() {
	l.mu.Lock()
	if l.busy > 0 {
		l.busy--
	}
	l.mu.Unlock()
	l.cond.Broadcast()
}

// setLimit 调整并发上限（<=0 忽略）；下调至低于在跑数时待在跑调用释放后收敛。
func (l *limiter) setLimit(n int) {
	if n <= 0 {
		return
	}
	l.mu.Lock()
	l.limit = n
	l.mu.Unlock()
	l.cond.Broadcast()
}
