// Package persist 是 data-<domain>-* 数据面服务（总纲附录/收口目标态：persist 服务面
// + 存储内核同归 chonkpilot-data）。实现 61-消息一览 §3 数据面：订阅**相对主题**
// data-<domain>-<op>（A2 正名；总线注入前缀后落 chonk.data-<domain>-<op>），应答
// list/load/save/delete/restore 等，save/delete 后广播 data-<domain>-refresh。
//
// 数据根解析（12-数据层）：自持实例视图（订阅 instance-register，携带 work_dir/data_dir），
// 按 req.instance_id 定位**会话数据层 = prjusr 主库**（data.Register → data.PrjUsr）；
// 配置域按 §2.4 语义落库：
//   - user-config / scenario 为 usr 全局（逐 key + llms/mcps 专用表 / scenario_list）
//   - prj-config / prompt / prj-security 走 prj config 表（无前缀 / prompt- / security- 前缀）；
//     其中 prj-config 的个人运行态 key（window/layout/filetree-*/opened-*）路由到 prjusr
//   - 会话/轮次/消息/任务树/快照 = prjusr（项目用户级，多人多开互不干扰）
//
// usr 库经 data.OpenSharedLayer(UserPath, LayerUsr)（usrPath 可注入，测试避免污染用户配置）。
//
// 阶段 4 门面化 + internal 下沉（23 §7）：**本包只保留「MQ 信封 + 装配」**——
//   - 门面实现（各域的业务逻辑）已下沉 `chonkpilot-data/internal/<域>/`（编译器门禁：
//     模块外不可直接 import）；本包的 data-* handler 只做「wire 载荷 → 门面入参 → 门面调用
//     → 门面出参 → wire 载荷」（信封形状见 61 §3，零变更）；
//   - 共享内核（实例视图 / 数据根解析 / 配置表原语 / 视图 helper）下沉
//     `chonkpilot-data/internal/kernel`；capability 文件树与契约解析下沉
//     `chonkpilot-data/internal/capfs`；
//   - 故 MQ 绑定与门面 inline 绑定是**同一份实现**（行为等价有测试，41 G-32/G-34/G-35/G-36）。
//
// 对外可见面 = 门面（`chonkpilot-data/facade`）+ 本包的装配与转发面（见 compat.go）。
package persist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/config"
	"github.com/chonkpilot/chonkpilot-data/internal/filelist"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/knowledge"
	"github.com/chonkpilot/chonkpilot-data/internal/mcp"
	"github.com/chonkpilot/chonkpilot-data/internal/memory"
	"github.com/chonkpilot/chonkpilot-data/internal/project"
	"github.com/chonkpilot/chonkpilot-data/internal/scenario"
	"github.com/chonkpilot/chonkpilot-data/internal/session"
	"github.com/chonkpilot/chonkpilot-data/internal/snapshot"
	"github.com/chonkpilot/chonkpilot-data/internal/tasktree"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// 订阅主题（相对；总线命名空间前缀 chonk. 在 mq 初始化注入一次）。
const (
	instanceRegister  = msgkeys.TopicInstanceRegister  // 自持实例表登记主题（61-消息一览 §4.1）
	instanceHeartbeat = msgkeys.TopicInstanceHeartbeat // 实例心跳（保活；分离形态据超时清理，见 sweep_split.go）
	instanceExit      = msgkeys.TopicInstanceExit      // 实例退出（移除绑定 + data.Unregister）
)

