package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chonkpilot/chonkpilot-assembly"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
	"github.com/lsong98sh/chonkpilot-agent/router"
	"github.com/lsong98sh/chonkpilot-agent/session/runner"
	"github.com/lsong98sh/chonkpilot-agent/chat"
)

// New 构建会话服务（v2：双参，不再接收数据层 cfg——会话持久化经总线 persist，
// prompt user_config 经 opts.UsrPath 直开 usr 库；对齐 61-消息一览 §3/§4）。
func New(bus mq.Bus, opts Options) *Server {
	if opts.LLMBase == "" {
		opts.LLMBase = "http://127.0.0.1:8901/v1"
	}
	if opts.LLMModel == "" {
		opts.LLMModel = "mock"
	}
	// 额外日志 sink（可选；nil = 仅 stdout，行为同改前）：宿主注入后本包与插件日志同落文件
	// （GUI = `<dataDir>/logs/gui.log`，见 log.go）。
	setLogSink(opts.LogWriter)
	s := &Server{
		bus:             bus,
		opts:            opts,
		rt:              router.New(router.Config{}),
		gc:              newGWClient(bus),
		im:              instance.New(bus),
		engine:          chat.New(chat.Deps{}),
		turns:           runner.NewRegistry[turnSession](),
		busy:            runner.NewRegistry[string](),
		asks:            runner.NewRegistry[*askWaiter](),
		groupRuns:       make(map[string]*groupSched),
		taskExecCancels: make(map[string]context.CancelFunc),
		cleaned:         make(map[string]bool),
		dirNodes:        make(map[string][]string),
		appTools:        make(map[string]bool),
		agents:          make(map[string]AgentDef),
		continuePending: make(map[string]bool),
		taskTurnIndex:   make(map[string]*turnCtx),
		inboxIndex:      make(map[string]facade.InboxItem),
		compressRunning: make(map[string]bool),
		compressPending: make(map[string]compressJob),
	}
	// instance.Manager 告警接入宿主日志（F-10）：GUI/分离形态下超限告警不再只落 stderr。
	s.im.SetLogf(logf)
	s.memCache = newMemoryCategoryCache(memoryCategoryTTL)
	s.noticeSeen = newNoticeDedup()
	s.tasks = newTaskManager(s)
	s.plugins = append([]hook.Hook{}, opts.Plugins...)
	// 内嵌 persist 数据服务（同一总线应答 data-* 面；usrPath 测试可注入）
	// Warnf（MW-11）：数据的「唯一实例回退 / 缺 instance_id」告警经本服务日志出口暴露。
	// AppDir = 系统级 capability 根（与 assembly 的 `Root` 同源，opts.MCPServerRoot）→ 数据层场景
	// 根随之 = `<AppDir>/../scenarios`（与 capability/ 平级，25 §6）；两边同源避免路径规则分叉。
	s.data = persist.New(bus, persist.Options{UsrPath: opts.UsrPath, AppDir: opts.MCPServerRoot, Warnf: logf})
	// config 域门面绑定 = inline（同进程直调）：实现 = 内嵌 persist（与 MQ handler 同源实现）
	s.cfg = s.data
	// LLM provider 对账（LR-11）：usr `llms` 全量 + exe flags 隐含默认 → router 注册集
	// （启动一次 + `data-user-config-refresh` 各一次；调用路径亦按需幂等 Register）。
	s.reconcileLLMProviders()
	// 任务层（21 §9.2 P2）：订阅既有事件 → 同步写权威表 tasktree（单写者；llm 不再自写库）
	// + 执行句柄回调（层 → 执行侧取消）。构造无失败路径；Start 见 Start()（失败只告警）。
	s.taskLayer = tasklayer.New(bus, tasklayer.Options{
		Logf:         taskLayerLogf,
		OnCancelExec: s.onTaskLayerCancelExec,
		DialogOf:     s.dialogOf, // 一树一库（P3-4b）：任务树行随会话落对话库
	})
	if !opts.DisableMCP {
		// 静态装配（RB-5 L5）：能力源（capability 契约）+ 执行配置 + 内嵌 gateway 由**入口
		// 装配器** `src/lib/assembly` 统一完成（gui / server 两个入口共用一份，避免装配漂移）；
		// 本函数只接成品 + 注入本层的运行期装配（四级文件 MCP 下游、执行池 sink）。
		// GATEWAY-2 L5：gateway 只收 Transport + Servers（能力源留在本层，不作 self 节点）。
		fileServers := s.loadGatewayServers("") // 启动期尚无实例 → 空 instance_id（仅 app + user 级）
		s.applyGatewaySandboxDirs(fileServers)  // agentbox：为开启 sandbox 的条目补允许目录快照（启动期实例未注册 → 通常为空，实例注册后经 reconcile 补齐）
		servers := append([]mcpgateway.ServerEntry{}, opts.GatewayServers...)
		servers = append(servers, fileServers...)
		// 对账基线 = 本次随 Params.Servers 下发的四级文件 MCP 集合（T-25：后续保存按增量对账）。
		s.mcpApplied = make(map[string]mcpgateway.ServerEntry, len(fileServers))
		for _, e := range fileServers {
			s.mcpApplied[e.ID] = e
		}
		// 执行池控制面适配器（交付 2：层 → 执行侧，进程内直调，不经 MQ）：先建（gateway 需要
		// ExecSink 注入），gateway 建好后回填句柄。
		sink := &taskExecSink{layer: s.taskLayer}
		// P6（2026-10-19）：外部工具模式（CHONKPILOT_EXTERNAL_TOOLS=1）→ 系统级 app 根只注册
		// 资产（prompts/skills/resources），可执行工具由外部进程 chonkpilot-core-mcp-server
		// 注册/执行；资产仍在本层能力源（assembly.Stack.Server）对外暴露。
		externalTools := os.Getenv("CHONKPILOT_EXTERNAL_TOOLS") == "1"
		st, err := assembly.Build(assembly.Options{
			Bus:       bus, // 2026-09-03 去 NATS：gateway 始终复用会话总线（61-消息一览 §0.1/§5）
			Root:      opts.MCPServerRoot,
			Servers:   servers,
			AsyncMode: opts.AsyncMode,
			SkipTools: externalTools,
			// P3（21 §9.3）：gateway 执行态（started/detached/awaiting/error）**经层内 API 直调**
			// 纳入任务层（不新增 MQ 主题）；2026-09-18 起取消/转后台亦经本 sink 进程内直调。
			// 层未注入（库内 / gateway 单体）→ gateway 侧全部 no-op。
			ExecSink: sink,
			Logf:     logf,
		})
		// 能力源 / 执行配置恒保留（与改前「gateway 失败仍保留 mcpServer/mcpCfg」一致）
		s.stack = st
		s.mcpServer = st.Server
		s.mcpCfg = st.Config
		s.appTools = st.AppTools
		if err != nil {
			logf("[chonkpilot-server] 内嵌 gateway 失败: %v\n", err)
		} else {
			s.gw = st.Gateway
			sink.gw = st.Gateway
		}
		s.execSink = sink
	}
	return s
}

