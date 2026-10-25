// 压缩内建（SESSION-1 阶段一/二）：进程内、异步、复用「刚发出的 payload」——
// 不再走 UDS 插件（原 `chonkpilot-plugin-compress.exe` 形态已删除）。
//
// SESSION-1 阶段二：纯计算部分（payload 构造 / 阈值判定 / CAS / 快照消息构造 / 一次压缩编排）
// 已下沉到纯 lib 组件 `chonkpilot-session`（`src/lib/chonkpilot-session/compress.go`）；本文件只剩**宿主
// 适配**——单飞调度、退避重试、日志文案、提示词读取、摘要调用（`s.chat`）与快照读写端口。
//
// 语义（用户 2026-10-23 定案）：
//  1. **触发**：本轮组装完成（`Assembly.Usage` 已算出）→ 正常请求照常发出（用户无感）→
//     若达阈值则后台触发压缩。阈值沿用现有配置键（`keep_full_max_tokens` +
//     `compress_token_threshold`，读序 prjusr → prj → usr），**不新增配置键**。
//  2. **输入 = 刚发出的 payload 复用**：取 `Assembly.Context.Messages`，**去掉全部 system 角色
//     消息**（含系统提示词 / 记忆指引 / 资产指引 / 既有摘要），把**最后一条 user** 换成压缩要求
//     （要求文本读 `capability/system/summary.md`）→ 走进程内单轮 LLM（`llmOnceMessages`，
//     不新开 HTTP 通道）。
//  3. **回填**：摘要 → 写会话快照 `[system 摘要] + 保留段`、`snapshot_turn = Assembly.LastTurnID`；
//     **CAS**：仅当快照仍停在压缩基准时才写，否则丢弃（已有别的压缩 / 新一轮推进过快照）。
//  4. **四约束**：① 同会话同一时刻只允许一个压缩（单飞）；② 快照 + snapshot_turn 单事务写
//     （一次 `SnapshotSet` = 同一条记录）、`snapshot_turn` 单调不减；③ 组装只读**已提交**快照
//     （组装走 `BuildContextTokens`，本文件不参与读路径）；④ 压缩失败不影响主流程（原文保留、
//     只记日志、按退避重试）。
//  5. **兜底**：正常请求因**超窗**失败 → 用同一份 payload 立刻压缩后**重试一次**（见
//     `compressOnOverflow` / `engineStepLLM.Step`）。
//  6. **进度通知**：后台压缩在**既有**通知面 `tool-notify` 发 `compress-queued` / `compress-start` /
//     `compress-done`（与原 compress 插件同形，前端零改动）；失败另发 `plugin-failure`。
//
// 零新增 MQ 主题：进度/失败均复用既有 `tool-notify`（61-消息一览 §4.3）。
package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
	"github.com/lsong98sh/chonkpilot-router"
	"github.com/lsong98sh/chonkpilot-session"
)

const (
	// 压缩进度通知取值（既有通知面 tool-notify；与原 compress 插件逐字一致，见 61-消息一览 §4.3）：
	// 入队（排队）/ 开始前 / 结束后各一次，供前端显示会话级压缩指示 + 队列状态。
	noticeCompressQueued = "compress-queued"
	noticeCompressStart  = "compress-start"
	noticeCompressDone   = "compress-done"
)

const (
	// compressRetryMax 压缩失败后的重试次数（不含首次；退避 = attempt × compressRetryBackoff）。
	compressRetryMax = 2
	// compressRetryBackoff 退避基数（线性递增：2s / 4s）。
	compressRetryBackoff = 2 * time.Second
	// compressTimeout 单次摘要调用的外层超时（无上下文单轮 LLM 的压缩上限）。
	compressTimeout = 5 * time.Minute
	// llmCompressSubsystemKey 压缩子系统默认 LLM（usr；缺省回落 defaultLLM，由数据层读侧完成）。
	llmCompressSubsystemKey = "llm.compress"
	// compressedSummaryPrefix 摘要消息前缀（与既有压缩快照同形；唯一来源 = 组件）。
	compressedSummaryPrefix = session.SummaryPrefix
	// summarySystemDocKind 摘要提示词的出厂文档 kind（`capability/system/summary.md`）。
	summarySystemDocKind = "summary"
)

