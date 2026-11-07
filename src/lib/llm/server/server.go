// Package server 是 chonkpilot-server 的 LLM 会话服务核心（对齐 21-llm-server）。
//
// 定位：**调度器**——低耦合消息驱动，llm 会话上下文（instance/session/turn/tool-call-id）层层传递：
//
//	llm-start（初始化会话）→ llm-send{type=text-user|tool-result|text-notify}（进入 LLM 的统一消息）
//	→ LLM 返回经 llm-notify{type=reason|text|tool-call} 广播给监听者（UI）
//	→ tool-call → 调 gateway（上下文带 tool-call-id）→ 结果喂回 → 循环 → eof 发 llm-complete（唯一终态）
//	→ ask_user：发 ask-user → 收 ask-user-reply → 结果喂回
//
// 数据面（会话/tasktree/快照/配置持久化）一律经总线 persist 服务（chonkpilot-data/persist，本包内嵌，
// 61-消息一览 §3）；域工具经 gateway 分组注册方法面（tools/register → mcp-tools-register，
// 61-消息一览 §5）；内置 agent 集（**app 级场景** `<exeDir>/scenarios/*/` 内的 agent，25 §8.1 #8/T6）
// 只登记到内存表供子轮次回落（25 §5/T2：agent 只"注入"不"注册"）。
package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// ─── tasktree 手动删除同步 ─────────────────────