// dataReqSubjects 是 persist 订阅的请求动作主题（相对主题 data-<domain>-<op>，61-消息一览
// §3；A2 正名后为单段连字符一体主题，逐条精确订阅）。-refresh 广播只发布不订阅——
// 避免 persist 收到自己的广播形成自环（A1 时代靠 srv.notify.> 通配 + ok 过滤防环，正名后
// 结构上消除）。
var dataReqSubjects = []string{
	// 配置五域（§3.1）：user-config/prj-config/prompt/prj-security/scenario 通用 list/load/save/delete。
	msgkeys.TopicDataUserConfigList, msgkeys.TopicDataUserConfigLoad, msgkeys.TopicDataUserConfigSave, msgkeys.TopicDataUserConfigDelete,
	msgkeys.TopicDataPrjConfigList, msgkeys.TopicDataPrjConfigLoad, msgkeys.TopicDataPrjConfigSave, msgkeys.TopicDataPrjConfigDelete,
	msgkeys.TopicDataPromptList, msgkeys.TopicDataPromptLoad, msgkeys.TopicDataPromptSave, msgkeys.TopicDataPromptDelete,
	msgkeys.TopicDataPrjSecurityList, msgkeys.TopicDataPrjSecurityLoad, msgkeys.TopicDataPrjSecuritySave, msgkeys.TopicDataPrjSecurityDelete,
	msgkeys.TopicDataScenarioList, msgkeys.TopicDataScenarioLoad, msgkeys.TopicDataScenarioSave, msgkeys.TopicDataScenarioDelete,
	// MCP 配置域（§3.1 家族扩展）：四级 `<级别>/capability/mcps/<名>.json` 文件化配置
	msgkeys.TopicDataMcpList, msgkeys.TopicDataMcpLoad, msgkeys.TopicDataMcpSave, msgkeys.TopicDataMcpDelete,
	// 会话域（§3.2）
	msgkeys.TopicDataSessionList, msgkeys.TopicDataSessionGet, msgkeys.TopicDataSessionHistory, msgkeys.TopicDataSessionLatest,
	msgkeys.TopicDataSessionTitle, msgkeys.TopicDataSessionDelete, msgkeys.TopicDataSessionActiveSet, msgkeys.TopicDataSessionActiveGet,
	msgkeys.TopicDataSessionContent,
	// 会话域 A3 运行时扩展（server sessionStore 总线化原语，语义对齐 chonkpilot-server/session.go）
	msgkeys.TopicDataSessionEnsureSession, msgkeys.TopicDataSessionEnsureTurn, msgkeys.TopicDataSessionAppendMessage,
	msgkeys.TopicDataSessionSetSummary, msgkeys.TopicDataSessionCompleteTurn, msgkeys.TopicDataSessionCleanupStale,
	msgkeys.TopicDataSessionLoadMessages, msgkeys.TopicDataSessionContext,
	// 会话快照域（A3 扩展；sessions 表 history/snapshot_turn 两字段读写）
	msgkeys.TopicDataSnapshotGet, msgkeys.TopicDataSnapshotSet,
	// 任务树域（§3.4）
	msgkeys.TopicDataTasktreeList, msgkeys.TopicDataTasktreeTasks, msgkeys.TopicDataTasktreeDelete,
	// 任务树域 A3 运行时扩展（server taskManager.persist 节点落库）
	msgkeys.TopicDataTasktreeUpsert,
	// 知识库域（§3.3）
	msgkeys.TopicDataKnowledgeRoot, msgkeys.TopicDataKnowledgeList, msgkeys.TopicDataKnowledgeRead, msgkeys.TopicDataKnowledgeSave,
	msgkeys.TopicDataKnowledgeCreate, msgkeys.TopicDataKnowledgeDelete, msgkeys.TopicDataKnowledgeRename, msgkeys.TopicDataKnowledgeMkdir,
	msgkeys.TopicDataKnowledgeRmdir, msgkeys.TopicDataKnowledgeRenameDir,
	// 记忆库域（42 §2 (27)）：类别清单 + 全文读写（save/delete 后广播 data-memory-refresh）
	msgkeys.TopicDataMemoryList, msgkeys.TopicDataMemoryRead, msgkeys.TopicDataMemorySave, msgkeys.TopicDataMemoryDelete,
	// 记忆提取进度专用表（OP-05/06，2026-10-06）：(会话, 类别) → 最后已成功提取的 turn（prjusr 表 memory_extract）：
	// 不广播 -refresh（进度只由 memory 插件读写）。
	msgkeys.TopicDataMemoryExtractLoad, msgkeys.TopicDataMemoryExtractSave, msgkeys.TopicDataMemoryExtractDelete,
	// 文件清单域（vfts 增量清单；项目级 prj 库 file_list 表；不广播 -refresh）
	msgkeys.TopicDataFilelistList, msgkeys.TopicDataFilelistPut, msgkeys.TopicDataFilelistDel,
	// 索引排除判定域（2026-09-27 新增只读面）：判定一组 workdir 相对路径是否被索引排除规则排除
	// （供前端文件树灰显被排除条目；不写库、不广播 -refresh）。
	msgkeys.TopicDataIndexIgnored,
}