// compressJob 是一次压缩的输入（全部来自组装结果，压缩期间不再读消息表）。
// 类型定义在组件包（`session.CompressionJob`），此处别名保持宿主调用点不变。
type compressJob = session.CompressionJob

// maybeStartCompress 组装完成后的**异步**触发点（非阻塞；不等待结果、不影响主流程）。
//
// 门控：
//   - 无已完成轮（首轮）→ 不压缩（无回填锚点）；
//   - 未达阈值 → 不压缩；
//   - 子会话 + 项目未开「压缩子会话」（`compress.subsession`，默认关）→ 不压缩（既有口径）。
func (tc *turnCtx) maybeStartCompress(a session.Result) {
	if tc == nil || tc.server == nil || a.LastTurnID == "" {
		return
	}
	if !session.Due(a.Usage, tc.keepFullTokens, tc.briefBudget) {
		return
	}
	if tc.isSubsession() && !tc.server.compressSubsessionOn(tc.req.InstanceID) {
		return
	}
	tc.server.startCompress(session.NewJob(a, tc.req.InstanceID, tc.req.Session, tc.req.Turn))
}

// startCompress 后台起一次压缩（**每会话串行 + 覆盖合并队列**）。
//
// 返回 true = 本次立即起 worker；false = 同会话已有任务在飞 → 本次进 compressPending（同会话
// 多次触发**只留最新**，由该会话 worker 依次消费）并广播 `compress-queued`。
func (s *Server) startCompress(job compressJob) bool {
	if s == nil || job.SessionID == "" {
		return false
	}
	key := instKey(job.InstanceID, job.SessionID)
	s.compressMu.Lock()
	if s.compressRunning == nil {
		s.compressRunning = map[string]bool{}
	}
	if s.compressRunning[key] {
		if s.compressPending == nil {
			s.compressPending = map[string]compressJob{}
		}
		s.compressPending[key] = job // 覆盖合并：同会话只留最新一次
		s.compressMu.Unlock()
		s.emitCompressNotice(job, noticeCompressQueued, "压缩排队中…")
		return false
	}
	s.compressRunning[key] = true
	s.compressMu.Unlock()
	go s.compressWorker(key, job)
	return true
}

// compressWorker 按会话串行消费压缩任务：压缩一个 → 取下一个待处理（覆盖合并）→ 直到队列空。
//
// 三个时点各广播一次**既有**通知面 `tool-notify`（61-消息一览 §4.3；不新增主题）：
// `compress-start`（每次开始前）/ `compress-done`（每次结束后，失败也发 → 终态必有）；
// `compress-queued` 由 startCompress 在入队时发。失败另经 plugin-failure 上报（既有失败字段）。
func (s *Server) compressWorker(key string, job compressJob) {
	for {
		s.emitCompressNotice(job, noticeCompressStart, "正在压缩上下文…")
		if err := s.compressWithRetry(context.Background(), job); err != nil {
			logf("[chonkpilot-server] compress: 失败（session=%s turn=%s）: %v\n", job.SessionID, job.TurnID, err)
			// 失败的用户可见面：沿用既有 `plugin-failure`（plugin=compress，61 §4.3），前端据此落
			// 错误态；随后仍发 `compress-done`（前端「失败优先」→ done 不覆盖错误态）。
			s.pluginNotify(hook.Notice{
				Plugin: "compress", Kind: "compress", Reason: err.Error(),
				InstanceID: job.InstanceID, Session: job.SessionID, Turn: job.TurnID,
			})
		}
		s.emitCompressNotice(job, noticeCompressDone, "上下文压缩完成")
		s.compressMu.Lock()
		next, ok := s.compressPending[key]
		if ok {
			delete(s.compressPending, key)
			s.compressMu.Unlock()
			job = next // 串行消费下一项（同会话不并发、不重入）
			continue
		}
		delete(s.compressRunning, key)
		s.compressMu.Unlock()
		return
	}
}