// onTaskTreeDeleted persist data-tasktree-delete 级联删子树后广播（相对主题 task-deleted）
// → server 内存同步删除 + 标记（后续 persist 跳过，节点不复活）。
func (s *Server) onTaskTreeDeleted(_ string, payload []byte) {
	var msg struct {
		NodeID string `json:"node_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.NodeID == "" {
		var raw string
		if err := json.Unmarshal(payload, &raw); err == nil && raw != "" {
			msg.NodeID = raw
		}
	}
	if msg.NodeID != "" {
		s.tasks.markDeleted(msg.NodeID)
	}
}

// onMemoryRefresh persist data-memory-refresh（记忆域 save/delete 后广播，既有主题）→ 失效
// **该项目**的记忆类别清单缓存（I-68 ②），下次组装指引时重新取数；投影到项目键（SCOPE-1：
// 类别清单是项目级事实，缓存按 work_dir 分桶）。实例未登记 / 载荷未带 instance_id → 全清
// （保守处置，避免漏失效导致新类别不出现）。
func (s *Server) onMemoryRefresh(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	s.memCache.invalidate(s.projectScopeOf(msg.InstanceID))
}

// ─── 目录/接入变化（gateway 通知） ────────────────

// onMCPChanged gateway 广播目录/接入变化（mcp-gateway-changed，kind=tool 等）
// → 刷新工具定义缓存（tools/list）。
func (s *Server) onMCPChanged(_ string, _ []byte) {
	// 本回调在总线派发线程内执行：tools/list 是同步 RPC（Emit().Wait）→ 转后台，避免
	// 派发线程等待 gateway；工具缓存属附属能力，失败只降级当次刷新。
	go func() {
		defer recoverLog("onMCPChanged/ListTools")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = s.gc.ListTools(ctx)
	}()
}

// ─── instance 管理 ────────────────────────────
// 实例注册/心跳/退出由共通 instanceManager 订阅处理（21-llm-server）；
// server 在 instance-exit 时取消名下 running turn + 释放 session 锁 + 注销 capability dir 节点。

func (s *Server) onExit(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.InstanceID == "" {
		return
	}
	s.exitInstance(msg.InstanceID)
}

// exitInstance 实例退出清理（幂等：重复调用无副作用）——显式 `instance-exit`（onExit）与
// 分离形态心跳超时（sweepStaleInstances，见 sweep_split.go）**共用同一实现**：
// 取消名下 running turn + 释放 session 锁 + 注销 capability dir 节点。
func (s *Server) exitInstance(instanceID string) {
	if instanceID == "" {
		return
	}
	var removedAsks []*askWaiter
	s.mu.Lock()
	// 只清该 instance 名下的运行态（2026-09-19 缺口 5：按 instance 分桶 → 不误伤其他 instance）。
	// 锁内仅做 map 摘除与收集（无 IO），Close（含 session 锁释放 = 文件解锁 IO）留到锁外执行。
	var closing []turnSession
	for key, sess := range s.turns {
		if sess.InstanceID() != instanceID {
			continue
		}
		closing = append(closing, sess)
		delete(s.turns, key)
		delete(s.busy, instKey(instanceID, sess.SessionID()))
		delete(s.continuePending, key)
	}
	// 实例退出 → 摘除该实例的**全部** ask 等待登记（E2-1：其轮次可能已被移出 s.turns，
	// 按 instance 归属整体清，不依赖 turn 存活）。
	removedAsks = s.clearAsksByInstanceLocked(instanceID)
	// 本实例若是 Ask 中心订阅者 → 注销订阅（后端唯一分发不再向已关闭窗口推送）。
	for user, sub := range s.askCenterSubs {
		if sub.instanceID == instanceID {
			delete(s.askCenterSubs, user)
		}
	}
	s.mu.Unlock()
	for _, w := range removedAsks {
		s.notifyAskCenterRemove(w, "instance_exit")
	}
	for _, sess := range closing {
		sess.Close() // 锁外释放 session 锁（文件 IO）；turnCtx.Close 幂等
	}
	// 整窗释放（SCOPE-1 第一条释放路径）：该实例名下的运行态归属一并清空，防止窗口/进程退出后
	// 永久占位（否则别的窗口再发 llm-start 会被永远不会空闲的 owner 拒掉）。
	s.forgetOwnersOfInstance(instanceID)
	// 关窗即停（SUBCHAT-3 阶段 F-2 · 群聊 §15.4 N23）：本实例若为某群聊的**驱动实例**（群聊窗口），
	// 其退出 → 停机该群聊调度器（进程内停机，lifecycle 不变；重开点「继续」据 round_targets 断点补跑）。
	s.stopGroupRunsForInstance(instanceID)
	flushInstanceIndex(s.bus, instanceID) // 回收该实例的 session/turn 备忘（长驻服务端防累积）
	s.unregisterCapabilityNodes(instanceID)
	if s.capWatch != nil {
		// B-38：回收该实例派生的 capability 根监听（防长驻服务端 fswatcher 句柄随实例累积）。
		s.capWatch.unwatchInstance(instanceID)
	}
}

// onInstanceRegister 实例注册 → 接入该实例的用户级 + 项目级 capability 根（三级根的
// 用户/项目两级；系统级 = <exeDir>/capability 由内嵌 self 节点在 New 时承载）→ 经 gateway
// 方法面 servers/register（dir 类型节点，scope=instance_id，gateway 自建官方 server +
// RegisterContracts 扫描）动态接入；目录缺失跳过、重复注册幂等。
// 注销由 onExit → servers/unregister（dir 拆除）。dir 节点工具带节点前缀
// （<instanceID>-user_ / <instanceID>-project_），归属 instance，不污染其他实例的工具面。
func (s *Server) onInstanceRegister(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
		WorkDir    string `json:"work_dir"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.InstanceID == "" || msg.WorkDir == "" {
		return
	}
	// 执行配置注入（本实例的 usr/prj 配置 → 内嵌 mcp-server Config；见 loadExecConfig）。
	// 时序：persist 先订阅 instance-register 并登记绑定，本 handler 在其后执行（同主题按注册序），
	// 故此处经总线读 usr/prj 配置时实例已可解析。
	s.loadExecConfig(msg.InstanceID)
	if s.gw == nil {
		return // DisableMCP（宿主自建 gateway）
	}
	// agentbox（决策 42 §2 (109)）：实例的 prj `security-*` 此刻才可读 → 为开启 sandbox 的
	// MCP 条目补齐「允许目录快照」，经既有 servers/register 重下发（**零新增主题**）。
	// 仅 sandbox=true 的条目会因快照变化触发一次重注册（respawn 该上游进程）；其余条目
	// 快照保持 nil → 与已下发集合逐字段相同 → 零 diff、零动作。
	// 对账含 spawn/connect/tools-list（可达数十秒）——**必须放后台**（与 onMCPConfigRefresh 完全
	// 同口径）：本 handler 在 persist 同步派发链路内，同步执行会阻塞 instance-register 应答；
	// MCP 对账不阻塞实例注册（工具面稍后收敛，注册语义不受影响）。故后台化并加 panic 兜底：
	// 对账属附属能力，失败/panic 只降级当次动作，不拖垮常驻服务。
	// 实例此刻**已登记**（persist 先订阅 instance-register 并登记绑定），故对账须带
	// instance_id —— 使该实例的 project/prjusr 级 MCP 正确解析（否则多实例下"唯一实例回退"串库/失败）。
	go func() {
		defer recoverLog("onInstanceRegister/reconcileUserMCPs")
		s.reconcileUserMCPs(msg.InstanceID)
	}()
	// 重启恢复：进程内 session→dialog 备忘为空，群聊「继续/结束」等入口不经 llm-start
	// 无法自愈 → 实例注册后后台预热一次（失败静默，仍有 llm-start 兜底）。
	go func() {
		defer recoverLog("onInstanceRegister/prewarmSessionIndex")
		s.newSessionStore(msg.InstanceID).Prewarm()
	}()
	// 实例 → work_dir 映射**只有一份**：权威 = 共通实例视图 `s.im`（instance.Manager，
	// 本包在其后订阅 instance-register → 此处读到的是刚登记的记录）。不再维护本地副本。
	// 目录节点接入含 gateway `servers/register`（spawn 下游进程）→ **必须放后台**（与
	// MCP 对账同口径）：本 handler 在总线同步派发线程内，不得等待子进程/网络。
	s.capRegisterWG.Add(1)
	go func() {
		defer s.capRegisterWG.Done()
		defer recoverLog("onInstanceRegister/registerCapabilityNodes")
		s.registerCapabilityNodes(msg.InstanceID, msg.WorkDir)
		if s.capWatch != nil {
			s.capWatch.watchInstance(msg.InstanceID, msg.WorkDir) // T-21：监听该实例可见的 user/project/prjusr 级 capability 根
		}
	}()
}

// ─── llm-start / llm-send / llm-cancel / ask-user-reply ─────────────

// SetFilters 注入插件 filter 链（BRIDGE-2）；nil = 关闭。
// tool.before_call 同时下推到内嵌 gateway（工具执行前 fail-closed）。
func (s *Server) SetFilters(f *pluginbridge.FilterChain) {
	s.filterMu.Lock()
	s.filters = f
	s.filterMu.Unlock()
	if f == nil || s.gw == nil {
		return
	}
	s.gw.SetBeforeToolCall(func(tool string, args map[string]any) (map[string]any, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		out, err := f.Run(context.Background(), "tool.before_call", raw)
		if err != nil {
			return nil, err
		}
		var next map[string]any
		if err := json.Unmarshal(out, &next); err != nil {
			return nil, err
		}
		return next, nil
	})
}

// runFilter 在副作用前执行某个 stage 的 filter 链；无链 = 原样通过。
func (s *Server) runFilter(ctx context.Context, stage string, v *mq.Value) error {
	s.filterMu.RLock()
	f := s.filters
	s.filterMu.RUnlock()
	if f == nil {
		return nil
	}
	out, err := f.Run(ctx, stage, v.Payload)
	if err != nil {
		return err
	}
	v.Payload = out
	return nil
}

// externalToolsEnabled 表示宿主已接管 server 域/元工具（由 core-mcp-server 按 core-only
// 契约注册并经 ExecTool.Call 执行）。
// 单元测试不设置该环境变量 → 保持进程内注册（既有测试夹具无需插件桥）。
func externalToolsEnabled() bool {
	return os.Getenv("CHONKPILOT_EXTERNAL_TOOLS") == "1"
}

func (s *Server) turnSession(turnID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tc := s.lookupTurnLocked("", turnID); tc != nil {
		return tc.req.Session
	}
	return ""
}

// visibleTools 组装某 instance **可见**的全部工具定义（**不过滤 hot**）：
// 工具面取自 gwClient **按 instance 分桶**的可见集（RB-5 L2：global ∪ 归属当前 instance；
// 与改前「全量 tools/list 后每次按 scope 过滤」逐项同序）。
// 工具契约描述里的 {{toolchain.<key>}} 占位符在此替换为用户配置的工具链路径
// （仅当确有占位符才读 usr 配置；替换只改本次下发的副本，不动 gateway 缓存）。
//
// 供两处消费：① `toolsForLLM`（缺省下发面 = hot 子集）；② `llmTools` 的白名单分支
// （25 §4.3：白名单非空 → 白名单里的工具**不论是否 hot**都下发，见 T1 缺陷修正）。
func (s *Server) visibleTools(instance string) []ToolDef {
	visible := s.gc.ToolsFor(instance)
	out := make([]ToolDef, 0, len(visible))
	var vars map[string]string // 懒加载（无占位符则不发 data-user-config-load）
	for _, t := range visible {
		if strings.Contains(t.Description, mcpms.ToolchainPlaceholder) {
			if vars == nil {
				vars = s.toolchainVars(instance)
			}
			t.Description = mcpms.ReplaceToolchain(t.Description, vars)
		}
		out = append(out, t)
	}
	return out
}

// toolsForLLM 组装发给 LLM 的**缺省**工具列表（21-llm-server · 25 §4.1）：
// = visibleTools 筛 `hot==true`（hot 工具 ∪ gateway in-memory meta 工具；实例级 capability
// dir 节点工具不污染其他 instance 的 LLM 工具面）。白名单非空时改走 `llmTools` 的白名单分支。
func (s *Server) toolsForLLM(instance string) []ToolDef {
	visible := s.visibleTools(instance)
	out := make([]ToolDef, 0, len(visible))
	for _, t := range visible {
		if !t.Hot {
			continue
		}
		out = append(out, t)
	}
	return out
}