// Options 是 persist 服务构造参数（= internal/kernel.Options 的转发别名；字段含义见 kernel）。
type Options = kernel.Options

// Service 实现 data-<domain> 消息面（61-消息一览 §3）：**MQ 信封 + 装配**。
//
//   - 信封：订阅 data-* 各动作主题 → 解析 wire 载荷 → 调用对应域门面方法 → 按 61 形状回载荷；
//   - 装配：构造各域门面实现（internal/<域>）并共享同一 internal/kernel.Base；嵌入各域面接口
//     → 本类型**同时**是完整门面实现（`facade.API`），供宿主（llm server / 插件 / 入口桥）
//     直接持有（23 §7「同一份定义，多种绑定」）；
//   - 实例视图：订阅 instance-register/heartbeat/exit 维护 instance_id ↔ {work_dir,data_dir}
//     （视图本体在 kernel.View，与各域实现同源）。
type Service struct {
	*kernel.Base

	// 域门面实现（各域面接口；嵌入 → 方法提升，Service 即完整 facade.API）。
	facade.SnapshotAPI
	facade.ConfigAPI
	facade.SessionAPI
	facade.TurnAPI
	facade.MessageAPI
	facade.TasktreeAPI
	facade.KnowledgeAPI
	facade.FileListAPI
	facade.ScenarioAPI
	facade.MemoryAPI
	facade.McpAPI
	facade.ProjectAPI

	mu      sync.Mutex
	subs    []mq.Sub // 全部订阅句柄（Start 累计 / Stop 退订）
	started bool
	// sweepStop 心跳超时扫描停止函数（分离形态非空；默认构建 startStaleSweep 返回 nil，
	// 见 sweep_split.go / sweep_inprocess.go——desktop 不判超时）。
	sweepStop func()
}

// 编译期断言：Service = 完整门面实现（各域面在装配期注入，见 New）。
var _ facade.API = (*Service)(nil)

// New 构造 persist 服务（不订阅；需 Start）：装配各域门面实现 + 共享内核。
//
// 注：注入 UsrPath（测试隔离 / 自定义数据主目录）时，prjusr 数据根随之重定位到
// <UsrPath 所在目录>/data，与默认形态的 ~/.chonkpilot/{chonkpilot.db,data/} 同构
// （见 12-数据层）。
func New(bus mq.Bus, opts Options) *Service {
	if opts.UsrPath != "" {
		data.SetDataHome(opts.UsrPath, filepath.Join(filepath.Dir(opts.UsrPath), "data"))
	}
	base := kernel.NewBase(bus, opts)
	capfs.SetWarnf(base.Warnf) // capfs 包级告警出口注入（A-42：场景元信息解析 / 旧 agent 清理失败留痕）
	ses := session.New(base)
	s := &Service{
		Base:         base,
		SnapshotAPI:  snapshot.New(),
		ConfigAPI:    config.New(base),
		SessionAPI:   ses,
		TurnAPI:      ses,
		MessageAPI:   ses,
		TasktreeAPI:  tasktree.New(base),
		KnowledgeAPI: knowledge.New(base),
		FileListAPI:  filelist.New(base),
		ScenarioAPI:  scenario.New(base),
		MemoryAPI:    memory.New(base),
		McpAPI:       mcp.New(base),
		ProjectAPI:   project.New(base),
	}
	// refresh 广播附带的域列表（订阅面）由信封层提供 → 与各域 list 应答同源。
	base.DomainList = s.domainList
	return s
}

// Start 订阅 data-* 请求面（逐主题精确订阅）与 instance-* 生命周期（自驱动）。
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	// data-* 请求面：相对主题 data-<domain>-<op> 逐条精确订阅（A2 正名；refresh 广播不订阅）
	for _, subj := range dataReqSubjects {
		sh, err := s.on(subj, s.route)
		if err != nil {
			s.unsubscribeAll()
			return err
		}
		s.subs = append(s.subs, sh)
	}
	for _, sub := range []struct {
		subject string
		h       func(subject string, payload []byte)
	}{
		{instanceRegister, s.onInstanceRegister},
		{instanceHeartbeat, s.onInstanceHeartbeat},
		{instanceExit, s.onInstanceExit},
	} {
		sh, err := s.on(sub.subject, sub.h)
		if err != nil {
			s.unsubscribeAll()
			return err
		}
		s.subs = append(s.subs, sh)
	}
	// 心跳超时扫描（分离形态起周期 goroutine；默认构建空实现 = 零 ticker，见 sweep_*.go）。
	s.sweepStop = s.startStaleSweep()
	s.started = true
	return nil
}

