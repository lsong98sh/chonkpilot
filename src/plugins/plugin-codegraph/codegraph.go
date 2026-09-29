// Package codegraph 是 codegraph 代码检索/依赖分析插件（server 内嵌插件钩子，无 CGO）。
//
// 定位（对齐 29-codegraph 与任务定稿）：
//   - codegraph 引擎 = 独立 exe（chonkpilot-codegraph-mcp-server，console CGO），由本插件按
//     workdir 以 stdio 子进程形态管理；引擎全部工具带 workdir 参数（含查询工具）。
//   - 多 workdir（T-12/P2-4）：**每个 workdir 一个独立引擎子进程**（p.clients[workdir]，
//     独立索引内存/独立串行），互不干扰；空闲按 workdir 独立回收（childIdleTimeout）。
//   - 本插件对外（gateway）注册 6 个查询工具：与引擎同名、schema 去掉 workdir（由插件注入）。
//     可见性门控（enable-codegraph）与索引就绪编排全在插件内存（源 = prj-config）。
//   - instance 生命周期驱动 workdir 引用计数与 gateway 工具注册/注销：register/exit 为变更源
//     （heartbeat 记录时刻；合并单进程形态不发布心跳、不做心跳超时退出判定，分离形态
//     `-tags split` 由 sweepInstances 判定超时 → instanceGone）；
//     enable-codegraph 开关经 data-prj-config-refresh 实时同步。
//   - 引擎应答一律为 JSON 文本：查询未就绪（not_initialized/indexing/error 等）原样透传，
//     插件不做隐式补 init（仅启用时后台自动 configure+initialize 一次，状态回写 codegraph.status）。
package codegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ignore "github.com/chonkpilot/chonkpilot-ignore"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// 相对主题与 prj-config 键（chonk. 前缀由总线初始化时注入，业务层只写相对名；
// 对齐 61-消息一览：gateway 方法面 = mcp-<组>-<动作>，data 面 = data-<域>-<动作>）。
const (
	// gateway 方法面：tools/register | tools/unregister → mcp-tools-register/unregister
	subjectToolRegister   = "mcp-tools-register"
	subjectToolUnregister = "mcp-tools-unregister"
	// 本插件声明的 gateway 工具回调主题（gateway regProv 命中后向该主题发 {tool,args,context}，
	// 订阅者写回 v.Result，同一主题 promise——镜像 llm domain-tool-call 模式）
	toolCallSubject = "codegraph-tool-call"

	// prj-config 键
	engineName        = "codegraph"                 // 引擎标识（配置键前缀 = 引擎名；ignore.ConfigOptions 用）
	enableKey         = "enable-codegraph"          // "true"/"false"（UI 开关）
	extsKey           = "codegraph.exts"            // 参与索引的扩展名（逗号/换行分隔；空 = 引擎默认集）
	skipDirsKey       = "codegraph.skip-dirs"       // 用户排除规则（gitignore 语法，逗号/换行分隔；空 = 无用户规则）
	stackGitignoreKey = "codegraph.stack-gitignore" // "true"/"false"：是否让引擎叠加各级 .gitignore / info/exclude / 全局 ignore
	actionKey         = "codegraph.action"          // 动作信号键：值 = rebuild|clear|retry（UI 按钮写入一次 → 插件执行）
	statusKey         = "codegraph.status"          // 引擎状态 JSON 文本（插件回写，UI 只读回显）

	// codegraph.action 取值（前端按钮 → 一次 prj-config 变更信号；复用既有
	// data-prj-config-refresh 面 = 零新增消息主题）
	actionRebuild = "rebuild" // 强制全量重建索引
	actionClear   = "clear"   // 清除索引产物（引擎 mode=clear → 状态回「未初始化」）
	actionRetry   = "retry"   // 重试失败项（引擎未记录失败明细 → 降级为全量重建）

	// data 面（persist 订阅）
	subjectPrjConfigRefresh = "data-prj-config-refresh"
	subjectPrjConfigLoad    = "data-prj-config-load"
	subjectPrjConfigSave    = "data-prj-config-save"

	// 周期/超时
	sweepInterval    = 15 * time.Second // 空闲子进程回收周期
	childIdleTimeout = 5 * time.Minute  // 无活跃实例后空闲引擎子进程回收阈值（sweepIdleClient 消费）
	dataTimeout      = 5 * time.Second  // data-* 请求应答超时
	callTimeout      = 60 * time.Second // 查询工具引擎调用超时
	initTimeout      = 15 * time.Minute // 管理工具（configure/initialize/status）调用超时

	// 索引编排（configure/initialize）失败重试：缺省保守（一次重试、退避 2s），Options 可覆盖。
	defaultRetryAttempts = 2
	defaultRetryBackoff  = 2 * time.Second

	// 索引配置去抖窗口：设置页一次保存会分别写 codegraph.exts + codegraph.skip-dirs 两键 →
	// 两次 data-prj-config-refresh；短窗口内合并为一次强制重建（同一份配置不重建两轮）。
	// 窗口外的单次键变更仍会（延迟 window 后）触发一次重建——不会出现"改了不重建"。
	defaultRebuildDebounce = 400 * time.Millisecond

	// 索引进度：轮询引擎落盘 meta.json（引擎 Initialize 期间按批写入，见引擎 index.go）的间隔；
	// 读到的 done/total 变化即回写 codegraph.status（复用既有状态面，不新增消息主题）。
	progressPollInterval = 500 * time.Millisecond
	engineMetaRel        = ".chonkpilot/codegraph/meta.json"

	// 索引编排阶段（进度快照 JSON 的 phase 字段）
	phaseConfigure = "configure"
	phaseIndex     = "index"
)

