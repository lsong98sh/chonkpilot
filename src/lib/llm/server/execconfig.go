package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/lockfile"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
)

// loadExecConfig 把本实例的 usr/prj 配置注入内嵌 mcp-server 执行配置（见 36-配置 §3.1）：
//   - usr 标量（pythonPath/nodePath/javaPath/chromePath/goPath/rustPath/cCompilerPath）
//     经 data-user-config-load 读 {data:{...}}
//   - prj 键（timeout_sec/max_concurrency/skip_dirs）经 data-prj-config-load 读 {data:<值字符串>}
//
// 说明（R-11 二次升级）：实例上下文（instance/workDir/dataDir）不再经 Config.SetContext 注入
// tool arguments——改由每次调用经协议 _meta 从调用链透传（gateway → mcp-server → executor env）。
//
// 合并规则：prj 覆盖 usr（本处两组键不重叠，逐项按非空/正值生效）；prj 未配置的项取
// mcpms 内置默认（见函数内 def）。
// chromePath：注入 CHONK_CHROME（浏览器工具用），行为不变。
// 解释器路径（pythonPath/nodePath/javaPath/goPath/rustPath）：2026-10-28（P2-3）起**不再下发**
// 给执行层——脚本执行（`task_run`）随 task 插件，解释器配置归插件（`plugins.task.interpreter-*`）；
// 这些键仍供 {{toolchain.<key>}} 占位符取值（见 toolchain.go）。
// 另：usr 工具链路径同时注入 Config.SetToolchain（capability prompt/skill 文本的
// {{toolchain.<key>}} 占位符取值，见 toolchain.go）。
// 另：usr 键 `tool_async`（结构化 JSON 对象字符串）注入 Config.SetToolAsync（工具级异步配置
// 覆盖表；有变化时触发 reloadAppContracts → 暴露 _meta 热更新，见 applyToolAsync）。
// 调用时机：instance-register（注册即注入）与 data-prj-config-refresh（prj 执行配置保存/删除
// 即重跑 → 保存即生效，P0-B；见 onPrjConfigRefresh）；usr `tool_async` 的保存/删除另走
// data-user-config-refresh（见 onUserConfigRefresh / reloadToolAsyncOverrides）。
func (s *Server) loadExecConfig(instanceID string) {
	if s.mcpCfg == nil {
		return
	}
	// G-27（2026-09-22）：本函数是「读配置 → 写运行期覆盖」的**读改写序列**，会被两路并发调用
	// （`instance-register` 的订阅回调 与 prj 配置刷新的后台 goroutine，见 onPrjConfigRefresh）；
	// 交错执行即 data race（`go test -race` 实测）。用**专用互斥**串行化整段（不改锁内语义、
	// 不触碰 `s.mu`；本函数不重入 → 无自锁）。
	s.execCfgMu.Lock()
	defer s.execCfgMu.Unlock()
	// 门面读取**去重**（2026-09-22，启动首屏优化）：本函数原为 7 次门面往返
	// （usr ×2 + prj 平铺 ×1 + prj 单键 ×3 + prj-security ×1），其中 usr 被读两遍、
	// 3 个 prj 执行键各发一次 ConfigKVGet。改为 4 次：① usr（readUserConfig）
	// ② prj 平铺（readPrjConfigList，供分层路径键）③ prj 执行键**批量一次**
	// （prjConfigValues，与逐键 ConfigKVGet 同读序/同域）④ prj-security（securityRules）。
	// 取值语义逐项不变（同门面、同读序、同键）。
	usrCfg, usrErr := s.readUserConfig(instanceID)
	prjList, prjErr := s.readPrjConfigList(instanceID)
	execVals := s.prjConfigValues(instanceID, "timeout_sec", "max_concurrency", "skip_dirs")

	var toolAsyncRaw, toolSandboxRaw string
	loaded := false
	if usrErr == nil {
		if usrCfg != nil {
			loaded = true
			toolAsyncRaw, _ = usrCfg[toolAsyncUserConfigKey].(string)
			toolSandboxRaw, _ = usrCfg[toolSandboxUserConfigKey].(string)
		}
	} else {
		logf("[chonkpilot-server] loadExecConfig: UserConfigGet failed: %v\n", usrErr)
	}
	// 路径键分层（prj > usr > 默认，2026-09-19 接线）：统一入口 layeredPathValues；
	// {{toolchain.<key>}} 占位符取值走同一结果。
	// 2026-10-28（P2-3）：解释器映射不再下发（脚本执行随 task 插件，解释器配置归插件）。
	paths := layeredPathValues(usrCfg, prjList)
	pathsOK := usrErr == nil || prjErr == nil
	chromePath := paths["chromePath"]
	toolchain := make(map[string]string, len(mcpms.ToolchainKeys))
	for _, k := range mcpms.ToolchainKeys {
		toolchain[k] = paths[toolchainUserConfigKeys[k]]
	}
	if pathsOK {
		// 读成功才应用（读失败保留现值，避免把已有取值误清空——与下方 loaded 同口径）。
		s.mcpCfg.SetToolchain(toolchain)
	}
	if loaded {
		// 读成功才应用（读失败保留现值，避免把已有覆盖误清空）。
		s.applyToolAsync(toolAsyncRaw)
		s.applyToolSandbox(toolSandboxRaw)
	}
	// 基线 = 内嵌 mcp-server 内置默认（mcpms.DefaultConfig：300s / 16 / 12 个跳过目录）：
	// prj 未配置 → 显式用默认（与 mcp-tools 内置 SkipDirs 同集，行为等价）；
	// 「重置」（delete 键）后热生效重跑也回到默认，不残留旧覆盖（P0-B/C）。
	def := mcpms.DefaultConfig()
	timeoutSec := def.TimeoutSec
	if v := execVals["timeout_sec"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeoutSec = n
		}
	}
	maxConcurrency := def.MaxConcurrency
	if v := execVals["max_concurrency"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrency = n
		}
	}
	skipDirs := def.Defaults.SkipDirs
	if v := execVals["skip_dirs"]; v != "" {
		var arr []string
		if json.Unmarshal([]byte(v), &arr) == nil && len(arr) > 0 {
			skipDirs = arr // 空数组 = 未覆盖 → 保留内置默认（前端「重置」后同口径）
		}
	}
	s.mcpCfg.SetRuntime(timeoutSec, maxConcurrency, chromePath, skipDirs)
}

