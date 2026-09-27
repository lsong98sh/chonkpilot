// Package vfts 是 vfts 全文检索插件的宿主侧实现（server 内嵌插件钩子，无 CGO）。
//
// 定位（对齐 42 §2 (15)/(17)/(27) 与 codegraph 同构）：
//   - vfts 引擎 = 独立 exe（chonkpilot-vfts-mcp-server，console CGO + zvec），由本插件按
//     workdir 以 stdio 子进程形态管理；引擎全部工具带 workdir 参数（含查询工具）。
//   - 本插件对外（gateway）注册查询工具：与引擎同名、schema 去掉 workdir（由插件注入）。
//     可见性门控（enable-vfts）与索引就绪编排全在插件内存（源 = prj-config）。
//   - instance 生命周期驱动 workdir 引用计数与 gateway 工具注册/注销：register/exit 为变更源
//     （heartbeat 记录时刻；合并单进程形态不发布心跳、不做心跳超时退出判定，分离形态
//     `-tags split` 由 sweepInstances 判定超时 → instanceGone）；
//     enable-vfts 开关经 data-prj-config-refresh 实时同步。
//   - 引擎应答一律为 JSON 文本：查询未就绪（not_initialized/indexing/error 等）原样透传，
//     插件不做隐式补 init（仅启用时后台自动 configure+index 一次，状态回写 vfts.status）。
package vfts

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
	// 订阅者写回 v.Result，同一主题 promise——镜像 codegraph 插件模式）
	toolCallSubject = "vfts-tool-call"

	// prj-config 键
	engineName        = "vfts"                 // 引擎标识（配置键前缀 = 引擎名；ignore.ConfigOptions 用）
	enableKey         = "enable-vfts"          // "true"/"false"（UI 开关；缺省 = 关闭，对齐 42 §2 (17)）
	extsKey           = "vfts.exts"            // 参与索引的扩展名（逗号/换行分隔；空 = 引擎默认集）
	skipDirsKey       = "vfts.skip-dirs"       // 用户排除规则（gitignore 语法，逗号/换行分隔；空 = 无用户规则）
	stackGitignoreKey = "vfts.stack-gitignore" // "true"/"false"：是否让引擎/清单扫描叠加各级 .gitignore / info/exclude / 全局 ignore
	statusKey         = "vfts.status"          // 引擎状态 JSON 文本（插件回写，UI 只读回显）

	// data 面（persist 订阅）
	subjectPrjConfigRefresh = "data-prj-config-refresh"
	subjectPrjConfigLoad    = "data-prj-config-load"
	subjectPrjConfigSave    = "data-prj-config-save"

	// 周期/超时
	sweepInterval    = 15 * time.Second // 空闲子进程回收周期
	childIdleTimeout = 5 * time.Minute  // 无活跃实例后空闲引擎子进程回收阈值（sweepIdleClient 消费）
	dataTimeout      = 5 * time.Second  // data-* 请求应答超时
	callTimeout      = 60 * time.Second // 查询工具引擎调用超时
	initTimeout      = 15 * time.Minute // 管理工具（configure/index/status）调用超时

	// 索引配置去抖窗口：设置页一次保存会分别写 vfts.exts + vfts.skip-dirs 两键 →
	// 两次 data-prj-config-refresh；短窗口内合并为一次强制重建（同一份配置不重建两轮）。
	// 窗口外的单次键变更仍会（延迟 window 后）触发一次重建——不会出现"改了不重建"。
	defaultRebuildDebounce = 400 * time.Millisecond

	// 索引进度：轮询引擎落盘 meta.json（Initialize 期间按批写入，见引擎 index.go）的间隔；
	// 读到的 done/total 变化即回写 vfts.status（复用既有状态面，不新增消息主题）。
	progressPollInterval = 500 * time.Millisecond
	engineMetaRel        = ".chonkpilot/vfts/meta.json"

	// 索引编排阶段（进度快照 JSON 的 phase 字段）
	phaseConfigure = "configure"
	phaseIndex     = "index"
)

// Options 构造参数。
type Options struct {
	// Exe vfts 引擎可执行文件路径；空 = 自动解析（见 resolveExe）。
	Exe string
	// RebuildDebounce 索引配置变更去抖窗口；<=0 → defaultRebuildDebounce。
	RebuildDebounce time.Duration
}