// Options 构造参数。
type Options struct {
	// Exe codegraph 引擎可执行文件路径；空 = 自动解析（见 resolveExe）。
	Exe string
	// RetryAttempts 索引编排失败重试总次数（含首次）；<=0 → 缺省保守值 defaultRetryAttempts。
	RetryAttempts int
	// RetryBackoff 每次重试前的退避时长；<=0 → 缺省保守值 defaultRetryBackoff。
	RetryBackoff time.Duration
	// RebuildDebounce 索引配置变更去抖窗口；<=0 → defaultRebuildDebounce。
	RebuildDebounce time.Duration
}

// Codegraph 是 codegraph 插件（plugin.Hook 实现）。
type Codegraph struct {
	deps plugin.Deps
	logf func(format string, args ...any)
	exe  string // 引擎 exe 绝对路径（resolveExe 结果）
	opt  Options

	mu         sync.Mutex // 保护 insts / works / registered / regOwner
	insts      map[string]*instRec
	works      map[string]*workRec   // key = workdir（Clean 后绝对路径）
	registered bool                  // 6 个查询工具是否已注册到 gateway（全局仅一份）
	regOwner   string                // 当前注册归属的 workdir（多 workdir 同时启用时取第一个）
	syncMu     sync.Mutex            // syncTools 单飞（防并发重复注册/注销）
	clientMu   sync.Mutex            // 每 workdir 引擎子进程（懒建/回收）保护
	clients    map[string]*clientRec // key = workdir；每 workdir 一独立引擎子进程（独立索引互不干扰）
	rebuild    *rebuildDebouncer     // 索引配置变更去抖（一次保存两键 → 只重建一次）

	subs []mq.Sub // 订阅句柄（Start 失败回滚用；宿主不提供 Stop）
}

// instRec 单个实例的绑定视图（instance_id → workdir + 最近心跳）。
// last 仅记录最近注册/心跳时刻；合并单进程形态不据此判退出，分离形态（`-tags split`）
// 由 sweepInstances 据 last 判超时（见 sweep_split.go）。
type instRec struct {
	workdir string
	last    time.Time
}

// workRec 单个 workdir 的工作区记录（开关/引用计数维度）。
// 字段锁约定：workDir/dataDir/enabled/refs 由 p.mu 保护；
// cmu/busy/state 由 cmu 保护（同一 workdir 的后台 ensure 串行）。
// 引擎子进程为**每 workdir 独立**（p.clients[workdir]），按工具参数 workdir 定位；
// 同一 workdir 的调用经本记录 cmu 串行，不同 workdir 互不阻塞。
type workRec struct {
	workDir string
	dataDir string // 首个实例自带 data_dir（预留）
	enabled bool   // enable-codegraph == "true"
	refs    int    // 活跃实例引用数（同 workdir 多实例去重）

	cmu   sync.Mutex
	busy  bool   // 后台 configure/initialize 流程进行中（同 workdir 串行）
	state string // 最近一次 codegraph_status 原始 JSON 文本（= codegraph.status 同源）

	// 索引配置比较基线（由 p.mu 保护）：最近一次已知的 codegraph.exts / skip-dirs /
	// stack-gitignore 原始值。保存幂等依据——新旧值相同（重复保存同一份配置）不排重建；
	// cfgSeen 标记基线是否已建立（首次读 prj-config 时播种，此后只由 refresh 处理更新，
	// 避免覆盖用户刚保存的值）。
	cfgExts     string
	cfgSkipDirs string
	cfgStack    string
	cfgSeen     bool
}

// clientRec 单个 workdir 的引擎子进程记录（每 workdir 独立子进程 = 独立索引内存/独立串行）。
// 字段由 p.clientMu 保护：lastUsed = 该 workdir 最近一次活跃（引擎调用 / 活跃实例刷新）时刻，
// 即该 workdir 空闲回收的基准。
type clientRec struct {
	c        engineClient
	lastUsed time.Time
}

// New 构建 codegraph 插件。
func New(opts Options) *Codegraph {
	if opts.RetryAttempts <= 0 {
		opts.RetryAttempts = defaultRetryAttempts
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = defaultRetryBackoff
	}
	return &Codegraph{
		opt:     opts,
		insts:   make(map[string]*instRec),
		works:   make(map[string]*workRec),
		clients: make(map[string]*clientRec),
		rebuild: newRebuildDebouncer(opts.RebuildDebounce),
	}
}

// Name 插件名。
func (p *Codegraph) Name() string { return "codegraph" }

// Start 解析引擎路径、订阅生命周期/配置/工具回调并启动后台清扫（宿主在全部插件前序就绪后调用）。
func (p *Codegraph) Start(d plugin.Deps) error {
	p.deps = d
	logf := d.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	p.logf = logf

	exe, err := resolveExe(p.opt.Exe)
	if err != nil {
		return err
	}
	p.exe = exe

	// instance 生命周期（v1 事件订阅，只读）
	for _, s := range []struct {
		subject string
		h       mq.Handler
	}{
		{instance.SubjectRegister, p.onInstanceRegister},
		{instance.SubjectHeartbeat, p.onInstanceHeartbeat},
		{instance.SubjectExit, p.onInstanceExit},
	} {
		sub, err := d.Bus.On(s.subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
			s.h(subj, v.Payload)
			return nil
		})
		if err != nil {
			p.unsubscribeAll()
			return err
		}
		p.subs = append(p.subs, sub)
	}
	// prj-config 变更广播（enable-codegraph 开关；order 0）
	sh, err := d.Bus.On(subjectPrjConfigRefresh, 0, p.onPrjConfigRefresh)
	if err != nil {
		p.unsubscribeAll()
		return err
	}
	p.subs = append(p.subs, sh)
	// gateway 工具执行回调（regProv 向本主题发请求，写回 v.Result）
	sh, err = d.Bus.On(toolCallSubject, 0, p.onToolCall)
	if err != nil {
		p.unsubscribeAll()
		return err
	}
	p.subs = append(p.subs, sh)

	// 后台循环：周期回收空闲 5 分钟的子进程（实例生命周期只由 instance-register/exit 驱动）
	go p.loop()

	logf("codegraph: 插件就绪（引擎 exe=%s，回调主题=%s）", p.exe, toolCallSubject)
	return nil
}