// on 订阅主题并把载荷回调适配为 v2 On（内部回调形态 func(subject, payload)；
// 总线收发 API = v2 On/Emit，见 mq 包）。
func (s *Service) on(subject string, h func(subject string, payload []byte)) (mq.Sub, error) {
	return s.Bus.On(subject, 0, func(_ context.Context, subject string, v *mq.Value) error {
		h(subject, v.Payload)
		return nil
	})
}

// Stop 退订全部（并停心跳超时扫描）。
func (s *Service) Stop() {
	s.mu.Lock()
	stopSweep := s.sweepStop
	s.sweepStop = nil
	s.unsubscribeAll()
	s.started = false
	s.mu.Unlock()
	if stopSweep != nil {
		// 分离形态：停扫描并等其退出。不在锁内调用——扫描侧 sweepStale 需取 s.mu。
		stopSweep()
	}
}

func (s *Service) unsubscribeAll() {
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	s.subs = nil
}

// ─── 实例视图（自持；本体在 kernel.View）────────────────────────────

func (s *Service) onInstanceRegister(_ string, payload []byte) {
	var req struct {
		InstanceID string `json:"instance_id"`
		WorkDir    string `json:"work_dir"`
		DataDir    string `json:"data_dir,omitempty"`
	}
	_ = json.Unmarshal(payload, &req)
	s.View.Register(req.InstanceID, req.WorkDir, req.DataDir)
}

// onInstanceHeartbeat 心跳保活：刷新 LastBeat（仅分离形态的超时扫描消费；默认构建
// 无心跳发布方、亦不做超时判定——见 sweep_split.go / sweep_inprocess.go）。
func (s *Service) onInstanceHeartbeat(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	s.View.Touch(msg.InstanceID)
}

func (s *Service) onInstanceExit(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	s.instanceGone(msg.InstanceID)
}

// instanceGone 释放实例数据根绑定：移除实例视图 + data.Unregister（使后续按该 instance
// 解析库连接报"未登记"）。显式 `instance-exit`（onInstanceExit）与分离形态心跳超时
// （sweepStale，见 sweep_split.go）**共用同一实现**；幂等：重复调用无副作用。
func (s *Service) instanceGone(instanceID string) { s.View.Exit(instanceID) }

// lookupInstance 查实例数据根绑定（未登记 → ok=false）——信封/测试用的薄转发（同 kernel.View）。
func (s *Service) lookupInstance(instanceID string) (kernel.Info, bool) {
	return s.View.Lookup(instanceID)
}

// ─── 路由与分发 ──────────────────────────────────

// dataDomains 是 data-<domain> 消息面支持的域（61-消息一览 §3.1-§3.6）：配置五域 +
// 会话/任务树/知识库/记忆库四域（B 类随迁补齐）；snapshot = A3 运行时扩展（会话快照 get/set）；
// index = 2026-09-27 新增只读域（索引排除判定，index-ignored）；file-versions 归属 history.db 外部，不在本面。
var dataDomains = []string{"user-config", "prj-config", "prj-security", "scenario", "mcp", "prompt", "session", "snapshot", "knowledge", "tasktree", "memory", "filelist", "index"}