// Vfts 是 vfts 插件（plugin.Hook 实现）。
type Vfts struct {
	deps plugin.Deps
	logf func(format string, args ...any)
	exe  string // 引擎 exe 绝对路径（resolveExe 结果）
	opt  Options

	mu           sync.Mutex // 保护 insts / works / registered / regOwner / lastActiveAt
	insts        map[string]*instRec
	works        map[string]*workRec // key = workdir（Clean 后绝对路径）
	registered   bool                // 查询工具是否已注册到 gateway（全局仅一份）
	regOwner     string              // 当前注册归属的 workdir（多 workdir 同时启用时取第一个）
	lastActiveAt time.Time           // 最近一次观察到活跃实例的时刻（sweepIdleClient 空闲回收基准）
	syncMu       sync.Mutex          // syncTools 单飞（防并发重复注册/注销）
	clientMu     sync.Mutex          // 共享引擎子进程懒建/回收保护
	client       *client             // 全局共享引擎子进程客户端（一对多；无活跃实例超 childIdleTimeout 后回收）
	rebuild      *rebuildDebouncer   // 索引配置变更去抖（一次保存两键 → 只重建一次）

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
// 引擎子进程为全局共享单例（p.client，一对多），按工具参数 workdir 路由。
type workRec struct {
	workDir string
	dataDir string // 首个实例自带 data_dir（预留）
	enabled bool   // enable-vfts == "true"
	refs    int    // 活跃实例引用数（同 workdir 多实例去重）

	cmu   sync.Mutex
	busy  bool   // 后台 configure/index 流程进行中（同 workdir 串行）
	state string // 最近一次 vfts_status 原始 JSON 文本（= vfts.status 同源）
}

// New 构建 vfts 插件。
func New(opts Options) *Vfts {
	return &Vfts{
		opt:     opts,
		insts:   make(map[string]*instRec),
		works:   make(map[string]*workRec),
		rebuild: newRebuildDebouncer(opts.RebuildDebounce),
	}
}

// Name 插件名。
func (p *Vfts) Name() string { return "vfts" }

// Start 解析引擎路径、订阅生命周期/配置/工具回调并启动后台清扫（宿主在全部插件前序就绪后调用）。
func (p *Vfts) Start(d plugin.Deps) error {
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
	// prj-config 变更广播（enable-vfts 开关；order 0）
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

	logf("vfts: 插件就绪（引擎 exe=%s，回调主题=%s）", p.exe, toolCallSubject)
	return nil
}

func (p *Vfts) unsubscribeAll() {
	for _, s := range p.subs {
		_ = s.Unsubscribe()
	}
	p.subs = nil
}

// ─── 后台循环 ────────────────────────────────────────────

// loop 后台循环：周期执行 tick（空闲子进程回收）。
func (p *Vfts) loop() {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for range t.C {
		p.tick()
	}
}

// tick 单轮周期维护：实例心跳超时判定（形态开关，见 sweep_split.go / sweep_inprocess.go）
// + 回收空闲引擎子进程。实例退出一律走既有 instanceGone 路径：显式 instance-exit（GUI/CLI
// 退出注销）恒有效；心跳超时仅在分离形态（`-tags split`）编入，合并单进程形态为空实现。
func (p *Vfts) tick() {
	p.sweepInstances(time.Now())
	p.sweepIdleClient()
}

// sweepIdleClient 回收空闲引擎子进程：无任何活跃实例（refs>0），且距最近一次活跃
// 已超 childIdleTimeout → 关闭共享 client（下次调用懒重建）。
func (p *Vfts) sweepIdleClient() {
	now := time.Now()
	p.mu.Lock()
	anyActive := false
	for _, r := range p.works {
		if r.refs > 0 {
			anyActive = true
			break
		}
	}
	last := p.lastActiveAt
	if anyActive {
		p.lastActiveAt = now
	}
	p.mu.Unlock()

	if !idleReclaimDue(anyActive, last, now, childIdleTimeout) {
		return
	}
	p.clientMu.Lock()
	c := p.client
	p.client = nil
	p.clientMu.Unlock()
	if c != nil {
		p.logf("vfts: 引擎子进程空闲超 %v → 回收（下次调用懒重建）", childIdleTimeout)
		c.close()
	}
}

// idleReclaimDue 判定是否应回收空闲引擎子进程（纯函数，便于白盒）：无活跃实例、
// 曾有过活跃记录（lastActiveAt 非零）、且距最近活跃已达 timeout。
func idleReclaimDue(anyActive bool, lastActiveAt, now time.Time, timeout time.Duration) bool {
	if anyActive || lastActiveAt.IsZero() {
		return false
	}
	return now.Sub(lastActiveAt) >= timeout
}

// ─── instance 生命周期 ──────────────────────────────────────

func (p *Vfts) onInstanceRegister(_ string, payload []byte) {
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
	p.lastActiveAt = time.Now() // 活跃标记（空闲回收基准）
	p.mu.Unlock()

	// 异步回读一次 enable-vfts（慢请求不进总线派发路径）；读回后据开关触发
	// 自动初始化并同步 gateway 工具注册。
	go p.readEnableAndEnsure(ev.InstanceID, wd)
}

func (p *Vfts) onInstanceHeartbeat(_ string, payload []byte) {
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
	p.lastActiveAt = time.Now() // 活跃标记（空闲回收基准）
	p.mu.Unlock()
}

func (p *Vfts) onInstanceExit(_ string, payload []byte) {
	var ev struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.InstanceID == "" {
		return
	}
	p.instanceGone(ev.InstanceID)
}

// instanceGone 实例退出统一路径：解绑并递减所属 workdir 引用，引用归零 → 注销工具（若适用）。
func (p *Vfts) instanceGone(id string) {
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
		p.logf("vfts: workdir %s 已无活跃实例（记录保留，空闲子进程由清扫回收）", r.workDir)
	}
	p.syncTools()
}