func (p *Codegraph) unsubscribeAll() {
	for _, s := range p.subs {
		_ = s.Unsubscribe()
	}
	p.subs = nil
}

// ─── 后台循环 ────────────────────────────────────────────

// loop 后台循环：周期执行 tick（按 workdir 回收空闲引擎子进程）。
func (p *Codegraph) loop() {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for range t.C {
		p.tick()
	}
}

// tick 单轮周期维护：实例心跳超时判定（形态开关，见 sweep_split.go / sweep_inprocess.go）
// + 回收空闲引擎子进程。实例退出一律走既有 instanceGone 路径：显式 instance-exit（GUI/CLI
// 退出注销）恒有效；心跳超时仅在分离形态（`-tags split`）编入，合并单进程形态为空实现。
func (p *Codegraph) tick() {
	p.sweepInstances(time.Now())
	p.sweepIdleClient()
}

// sweepIdleClient 按 workdir 回收空闲引擎子进程：该 workdir 已无活跃实例（refs==0），
// 且距其最近一次活跃已超 childIdleTimeout → 关闭并移除该 workdir 的 client（下次调用懒重建）。
// 每个 workdir 独立判定/回收：某 workdir 空闲回收不影响其它 workdir 的引擎子进程。
func (p *Codegraph) sweepIdleClient() {
	now := time.Now()
	p.mu.Lock()
	active := make(map[string]bool, len(p.works))
	for wd, r := range p.works {
		if r.refs > 0 {
			active[wd] = true
		}
	}
	p.mu.Unlock()

	type victim struct {
		wd  string
		rec *clientRec
	}
	var reclaim []victim
	p.clientMu.Lock()
	for wd, rec := range p.clients {
		if active[wd] {
			rec.lastUsed = now // 该 workdir 活跃 → 刷新其空闲基准（等价旧全局 lastActiveAt）
			continue
		}
		if !idleReclaimDue(false, rec.lastUsed, now, childIdleTimeout) {
			continue
		}
		delete(p.clients, wd)
		reclaim = append(reclaim, victim{wd: wd, rec: rec})
	}
	p.clientMu.Unlock()

	for _, v := range reclaim {
		p.logf("codegraph: workdir %s 引擎子进程空闲超 %v → 回收（下次调用懒重建）", v.wd, childIdleTimeout)
		v.rec.c.close()
	}
}

// idleReclaimDue 判定是否应回收某 workdir 的引擎子进程（纯函数，便于白盒）：该 workdir 无活跃实例、
// 曾有过活跃记录（lastUsed 非零）、且距最近活跃已达 timeout。
func idleReclaimDue(anyActive bool, lastUsed, now time.Time, timeout time.Duration) bool {
	if anyActive || lastUsed.IsZero() {
		return false
	}
	return now.Sub(lastUsed) >= timeout
}

// ─── instance 生命周期 ──────────────────────────────────────