// emitCompressNotice 经**既有通知面** tool-notify（61-消息一览 §4.3；不新增主题）投递压缩队列
// 进度（`compress-queued` / `compress-start` / `compress-done`），字段形状沿用原 compress 插件
// （前端 `useCompressStatus` / `useQueueStatus` 无需改动即可消费）。
//
// turn_id = 压缩基准（`Anchor` = 快照将被写到的轮；空则回落当前轮），与原插件 `snapshot_turn` 同义；
// message_id 前缀 / 拼法与原插件一致（前端按 message_id 幂等去重）。实例字段必带（61 §0）。
func (s *Server) emitCompressNotice(job compressJob, notice, message string) {
	if s == nil || s.bus == nil || job.InstanceID == "" {
		return
	}
	turn := job.Anchor
	if turn == "" {
		turn = job.TurnID
	}
	s.publish(msgkeys.TopicToolNotify, map[string]any{
		"instance_id": job.InstanceID,
		"session_id":  job.SessionID,
		"turn_id":     turn,
		"notice":      notice,
		"message":     message,
		"message_id":  "msg-compress-" + notice + "-" + job.SessionID + "-" + turn,
		"plugin":      "compress",
	})
}

// compressWithRetry 执行压缩并按退避重试（失败不阻断主流程；`ok=false` = 无变更/已丢弃，不算失败）。
func (s *Server) compressWithRetry(ctx context.Context, job compressJob) error {
	var lastErr error
	for attempt := 0; attempt <= compressRetryMax; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(time.Duration(attempt) * compressRetryBackoff):
			}
		}
		if _, err := s.compressOnce(ctx, job); err == nil {
			return nil
		} else {
			lastErr = err
			logf("[chonkpilot-server] compress: 第 %d 次失败（session=%s）: %v\n", attempt+1, job.SessionID, err)
		}
	}
	return lastErr
}

// compressOnce 一次完整压缩：组装 payload → 摘要 → CAS 回填快照（实现在组件）。
// 返回 ok=true 表示已写入快照；ok=false = 无需压缩 / CAS 丢弃（均非错误）。
func (s *Server) compressOnce(ctx context.Context, job compressJob) (bool, error) {
	if s.cfg == nil || job.Anchor == "" {
		return false, nil
	}
	requirement := s.summaryPrompt(job.InstanceID)
	if requirement == "" {
		return false, errors.New("摘要提示词为空（capability/system/summary.md）")
	}
	res, err := session.CompressOnce(ctx, s.snapshotStore(job),
		compressSummarizer{s: s, instanceID: job.InstanceID}, job, requirement)
	if err != nil {
		return false, err
	}
	return logCompressCommit(job, res), nil
}

// commitCompressed 把摘要写回会话快照（**单事务**：摘要 + snapshot_turn 同一条记录）。
//
// CAS：仅当当前快照的 `snapshot_turn` 仍等于压缩基准（`Assembly.LastTurnID`）时才写；
// 已推进（别的压缩 / 新一轮 finish 写了快照）→ 丢弃本次结果（不反查"该写到哪个 turn"）。
func (s *Server) commitCompressed(ctx context.Context, job compressJob, summary string) (bool, error) {
	res, err := session.Commit(ctx, s.snapshotStore(job), job, summary)
	if err != nil {
		return false, err
	}
	return logCompressCommit(job, res), nil
}

// logCompressCommit 按既有文案记录回填结果（CAS 丢弃 / 已压缩），返回是否写入快照。
func logCompressCommit(job compressJob, res session.CommitResult) bool {
	if res.CASMiss {
		logf("[chonkpilot-server] compress: CAS 丢弃（session=%s snapshot_turn=%q anchor=%q）\n",
			job.SessionID, res.Snapshot.Turn, job.Anchor)
		return false
	}
	if res.Written {
		logf("[chonkpilot-server] compress: 已压缩（session=%s snapshot_turn=%s，保留 %d 条）\n",
			job.SessionID, job.Anchor, res.Messages-1)
	}
	return res.Written
}

