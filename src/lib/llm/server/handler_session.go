package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-agent/router"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-util/pathx"
)

// onLLMSimple 无上下文单轮 LLM 方法面（SubjectLLMSimple，mq 方法调用：请求→同主题 promise）：
// 请求 {prompt[, system], instance_id[, llm]} → 单轮无 session/turn 上下文调用 LLM → 应答 {text}。
// llm 可选（SL-2 / SL-C7，2026-09-24）：LLM 选择引用（llmref = provider name）——**不传/空 =
// 现状**（exe flags 隐含默认，载荷逐字节兼容）；命中 usr provider → 用该 provider。
// 调用方 = 同进程任何总线参与方（compress 插件摘要 / memory 沉淀 / gateway mcp_find 语义检索等），
// 不依赖插件函数注入。
// prompt/system 文本里的 {{toolchain.<key>}} 占位符按实例 usr 配置替换（如摘要 system 提示词
// capability/system/summary.md 引用工具链路径）；instance_id 缺失 → 已知 key 按未配置（空串）替换。
func (s *Server) onLLMSimple(ctx context.Context, _ string, v *mq.Value) error {
	if err := s.runFilter(ctx, "llm.before_simple", v); err != nil {
		return methodErr(err.Error())
	}
	var req struct {
		Prompt     string `json:"prompt"`
		System     string `json:"system,omitempty"`
		InstanceID string `json:"instance_id,omitempty"`
		// LLM 子系统默认 LLM（可选；llmref = provider name）。空 = 不指定 → specFor(nil) 回落
		// exe flags 隐含默认，且 loadLLMProvider 对空名**不做任何配置读取**（零额外开销）。
		LLM string `json:"llm,omitempty"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return methodErr("prompt is required")
	}
	text, err := s.llmOnceSpec(
		s.specFor(s.loadLLMProvider(req.InstanceID, req.LLM)),
		s.replaceToolchain(req.InstanceID, req.System), s.replaceToolchain(req.InstanceID, req.Prompt),
	)
	if err != nil {
		return methodErr(err.Error())
	}
	v.Result = map[string]any{"text": text}
	return nil
}

// llmOnceTimeout 一次性无上下文 LLM 调用的**外层 ctx 超时上限**（llm-simple / prompt-optimise /
// config.suggest 共用入口 llmOnceSpec）。默认 60s；`var` 便于单测临时替换（同 llmTestTimeout 范式），
// 使「生成耗时较长 → 明确超时错误」边界（配置智能填充 §6）可在毫秒级验证。
var llmOnceTimeout = 60 * time.Second

// llmOnceSpec 单轮无上下文 LLM 调用（60s 超时；无可用 LLM → error），按 spec 选 provider
// （spec 来自 specFor(loadLLMProvider(...))：命中 usr provider → 该 provider；
// 未命中/未指定 → exe flags 隐含默认 `__default__`，与旧 `s.llm` 默认客户端同口径：
// -llm-base / -llm-model，openai 协议、无 apiKey）。
func (s *Server) llmOnceSpec(spec router.Spec, system, prompt string) (string, error) {
	if s.rt == nil {
		return "", errors.New("llm not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmOnceTimeout)
	defer cancel()
	msgs := []ChatMsg{{Role: "user", Content: prompt}}
	if system != "" {
		msgs = append([]ChatMsg{{Role: "system", Content: system}}, msgs...)
	}
	events, err := s.chat(ctx, spec, msgs, nil, ChatOptions{})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for ev := range events {
		if ev.Err != nil {
			return "", ev.Err
		}
		out.WriteString(ev.Content)
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", errors.New("empty llm result")
	}
	return strings.TrimSpace(out.String()), nil
}

// onLLMStart 受理轮次开始：session/turn 由客户端分配（必填，不可为空）；work_dir/data_dir
// 取 instance-register 绑定的记录（载荷不传）。幂等落库 session + turn（已存在复用，不重新分配）。
//
// Continue（同轮次继续，llm-start 可选字段）：复用该 session 最后一轮 turn（未带 turn 时
// 由后端按 session 取最近一轮），turn id 保持不变、状态回写 running；不发 turn-start（非新轮）。
// 结果直接写 v.Result（同主题 promise），无 .reply。
func (s *Server) onLLMStart(ctx context.Context, subject string, v *mq.Value) error {
	// 关停闸门（第 1 条 C）：Stop 已置位 → 拒绝新轮，避免 Stop 后仍建轮次 / 写库。
	if s.isStopped() {
		v.Result = map[string]any{"accepted": false, "error": "server stopped"}
		return nil
	}
	// 外部 llm-start = 用户动作（发新轮 / 点「继续」）→ 重置 next_run 连续计数（NEXTRUN-1 §3.3.3）。
	return s.acceptLLMStart(ctx, subject, v, true)
}

// acceptLLMStart 承载 llm-start 方法面（外部 JSON 载荷）：先过 filter、解析 JSON，
// 再交 serveLLMStart 走同一受理主体。
// resetNextRun = true（外部用户动作）→ 先归零 `sessions.next_run_count`；false（内部
// consumeNextTurn 起自动轮）→ 保持计数，自动链才能连续接续到上限。
func (s *Server) acceptLLMStart(ctx context.Context, _ string, v *mq.Value, resetNextRun bool) error {
	if err := s.runFilter(ctx, "llm.before_start", v); err != nil {
		v.Result = map[string]any{"accepted": false, "error": err.Error()}
		return nil
	}
	var req StartReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Result = map[string]any{"accepted": false, "error": "bad request"}
		return nil
	}
	return s.serveLLMStart(v, req, resetNextRun)
}

// serveLLMStart 是 llm-start 的**受理主体**（校验 + owner/busy + 落库 + 建轮次）：
// 外部方法面（acceptLLMStart）与内部调用（acceptStart）共用；后者**不经过 JSON**，
// 因此 StartReq 的内部字段（如 NotifyTurn，json:"-"）不会出现在消息面契约里。
func (s *Server) serveLLMStart(v *mq.Value, req StartReq, resetNextRun bool) error {
	if req.Session == "" {
		v.Result = map[string]any{"accepted": false, "error": "session is required"}
		return nil
	}
	// turn 必填；例外：同轮次继续（continue=true）未带 turn → 由后端按 session 解析最近一轮。
	if req.Turn == "" && !req.Continue {
		v.Result = map[string]any{"accepted": false, "error": "session and turn are required"}
		return nil
	}
	// instance 不应为空（R-11 二次升级：instance 为空即异常，顶层错误、不进 turn）。
	if strings.TrimSpace(req.InstanceID) == "" {
		v.Result = map[string]any{"accepted": false, "error": "instance_id is required"}
		return nil
	}
	rec, ok := s.im.Lookup(req.InstanceID)
	if !ok {
		v.Result = map[string]any{"accepted": false, "error": "instance not registered"}
		return nil
	}
	// 项目作用域：显式 work_dir 优先（一个窗口可运行多个项目）；缺省才用窗口绑定。
	// 本轮开始后 scope 不可变，后续 send/cancel/recover 都从 turnCtx 取。
	explicitScope := strings.TrimSpace(req.WorkDir) != ""
	req.ScopeExplicit = explicitScope
	if !explicitScope {
		req.WorkDir = rec.WorkDir
		req.DataDir = rec.DataDir
	} else {
		if !filepath.IsAbs(req.WorkDir) {
			v.Result = map[string]any{"accepted": false, "error": "work_dir must be absolute"}
			return nil
		}
		// 信任边界：显式 work_dir 必须已在项目注册表中（前端/插件不得凭任意绝对路径起轮）。
		if !data.HasWorkDir(req.WorkDir) {
			v.Result = map[string]any{"accepted": false, "error": "work_dir is not a registered project"}
			return nil
		}
		dd, _ := data.DataDirForWorkDir(req.WorkDir)
		if req.DataDir != "" && !pathx.Same(dd, req.DataDir) {
			v.Result = map[string]any{"accepted": false, "error": "data_dir does not match the registered project"}
			return nil
		}
		req.DataDir = dd
	}
	scope := facade.Scope{}
	if explicitScope {
		scope = facade.Scope{WorkDir: req.WorkDir, DataDir: req.DataDir}
		// 同窗口新增 workdir：按该项目的 capability 根补注册 dir 节点（热重扫不覆盖显式 scope 节点）。
		s.registerCapabilityNodes(req.InstanceID, req.WorkDir)
	}
	s.rememberScope(req.InstanceID, req.Session, req.Turn, scope)
	// 运行态归属（SCOPE-1 强制不变式，2026-10-28）：该 session 已被**另一个窗口实例**持有活动
	// 运行态 → 拒绝（同一 dialog 不得在两个窗口跑；走既有 accepted:false 应答，不新增字段）。
	// 无主 / 同一实例 → 本次受理即成为 owner（「用户选择即激活」）。
	if owner, ok := s.claimSessionOwner(req.InstanceID, req.Session); !ok {
		v.Result = map[string]any{"accepted": false, "error": ownerConflictText + owner}
		return nil
	}
	// 用户发送即重置 next_run 计数（NEXTRUN-1 §3.3.3）：放在受理前 → 即使随后被 busy 拒绝，
	// 也表达「用户已介入」；内部自动轮（resetNextRun=false）不重置。
	// 一树一库：首轮自举前本进程尚不知 dialog_id（重置会返回 false）→ 建会话后再补一次。
	nextRunReset := true
	if resetNextRun {
		nextRunReset = s.resetNextRunCount(req.InstanceID, req.Session)
	}

	// 首次使用该 instance 时清理遗留（多 workdir 服务，启动时无从预知数据根）：
	// 会话 turn（data-session-cleanup-stale）+ 任务树遗留非终态（**任务层 Recover**，21 §9.2 ⑥）
	s.mu.Lock()
	firstUse := !s.cleaned[req.InstanceID]
	if firstUse {
		s.cleaned[req.InstanceID] = true // 先占位，防并发首轮重复清理
	}
	s.mu.Unlock()
	if firstUse {
		// MOD-1 M6b：启动扫描未答 ask（须在遗留清理**之前**——清理会把 running 行置
		// interrupted，扫描只用于留痕 / 计数，失效应答由 ask_id 回查兜底）。
		s.scanPendingAsks(req.InstanceID)
		store := s.newSessionStore(req.InstanceID, scope)
		if err := store.CleanupStaleTurns(); err != nil {
			// B-41：失败须留痕且**撤销占位** → 下次使用重试（原先吞错 + 提前置位 → 永不重试）。
			logf("[chonkpilot-server] CleanupStaleTurns failed (instance=%s): %v\n", req.InstanceID, err)
			s.mu.Lock()
			delete(s.cleaned, req.InstanceID)
			s.mu.Unlock()
		} else {
			s.tasks.recoverStaleTasktree(req.InstanceID)
			// MOD-1 M3：终态 turn 的事件日志按保留天数清理（usr turnEventLogRetentionDays；
			// 0 = 永久；缺失 → 7 天）。sweep 周期 = 首次使用该 instance（与 CleanupStaleTurns 同点）。
			retention := 7
			if uc, err := s.loadUserConfig(req.InstanceID); err == nil {
				if n, ok := configInt(uc, "turnEventLogRetentionDays"); ok {
					retention = n
				}
			}
			if err := store.CleanupTurnEvents(retention); err != nil {
				logf("[chonkpilot-server] CleanupTurnEvents failed (instance=%s): %v\n", req.InstanceID, err)
			}
		}
	}

	s.mu.Lock()
	// 同 instance 同 session 互斥（2026-09-19 缺口 5：busy 按 instance 分桶，
	// 不同 instance 的同名 session 互不阻塞）。
	if busy, exists := s.busy[s.runKey(req.InstanceID, req.Session)]; exists {
		s.mu.Unlock()
		v.Result = map[string]any{"accepted": false, "error": "session busy: " + busy}
		return nil
	}
	s.mu.Unlock()

	store := s.newSessionStore(req.InstanceID, scope)
	// 同轮次继续：未带 turn → 后端取该 session 最近一轮（带 turn 则直接采用）。
	if req.Continue && req.Turn == "" {
		req.Turn = store.LatestTurnID(req.Session)
		if req.Turn == "" {
			v.Result = map[string]any{"accepted": false, "error": "no turn to continue"}
			return nil
		}
	}

	// TURNRUN-1 批次 1：跨进程互斥已由桌面进程单例（`singleinstance`：实例 UDS 独占 listen）保证，
	// 进程内同 session 互斥由上方 `busy` 注册表承担 → 不再使用 session 文件锁。

	// 落库：session + turn（幂等；监听时检查并落库，不重新分配）——continue 复用同一 turn，
	// 回写 running 并清空 finish_reason（原 finished/interrupted 脏状态复位）。
	// 落库失败即中止受理（B-34）：落库失败仍起状态机并喂消息 = 写向不存在的 turn，产生半成品
	// 数据；按既有拒绝应答形态回 {accepted:false,...}。
	if err := store.EnsureSession(req.Session); err != nil {
		v.Result = map[string]any{"accepted": false, "error": "ensure session failed: " + err.Error()}
		return nil
	}
	// 补一次 next_run 重置：首轮自举后 dialog_id 才可知（幂等；此前已成功则不重复）。
	if resetNextRun && !nextRunReset {
		s.resetNextRunCount(req.InstanceID, req.Session)
	}
	if err := store.EnsureTurn(req.Turn, req.Session); err != nil {
		v.Result = map[string]any{"accepted": false, "error": "ensure turn failed: " + err.Error()}
		return nil
	}
	if req.Continue {
		if err := store.ReopenTurn(req.Turn); err != nil {
			v.Result = map[string]any{"accepted": false, "error": "reopen turn failed: " + err.Error()}
			return nil
		}
	}

	// 建立 turn 状态机（调度器）：主轮次无上一级 → ctx 根为 Background（G-18 gapA）。
	tc := newTurnCtx(context.Background(), s, req)
	s.mu.Lock()
	// 运行态登记键 = instKey(instance, id)（缺口 5：按 instance 分桶）。
	s.busy[s.runKey(req.InstanceID, req.Session)] = req.Turn
	s.registerTurnLocked(s.runKey(req.InstanceID, req.Turn), tc)
	if req.Continue {
		// 同轮次继续：首个 text-user 以 Kind=continue 注入（非新轮边界）。
		s.continuePending[s.runKey(req.InstanceID, req.Turn)] = true
	} else {
		delete(s.continuePending, s.runKey(req.InstanceID, req.Turn))
	}
	s.mu.Unlock()

	// 轮次开始广播（turn-start；主轮次 parents 空——前端/插件据此驱动会话树与 history 提交）。
	// continue 复用同一轮：不再广播，避免会话树/history 重复提交。
	if !req.Continue {
		s.emitTurnStart(req)
	}

	v.Result = map[string]any{"accepted": true, "session": req.Session, "turn": req.Turn}
	return nil
}

// acceptStart 以内部请求走 llm-start 同一受理路径（INBOX 的 next_turn 消费 / 内部通知轮 /
// 测试复用）：**直接**调用 serveLLMStart（不 marshal/unmarshal JSON），因此 StartReq 的
// 内部字段（NotifyTurn，json:"-"）可传达到 newTurnCtx，同时不进入 llm-start 消息面契约。
func (s *Server) acceptStart(req StartReq) map[string]any {
	v := &mq.Value{}
	_ = s.serveLLMStart(v, req, false)
	if v.Result == nil {
		return map[string]any{"accepted": false}
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		return map[string]any{"accepted": false}
	}
	return res
}

// onLLMSend 调度核心：text-user / text-notify / tool-result → 喂给 turn 状态机。
// 同轮次继续（llm-start{continue:true}）后首个 text-user → Kind=continue 注入。
// turn 缺省（continue 未带 turn，桥原样透传空 turn）→ 按 session 回退运行中轮次。
func (s *Server) onLLMSend(ctx context.Context, _ string, v *mq.Value) error {
	// 关停闸门（第 1 条 C）：Stop 已置位 → 不投递输入（轮次已统一 Close；不再写库）。
	if s.isStopped() {
		v.Result = map[string]any{"accepted": false, "error": "server stopped"}
		return nil
	}
	if err := s.runFilter(ctx, "llm.before_send", v); err != nil {
		v.Result = map[string]any{"accepted": false, "error": err.Error()}
		return nil
	}
	var req SendReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		// 回执语义（R9-4-1，与下方 tc==nil 分支同口径）：载荷非法 → 明确拒绝（不再静默丢弃）。
		v.Result = map[string]any{"accepted": false, "error": "bad request"}
		return nil
	}
	if req.WorkDir != "" {
		s.rememberScope(req.InstanceID, req.Session, req.Turn, facade.Scope{WorkDir: req.WorkDir, DataDir: req.DataDir})
	}
	// 运行态归属预检（SCOPE-1 强制不变式，2026-10-28）：该 session 已被**另一个窗口实例**持有
	// 活动运行态 → 拒绝（走既有 accepted:false 应答，不新增字段）。载荷只带 turn 时按 turn 全局
	// 反查会话（不按 instance 过滤），否则认不出「别的窗口正在跑」。
	if owner, ok := s.ownerConflict(req.InstanceID, req.Session, req.Turn); !ok {
		v.Result = map[string]any{"accepted": false, "error": ownerConflictText + owner}
		return nil
	}
	s.mu.Lock()
	// 按 instance 归属取轮次（缺口 5）：载荷带 instance_id → 只在本 instance 桶内命中；
	// 未带（旧调用方）→ 全桶回退（旧语义）。A4-P3a-0：定位走 turnSession 接口句柄。
	sess := s.lookupTurnSessionLocked(req.InstanceID, req.Turn)
	if sess == nil && req.Turn == "" && req.Session != "" {
		sess = s.turnBySessionSessionLocked(req.InstanceID, req.Session)
	}
	key := s.runKey(req.InstanceID, req.Turn)
	if sess != nil {
		key = s.runKey(sess.InstanceID(), sess.TurnID())
	}
	cont := s.continuePending[key]
	if cont {
		delete(s.continuePending, key)
	}
	s.mu.Unlock()
	if sess == nil {
		// 回执语义（R8-D-3）：turn 未找到/已回收 → 明确拒绝（不再静默丢弃），便于发送端据回执
		// 区分「已受理 / 被丢弃」。字段沿用本域既有口径（accepted/error，同 onLLMStart）。
		// 成功路径**不写 Result**（命中即 return nil）——二者据此可区分：空 Result = 已受理。
		v.Result = map[string]any{"accepted": false, "error": "turn not found"}
		return nil
	}
	in, ok := sess.(turnInput)
	if !ok {
		// 防御分支（现行代码路径不可达：登记的实现均为 *turnCtx）：不投递输入，回执口径同上。
		logf("[chonkpilot-server] llm-send: turn %s 不接受输入（%T），按未命中处理\n", sess.TurnID(), sess)
		v.Result = map[string]any{"accepted": false, "error": "turn not found"}
		return nil
	}
	// 受理成功（轮次就在本实例上）→ 无主时本实例成为 owner（同一 instance 重入幂等；
	// SCOPE-1：「用户选择即激活」）。
	s.claimSessionOwner(req.InstanceID, sess.SessionID())
	switch req.Type {
	case SendTypeTextUser:
		// INPUTVAL-1：后端复验单条输入长度（前端仅体验，后端是唯一可信边界）；超限明确拒绝。
		if n := len([]rune(req.Content)); n > maxChatInputChars {
			v.Result = map[string]any{"accepted": false, "error": fmt.Sprintf("消息超长（%d 字符，上限 %d）", n, maxChatInputChars)}
			return nil
		}
		// 用户发送即重置 next_run 连续计数（NEXTRUN-1 §3.3.3）。
		s.resetNextRunCount(sess.InstanceID(), sess.SessionID())
		if cont {
			in.FeedContinue(req.Content)
		} else {
			in.FeedText(req.Content)
		}
	case SendTypeTextNotify:
		in.FeedNotify(req.Content)
	case SendTypeToolResult:
		in.FeedToolResult(req.ToolCallID, req.Content)
	}
	return nil
}

// isStopped 读取关停闸门（s.stopped 受 s.mu 保护）。
func (s *Server) isStopped() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// onLLMCancel 用户取消：以 llm-start 分配的 session+turn 为依据定位轮次。
// S6：取消发 llm-complete{status:interrupted} 终态（引擎侧 ctx 取消静默，不再发 llm-error）。
// turn 缺省（旧 RPC CancelChat 不带 turn）→ 按 session 回退运行中轮次。
//
// 2026-11-16（第 1 条：turn 取消快速终态）：清理编排拆成**快路径 + 后台清理**——
//   - 快路径只做「ask 终止 → 递归关子轮 → 落自身终态 + 关轮」（毫秒级；终态屏障语义不变：
//     必须先关子树再落自身终态）；
//   - 任务树取消（cancelByTurn → 层 CancelSubtree → 执行侧取消阶梯）与可选 gateway
//     `CancelTurn` 移到后台 goroutine（总预算 5s），**绝不阻塞总线同步派发的 UI 线程**。
func (s *Server) onLLMCancel(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		Session    string `json:"session"`
		Turn       string `json:"turn"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	// 关停闸门（第 1 条 C）：Stop 已置位 → 立即返回，不再写库 / 不再起后台清理 goroutine
	//（轮次已由 Stop 统一 Close，执行侧由 gateway Stop 抑制回报）。
	if s.isStopped() {
		logf("[llm-cancel] server stopped → ignore cancel instance=%s session=%s turn=%s\n",
			req.InstanceID, req.Session, req.Turn)
		v.Result = map[string]any{"cancelled": false, "turn": req.Turn}
		return nil
	}
	// 锁内**只做定位**（缺口 5）：带 instance_id → 只在本 instance 桶内命中（**不会取消
	// 其他 instance 的同名 session/turn**）；未带 → 全桶回退（旧语义）。
	s.mu.Lock()
	tc := s.lookupTurnLocked(req.InstanceID, req.Turn)
	if tc == nil && req.Turn == "" && req.Session != "" {
		tc = s.turnBySessionLocked(req.InstanceID, req.Session)
	}
	matched := tc != nil && tc.req.Session == req.Session
	busyTurns := len(s.turns)
	s.mu.Unlock()
	if !matched {
		logf("[llm-cancel] no running turn for instance=%s session=%s turn=%s (busy turns=%d)\n",
			req.InstanceID, req.Session, req.Turn, busyTurns)
		v.Result = map[string]any{"cancelled": false, "turn": req.Turn}
		return nil
	}
	// I-35：级联取消该 turn 在飞工具/任务（含 llm_run 子树节点 + gateway 异步任务），
	// 否则用户"停止"后工具可能仍在跑。取消优先标记：终态屏障「先关子树、再落自身终态」，
	// 并阻止自然完成路径误发 complete。
	//
	// **必须在 s.mu 之外**（2026-09-22 修）：总线 Emit 是**同 goroutine 同步派发**，
	// 取消链会在本 goroutine 上同步回到 `onGatewayTaskDone`（`s.mu.Lock()`）；持锁级联 =
	// 同一 goroutine 对非重入互斥自锁 → 永久挂起（宿主 publish 走 WebView2 UI 线程 → 窗口整体冻结）。
	// 2026-11-16：即使不持锁，层回调 → gateway 取消阶梯仍可能阻塞数十秒 → 故级联取消移入后台。
	tc.cancelRequested.Store(true)

	// ① ask 终止（用户取消 = 提问终止）：持 s.mu 只摘登记；解锁后发 Ask 中心摘除 + 落库终态。
	// 未回复 ask_user 的 role=tool 行从 running 立即改为 interrupted（历史/重载可见终态）。
	s.mu.Lock()
	removedAsks := s.clearAsksByTurnLocked(tc.req.InstanceID, tc.req.Turn)
	s.mu.Unlock()
	for _, w := range removedAsks {
		s.notifyAskCenterRemove(w, "turn_closed")
		if w != nil && w.toolID != "" {
			tc.persistToolResult(w.toolID, engineInterruptedToolText, persist.ToolStatusInterrupted, nil)
		}
	}

	// ② 快速关闭委派子轮次（递归；只落终态 + 关轮，不做任务树/执行侧取消）。
	// 返回的 turnIDs 含本 turn → 后台逐项补 cancelByTurn / CancelTurn。
	turnIDs := s.closeDelegatedChildrenFast(tc.req.InstanceID, tc.req.Turn)

	// ③ 自身终态：**必须晚于子轮次 Done**（finish → awaitDelegatedChildren 需子已收尾；
	// 子 Close 会关闭 ctx）。顺序不能反。
	//
	// 注意：这里**不**在终态落库前调用 gateway CancelTurn —— 终态写入（快照取消息）与
	// 工具结果落库并发会交错；执行侧取消统一放到 tc.Close() 之后的后台清理，先 cancel ctx
	// 阻断新结果，再按 turn 兜底取消（工具行终态由各自 worker 处理）。
	s.complete(tc, "interrupted", "interrupted", "")
	tc.Close()

	// ④ 后台清理（执行侧 CancelTurn + 任务树状态 + 层回调）：总预算 5s，超时只留痕。
	// goroutine 内自带 recover + 超时日志，不 panic、不阻塞本消息派发线程。
	go s.cancelTurnCleanup(tc.req.InstanceID, turnIDs, turnCancelCleanupBudget)

	v.Result = map[string]any{"cancelled": true, "turn": req.Turn}
	return nil
}

// turnCancelCleanupBudget 是 turn 取消**后台清理**的总预算（第 1 条：后台最多等 5s）。
const turnCancelCleanupBudget = 5 * time.Second

// cancelTurnCleanup 在后台补做 turn 取消的**重**清理（第 1 条）：对每个 turnID 依次
//   - 可选 `CancelTurn`（**先**做：O(任务数) 扫描 + cancel ctx 立即返回，覆盖任务树未绑定 /
//     gateway 已转后台的执行；provider 阶梯在 gateway 后台，不阻塞本 goroutine）；
//   - `cancelByTurn`（再做任务树状态推进 + 层回调；此时执行侧多已终态，层回调不再触发阶梯）。
//
// 顺序不可反：先任务树会让层回调同步进入 provider 阶梯（最长数十秒），把 5s 预算耗在
// “等待一个已经可以立即 cancel ctx 的任务”上。
// 总预算 budget：内部 worker 到点仍未完成 → 记录「turn cancel cleanup timed out」+ 剩余 turn；
// 内部 panic 由 recover 兜住（取消路径不得把宿主拖崩）。
func (s *Server) cancelTurnCleanup(instanceID string, turnIDs []string, budget time.Duration) {
	if len(turnIDs) == 0 || s.tasks == nil {
		return
	}
	done := make(chan struct{})
	var processed atomic.Int32
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logf("[chonkpilot-server] turn cancel cleanup panic（instance=%s）: %v\n", instanceID, r)
			}
			close(done)
		}()
		for _, id := range turnIDs {
			if id != "" {
				// 执行侧先取消：cancel ctx + 终态迁移立即完成，后续任务树回调不再跑阶梯。
				if n, err := s.cancelExecTurn(instanceID, id); err != nil {
					logf("[chonkpilot-server] turn cancel cleanup: CancelTurn 失败（instance=%s turn=%s）: %v\n",
						instanceID, id, err)
				} else if n > 0 {
					logf("[chonkpilot-server] turn cancel cleanup: CancelTurn 兜底取消 %d 个执行（instance=%s turn=%s）\n",
						n, instanceID, id)
				}
				s.tasks.cancelByTurn(instanceID, id)
			}
			processed.Add(1)
		}
	}()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		i := int(processed.Load())
		if i > len(turnIDs) {
			i = len(turnIDs)
		}
		remaining := append([]string{}, turnIDs[i:]...)
		// 此时内部 worker 可能仍在某个阻塞调用上（provider 阶梯）；本 goroutine 返回，
		// 不阻塞调用方，也不等待（预算已到）。
		logf("[chonkpilot-server] turn cancel cleanup timed out after %s（instance=%s remaining=%v）\n",
			budget, instanceID, remaining)
	}
}