// prjExecConfigKeys 是运行期热生效的执行配置 prj 键（loadExecConfig 读取）：
// timeout_sec → tools/call 执行超时；max_concurrency → 并发限流上限；skip_dirs → 递归跳过目录集；
// 另含**路径键**（2026-09-19 接线 prj 覆盖 usr，prj > usr > 默认）：保存 prj 路径即重跑
// loadExecConfig → {{toolchain.*}} 立刻取 prj 值（无需重开项目）。
// 注（2026-10-28 P2-3）：解释器 env 下发已删除（归 task 插件），路径键只服务占位符。
var prjExecConfigKeys = map[string]bool{
	"timeout_sec":     true,
	"max_concurrency": true,
	"skip_dirs":       true,
	"javaPath":        true,
	"pythonPath":      true,
	"nodePath":        true,
	"goPath":          true,
	"rustPath":        true,
	"cCompilerPath":   true,
	"chromePath":      true,
}

// toolAsyncUserConfigKey 是**工具级异步配置**的 usr 键（64-配置项一览 §3）：值 = 结构化 JSON
// 对象字符串（persist 自由键通道，与 recent_dirs 同形态），形状 =
// `{"<工具暴露名>": {"mode": "always|never|auto|manual", "threshold": 30, "hard_timeout": 300,
// "cancel_on_timeout": 30, "touch_files": true}}`。
// key = tools/list 暴露名（P0a 2026-10-29 起内置 self 节点 = 契约名，历史 `self_<契约名>` 键仍兼容；
// 第三方 = <节点名>_<原名>，天然消歧）；
// threshold（秒，可选，auto/manual 档的超时转后台点）、hard_timeout（秒，可选，executor 执行
// 硬杀上限）、cancel_on_timeout（秒，可选，>0 = 到超时点自动取消；gateway 消费）、
// touch_files（bool，可选，是否涉及文件变动）。未配置的工具 = 维持契约现值；「恢复默认」= 删该工具键项。
const toolAsyncUserConfigKey = "tool_async"