// compressOnOverflow 超窗兜底：用**同一份 payload** 立刻压缩一次（同步）。
// 返回 true = 压缩已写入快照（调用方据此重新读快照组装并重试一次）。
func (tc *turnCtx) compressOnOverflow(ctx context.Context, payload []ChatMsg) bool {
	if tc == nil || tc.server == nil {
		return false
	}
	a := tc.buildAssembly()
	if a.LastTurnID == "" || len(payload) == 0 {
		return false
	}
	job := session.NewJob(a, tc.req.InstanceID, tc.req.Session, tc.req.Turn)
	job.Payload = payload
	ok, err := tc.server.compressOnce(ctx, job) // 单飞：同会话后台任务在飞时这里仍会跑，
	// 但 CAS 保证只有一份结果能落快照（另一份被丢弃），不产生重复压缩。
	if err != nil {
		logf("[chonkpilot-server] compress: 超窗兜底失败（session=%s）: %v\n", tc.req.Session, err)
		return false
	}
	return ok
}

// refreshHistoryFromSnapshot 重新读**已提交**快照，刷新本轮历史（hist / turnTokens）：
// 压缩回填后（超窗兜底重试前）必须重读，否则仍用轮次开始时的旧历史 → 重试仍超窗。
// 读不到（nil）→ 保持不变（保守）。
func (tc *turnCtx) refreshHistoryFromSnapshot() {
	if tc == nil || tc.server == nil {
		return
	}
	hist, tokens := newSessionStore(tc.server.bus, tc.req.InstanceID).
		BuildContextTokens(tc.req.Session, tc.req.Turn, true)
	if hist == nil {
		return
	}
	if tc.sysPromptText != "" {
		hist = append([]ChatMsg{{Role: "system", Content: tc.sysPromptText}}, hist...)
	}
	tc.hist = hist
	tc.turnTokens = tokens
}

// snapshotWriteBase 返回终态写快照的**基**（已提交快照消息）——
// 与本轮消息拼接后写入（`snapshot_turn` 仍 = 本轮）。
//
// 存在理由（SESSION-1）：压缩内建在**本轮进行中**回填快照；若终态仍用轮次开始时的 `tc.hist`
// 覆盖，压缩结果会被立刻抹掉。故仅当「当前快照的覆盖轮 == `tc.hist` 最后一个轮」（= 同一批
// 历史、压缩不改这个 id）时以已提交快照为基；其余（无快照 / 同轮继续 / 不衔接）→ false（回落
// 改动前的 `tc.hist + cur`，行为不变）。
func (s *Server) snapshotWriteBase(tc *turnCtx) ([]ChatMsg, bool) {
	if s.cfg == nil || tc == nil || tc.req.Continue || len(tc.turnTokens) == 0 {
		return nil, false
	}
	last := tc.turnTokens[len(tc.turnTokens)-1].TurnID
	if last == "" {
		return nil, false
	}
	resp, err := s.cfg.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: tc.req.InstanceID, SessionID: tc.req.Session, Scope: s.cfgScope(tc.req.InstanceID),
		DialogID: s.dialogOf(tc.req.InstanceID, tc.req.Session),
	})
	if err != nil || !resp.Found || resp.Snapshot.Turn != last {
		return nil, false
	}
	return chatMessagesFromFacade(resp.Snapshot.Messages), true
}

// isContextOverflow 判定错误是否为「上下文超窗」（厂商文案差异大，按关键词匹配）。
func isContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, k := range []string{
		"context length", "context_length_exceeded", "context window", "maximum context",
		"too many tokens", "prompt is too long", "input is too long", "request too large",
		"reduce the length", "exceeds the maximum",
	} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// llmOnceMessages 进程内单轮 LLM 调用（无 session/turn 上下文；压缩摘要用）。
