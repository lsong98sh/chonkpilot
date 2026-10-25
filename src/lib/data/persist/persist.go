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
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/config"
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
	msgkeys.TopicDataSessionContent, msgkeys.TopicDataSessionFork,
	// 会话域 A3 运行时扩展（server sessionStore 总线化原语，语义对齐 chonkpilot-server/session.go）
	msgkeys.TopicDataSessionEnsureSession, msgkeys.TopicDataSessionEnsureTurn, msgkeys.TopicDataSessionAppendMessage,
	msgkeys.TopicDataSessionSetSummary, msgkeys.TopicDataSessionCompleteTurn, msgkeys.TopicDataSessionCleanupStale,
	msgkeys.TopicDataSessionLoadMessages, msgkeys.TopicDataSessionContext,
	// 群聊两表（SUBCHAT-3 阶段 E，2026-10-16）：参与者名单 / 每大轮次投递对象（prjusr；
	// group_participants / group_round_targets）。**不广播 -refresh**（名册/进度类表）。
	msgkeys.TopicDataSessionGroupParticipantsLoad, msgkeys.TopicDataSessionGroupParticipantsSave,
	msgkeys.TopicDataSessionGroupRoundTargetsLoad, msgkeys.TopicDataSessionGroupRoundTargetsSave,
	// 会话快照域（A3 扩展；sessions 表 history/snapshot_turn 两字段读写）
	msgkeys.TopicDataSnapshotGet, msgkeys.TopicDataSnapshotSet,
	// INBOX 内部面（2026-10-17，中途插话 / 等下一轮；prjusr `inbox_items` 表）：
	// 只由 llm-server 读写（客户端只经 llm-inbox-* 方法面）。
	msgkeys.TopicDataInboxAppend, msgkeys.TopicDataInboxList, msgkeys.TopicDataInboxUpdate, msgkeys.TopicDataInboxCommit,
	msgkeys.TopicDataAskPending,
	// TURN EVENT 内部面（MOD-1 M3；prjusr `turn_events` 表）：TurnState 事件日志
	// （只由 llm-server 读写；清理随既有 sweep 周期）。
	msgkeys.TopicDataTurnEventAppend, msgkeys.TopicDataTurnEventCleanup,
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
	facade.TurnEventAPI
	facade.AskAPI
	facade.MessageAPI
	facade.InboxAPI
	facade.TasktreeAPI
	facade.KnowledgeAPI
	facade.ScenarioAPI
	facade.MemoryAPI
	facade.McpAPI
	facade.ProjectAPI
	facade.GroupAPI

	mu      sync.Mutex
	subs    []mq.Sub // 全部订阅句柄（Start 累计 / Stop 退订）
	started bool
	// shardDone 记录已完成「旧布局 → dialogs/ 一树一库」迁移的 prjusr 路径（SESSION-COMP-1 P0；
	// 每路径一次，失败会移除以便下次重试）。
	shardDone sync.Map
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
		TurnEventAPI: ses,
		AskAPI:       ses,
		MessageAPI:   ses,
		InboxAPI:     ses,
		TasktreeAPI:  tasktree.New(base),
		KnowledgeAPI: knowledge.New(base),
		ScenarioAPI:  scenario.New(base),
		MemoryAPI:    memory.New(base),
		McpAPI:       mcp.New(base),
		ProjectAPI:   project.New(base),
		// 群聊两表（参与者名单 / 大轮次投递对象）与会话同域同库（prjusr）→ 复用 session 实现。
		GroupAPI: ses,
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
	// INPUTVAL-1 P0-4：实例 / 项目路径写侧校验（instance_id 标识符、work_dir/data_dir 绝对路径、
	// 长度上限、禁上级引用）。失败**不登记**（后续按未登记处置，不把脏路径写进数据根绑定）。
	if err := validateInstanceRegister(req.InstanceID, req.WorkDir, req.DataDir); err != nil {
		if s.Warnf != nil {
			s.Warnf("persist: instance-register 拒绝（instance=%q）: %v", req.InstanceID, err)
		}
		return
	}
	s.View.Register(req.InstanceID, req.WorkDir, req.DataDir)
	// SESSION-COMP-1 P0：`session.db.shard=on` 时把旧布局搬到 dialogs/（一树一库）。
	// 异步执行：搬运可能涉及多库，不阻塞 instance-register 的应答。
	go s.maybeShardDialogs(req.InstanceID)
}