// applyToolAsync 解析 usr `tool_async` 值并写入内嵌 mcp-server 覆盖表（Config.SetToolAsync）。
// raw 为空/键缺失 → 清空全部覆盖（= 恢复契约默认）；非法 JSON → 忽略并记日志、保留现值。
// 覆盖表**有变化**时触发 reloadAppContracts → 契约重注册（同名工具 AddTool 覆盖更新）
// + 既有 gateway/reload 让 self 节点重拉 list → tools/list 的 _meta.async / async-threshold
// 秒级更新（无需重启；gateway 侧 doCall 读的是重注册后的 route.Tool.Meta）。
func (s *Server) applyToolAsync(raw string) {
	if s.mcpCfg == nil {
		return
	}
	var next map[string]mcpms.ToolAsyncOverride
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &next); err != nil {
			logf("[chonkpilot-server] %s 解析失败（忽略，保留现值）: %v\n", toolAsyncUserConfigKey, err)
			return
		}
	}
	// 工具级覆盖**下沉 gateway 统一施加**（I-82）：按暴露名覆盖全部工具类（含 dir 节点/第三方
	// 工具，mcp-server 侧只按契约名覆盖 self 节点）。归一结果由 mcp-server 单口径提供，避免两处各归一。
	// RB-2：gateway 侧覆盖类型归 gateway 所有 → 在此转换（仅取 gateway 面消费的 mode/threshold）。
	if s.gw != nil {
		s.gw.SetAsyncOverrides(toGatewayAsyncOverrides(mcpms.NormalizeToolAsync(next)))
	}
	if !s.mcpCfg.SetToolAsync(next) {
		return // 覆盖表未变化（普通 usr 配置写入的常态）→ 不重注册、零抖动
	}
	s.reloadAppContracts()
}

// toGatewayAsyncOverrides 把 mcp-server 归一后的工具级异步覆盖转为 gateway 自有类型（RB-2：
// gateway lib 不依赖 chonkpilot-mcp-server 包）。携带 gateway 面消费的 mode / threshold /
// touch_files / cancel_on_timeout（hard_timeout 属执行硬上限，仅 executor 消费、不透出 → 不携带）。
func toGatewayAsyncOverrides(in map[string]mcpms.ToolAsyncOverride) map[string]mcpgateway.ToolAsyncOverride {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]mcpgateway.ToolAsyncOverride, len(in))
	for k, v := range in {
		out[k] = mcpgateway.ToolAsyncOverride{
			Mode: v.Mode, Threshold: v.Threshold, ThresholdSet: v.ThresholdSet,
			TouchFiles: v.TouchFiles, TouchFilesSet: v.TouchFilesSet,
			CancelOnTimeout: v.CancelOnTimeout, CancelOnTimeoutSet: v.CancelOnTimeoutSet,
		}
	}
	return out
}

// toolSandboxUserConfigKey 是 **agentbox 沙箱开关**的 usr 键（64-配置项一览 §3，agentbox 决策
// 42 §2 (109)；2026-10-12 由 executor 类别重整为**被 spawn 的执行体**单位）：值 = 结构化 JSON
// 对象字符串，形状 = `{"executors":{"<executor exe 基名>":true|false},
// "mcps":{"<外部 MCP 名>":true|false}}`（persist 自由键通道，形态同 tool_async）。
//   - executors：内置 executor，key = executor exe 基名（chonkpilot-{core,desktop,browser,dsl}-executor.exe）；
//   - mcps：外部 MCP，key = MCP 条目名（gateway stdio spawn 时按 name 查）。
//
// **未配置 / false = 不启用隔离（默认兼容）**。**旧形状（按 executor 类别 core/desktop/browser、
// 及 usr `mcps[].sandbox`）读取时迁移**（见 mcpms.ParseToolSandbox / gateway mergeLegacySandboxMCP）。
// 消费链：loadExecConfig/reloadToolAsyncOverrides → gateway SetSandboxOverrides → mcp-server
// Config.SetToolSandbox → gateway stdio spawn / mcp-server callTool 按单位键查开关。
// 2026-10-21：应用层路径校验已废弃，沙箱改由 gateway 在 spawn 时以进程级 Low IL 施加；
// 开关表暂保留待 L6 迁移（当前 spawn 不再注入 CHONKPILOT_SANDBOX）。
const toolSandboxUserConfigKey = "tool_sandbox"