// 与 `llmOnceSpec` 同一出网路径（`s.chat`），区别只在入参是**完整消息列表**（payload 复用）。
func (s *Server) llmOnceMessages(ctx context.Context, spec router.Spec, msgs []ChatMsg) (string, error) {
	if s.rt == nil {
		return "", errors.New("llm not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, compressTimeout)
	defer cancel()
	events, err := s.chat(cctx, spec, msgs, nil, ChatOptions{})
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

// compressSpec 压缩子系统的 provider（usr `llm.compress`，缺省回落 defaultLLM → exe flags 默认）。
// 每次压缩现读配置 → 配置改动热生效（与既有 SL-3 口径一致）。
func (s *Server) compressSpec(instanceID string) router.Spec {
	cfg, err := s.loadUserConfig(instanceID)
	if err != nil {
		logf("[chonkpilot-server] compress: 读 usr 配置失败（instance=%s）: %v\n", instanceID, err)
	}
	ref := data.LLMRefName(cfg[llmCompressSubsystemKey], cfg["llms"])
	return s.specFor(providerFromConfig(cfg, ref))
}

// summaryPrompt 读压缩要求文本（`capability/system/summary.md`，纯文本）：
// 项目级 → 用户级 → 系统级 → embed 出厂内置（`data.SystemDoc`，同一份出厂内容）。
// 每次压缩读文件（改提示词后下一次压缩即生效）。
func (s *Server) summaryPrompt(instanceID string) string {
	workDir := ""
	if s != nil && s.im != nil {
		if info, ok := s.im.Lookup(instanceID); ok {
			workDir = info.WorkDir
		}
	}
	for _, p := range summaryPromptPaths(workDir) {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if content := strings.TrimSpace(string(raw)); content != "" {
			return s.replaceToolchain(instanceID, content)
		}
	}
	return s.replaceToolchain(instanceID, strings.TrimSpace(data.SystemDoc(summarySystemDocKind)))
}

// summaryPromptPaths 摘要提示词按级 fallback 路径：项目级 → 用户级 → 系统级（磁盘）。
func summaryPromptPaths(workDir string) []string {
	var out []string
	if workDir != "" {
		out = append(out, filepath.Join(workDir, ".chonkpilot", "capability", "system", "summary.md"))
	}
	out = append(out, filepath.Join(filepath.Dir(data.UserPath()), "capability", "system", "summary.md"))
	if dir, err := exedir.Dir(); err == nil {
		out = append(out, filepath.Join(dir, "capability", "system", "summary.md"))
	}
	return out
}

// ─── 端口实现（压缩）────────────────────────────────────────────

// compressSnapshotStore 是 `session.SnapshotStore` 的宿主实现（门面 SnapshotGet / SnapshotSet）。
type compressSnapshotStore struct {
	s          *Server
	instanceID string
	sessionID  string
}

// snapshotStore 按任务范围构造快照端口（读写同一会话）。
func (s *Server) snapshotStore(job compressJob) session.SnapshotStore {
	return compressSnapshotStore{s: s, instanceID: job.InstanceID, sessionID: job.SessionID}
}

func (st compressSnapshotStore) Current(context.Context) (session.Snapshot, error) {
	resp, err := st.s.cfg.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: st.instanceID, SessionID: st.sessionID, Scope: st.s.cfgScope(st.instanceID),
		DialogID: st.s.dialogOf(st.instanceID, st.sessionID),
	})
	if err != nil {
		return session.Snapshot{}, err
	}
	if !resp.Found {
		return session.Snapshot{}, nil // 无快照 → 写侧保守丢弃
	}
	return session.Snapshot{
		Found:    true,
		Turn:     resp.Snapshot.Turn,
		Messages: chatMessagesFromFacade(resp.Snapshot.Messages),
	}, nil
}

func (st compressSnapshotStore) Commit(_ context.Context, snap session.Snapshot) error {
	_, err := st.s.cfg.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: st.instanceID,
		DialogID:   st.s.dialogOf(st.instanceID, st.sessionID),
		Snapshot: facade.Snapshot{
			SessionID: st.sessionID, Turn: snap.Turn, Messages: facadeMessagesFromChat(snap.Messages),
		},
		Scope: st.s.cfgScope(st.instanceID),
	})
	return err
}

// compressSummarizer 是 `session.Summarizer` 的宿主实现（进程内单轮 LLM，复用 `s.chat`）。
type compressSummarizer struct {
	s          *Server
	instanceID string
}

func (c compressSummarizer) Summarize(ctx context.Context, msgs []ChatMsg) (string, error) {
	return c.s.llmOnceMessages(ctx, c.s.compressSpec(c.instanceID), msgs)
}