// readEnableAndEnsure 注册后异步回读 enable-vfts 并据此收敛可见性。
func (p *Vfts) readEnableAndEnsure(instanceID, wd string) {
	val, err := prjConfigReadKey(p.deps.Bus, instanceID, enableKey)
	if err != nil {
		p.logf("vfts: 读 prj-config %s 失败（instance=%s）：%v（视为未启用）", enableKey, instanceID, err)
	}
	enabled := val == "true"
	p.mu.Lock()
	r := p.works[wd]
	if r == nil || r.refs <= 0 {
		p.mu.Unlock() // 读回前实例已退出
		return
	}
	if r.enabled != enabled {
		p.logf("vfts: %s → %v（instance=%s）", wd, enabled, instanceID)
	}
	r.enabled = enabled
	p.mu.Unlock()
	if enabled {
		p.maybeEnsure(wd)
	}
	p.syncTools()
}

// ─── prj-config 变更 ──────────────────────────────────────

// onPrjConfigRefresh 处理 data-prj-config-refresh：关心 enable-vfts 与索引配置
// （vfts.exts / vfts.skip-dirs）。
// 广播载荷 {id, op, list}，无 instance_id（persist refresh 不携带）——v1 限制：
// 单宿主典型形态为单实例/单 workdir，直接把开关应用到全部活跃 workdir；
// 多 workdir 并存时按同一开关刷新（差异化的按 instance 隔离留待 v2）。
func (p *Vfts) onPrjConfigRefresh(_ context.Context, _ string, v *mq.Value) error {
	var ev struct {
		ID   string         `json:"id"`
		Op   string         `json:"op"`
		List map[string]any `json:"list"`
	}
	if json.Unmarshal(v.Payload, &ev) != nil {
		return nil
	}
	switch ev.ID {
	case enableKey:
		enabled := false
		if ev.List != nil {
			if raw, ok := ev.List[enableKey]; ok {
				enabled = strval(raw) == "true"
			}
		}
		if ev.Op == "delete" {
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
			return nil
		}
		sort.Strings(affected)
		p.logf("vfts: prj-config %s=%v（op=%s）→ workdir %v", enableKey, enabled, ev.Op, affected)
		for _, wd := range affected {
			if enabled {
				p.maybeEnsure(wd)
			}
		}
		p.syncTools()
	case extsKey, skipDirsKey, stackGitignoreKey:
		// 索引配置变更 → 对启用中的 workdir 强制重建索引（exts/skip-dirs/stack-gitignore 变更
		// 均须重建才生效）。
		// 经去抖合并：一次保存（同一次操作写多键 = 多次 refresh）只重建一轮。
		var affected []string
		p.mu.Lock()
		for wd, r := range p.works {
			if r.refs > 0 && r.enabled {
				affected = append(affected, wd)
			}
		}
		p.mu.Unlock()
		sort.Strings(affected)
		id, op := ev.ID, ev.Op
		for _, wd := range affected {
			wd := wd
			p.rebuild.schedule(wd, func() {
				p.logf("vfts: prj-config %s 变更（op=%s，去抖收敛）→ workdir %s 强制重建索引", id, op, wd)
				p.ensureWorkspace(wd, true)
			})
		}
	}
	return nil
}

