// Package appwire 是 ChonkPilot 的**启动期统一装配入口**（GATEWAY-2 · L12）：
// gui 宿主（`gui/host`）与 server 入口（`src/server`）经同一 `Build` 装配**共用脊柱**
// （总线 → data 门面 → 可选 filesys → llm server + 默认插件），差异只保留在各自 Phase 3
// （窗口 / HTTP · 托盘 · UDS · 插件桥）。
//
// # BootConfig 的边界（重要）
//
// BootConfig 只是**启动期环境输入的一次性快照**：命令行解析结果、路径、旗标、LLM 缺省、
// 认证域参数、外部下游清单、执行池参数。它**不**搬运 usr/prj 运行期配置 —— 后者的权威
// 单一来源是数据面（`llm/server.loadExecConfig` 按实例读取 usr/prj 并热生效，见 12-数据层）。
// 故本包**不开库、不读配置**：组件构造完成后自行读库即为装配漂移（本包存在的意义就是禁止它）。
//
// Revision 是快照的确定性哈希（同一输入恒同一值），用于诊断「两个入口是否装配了同一套输入」；
// `Redacted()` 返回脱敏视图（URL 内嵌凭据一律抹除），供日志/审计使用。
package appwire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// Auth 是认证域启动输入（仅 server 形态消费；gui 桌面单体留零值 = 免鉴权）。
type Auth struct {
	DBPath        string
	UserDataRoot  string
	AllowRegister *bool
	ProjectRoots  []string
}

// ServerSpec 是外部下游（servers.list 解析结果）的**无凭据快照**：只记标识与链路形态。
type ServerSpec struct {
	ID        string
	Name      string
	Category  string
	Transport string
	Enabled   bool
}

// Pool 是 gateway 任务层执行池的启动参数快照。当前取值 = assembly 既有硬编码
// （`CallTimeout 60s / MaxTasks 8 / AsyncMode auto`）；后续池化参数改动在此登记，
// 运行期（usr/prj）变更仍走订阅，不在此热改。
type Pool struct {
	CallTimeoutSeconds int
	MaxTasks           int
	AsyncMode          string
}

// BootConfig 是启动期环境输入快照（不可变；构造后下游只读）。
type BootConfig struct {
	// Revision 由 Snapshot 计算（sha256，含凭据的字段已被排除）。
	Revision string

	// ── 形态与路径 ──
	Form           string // desktop / gui / browser（61 §4.6）
	WorkDir        string
	DataDir        string // 空 = 数据层按 project-id 分支（desktop 缺省）
	ProjectDataDir string // <workDir>/.chonkpilot
	CapabilityRoot string // 系统级 capability 根（空 = <exe 目录>/capability）

	// ── LLM 缺省 ──
	LLMBase  string
	LLMModel string

	// ── 旗标 ──
	NoServer bool // 不启动内嵌 llm server（UI 注入测试）
	Filesys  bool // 启动 filesys 文件服务（gui 宿主；server 入口不启动）

	// ── 认证域 / 外部下游 / 执行池 ──
	Auth    Auth
	Servers []ServerSpec
	Pool    Pool

	// ── 其余启动开关（各宿主 Phase 3 消费；集中登记便于审计）──
	Tray           bool
	AutoExit       bool
	HTTPAddr       string
	WebRoot        string
	PluginAddr     string
	PluginHTTPAddr string
	LLMBridgeAddr  string

	// EnvKeys 只登记**注入的环境变量键名**（值可能含凭据/路径，不进入快照）。
	EnvKeys []string
}

// DefaultPool 返回 gateway 执行池的当前缺省（与 assembly 既有硬编码一致）。
func DefaultPool() Pool {
	return Pool{CallTimeoutSeconds: 60, MaxTasks: 8, AsyncMode: "auto"}
}