// dataReq 是 data-<domain>-* 请求的通用载荷（§3.1）。ID 宽松接收 string / number。
type dataReq struct {
	ReqID      string         `json:"req_id"`
	InstanceID string         `json:"instance_id,omitempty"`
	ID         any            `json:"id,omitempty"`
	Filter     map[string]any `json:"filter,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

// route 分发 data-<domain>-<op>（subject = 精确订阅的相对主题，A2 正名后直通，无前缀可剥）。
func (s *Service) route(subject string, payload []byte) {
	rest := strings.TrimPrefix(subject, "data-")
	for _, d := range dataDomains {
		if strings.HasPrefix(rest, d+"-") {
			s.handle(subject, d, strings.TrimPrefix(rest, d+"-"), payload)
			return
		}
	}
}

// errInstanceIDRequired 是 data 面入站「缺 instance_id」的明确错误（G-41-c）：
// 该主题按契约必带 instance_id（§0 硬规则）却未携带 → 拒绝处理（不静默走"唯一实例回退"）。
var errInstanceIDRequired = errors.New("persist: data 请求缺 instance_id（该主题按契约必带；全局级域 user-config/scenario/mcp/knowledge 除外）")

// instanceFreeDomains 是 data 面**按设计无需 instance_id** 的请求主题白名单（G-41-c 审计，
// 见 [20-实例隔离与后端分离]）。白名单外：请求缺 instance_id → 明确失败 —— 避免多实例下
// 「唯一实例回退」被误用导致跨实例串库（与 task 层 errInstanceRequired 同口径）。
//
// 豁免集合 = **存在全局级（app/user）而无需实例即可工作**的域（61 §3.1）：`user-config`
// （usr 全局）+ `scenario` / `mcp` / `knowledge`（app/user/project/prjusr 四级；缺实例 = 仅
// app+user 级，既有宽松语义）。其余域为**项目/实例严格作用域**，必带 instance_id。
var instanceFreeDomains = map[string]bool{
	"user-config": true,
	"scenario":    true,
	"mcp":         true,
	"knowledge":   true,
}

func (s *Service) handle(subject, domain, op string, payload []byte) {
	// 跳过回环应答：reply/fail 与请求**同主题**发布，persist 会收到自己发出的应答（见 isLoopbackReply）。
	if isLoopbackReply(payload) {
		return
	}
	req := parseDataReq(payload)
	// 入站契约校验（G-41-c）：白名单外的 data 请求必须显式携带 instance_id。
	if req.InstanceID == "" && !instanceFreeDomains[domain] {
		s.fail(subject, req, errInstanceIDRequired)
		return
	}
	switch domain {
	case "user-config":
		s.handleUserConfig(op, req)
	case "prj-config", "prompt", "prj-security":
		s.handleConfigKV(domain, op, req)
	case "scenario":
		s.handleScenario(op, req)
	case "mcp":
		s.handleMCP(op, req)
	case "session":
		s.handleSession(op, req, payload)
	case "snapshot":
		s.handleSnapshot(op, req)
	case "knowledge":
		s.handleKnowledge(op, req, payload)
	case "tasktree":
		s.handleTasktree(op, req, payload)
	case "memory":
		s.handleMemory(op, req)
	case "filelist":
		s.handleFileList(op, req)
	case "index":
		s.handleIndex(op, req)
	}
}

// reply 统一回复：直接 publish 结果到请求同一主题（相对主题 data-<method>，无 .reply 后缀）。
func (s *Service) reply(method string, req dataReq, result map[string]any) {
	b, _ := json.Marshal(map[string]any{"req_id": req.ReqID, "ok": true, "result": result})
	_ = s.Bus.Emit(context.Background(), method, b)
}

// fail 统一错误回复。
func (s *Service) fail(method string, req dataReq, err error) {
	b, _ := json.Marshal(map[string]any{"req_id": req.ReqID, "ok": false, "error": err.Error()})
	_ = s.Bus.Emit(context.Background(), method, b)
}

// isLoopbackReply 判定入站载荷是否为 persist 自身应答（reply/fail）的回环。
//
// reply/fail 与请求**同主题**发布（相对主题 data-<domain>-<op> 无 .reply 后缀），故 persist 会收到
// 自己发出的应答，必须在 handle 入口跳过，否则会把应答当作请求再次处理。
//
// 应答恒为 {req_id, ok, result|error}（见 reply/fail 二者必居其一）；请求则为业务载荷
// （list/load/save/delete 的入参）。**单凭顶层 `ok` 键**会把「业务载荷恰好含顶层 ok 的请求」
// 误判为应答而丢弃（且不 reply → 调用方 Promise 永不 resolve），故用「顶层 ok + 应答专属
// 信封键 result/error」组合判定：仅 reply（带 result）/ fail（带 error）命中。
func isLoopbackReply(payload []byte) bool {
	var env struct {
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
		Err    json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &env) != nil || env.OK == nil {
		return false
	}
	return env.Result != nil || env.Err != nil
}

// ─── 数据根解析（信封层用；解析规则单源 = internal/kernel/root.go）──

// prjUsrDB 按 instance 解析 prjusr 主库（会话/任务树/快照/个人运行态；12-数据层）。
func (s *Service) prjUsrDB(instanceID string) (*data.DB, error) {
	info, ok := s.View.Lookup(instanceID)
	if !ok {
		return nil, kernel.ErrInstanceNotRegistered
	}
	return s.PrjUsrByInst(instanceID, info)
}

// sessionDB 按实例定位**会话数据层 = prjusr 库**（12-数据层：会话/任务树/快照属"项目用户级"）；
// 供同层域（snapshot / tasktree）沿用既有 MQ 路径定位（门面侧用 PrjUsrFor，见 internal/kernel）。
// 注：`handle` 入站已按白名单校验（G-41-c）——非全局级域缺 instance_id 在**更早处**即失败，
// 故此处 `Resolve` 的"唯一实例回退"对 MQ 请求不再被触发（仅门面/inline 路径沿用）。
// 信封 handler 中的局部变量 `prj` 即本返回值。
func (s *Service) sessionDB(req dataReq) (*data.DB, error) {
	instID, info, err := s.View.Resolve(req.InstanceID)
	if err != nil {
		return nil, err
	}
	return s.PrjUsrByInst(instID, info)
}

// ─── refresh 广播附带的域列表（订阅面；与各域 list 应答同源）──────────

// domainList 返回域当前列表（refresh 广播附带；查询失败返回 nil 表示不附带）。
// 各域与对应 list 应答**同源**（全部经该域门面实现 → 与 MQ / inline 两绑定同一份代码）。
func (s *Service) domainList(domain, instanceID string, scope facade.Scope) any {
	switch domain {
	case "user-config":
		resp, err := s.UserConfigView(facade.UserConfigViewRequest{})
		if err != nil {
			return nil
		}
		return resp.List
	case "scenario":
		resp, err := s.ScenarioList(facade.ScenarioListRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			return nil
		}
		return wire.ScenarioListResult(resp.List)["list"]
	case "mcp":
		resp, err := s.McpList(facade.McpListRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			return nil
		}
		return wire.McpListResult(resp.List)["list"]
	case "prj-config", "prompt", "prj-security":
		resp, err := s.ConfigKVList(facade.ConfigKVListRequest{
			Domain: domain, InstanceID: instanceID, Scope: scope,
		})
		if err != nil {
			return nil
		}
		return resp.List
	case "memory":
		resp, err := s.MemoryList(facade.MemoryListRequest{InstanceID: instanceID, Scope: scope})
		if err != nil {
			return nil
		}
		return wire.MemoryListResult(resp.List)["list"]
	}
	return nil
}

// ─── 辅助 ────────────────────────────────────────

func parseDataReq(payload []byte) dataReq {
	var req dataReq
	_ = json.Unmarshal(payload, &req)
	// 收集顶层非 struct 字段到 Data（兼容 frontend → dataViaPersist 直通形态：
	// dataViaPersist 把 payload 平铺在 JSON 顶层，dataReq 仅识别 req_id/instance_id/
	// id/filter/data 五个字段；session_id/keys/before_turn_id 等业务字段需落入 Data 才
	// 能被各 handler 读取。双向兼容：以 struct 层为准，raw 层补充 struct 未捕获的字段）。
	if req.Data == nil {
		req.Data = make(map[string]any)
	}
	var raw map[string]any
	if json.Unmarshal(payload, &raw) == nil {
		for k, v := range raw {
			switch k {
			case "req_id", "instance_id", "id", "filter", "data", "_key":
				continue // 已由 struct 处理或内部字段
			}
			if _, exists := req.Data[k]; !exists {
				req.Data[k] = v
			}
		}
	}
	return req
}

// idKey 把消息面 id（string / number）统一转成桶 key 字符串。
func idKey(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	}
	return ""
}

// toInt64 兼容 json 数字解码（float64）的 int64 提取。
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// newID 生成短随机 hex id（消息 id / 短 key 用）。
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