// ─── 启用编排（configure → 视就绪 index → 状态回写）─────────

// maybeEnsure 启用态且引擎状态未 ready（缓存判定）→ 后台自动 configure+index。
func (p *Vfts) maybeEnsure(wd string) {
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
		p.logf("vfts: %s 已启用且索引未就绪 → 后台自动初始化", wd)
		go p.ensureWorkspace(wd, false)
	}
}

// ensureWorkspace 后台：configure(enabled:true, exts, skip_dirs) → status →
//   - 已就绪且非强制 → **增量同步**（扫描 → file_list diff → 按文件增量 → 回写清单）；
//   - 未就绪/强制（索引配置变更）→ 全量重建 → 重建清单。
//
// 末了回写 vfts.status（含 added/updated/removed/skipped 计数）。持有该 workdir 的 cmu 串行。
func (p *Vfts) ensureWorkspace(wd string, force bool) {
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
	if _, err := p.engineCall(ctx, "vfts_configure", map[string]any{
		"workdir": wd, "enabled": true, "exts": exts, "skip_dirs": rules, "stack_gitignore": stack,
	}); err != nil {
		p.saveStatusErr(r, "configure", err)
		return
	}
	// 2) 状态快检：先前会话已 ready（索引落盘）→ 走增量
	ready := false
	if err := p.refreshStatus(r, ctx); err == nil && r.ready() {
		ready = true
	}
	// 3) 已就绪且非强制 → 增量同步（清单驱动的按文件增量）
	if ready && !force {
		st, err := p.incrementalSync(r, ctx, effectiveExts(r.state, exts), rules, stack)
		if err != nil {
			p.logf("vfts: %s 增量同步失败：%v", wd, err)
			p.saveStatusErr(r, "sync", err)
			return
		}
		p.logf("vfts: %s 增量同步完成（added=%d updated=%d removed=%d skipped=%d）",
			wd, st.Added, st.Updated, st.Removed, st.Skipped)
		// 同步后重取状态（total/chunks 反映本次增量结果），再并入增量计数回写
		if err := p.refreshStatus(r, ctx); err != nil {
			p.logf("vfts: %s 增量后状态回读失败：%v", wd, err)
		}
		p.mergeSyncStatus(r, st)
		return
	}
	// 4) 未就绪（未初始化/索引中断）或强制重建 → 同步全量建索引（慢；调用方已放后台）。
	//    索引期间后台轮询引擎落盘 meta.json，把 done/total 实时回写 vfts.status（进度推送）。
	p.pushStatusPhase(r, phaseIndex, 0, 0) // 进度推送：进入全量索引
	stopPoll := p.startProgressPoll(r)
	text, err := p.engineCall(ctx, "vfts_index", map[string]any{
		"workdir": wd, "exts": exts, "skip_dirs": rules, "stack_gitignore": stack,
	})
	stopPoll()
	if err != nil {
		p.saveStatusErr(r, "index", err)
		return
	}
	var res engineIndexResult
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		p.saveStatusErr(r, "index", errors.New("vfts_index 应答解析失败: "+err.Error()))
		return
	}
	// 5) 状态回写（取生效扩展名）→ 清单重建（引擎应答已含逐文件 doc_ids）
	if err := p.refreshStatus(r, ctx); err != nil {
		p.logf("vfts: %s 重建完成但状态查询/回写失败：%v", wd, err)
	}
	effExts := effectiveExts(r.state, exts)
	if len(effExts) == 0 {
		// 生效扩展名不可得时**不重建清单**（否则会把整张表误判为待删除而清空）
		p.logf("vfts: %s 清单重建跳过（未取到生效扩展名）", wd)
	} else if err := p.rebuildManifest(r, res, effExts, rules, stack); err != nil {
		p.logf("vfts: %s 清单重建失败：%v", wd, err)
	}
	p.mergeSyncStatus(r, &syncStats{Added: res.Added})
	p.logf("vfts: %s 全量重建完成（%d 文件 / %d 块）", wd, res.Files, res.Chunks)
}