// Scan 已移除（GATEWAY-2 L5）：gateway 不再持有能力源/契约扫描器，capability 目录节点
// 改为经 `chonkpilot-core-mcp-server --stdio` 以普通 stdio server 接入（见 registerDirNode）。

// Start 启动内嵌数据服务 + gateway + 订阅客户端方法面/通知 + 注册域工具与 agent + 插件。
func (s *Server) Start(ctx context.Context) error {
	markStartupBegin() // OP-15：启动分段计时基准（仅插桩）
	// 内嵌 persist 数据服务（应答 data-* 面；先于 gateway/路由，保证数据请求可用）
	if s.data != nil {
		if err := s.data.Start(); err != nil {
			return err
		}
	}
	logStartupStage("数据服务(persist)启动")
	// 任务层（21 §9.2 P2）：订阅既有事件（task-started/updated/done、mcp-tasks-report、
	// task-deleted）→ 同步写权威表 tasktree。**失败只告警**——不进入本服务的失败路径。
	if s.taskLayer != nil {
		if err := s.taskLayer.Start(); err != nil {
			logf("[chonkpilot-task] 任务层启动失败（仅告警，主路径不受影响）: %v\n", err)
		}
	}
	logStartupStage("任务层启动")
	// 内嵌 gateway 启动（订阅 mcp-* 方法面 + 连接下游 inprocess/proxied）
	if s.gw != nil {
		if err := s.gw.Start(ctx); err != nil {
			return err
		}
	}
	logStartupStage("gateway 启动/下游连接")
	// capability 根 fswatcher 监听（T-21）：用户/项目级原语保存 → 重扫 dir 节点即时生效；
	// 系统级 app 根（<exeDir>/capability，内嵌 self 节点）→ 契约重注册 + gateway/reload 即时生效。
	// 监听失败不致命（退化为"需重注册/重启生效"的既有行为）。
	if s.gw != nil {
		if cw, err := newCapWatcher(s); err != nil {
			logf("[chonkpilot-server] capability watcher 启动失败（原语保存不热生效）: %v\n", err)
		} else {
			s.capWatch = cw
			if s.mcpCfg != nil {
				cw.watchApp(s.mcpCfg.Root) // T-21②：app 根只读，外部/构建期更新后热生效
			}
		}
	}
	logStartupStage("capability fswatcher 监听")
	// 共通实例视图（订阅 instance-register / heartbeat / exit；对齐 61-消息一览 §4.1。
	// 合并单进程形态：GUI 启动注册、进程随会话存续，无需心跳超时清理——startInstanceSweep
	// 为空实现、不调 Sweep；分离形态：起周期扫描 goroutine，心跳超时实例走 exitInstance。）
	if err := s.im.Start(); err != nil {
		return err
	}
	s.sweepStop = s.startInstanceSweep()
	logStartupStage("实例视图/心跳扫描启动")
	// PRODSIM-1：认证域（login-*）已整体移除（本地 auth 删；认证将来统一到 registry）。
	if err := s.subscribeAll(); err != nil {
		return err
	}
	logStartupStage("方法面/事件订阅")
	// 域工具注册（gateway 分组注册面：tools/register → mcp-tools-register，61-消息一览 §5.1.1）；
	// 内置 agent 集（**app 级场景** `<exeDir>/scenarios/*/`）**只登记到内存表**（25 §5 / T2：agent
	// 不注册资产；25 §8.1 #8 / T6：来源 = app 级场景，非代码 embed）。
	// 注册失败不致命（宿主 DisableMCP/无下游时跳过），hot/目录后续刷新仍可用。
	// 一工具一 exe：进程外模式 = 域/元工具全部由 core-mcp-server 注册执行（server 零域工具）。
	if s.gw != nil && !externalToolsEnabled() {
		if err := s.registerDomainTools(ctx); err != nil {
			return fmt.Errorf("registerDomainTools: %w", err)
		}
	}
	logStartupStage("域工具注册（进程外模式 = 跳过）")
	if s.gw != nil {
		if err := s.registerDomainAgents(ctx); err != nil {
			return fmt.Errorf("registerDomainAgents: %w", err)
		}
	}
	logStartupStage("域 agent 注册")
	// 预热工具定义缓存（gateway tools/list → ToolDef；失败降级空工具）
	preCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.gc.ListTools(preCtx)
	logStartupStage("工具定义缓存预热(gc.ListTools)")

	// 内嵌插件加载（gateway/路由/实例视图就绪后按序 Start；全部成功才广播就绪）
	deps := hook.Deps{Bus: s.bus, Logf: s.pluginLogf, Notify: s.pluginNotify, ToolExec: s.coreToolExec}
	for _, p := range s.plugins {
		if err := p.Start(deps); err != nil {
			return fmt.Errorf("plugin %s start: %w", p.Name(), err)
		}
	}
	logStartupStage("内嵌插件启动")
	// 就绪广播（用户要求：全部插件加载完成后发送——插件已订阅，前端/分离消费者可自检）
	s.publish(msgkeys.TopicServerStarting, map[string]any{"started_at": time.Now().Unix()})
	logStartupStage("server 启动完成")
	return nil
}