func (p *Codegraph) onInstanceRegister(_ string, payload []byte) {
	var ev struct {
		InstanceID string `json:"instance_id"`
		WorkDir    string `json:"work_dir"`
		DataDir    string `json:"data_dir,omitempty"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.InstanceID == "" || ev.WorkDir == "" {
		return
	}
	wd := filepath.Clean(ev.WorkDir)
	p.mu.Lock()
	ir := p.insts[ev.InstanceID]
	if ir == nil {
		ir = &instRec{}
		p.insts[ev.InstanceID] = ir
	}
	ir.workdir = wd
	ir.last = time.Now()
	r := p.works[wd]
	if r == nil {
		r = &workRec{workDir: wd}
		p.works[wd] = r
	}
	r.refs++
	if r.dataDir == "" && ev.DataDir != "" {
		r.dataDir = ev.DataDir
	}
	p.mu.Unlock()

	// 异步回读一次 enable-codegraph（慢请求不进总线派发路径）；读回后据开关触发
	// 自动初始化并同步 gateway 工具注册。
	go p.readEnableAndEnsure(ev.InstanceID, wd)
}

func (p *Codegraph) onInstanceHeartbeat(_ string, payload []byte) {
	var ev struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.InstanceID == "" {
		return
	}
	p.mu.Lock()
	if ir, ok := p.insts[ev.InstanceID]; ok {
		ir.last = time.Now()
	}
	p.mu.Unlock()
}

func (p *Codegraph) onInstanceExit(_ string, payload []byte) {
	var ev struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.InstanceID == "" {
		return
	}
	p.instanceGone(ev.InstanceID)
}

// instanceGone 实例退出统一路径：解绑并递减所属 workdir 引用，引用归零 → 注销工具（若适用）。
func (p *Codegraph) instanceGone(id string) {
	p.mu.Lock()
	ir, ok := p.insts[id]
	if !ok {
		p.mu.Unlock()
		return
	}
	delete(p.insts, id)
	var r *workRec
	refs := 0
	if ir.workdir != "" {
		r = p.works[ir.workdir]
		if r != nil {
			r.refs--
			if r.refs < 0 {
				r.refs = 0
			}
			refs = r.refs
		}
	}
	p.mu.Unlock()
	if r == nil {
		return
	}
	if refs == 0 {
		p.logf("codegraph: workdir %s 已无活跃实例（记录保留，空闲子进程由清扫回收）", r.workDir)
	}
	p.syncTools()
}

// readEnableAndEnsure 注册后异步回读 enable-codegraph 并据此收敛可见性。
func (p *Codegraph) readEnableAndEnsure(instanceID, wd string) {
	val, err := prjConfigReadKey(p.deps.Bus, instanceID, enableKey)
	if err != nil {
		p.logf("codegraph: 读 prj-config %s 失败（instance=%s）：%v（视为未启用）", enableKey, instanceID, err)
	}
	enabled := val == "true"
	p.mu.Lock()
	r := p.works[wd]
	if r == nil || r.refs <= 0 {
		p.mu.Unlock() // 读回前实例已退出
		return
	}
	if r.enabled != enabled {
		p.logf("codegraph: %s → %v（instance=%s）", wd, enabled, instanceID)
	}
	r.enabled = enabled
	p.mu.Unlock()
	if enabled {
		p.maybeEnsure(wd)
	}
	p.syncTools()
}

// ─── prj-config 变更 ──────────────────────────────────────

// onPrjConfigRefresh 处理 data-prj-config-refresh：关心 enable-codegraph 与索引配置
// （codegraph.exts / codegraph.skip-dirs / codegraph.stack-gitignore）+ 动作信号（codegraph.action）。
// 广播载荷 {id, ids?, op, list}：**批量写**（61 §3.1）带 `ids`（全组键）→ 逐键展开处理
// （= 与改前「逐键广播」等价）；缺省回落单键 `id`（向后兼容旧广播/旧发送方）。
// 无 instance_id（persist refresh v1 不携实例过滤）——v1 限制：单宿主典型形态为单实例/单 workdir，
// 直接把开关应用到全部活跃 workdir；多 workdir 并存时按同一开关刷新（按 instance 隔离留待 v2）。
func (p *Codegraph) onPrjConfigRefresh(_ context.Context, _ string, v *mq.Value) error {
	var ev struct {
		ID   string         `json:"id"`
		IDs  []string       `json:"ids"`
		Op   string         `json:"op"`
		List map[string]any `json:"list"`
	}
	if json.Unmarshal(v.Payload, &ev) != nil {
		return nil
	}
	keys := ev.IDs
	if len(keys) == 0 {
		keys = []string{ev.ID}
	}
	for _, key := range keys {
		p.applyPrjConfig(key, ev.Op, ev.List)
	}
	return nil
}

// applyPrjConfig 按单个键应用一次 prj-config 变更（批量广播逐键展开后调用）。
func (p *Codegraph) applyPrjConfig(key, op string, list map[string]any) {
	switch key {
	case enableKey:
		enabled := false
		if list != nil {
			if raw, ok := list[enableKey]; ok {
				enabled = strval(raw) == "true"
			}
		}
		if op == "delete" {
			enabled = false // 键被删 = 关闭
		}
		var affected []string
		p.mu.Lock()
		for wd, r := range p.works {
			if r.refs > 0 && r.enabled != enabled {
				r.enabled = enabled
				affected = append(affected, wd)
			}
		}
		p.mu.Unlock()
		if len(affected) == 0 {
			return
		}
		sort.Strings(affected)
		p.logf("codegraph: prj-config %s=%v（op=%s）→ workdir %v", enableKey, enabled, op, affected)
		for _, wd := range affected {
			if enabled {
				p.maybeEnsure(wd)
			}
		}
		p.syncTools()
	case extsKey, skipDirsKey, stackGitignoreKey:
		// 索引配置变更 → 对启用中的 workdir 强制重建索引（exts/skip-dirs/stack-gitignore 变更
		// 均须重建才生效）。
		// 保存幂等：新旧值相同（重复保存同一份配置）→ 不排重建；显式删键 = 回落引擎
		// 默认集 = 生效配置变化 → 重建。
		// 经去抖合并：一次批量保存（广播含多键 → 逐键展开）只重建一轮。
		newRaw := ""
		if list != nil {
			if raw, ok := list[key]; ok {
				newRaw = strval(raw)
			}
		}
		deleted := op == "delete"
		if deleted {
			newRaw = "" // 键被删 = 回落引擎默认集
		}
		var affected []string
		p.mu.Lock()
		for wd, r := range p.works {
			if r.refs <= 0 || !r.enabled {
				continue
			}
			old := r.cfgExts
			switch key {
			case skipDirsKey:
				old = r.cfgSkipDirs
			case stackGitignoreKey:
				old = r.cfgStack
			}
			if !deleted && old == newRaw {
				continue // 值未变 → 幂等，不重建
			}
			switch key {
			case skipDirsKey:
				r.cfgSkipDirs = newRaw
			case stackGitignoreKey:
				r.cfgStack = newRaw
			default:
				r.cfgExts = newRaw
			}
			affected = append(affected, wd)
		}
		p.mu.Unlock()
		if len(affected) == 0 {
			return
		}
		sort.Strings(affected)
		for _, wd := range affected {
			wd := wd
			p.rebuild.schedule(wd, func() {
				p.logf("codegraph: prj-config %s 变更（op=%s，去抖收敛）→ workdir %s 强制重建索引", key, op, wd)
				p.ensureWorkspace(wd, true)
			})
		}
	case actionKey:
		// 动作信号（清除/重建/重试）：值 = 动作名。前端每次点击都写一次该键，persist save 恒广播
		// refresh（同值重复写也生效）→ 重复点击有效。仅对「启用中」的 workdir 生效；删键不作动作。
		if op == "delete" {
			return
		}
		act := ""
		if list != nil {
			act = normalizeAction(strval(list[actionKey]))
		}
		if act == "" {
			return // 未知/空值忽略（向前兼容）
		}
		var affected []string
		p.mu.Lock()
		for wd, r := range p.works {
			if r.refs > 0 && r.enabled {
				affected = append(affected, wd)
			}
		}
		p.mu.Unlock()
		if len(affected) == 0 {
			return
		}
		sort.Strings(affected)
		for _, wd := range affected {
			wd := wd
			switch act {
			case actionClear:
				p.logf("codegraph: %s 清除索引产物（prj-config %s=%s）", wd, actionKey, act)
				go p.clearWorkspace(wd)
			case actionRetry:
				// 引擎未记录失败明细（parse 失败的文件被静默跳过，无失败清单可重试）→ 降级为全量重建。
				p.logf("codegraph: %s 重试失败项——引擎未记录失败明细 → 降级为全量重建", wd)
				go p.ensureWorkspace(wd, true)
			default: // actionRebuild
				p.logf("codegraph: %s 强制重建索引（prj-config %s=%s）", wd, actionKey, act)
				go p.ensureWorkspace(wd, true)
			}
		}
	}
}

// normalizeAction 归一 codegraph.action 取值（大小写/空白容错）；未知/空 → ""（忽略）。
func normalizeAction(v string) string {
	switch a := strings.ToLower(strings.TrimSpace(v)); a {
	case actionRebuild, actionClear, actionRetry:
		return a
	}
	return ""
}

// ─── 启用编排（configure → 视就绪 initialize → 状态回写）─────────

// maybeEnsure 启用态且引擎状态未 ready（缓存判定）→ 后台自动 configure+initialize。
func (p *Codegraph) maybeEnsure(wd string) {
	p.mu.Lock()
	r := p.works[wd]
	active := r != nil && r.refs > 0 && r.enabled
	p.mu.Unlock()
	if !active {
		return
	}
	r.cmu.Lock()
	need := !r.busy && !r.ready()
	r.cmu.Unlock()
	if need {
		p.logf("codegraph: %s 已启用且索引未就绪 → 后台自动初始化", wd)
		go p.ensureWorkspace(wd, false)
	}
}

// ensureWorkspace 后台：configure(enabled:true, exts, skip_dirs, stack_gitignore) → status
// （先前已 ready 且非强制则跳过全量重建）→ 未就绪/强制则 initialize → status 回写 codegraph.status。
// 持有该 workdir 的 cmu 串行；force=true 用于索引配置变更后的重建。
func (p *Codegraph) ensureWorkspace(wd string, force bool) {
	p.mu.Lock()
	r := p.works[wd]
	p.mu.Unlock()
	if r == nil {
		return
	}
	r.cmu.Lock()
	defer r.cmu.Unlock()
	r.busy = true
	defer func() { r.busy = false }()
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()

	// 0) 读取项目级索引配置（扩展名 / 用户排除规则 / 是否叠加 gitignore）；未配置 → 下发空数组 = 引擎默认集
	exts, rules, stack := p.readIndexConfig(wd)
	// 1) configure：enabled/exts/skip_dirs/stack_gitignore 存档到引擎工作区（可见性门控在插件，引擎仅记录）
	p.pushStatusPhase(r, phaseConfigure, 0, 0) // 进度推送：进入 configure
	if _, err := p.callWithRetry(ctx, "codegraph_configure", map[string]any{
		"workdir": wd, "enabled": true, "exts": exts, "skip_dirs": rules, "stack_gitignore": stack,
	}); err != nil {
		p.saveStatusErr(r, "configure", err)
		return
	}
	// 2) 状态快检：先前会话已 ready（索引落盘）→ 不再全量重建（force 时跳过）
	if !force {
		if err := p.refreshStatus(r, ctx); err == nil && r.ready() {
			p.logf("codegraph: %s 引擎已就绪（索引缓存命中）", wd)
			return
		}
	}
	// 3) 未就绪（未初始化/索引中断）或强制重建 → 同步全量建索引（慢；调用方已放后台）。
	//    索引期间后台轮询引擎落盘 meta.json，把 done/total 实时回写 codegraph.status（进度推送）；
	//    失败按 Options.RetryAttempts/RetryBackoff 重试（缺省保守）。
	p.pushStatusPhase(r, phaseIndex, 0, 0) // 进度推送：进入全量索引
	stopPoll := p.startProgressPoll(r)
	_, ierr := p.callWithRetry(ctx, "codegraph_initialize", map[string]any{
		"workdir": wd, "exts": exts, "skip_dirs": rules, "stack_gitignore": stack,
	})
	stopPoll()
	if ierr != nil {
		p.saveStatusErr(r, "initialize", ierr)
		return
	}
	// 4) 状态回写 codegraph.status（UI 回显）
	if err := p.refreshStatus(r, ctx); err != nil {
		p.logf("codegraph: %s 初始化完成但状态查询/回写失败：%v", wd, err)
		return
	}
	p.logf("codegraph: %s 自动初始化完成", wd)
}

// clearWorkspace 清除该 workdir 的索引产物：调引擎 codegraph_configure {mode:"clear"}
// （引擎删除落盘索引库 index.db + 内存索引置空 + 状态回「未初始化」；配置存档保留）→ 再查引擎
// 状态回写 codegraph.status（复用既有状态面，UI 据此回显「未初始化」）。
// 与 ensureWorkspace 同持该 workdir 的 cmu（互斥索引编排，避免与在途重建互踩）。
func (p *Codegraph) clearWorkspace(wd string) {
	p.mu.Lock()
	r := p.works[wd]
	active := r != nil && r.refs > 0 && r.enabled
	p.mu.Unlock()
	if !active {
		return
	}
	r.cmu.Lock()
	defer r.cmu.Unlock()
	r.busy = true
	defer func() { r.busy = false }()
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()

	if _, err := p.callWithRetry(ctx, "codegraph_configure", map[string]any{
		"workdir": wd, "mode": "clear",
	}); err != nil {
		p.logf("codegraph: %s 清除索引失败：%v", wd, err)
		p.saveStatusErr(r, "clear", err)
		return
	}
	if err := p.refreshStatus(r, ctx); err != nil {
		p.logf("codegraph: %s 索引已清除但状态查询/回写失败：%v", wd, err)
		return
	}
	p.logf("codegraph: %s 索引已清除（状态回未初始化）", wd)
}

// ─── 索引配置变更去抖（一次保存两键 → 只重建一轮）─────────────

// rebuildDebouncer 按 key 合并短窗口内的多次调度：同一 key 在窗口内重复 schedule 只保留
// 最后一次（重置计时），窗口到期触发一次 fn。单次变更仍会（延迟 window 后）触发——不丢重建。
type rebuildDebouncer struct {
	window time.Duration
	mu     sync.Mutex
	timers map[string]*time.Timer
}

// newRebuildDebouncer 构造去抖器（window<=0 → defaultRebuildDebounce）。
func newRebuildDebouncer(window time.Duration) *rebuildDebouncer {
	if window <= 0 {
		window = defaultRebuildDebounce
	}
	return &rebuildDebouncer{window: window, timers: map[string]*time.Timer{}}
}

// schedule 登记 key 的一次触发：窗口内重复调度重置计时，仅最后一次生效。
func (d *rebuildDebouncer) schedule(key string, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t := d.timers[key]; t != nil {
		t.Stop()
	}
	d.timers[key] = time.AfterFunc(d.window, func() {
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
		fn()
	})
}

// instanceForWorkdir 返回该 workdir 任一活跃实例 id（读 prj-config 用；无则空串）。
func (p *Codegraph) instanceForWorkdir(wd string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var inst string
	for id, ir := range p.insts {
		if ir.workdir == wd && (inst == "" || id < inst) {
			inst = id
		}
	}
	return inst
}

// readIndexConfig 读取项目级索引配置：codegraph.exts / codegraph.skip-dirs /
// codegraph.stack-gitignore。
// 读取失败/未配置 → 返回空切片（下发空数组 = 引擎默认集，不写第二套默认）。
// skip-dirs / stack-gitignore 的解析统一由 github.com/chonkpilot/chonkpilot-ignore 提供
// （ignore.ConfigOptions = 规则拆分 + 叠加开关；匹配语义在引擎侧单点实现，本插件不读 .gitignore）。
// 首次读取时播种「比较基线」（cfgExts/cfgSkipDirs/cfgStack）= 本次下发/生效的原始值，
// 使「重复保存同一份配置」不触发重建；此后基线只由 onPrjConfigRefresh 更新。
func (p *Codegraph) readIndexConfig(wd string) (exts, rules []string, stack bool) {
	inst := p.instanceForWorkdir(wd)
	if inst == "" {
		return []string{}, []string{}, false
	}
	rawExts, rawDirs, rawStack := "", "", ""
	if v, err := prjConfigReadKey(p.deps.Bus, inst, extsKey); err == nil {
		rawExts = v
		exts = splitList(v)
	}
	// 取值器顺带记录 skip-dirs / stack-gitignore 的原始值（供幂等比较基线播种）；
	// 规则拆分与叠加开关语义由 ignore.ConfigOptions 单点提供。
	get := func(key string) (string, bool) {
		v, err := prjConfigReadKey(p.deps.Bus, inst, key)
		if err != nil {
			return "", false
		}
		switch key {
		case skipDirsKey:
			rawDirs = v
		case stackGitignoreKey:
			rawStack = v
		}
		return v, true
	}
	if opts, _, err := ignore.ConfigOptions(engineName, get, wd); err == nil && opts != nil {
		rules = opts.UserRules
		stack = opts.StackGitignore
	}
	if exts == nil {
		exts = []string{}
	}
	if rules == nil {
		rules = []string{}
	}
	p.mu.Lock()
	if r := p.works[wd]; r != nil && !r.cfgSeen {
		r.cfgExts, r.cfgSkipDirs, r.cfgStack, r.cfgSeen = rawExts, rawDirs, rawStack, true
	}
	p.mu.Unlock()
	return exts, rules, stack
}

// splitList 解析项目级列表配置（扩展名）：按逗号/分号/换行分隔，去空去重。
func splitList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// splitRules 解析项目级排除规则（skip-dirs）：按逗号/分号/换行分隔，去空。
// **保序且保留重复项**——gitignore 语义下顺序有意义（'!' 取反 + 后一条覆盖前一条）。
func splitRules(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

// ─── 匹配语义落点 ────────────────────────────────────────
//
// .gitignore / 全局 ignore / .git/info/exclude 的读取与匹配全在**引擎侧**（引擎才知道遍历
// 上下文与逐级目录），由 github.com/chonkpilot/chonkpilot-ignore 单一实现：
// 本插件只把 codegraph.skip-dirs（用户规则）与 codegraph.stack-gitignore（叠加开关）原样下发。

// clientFor 返回该 workdir 的引擎子进程客户端（每 workdir 一独立子进程，首调懒建；
// 常驻复用，空闲由 sweepIdleClient 按 workdir 回收）。
func (p *Codegraph) clientFor(wd string) engineClient {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if rec := p.clients[wd]; rec != nil {
		rec.lastUsed = time.Now()
		return rec.c
	}
	rec := &clientRec{c: newClient(p.exe), lastUsed: time.Now()}
	p.clients[wd] = rec
	return rec.c
}

// engineCall 调引擎工具（client 取自 args["workdir"] 对应的**该 workdir 独立**子进程；
// client 内部串行，传输失败自动重连一次）。
func (p *Codegraph) engineCall(ctx context.Context, name string, args map[string]any) (string, error) {
	wd, _ := args["workdir"].(string)
	text, isErr, err := p.clientFor(wd).call(ctx, name, args)
	if err != nil {
		return text, err
	}
	if isErr {
		return text, errors.New(text)
	}
	return text, nil
}

// callWithRetry 调引擎工具并在失败时按 Options.RetryAttempts/RetryBackoff 重试（含首次）。
func (p *Codegraph) callWithRetry(ctx context.Context, name string, args map[string]any) (string, error) {
	var text string
	err := runWithRetry(ctx, p.opt.RetryAttempts, p.opt.RetryBackoff, func() error {
		var e error
		text, e = p.engineCall(ctx, name, args)
		return e
	})
	return text, err
}

// runWithRetry 执行 fn 至多 attempts 次（首次 + attempts-1 次重试）；每次失败后按 backoff 退避，
// ctx 取消即停。attempts <= 0 视为 1。返回最后一次错误（全部成功则 nil）。
func runWithRetry(ctx context.Context, attempts int, backoff time.Duration, fn func() error) error {
	if attempts <= 0 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		if backoff > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
	}
	return err
}

// ─── 索引进度推送（复用 codegraph.status 状态面，不新增消息主题）──────────

// pushStatusPhase 把索引阶段的进度快照回写 codegraph.status（仅落盘 prj-config）；
// 不写内存缓存 r.state（缓存由 refreshStatus/saveStatusErr 在流程收口统一维护，避免与
// 后台轮询 goroutine 争写）。
func (p *Codegraph) pushStatusPhase(r *workRec, phase string, done, total int) {
	p.saveStatusRaw(r, phaseStatusJSON(phase, done, total))
}

// phaseStatusJSON 构造进度阶段状态 JSON（state 恒 indexing，直至收口写 ready/error）。
func phaseStatusJSON(phase string, done, total int) string {
	b, _ := json.Marshal(map[string]any{
		"state":         "indexing",
		"phase":         phase,
		"progressDone":  done,
		"progressTotal": total,
	})
	return string(b)
}

// readEngineProgress 读取引擎落盘 meta.json（<workdir>/.chonkpilot/codegraph/meta.json）的
// 索引状态与进度（引擎 Initialize 期间按批写入；跨进程读取）。
func readEngineProgress(wd string) (state string, done, total int, ok bool) {
	b, err := os.ReadFile(filepath.Join(wd, filepath.FromSlash(engineMetaRel)))
	if err != nil {
		return "", 0, 0, false
	}
	var m struct {
		State         string `json:"state"`
		ProgressDone  int    `json:"progressDone"`
		ProgressTotal int    `json:"progressTotal"`
	}
	if json.Unmarshal(b, &m) != nil {
		return "", 0, 0, false
	}
	return m.State, m.ProgressDone, m.ProgressTotal, true
}

// pollEngineProgress 轮询引擎 meta.json：state=indexing 且 done/total 变化时回调 onUpdate；
// stop 关闭即退出。抽为独立函数便于白盒测试。
func pollEngineProgress(stop <-chan struct{}, wd string, interval time.Duration, onUpdate func(done, total int)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	last := ""
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		state, done, total, ok := readEngineProgress(wd)
		if !ok || state != "indexing" {
			continue
		}
		key := fmt.Sprintf("%d/%d", done, total)
		if key == last {
			continue
		}
		last = key
		onUpdate(done, total)
	}
}

// startProgressPoll 启动索引进度轮询（返回幂等停止函数）：索引进行中把 done/total 变化
// 回写 codegraph.status。
func (p *Codegraph) startProgressPoll(r *workRec) func() {
	stop := make(chan struct{})
	var once sync.Once
	go pollEngineProgress(stop, r.workDir, progressPollInterval, func(done, total int) {
		p.pushStatusPhase(r, phaseIndex, done, total)
	})
	return func() { once.Do(func() { close(stop) }) }
}

// refreshStatus 调 codegraph_status 缓存状态并回写 codegraph.status（须持 r.cmu）。
func (p *Codegraph) refreshStatus(r *workRec, ctx context.Context) error {
	text, err := p.engineCall(ctx, "codegraph_status", map[string]any{"workdir": r.workDir})
	if err != nil {
		return err
	}
	r.state = text
	p.saveStatusRaw(r, text)
	return nil
}

// saveStatusErr 引擎管理流程失败 → 构造 {"state":"error",...} JSON 落缓存并回写。
func (p *Codegraph) saveStatusErr(r *workRec, phase string, err error) {
	msg := err.Error()
	b, _ := json.Marshal(map[string]any{"state": "error", "phase": phase, "message": msg})
	r.state = string(b)
	p.saveStatusRaw(r, r.state)
}

// saveStatusRaw 把引擎状态 JSON 文本写回 prj-config codegraph.status（取该 workdir
// 任一实例的 prj 库；无活跃实例则仅缓存内存，下次注册再随 enable 流程回写）。
func (p *Codegraph) saveStatusRaw(r *workRec, raw string) {
	p.mu.Lock()
	var inst string
	for id, ir := range p.insts {
		if ir.workdir == r.workDir && (inst == "" || id < inst) {
			inst = id
		}
	}
	p.mu.Unlock()
	if inst == "" {
		return
	}
	if err := prjConfigSaveKey(p.deps.Bus, inst, statusKey, raw); err != nil {
		p.logf("codegraph: 回写 %s 失败（instance=%s）：%v", statusKey, inst, err)
	}
}

// ready 解析缓存状态 JSON 的 state 字段（须持 r.cmu；"ready" = 索引已就绪）。
func (r *workRec) ready() bool {
	var s struct {
		State string `json:"state"`
	}
	if r.state == "" || json.Unmarshal([]byte(r.state), &s) != nil {
		return false
	}
	return s.State == "ready"
}

// ─── 工具注册/注销收敛（gateway 全局一份）────────────────

// syncTools 决定并执行 gateway 工具注册/注销（syncMu 单飞）：
//
//	应注册 = 存在某 workdir：refs>0 且 enabled=true。
//
// 工具面仍全局一份（gateway tools/register 无 scope 扩展，且工具名须与引擎契约同名，
// 同名 (scope=global, name) 只能注册一份）：多个 workdir 同时启用时仅全局注册一份
// （归属排序后第一个 workdir），执行时经回调 context.instance_id 路由回正确 workdir。
// 引擎子进程/索引状态自 P2-4 起已**按 workdir 独立**（p.clients），工具面差异注册留待 v2。
func (p *Codegraph) syncTools() {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()

	p.mu.Lock()
	var active []string
	for wd, r := range p.works {
		if r.refs > 0 && r.enabled {
			active = append(active, wd)
		}
	}
	sort.Strings(active)
	want := len(active) > 0
	owner := ""
	if want {
		owner = active[0]
	}
	registered := p.registered
	p.mu.Unlock()

	switch {
	case !want:
		if !registered {
			return
		}
		if err := p.unregisterAll(); err != nil {
			p.logf("codegraph: gateway 工具注销失败（后续事件重试）：%v", err)
			return
		}
		p.registered = false
		p.regOwner = ""
		p.logf("codegraph: gateway 查询工具已注销（无启用中的 workdir）")
	case !registered:
		if err := p.registerAll(); err != nil {
			p.logf("codegraph: gateway 工具注册失败（保持未注册，后续事件重试）：%v", err)
			return
		}
		p.registered = true
		p.regOwner = owner
		if len(active) > 1 {
			p.logf("codegraph: 多个 workdir 同时启用（%s）——工具面全局仅注册一份（归属 %s，执行按 context.instance_id 路由）；各 workdir 引擎子进程/索引独立", strings.Join(active, ", "), owner)
		}
		p.logf("codegraph: gateway 查询工具已注册（%d 个，owner=%s）", len(queryTools), owner)
	case owner != p.regOwner:
		// 仍应注册但归属 workdir 已迁移（原归属停用/退出、其他 workdir 仍启用）：
		// 工具同名同回调主题，无需重注册，仅迁移归属记录。
		p.regOwner = owner
		if len(active) > 1 {
			p.logf("codegraph: 注册归属迁移至 %s（多 workdir 并存，工具面仍全局一份）", owner)
		}
	default:
		// 已注册且归属未变：保持现状。
		_ = len(active) > 1 // 工具面差异注册受 gateway 无 scope 限制，明确不做
	}
}

// ─── exe 路径解析 ────────────────────────────────────────

// resolveExe 定位引擎可执行文件：Options.Exe > 环境变量 CODEGRAPH_EXE > 运行 exe 同目录
// 下的 `mcps/codebase/`（发行布局：`<exeDir>/mcps/codebase/chonkpilot-codegraph-mcp-server.exe`）。
// 找不到返回 error（Start 失败）—— 不做多路径猜测。
func resolveExe(explicit string) (string, error) {
	abs := func(p string) (string, error) {
		a, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return a, nil
	}
	if explicit != "" {
		return abs(explicit)
	}
	if v := os.Getenv("CODEGRAPH_EXE"); v != "" {
		return abs(v)
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("codegraph: 定位引擎失败（os.Executable: %v）", err)
	}
	dir := filepath.Dir(self)
	const exeName = "chonkpilot-codegraph-mcp-server.exe"
	c := filepath.Join(dir, "mcps", "codebase", exeName) // <exeDir>/mcps/codebase/
	if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
		return abs(c)
	}
	return "", fmt.Errorf("codegraph: 引擎未找到——请设置 Options.Exe / 环境变量 CODEGRAPH_EXE，或将 %s 置于 <exeDir>/mcps/codebase/", exeName)
}

// strval 任意值 → 字符串（配置 list 值归一）。
func strval(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// ─── data-* 请求-响应（persist 同主题 promise + req_id 关联）──

var reqSeq atomic.Uint64

func newReqID() string {
	return fmt.Sprintf("codegraph-%d-%d", time.Now().UnixNano(), reqSeq.Add(1))
}

// dataEmit 向 data-<域>-<动作> 请求面发一次请求并等应答（镜像 llm dataclient 模式：
// persist 把应答 fire-and-forget 发布到请求同一主题，须按 req_id 关联收敛）。
func dataEmit(bus mq.Bus, subject string, req map[string]any) (map[string]any, error) {
	req["req_id"] = newReqID()
	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != req["req_id"] || m.OK == nil {
			return nil // 他人请求/应答忽略
		}
		if !*m.OK {
			msg := m.Error
			if msg == "" {
				msg = "persist error"
			}
			done <- reply{err: errors.New(msg)}
			return nil
		}
		done <- reply{result: m.Result}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if f := bus.Emit(context.Background(), subject, req); f.Wait().Err() != nil {
		return nil, f.Wait().Err()
	}
	select {
	case r := <-done:
		return r.result, r.err
	case <-time.After(dataTimeout):
		return nil, fmt.Errorf("%s via persist 应答超时", subject)
	}
}

// prjConfigReadKey 读 prj-config 单键：load 应答 result.data = 值字符串（键不存在 → ""）。
func prjConfigReadKey(bus mq.Bus, instanceID, key string) (string, error) {
	res, err := dataEmit(bus, subjectPrjConfigLoad, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"id": key},
	})
	if err != nil {
		return "", err
	}
	val, _ := res["data"].(string)
	return val, nil
}

// prjConfigSaveKey 写 prj-config 单键（值 = 字符串；save 载荷 {data:{key,value}}）。
func prjConfigSaveKey(bus mq.Bus, instanceID, key, value string) error {
	_, err := dataEmit(bus, subjectPrjConfigSave, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"key": key, "value": value},
	})
	return err
}