// applyToolSandbox 解析 usr `tool_sandbox` 并写入执行配置（SetToolSandbox；gateway 在场时
// 另同步其外部 MCP 侧开关，见函数体 L5 说明）。
// raw 为空/键缺失 → 清空全部开关（= 全部执行体不隔离，保守默认）；非法 JSON → 忽略并记日志、
// 保留现值；**旧形状 → 读取时迁移为新形状**（ParseToolSandbox）。
// 开关表变化无需重注册工具（开关在每次 tools/call 的 spawn 时读取，秒级生效）。
func (s *Server) applyToolSandbox(raw string) {
	if s.mcpCfg == nil {
		return
	}
	cfg, err := mcpms.ParseToolSandbox(raw)
	if err != nil {
		logf("[chonkpilot-server] %s 解析失败（忽略，保留现值）: %v\n", toolSandboxUserConfigKey, err)
		return
	}
	// 沙箱开关**双写**（GATEWAY-2 L5）：gateway 仅持有外部 MCP 连接侧的隔离档位
	// （mcp_server 规格的 sandbox 判定），内置 executor 的开关由执行配置侧
	// （mcp-server Config.SetToolSandbox，每次 tools/call spawn 时读取）承担 —— 二者键位同源、
	// 消费方不同，故此处两路都写；无 gateway（独立/测试形态）时只写执行配置。
	if s.gw != nil {
		s.gw.SetSandboxOverrides(cfg.Executors, cfg.MCPs)
	}
	s.mcpCfg.SetToolSandbox(cfg.Executors, cfg.MCPs)
}

// securityRules 读该实例的 prj `security-*`（可写白名单）并换算为安全目录规则集
// （`{dir}`，语义 = 可写目录且**递归**；读不受限——Low IL 只限写）。
// 契约（14 §2.3）：一条目录 = 一个 persist key（不透明 id），value = JSON 字符串 `{"dir"}`；
// list 返回平铺 map（已剥 `security-` 前缀）。旧值 `{dir,writable}` 兼容读入（writable 忽略）。
// 返回 (规则集, 读取是否成功)：读取失败 → ok=false（调用方保留现值，不误清空）；
// 单条 value 解析失败 → 跳过该条并记日志（不影响其余条目）。
func (s *Server) securityRules(instanceID string) ([]mcpgateway.SandboxRule, bool) {
	res, err := s.cfg.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjSecurity, InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] securityRules: ConfigKVList(prj-security) failed: %v\n", err)
		return nil, false
	}
	list := res.List
	if len(list) == 0 {
		return nil, true
	}
	// 保序（map 遍历无序 → 按 key 排序，保证同配置下策略 JSON 稳定，避免无谓 diff/respawn）
	keys := make([]string, 0, len(list))
	for k := range list {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rules := make([]mcpgateway.SandboxRule, 0, len(keys))
	for _, k := range keys {
		raw := list[k]
		var r mcpgateway.SandboxRule
		if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &r) != nil || strings.TrimSpace(r.Dir) == "" {
			logf("[chonkpilot-server] securityRules: 条目 %s 解析失败（跳过）: %q\n", k, raw)
			continue
		}
		r.Writable = true // C1：白名单即"可写"，旧值 writable 一律忽略
		rules = append(rules, r)
	}
	return rules, true
}