// Stop 退订 + 取消全部 running turn。
func (s *Server) Stop() {
	// 内嵌插件 teardown（按 Start 的逆序；仅实现 hook.Stopper 的插件有资源可关——
	// 停后台循环/关引擎子进程/退订订阅/停实例视图）。
	for i := len(s.plugins) - 1; i >= 0; i-- {
		if st, ok := s.plugins[i].(hook.Stopper); ok {
			st.Stop()
		}
	}
	if s.capWatch != nil {
		s.capWatch.Close()
	}
	if s.gw != nil {
		s.gw.Stop(context.Background())
	}
	// 能力源会话（GATEWAY-2 L5）：gateway 停后再关进程内 MCP 会话（nil = 未建立）。
	if s.stack != nil && s.stack.Close != nil {
		s.stack.Close()
	}
	if s.sweepStop != nil {
		s.sweepStop() // 分离形态：先停心跳超时扫描（默认构建为 nil，零开销）
		s.sweepStop = nil
	}
	s.im.Stop()
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	s.subs = nil
	if s.tasks != nil {
		s.tasks.stop()
	}
	// 任务层退订（P1：只订阅既有事件 + 写影子域，无自有 goroutine）
	if s.taskLayer != nil {
		s.taskLayer.Stop()
	}
	if s.data != nil {
		s.data.Stop()
	}
	flushSessionIndexes(s.bus) // 清本总线备忘（防总线指针复用导致跨宿主串味）
	// 锁内仅收集待关闭轮次，Close（含 session 锁释放 = 文件解锁 IO）在锁外逐个执行——
	// 避免持 s.mu 做 IO 阻塞其他 s.mu 访问（stop 期仍需幂等：turnCtx.Close 自身 once 守卫）。
	s.mu.Lock()
	turns := make([]turnSession, 0, len(s.turns))
	for _, sess := range s.turns {
		turns = append(turns, sess)
	}
	s.mu.Unlock()
	for _, sess := range turns {
		sess.Close()
	}
	// 群聊调度（Stop 兜底）：turns 只覆盖单轮，群聊 run 由 groupRuns 自持 ctx → 必须显式取消，
	// 否则停机后仍会继续发起 LLM 调用。
	s.stopAllGroupRuns()
}