// ─── 索引进度推送（复用 vfts.status 状态面，不新增消息主题）──────────

// pushStatusPhase 把索引阶段的进度快照回写 vfts.status（仅落盘 prj-config）；
// 不写内存缓存 r.state（缓存由 refreshStatus/saveStatusErr 在流程收口统一维护，避免与
// 后台轮询 goroutine 争写）。
func (p *Vfts) pushStatusPhase(r *workRec, phase string, done, total int) {
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

// readEngineProgress 读取引擎落盘 meta.json（<workdir>/.chonkpilot/vfts/meta.json）的
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
// 回写 vfts.status。
func (p *Vfts) startProgressPoll(r *workRec) func() {
	stop := make(chan struct{})
	var once sync.Once
	go pollEngineProgress(stop, r.workDir, progressPollInterval, func(done, total int) {
		p.pushStatusPhase(r, phaseIndex, done, total)
	})
	return func() { once.Do(func() { close(stop) }) }
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
func (p *Vfts) instanceForWorkdir(wd string) string {
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

// readIndexConfig 读取项目级索引配置：vfts.exts / vfts.skip-dirs / vfts.stack-gitignore。
// 读取失败/未配置 → 返回空切片（下发空数组 = 引擎默认集，不写第二套默认）。
// skip-dirs / stack-gitignore 的解析统一由 github.com/chonkpilot/chonkpilot-ignore 提供
// （ignore.ConfigOptions = 规则拆分 + 叠加开关；匹配语义在引擎侧与本插件清单扫描共用同一实现）。
func (p *Vfts) readIndexConfig(wd string) (exts, rules []string, stack bool) {
	inst := p.instanceForWorkdir(wd)
	if inst == "" {
		return []string{}, []string{}, false
	}
	if v, err := prjConfigReadKey(p.deps.Bus, inst, extsKey); err == nil {
		exts = splitList(v)
	}
	// 用户排除规则（gitignore 语法，原样透传：不折名、不丢 '!'/glob）+ 是否叠加 ignore 体系：
	// 均由 ignore.ConfigOptions 按引擎配置键统一组装。
	if opts, _, err := ignore.ConfigOptions(engineName, p.prjConfigGetter(inst), wd); err == nil && opts != nil {
		rules = opts.UserRules
		stack = opts.StackGitignore
	}
	if exts == nil {
		exts = []string{}
	}
	if rules == nil {
		rules = []string{}
	}
	return exts, rules, stack
}

// prjConfigGetter 把 prj-config 单键读封装为 ignore.ConfigOptions 的取值器（键不存在/读取失败 → ok=false）。
func (p *Vfts) prjConfigGetter(inst string) func(key string) (string, bool) {
	return func(key string) (string, bool) {
		v, err := prjConfigReadKey(p.deps.Bus, inst, key)
		return v, err == nil
	}
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

// ─── 匹配语义落点（vfts.stack-gitignore）────────────────
//
// .gitignore / 全局 ignore / .git/info/exclude 的读取与匹配由 github.com/chonkpilot/chonkpilot-ignore
// 单一实现：引擎遍历（collectFiles）与本插件清单扫描（manifest.scanFiles）共用同一实现 +
// 同一份规则来源（用户规则 + stack 开关），故索引集合与 file_list 清单不会出现两套过滤。
// 本插件不自行解析 .gitignore，只把 vfts.skip-dirs / vfts.stack-gitignore 原样下发。

// sharedClient 返回全局共享引擎子进程客户端（懒建；一对多，常驻复用，空闲由 sweepIdleClient 回收）。
func (p *Vfts) sharedClient() *client {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.client == nil {
		p.client = newClient(p.exe)
	}
	return p.client
}

// engineCall 调引擎工具（全局共享 client；client 内部串行，失败重连一次）。
func (p *Vfts) engineCall(ctx context.Context, name string, args map[string]any) (string, error) {
	text, isErr, err := p.sharedClient().call(ctx, name, args)
	if err != nil {
		return text, err
	}
	if isErr {
		return text, errors.New(text)
	}
	return text, nil
}

// refreshStatus 调 vfts_status 缓存状态并回写 vfts.status（须持 r.cmu）。
func (p *Vfts) refreshStatus(r *workRec, ctx context.Context) error {
	text, err := p.engineCall(ctx, "vfts_status", map[string]any{"workdir": r.workDir})
	if err != nil {
		return err
	}
	r.state = text
	p.saveStatusRaw(r, text)
	return nil
}

// saveStatusErr 引擎管理流程失败 → 构造 {"state":"error",...} JSON 落缓存并回写。
func (p *Vfts) saveStatusErr(r *workRec, phase string, err error) {
	msg := err.Error()
	b, _ := json.Marshal(map[string]any{"state": "error", "phase": phase, "message": msg})
	r.state = string(b)
	p.saveStatusRaw(r, r.state)
}

// saveStatusRaw 把引擎状态 JSON 文本写回 prj-config vfts.status（取该 workdir
// 任一实例的 prj 库；无活跃实例则仅缓存内存，下次注册再随 enable 流程回写）。
func (p *Vfts) saveStatusRaw(r *workRec, raw string) {
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
		p.logf("vfts: 回写 %s 失败（instance=%s）：%v", statusKey, inst, err)
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
// v1 限制（gateway tools/register 无 scope 扩展，同名 (scope=global, name) 只能注册一份）：
// 多个 workdir 同时启用时仅全局注册一份（归属排序后第一个 workdir），执行时经回调
// context.instance_id 路由回正确 workdir；差异化的 per-workdir 注册/可见性留待 v2。
func (p *Vfts) syncTools() {
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
			p.logf("vfts: gateway 工具注销失败（后续事件重试）：%v", err)
			return
		}
		p.registered = false
		p.regOwner = ""
		p.logf("vfts: gateway 查询工具已注销（无启用中的 workdir）")
	case !registered:
		if err := p.registerAll(); err != nil {
			p.logf("vfts: gateway 工具注册失败（保持未注册，后续事件重试）：%v", err)
			return
		}
		p.registered = true
		p.regOwner = owner
		if len(active) > 1 {
			p.logf("vfts: 多个 workdir 同时启用（%s）——v1 限制：gateway 工具全局仅注册一份（归属 %s），执行按 context.instance_id 路由", strings.Join(active, ", "), owner)
		}
		p.logf("vfts: gateway 查询工具已注册（%d 个，owner=%s）", len(queryTools), owner)
	case owner != p.regOwner:
		// 仍应注册但归属 workdir 已迁移（原归属停用/退出、其他 workdir 仍启用）：
		// 工具同名同回调主题，无需重注册，仅迁移归属记录。
		p.regOwner = owner
		if len(active) > 1 {
			p.logf("vfts: 注册归属迁移至 %s（多 workdir 并存，v1 仍全局一份）", owner)
		}
	default:
		// 已注册且归属未变：保持现状。
		_ = len(active) > 1 // 多 workdir 并存时的差异注册是 v1 明确不做的限制
	}
}

// ─── exe 路径解析 ────────────────────────────────────────

// resolveExe 定位引擎可执行文件：Options.Exe > 环境变量 VFTS_EXE > 运行 exe 同目录
// > 运行 exe 上级目录/vfts > 运行 exe 上级目录 > 运行 exe 上上级目录/dist/vfts。
// 找不到返回 error（Start 失败）。
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
	if v := os.Getenv("VFTS_EXE"); v != "" {
		return abs(v)
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("vfts: 定位引擎失败（os.Executable: %v）", err)
	}
	dir := filepath.Dir(self)
	const exeName = "chonkpilot-vfts-mcp-server.exe"
	cands := []string{
		filepath.Join(dir, exeName),                             // 运行 exe 同目录
		filepath.Join(dir, "..", "vfts", exeName),               // 运行 exe 上级目录/vfts（dist/<形态> 的兄弟目录）
		filepath.Join(dir, "..", exeName),                       // 运行 exe 上级目录
		filepath.Join(dir, "..", "..", "dist", "vfts", exeName), // 运行 exe 上上级目录/dist/vfts
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return abs(c)
		}
	}
	return "", fmt.Errorf("vfts: 引擎未找到——请设置 Options.Exe / 环境变量 VFTS_EXE，或将 %s 置于可执行文件同目录 / ../vfts/", exeName)
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
	return fmt.Sprintf("vfts-%d-%d", time.Now().UnixNano(), reqSeq.Add(1))
}

// dataEmit 向 data-<域>-<动作> 请求面发一次请求并等应答（镜像 codegraph dataclient 模式：
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