// gatewaySandboxRules 汇总**全部在线实例**的 security-* 允许目录（按目录+可写去重、保序），
// 作为 gateway 上游 server 的沙箱允许目录快照。上游 server 是 global 作用域（无实例归属），
// 单体典型形态只有一个实例；多实例并存时按并集放宽（保守：合并只增不减）。
//
// G-75 默认项（2026-10-22）：除用户配置的 security-* 外，**默认合并**每个实例的项目路径
// 为可写条目（不落盘、不与用户条目冲突——同目录以既有条目为准）：
//   - work_dir（项目根）
//   - 项目数据根（instance data_dir 非空 → 该目录；空 → <work_dir>/.chonkpilot）
//   - prjusr 数据根（data.PrjUsrDir；解析失败 → 跳过，不影响其余默认项）
//   - 进程级系统 temp（os.TempDir()，一次）
//
// 打 Low IL 标签由 gateway 在 stdio 槽位构建时按 SandboxDirs WritableRoots 执行（provider.go）。
func (s *Server) gatewaySandboxRules() []mcpgateway.SandboxRule {
	seen := map[string]bool{}
	var out []mcpgateway.SandboxRule
	add := func(dir string, writable bool) {
		d := strings.TrimSpace(dir)
		if d == "" {
			return
		}
		key := d + "\x00" + strconv.FormatBool(writable)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, mcpgateway.SandboxRule{Dir: d, Writable: writable})
	}
	for _, info := range s.im.List() {
		rules, ok := s.securityRules(info.ID)
		if ok {
			for _, r := range rules {
				add(r.Dir, r.Writable)
			}
		}
		// G-75 默认项：项目自身路径默认可写。
		if wd := strings.TrimSpace(info.WorkDir); wd != "" {
			add(wd, true)
			dd := strings.TrimSpace(info.DataDir)
			if dd == "" {
				dd = filepath.Join(wd, lockfile.ProjectDataDirName)
			}
			add(dd, true)
			if pr, err := data.PrjUsrDir(wd, strings.TrimSpace(info.DataDir)); err == nil {
				add(pr, true)
			} else {
				logf("[chonkpilot-server] gatewaySandboxRules: prjusr 数据根解析失败（跳过该默认项）: instance=%s workdir=%s err=%v\n", info.ID, wd, err)
			}
		}
	}
	// G-75 默认项：系统 temp（%TEMP%/%TMP%），进程级只加一次。
	add(os.TempDir(), true)
	return out
}

// applyGatewaySandboxDirs 为 usr mcps 条目补上 agentbox 允许目录快照（prj `security-*` 汇总）。
// 快照恒填（开关由 usr `tool_sandbox.mcps` 决定、在 gateway stdio spawn 时按 MCP 名查询）；
// 目录快照变化 → 条目 diff → 重注册/重连，使新允许目录在下一次 spawn 生效。
func (s *Server) applyGatewaySandboxDirs(entries []mcpgateway.ServerEntry) {
	rules := s.gatewaySandboxRules()
	// C3：同步刷新 gateway 的白名单快照（`_meta.chonkpilot.sandbox` 用它下发；插件 MCP
	// 不经 servers/register 拿 SandboxDirs，故必须有这条实时通道）。
	s.sandboxMu.Lock()
	s.sandboxRules = rules
	s.sandboxMu.Unlock()
	if len(entries) == 0 {
		return
	}
	for i := range entries {
		entries[i].SandboxDirs = rules
	}
}

// sandboxRulesForGateway 返回当前可写白名单快照（gateway `SetSandboxDirsProvider` 的回调）。
// 空 = 未初始化（gateway 侧只用 work_dir 内置默认）。
func (s *Server) sandboxRulesForGateway(_ string) []mcpgateway.SandboxRule {
	s.sandboxMu.RLock()
	defer s.sandboxMu.RUnlock()
	if len(s.sandboxRules) == 0 {
		return nil
	}
	return s.sandboxRules
}

// onPrjSecurityRefresh prj-security 域 save/delete 广播（data-prj-security-refresh，**既有主题**，
// 61-消息一览 §3 的 `data-<domain>-refresh` 家族）→ 安全目录（允许写目录）**保存即生效**：
//   - instance 归属明确（security-* 恒带 instance_id）→ 对该实例重跑 loadExecConfig；
//   - 并触发 usr mcps 增量对账（上游 server 的 sandbox 目录快照变化 → 重注册下发，零新增主题）。
//
// 异步执行：广播在 persist save 的同步派发链路内（处置同 onPrjConfigRefresh）。
// 注：进程级沙箱由 gateway 在 spawn 时施加（允许写目录 = prj `security-*` 的 WritableRoots）。
func (s *Server) onPrjSecurityRefresh(_ string, payload []byte) {
	if s.mcpCfg == nil {
		return
	}
	var ev struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.InstanceID == "" {
		return
	}
	go func() {
		defer recoverLog("onPrjSecurityRefresh")
		s.loadExecConfig(ev.InstanceID)
		s.reconcileUserMCPs(ev.InstanceID)
		logf("[chonkpilot-server] prj-security 变更热生效（agentbox 允许目录）: instance=%s\n", ev.InstanceID)
	}()
}