// onSessionRunningList 处理 `session-running-list`（61 §4.2，2026-11-03）：只读**内存快照** ——
// 读 `busy` 注册表（`instKey(instance, session) → running turn`），返回当前在跑轮次的会话清单
// `{list:[{session_id, turn, instance_id}]}`（本进程全部窗口；前端按自己的会话列表过滤）。
//
// **不落库**：进程重启后运行态本就不保留（遗留 `running` 由启动清理 `data-session-cleanup-stale`
// 置 `interrupted`），故内存快照即权威。
func (s *Server) onSessionRunningList(_ context.Context, _ string, v *mq.Value) error {
	s.mu.Lock()
	list := make([]map[string]any, 0, len(s.busy))
	// busy 键可能是项目作用域键（projectKey + session）或旧实例键（instance + session），
	// 不能统一反解出唯一 instance/workdir；运行态注册表 turns 保留了真实来源，优先用它。
	for key, turn := range s.busy {
		if sess := s.turns[key]; sess != nil {
			item := map[string]any{
				"session_id":  sess.SessionID(),
				"turn":        turn,
				"instance_id": sess.InstanceID(),
			}
			if tc, ok := sess.(*turnCtx); ok {
				if tc.req.WorkDir != "" {
					item["work_dir"] = tc.req.WorkDir
				}
				if tc.req.DataDir != "" {
					item["data_dir"] = tc.req.DataDir
				}
			}
			list = append(list, item)
			continue
		}
		// 无 turns 条目（旧调用方 / 仅 busy 注册）→ 按旧键拆分，字段口径不变。
		instanceID, session := splitInstKey(key)
		list = append(list, map[string]any{
			"session_id":  session,
			"turn":        turn,
			"instance_id": instanceID,
		})
	}
	s.mu.Unlock()
	v.Result = map[string]any{"list": list}
	return nil
}