// Snapshot 规范化入参并计算 Revision（幂等：同一输入恒同一哈希）。
func Snapshot(in BootConfig) BootConfig {
	cfg := in
	cfg.Form = strings.TrimSpace(cfg.Form)
	cfg.WorkDir = cleanPath(cfg.WorkDir)
	cfg.DataDir = cleanPath(cfg.DataDir)
	cfg.ProjectDataDir = cleanPath(cfg.ProjectDataDir)
	cfg.CapabilityRoot = cleanPath(cfg.CapabilityRoot)
	cfg.LLMBase = strings.TrimSpace(cfg.LLMBase)
	cfg.LLMModel = strings.TrimSpace(cfg.LLMModel)
	cfg.HTTPAddr = strings.TrimSpace(cfg.HTTPAddr)
	cfg.WebRoot = cleanPath(cfg.WebRoot)
	cfg.PluginAddr = strings.TrimSpace(cfg.PluginAddr)
	cfg.PluginHTTPAddr = strings.TrimSpace(cfg.PluginHTTPAddr)
	cfg.LLMBridgeAddr = strings.TrimSpace(cfg.LLMBridgeAddr)

	if cfg.Pool.CallTimeoutSeconds == 0 && cfg.Pool.MaxTasks == 0 && cfg.Pool.AsyncMode == "" {
		cfg.Pool = DefaultPool()
	}
	cfg.Auth.ProjectRoots = append([]string(nil), cfg.Auth.ProjectRoots...)
	sort.Strings(cfg.Auth.ProjectRoots)
	cfg.Servers = append([]ServerSpec(nil), cfg.Servers...)
	sort.Slice(cfg.Servers, func(i, j int) bool { return cfg.Servers[i].ID < cfg.Servers[j].ID })
	cfg.EnvKeys = append([]string(nil), cfg.EnvKeys...)
	sort.Strings(cfg.EnvKeys)

	cfg.Revision = revisionOf(cfg)
	return cfg
}

// revisionOf 计算确定性哈希：基于脱敏视图（不含凭据）+ 无 Revision 自身。
func revisionOf(cfg BootConfig) string {
	view := cfg.Redacted()
	raw, err := json.Marshal(view)
	if err != nil {
		// 理论上不可达（视图只含基本类型）；回退为键名集合的哈希，保持非空。
		raw = []byte(cfg.Form + "|" + cfg.WorkDir)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Redacted 返回脱敏视图（日志 / 审计用）：URL 内嵌凭据抹除，凭据类字段不出现。
func (c BootConfig) Redacted() map[string]any {
	view := map[string]any{
		"revision":         "", // 视图不含自身，避免自引用
		"form":             c.Form,
		"workDir":          c.WorkDir,
		"dataDir":          c.DataDir,
		"projectDataDir":   c.ProjectDataDir,
		"capabilityRoot":   c.CapabilityRoot,
		"llmBase":          scrubURL(c.LLMBase),
		"llmModel":         c.LLMModel,
		"noServer":         c.NoServer,
		"filesys":          c.Filesys,
		"authDBPath":       c.Auth.DBPath,
		"authUserDataRoot": c.Auth.UserDataRoot,
		"authAllowReg":     c.Auth.AllowRegister,
		"authProjectRoots": c.Auth.ProjectRoots,
		"servers":          c.Servers,
		"pool":             c.Pool,
		"tray":             c.Tray,
		"autoExit":         c.AutoExit,
		"httpAddr":         c.HTTPAddr,
		"webRoot":          c.WebRoot,
		"pluginAddr":       c.PluginAddr,
		"pluginHTTPAddr":   c.PluginHTTPAddr,
		"llmBridgeAddr":    c.LLMBridgeAddr,
		"envKeys":          c.EnvKeys,
	}
	return view
}

// scrubURL 抹除 URL 中的 userinfo（`scheme://user:pass@host` → `scheme://redacted@host`）。
func scrubURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("redacted")
	return u.String()
}

// cleanPath 统一路径分隔符与首尾空白（空串保持空）。
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return strings.ReplaceAll(p, "/", `\`)
}