// 实例标识符上限（与 config 包 MaxNameChars 同源）与路径上限（MaxPathChars 同源）。
const (
	maxInstanceIDChars = 64
	maxInstancePathLen = 512
)

// validateInstanceRegister 校验实例登记（INPUTVAL-1 BE-22）：instance_id 标识符；work_dir 必填
// 绝对路径；data_dir 可选，非空须为绝对路径；均禁 `..` 段与超长。
func validateInstanceRegister(instanceID, workDir, dataDir string) error {
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return fmt.Errorf("instance_id required")
	}
	if len(id) > maxInstanceIDChars {
		return fmt.Errorf("instance_id 超长（上限 %d 字符）", maxInstanceIDChars)
	}
	for _, r := range id {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		return fmt.Errorf("instance_id 仅允许字母 / 数字 / - / _")
	}
	if err := validateInstancePath("work_dir", workDir, true); err != nil {
		return err
	}
	return validateInstancePath("data_dir", dataDir, false)
}

// validateInstancePath 路径校验：必填开关 + 绝对路径 + 长度 + 禁 `..`。
func validateInstancePath(field, path string, required bool) error {
	p := strings.TrimSpace(path)
	if p == "" {
		if required {
			return fmt.Errorf("%s required", field)
		}
		return nil
	}
	if len(p) > maxInstancePathLen {
		return fmt.Errorf("%s 超长（上限 %d 字符）", field, maxInstancePathLen)
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s 必须为绝对路径（%s）", field, p)
	}
	for _, seg := range strings.FieldsFunc(filepath.ToSlash(p), func(r rune) bool { return r == '/' }) {
		if seg == ".." {
			return fmt.Errorf("%s 不得包含上级目录引用（..）", field)
		}
	}
	return nil
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

// dataDomains 是 data-<domain> 消息面支持的域（61-消息一览 §3.1-§3.5）：配置五域 +
// 会话/任务树/知识库/记忆库四域（B 类随迁补齐）；snapshot = A3 运行时扩展（会话快照 get/set）。
// file-versions 归属 history.db 外部，不在本面。
var dataDomains = []string{"user-config", "prj-config", "prj-security", "scenario", "mcp", "prompt", "session", "inbox", "turn-event", "ask", "snapshot", "knowledge", "tasktree", "memory"}

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
	// 入站契约校验（G-41-c）：白名单外的 data 请求必须显式携带 instance_id。
	// 载荷已按域定死 struct 解析（payload.go），handler 各自取 Header；此处只需公共头。
	var hdr Header
	_ = json.Unmarshal(payload, &hdr)
	if hdr.InstanceID == "" && !instanceFreeDomains[domain] {
		s.failID(subject, hdr.ReqID, errInstanceIDRequired)
		return
	}
	switch domain {
	case "user-config":
		s.handleUserConfig(op, payload)
	case "prj-config", "prompt", "prj-security":
		s.handleConfigKV(domain, op, payload)
	case "scenario":
		s.handleScenario(op, payload)
	case "mcp":
		s.handleMCP(op, payload)
	case "session":
		s.handleSession(op, payload)
	case "inbox":
		s.handleInbox(op, payload)
	case "turn-event":
		s.handleTurnEvent(op, payload)
	case "ask":
		s.handleAsk(op, payload)
	case "snapshot":
		s.handleSnapshot(op, payload)
	case "knowledge":
		s.handleKnowledge(op, payload)
	case "tasktree":
		s.handleTasktree(op, payload)
	case "memory":
		s.handleMemory(op, payload)
	}
}

// replyID 按 req_id 回复成功结果（各域 handler 统一用本方法）。
func (s *Service) replyID(method, reqID string, result map[string]any) {
	b, _ := json.Marshal(map[string]any{"req_id": reqID, "ok": true, "result": result})
	_ = s.Bus.Emit(context.Background(), method, b)
}

// failID 按 req_id 回复错误。
func (s *Service) failID(method, reqID string, err error) {
	b, _ := json.Marshal(map[string]any{"req_id": reqID, "ok": false, "error": err.Error()})
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

// sessionDBBy 按实例 id 解析 prjusr 主库（信封 handler 用；`handle` 已按白名单校验
// G-41-c，非全局级域缺 instance_id 在更早处即失败）。
func (s *Service) sessionDBBy(instanceID string) (*data.DB, error) {
	instID, info, err := s.View.Resolve(instanceID)
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
