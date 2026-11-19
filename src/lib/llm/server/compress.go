// 上下文压缩（四步策略 · 同步版，2026-11-14 取代旧后台异步压缩）：
//
// 组装路径（`assembly.go:buildAssembly`）在总输入超阈值时同步执行：
//
//	2.2 简化（历史轮去 reasoning / tool_calls / tool 结果）
//	2.3 压缩（历史 → 摘要；进程内单轮 LLM，同步等待）
//	2.4 报错（压缩后仍超阈值 / 无历史轮可压缩）
//
// 本文件保留宿主适配：
//   - 压缩前 `memory.flush`（跟随压缩的既有口径，不新增消息面）；
//   - 压缩进度通知（既有 `tool-notify`：compress-start / compress-done；失败另发 plugin-failure）；
//   - 摘要调用（`llmOnceMessages`，5 分钟上限、随本轮 ctx 取消）；
//   - 提示词读取（`capability/system/summary.md`，项目级 → 用户级 → 系统级 → embed）；
//   - 快照读写端口（门面 SnapshotGet / SnapshotSet，CAS 写 `[system 摘要]`）；
//   - 厂商超窗兜底（`compressOnOverflow`：同一压缩函数，同步压缩后重试一次）。
//
// **不再有**后台压缩队列 / 单飞 / 终态后启动 / 退避重试（同步路径天然串行，失败直接进入
// 本轮错误终态，不会与主流程竞争快照）。
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
	"github.com/lsong98sh/chonkpilot-agent/router"
	"github.com/lsong98sh/chonkpilot-agent/session"
	"github.com/lsong98sh/chonkpilot-framework/host/core/exedir"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

const (
	// 压缩进度通知取值（既有通知面 tool-notify；字段形状沿用原 compress 插件，前端零改动）：
	// 同步压缩只发 start / done（无排队语义）。
	noticeCompressStart = "compress-start"
	noticeCompressDone  = "compress-done"
)

const (
	// compressTimeout 单次摘要调用的外层超时（无上下文单轮 LLM 的压缩上限）。
	compressTimeout = 5 * time.Minute
	// llmCompressSubsystemKey 压缩子系统默认 LLM（usr；缺省回落 defaultLLM，由数据层读侧完成）。
	llmCompressSubsystemKey = "llm.compress"
	// compressedSummaryPrefix 摘要消息前缀（与既有压缩快照同形；唯一来源 = 组件）。
	compressedSummaryPrefix = session.SummaryPrefix
	// summarySystemDocKind 摘要提示词的出厂文档 kind（`capability/system/summary.md`）。
	summarySystemDocKind = "summary"
)

// compressJob 是一次压缩的身份（进度通知用）。
// 类型定义在组件包（`session.CompressionJob`），此处别名保持宿主调用点不变。
type compressJob = session.CompressionJob

// flushMemoryBeforeCompress 在压缩前用**既有** `memory.flush` 主题触发一次记忆沉淀
// （2026-10-31 用户定案：提取时机跟随压缩 —— 阈值判定通过后、执行压缩前；不新增消息面）。
//
// payload 与手动沉淀同形（`instance_id` / `session` / `dialog_id`，61 §1.1）；memory 插件侧
// force=true（跳过最小轮次 token 门控），范围 = 各类别进度 → 最新轮。投递即回（插件入队后
// 立即应答），故异步发出：不阻塞本轮组装、不影响压缩与主流程；失败只记日志。
// dialog_id 缺失（无法定位会话库，61 §0.6）→ 不发（插件侧亦会以 dialog_required 拒绝）。
func (s *Server) flushMemoryBeforeCompress(instanceID, session string) {
	if s == nil || s.bus == nil || instanceID == "" || session == "" {
		return
	}
	dialogID := s.dialogOf(instanceID, session)
	if dialogID == "" {
		logf("[chonkpilot-server] memory: 压缩前沉淀跳过（session=%s 无 dialog_id）\n", session)
		return
	}
	payload := map[string]any{
		msgkeys.MemoryFlushPayloadInstanceId: instanceID,
		msgkeys.MemoryFlushPayloadSession:    session,
		msgkeys.MemoryFlushPayloadDialogId:   dialogID,
	}
	go func() {
		if err := s.bus.Emit(context.Background(), msgkeys.TopicMemoryFlush, payload).Wait().Err(); err != nil {
			logf("[chonkpilot-server] memory: 压缩前沉淀触发失败（session=%s）: %v\n", session, err)
		}
	}()
}

// emitCompressNotice 经**既有通知面** tool-notify（61-消息一览 §4.3；不新增主题）投递压缩进度
// （`compress-start` / `compress-done`），字段形状沿用原 compress 插件（前端 `useCompressStatus` /
// `useQueueStatus` 无需改动即可消费）。
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
	payload := map[string]any{
		"instance_id": job.InstanceID,
		"session_id":  job.SessionID,
		"turn_id":     turn,
		"notice":      notice,
		"message":     message,
		"message_id":  "msg-compress-" + notice + "-" + job.SessionID + "-" + turn,
		"plugin":      "compress",
	}
	s.publish(msgkeys.TopicToolNotify, s.addScopeToEvent(payload, job.InstanceID, job.SessionID, turn))
}

// compressOnOverflow 厂商超窗兜底：用**同一压缩函数** 立刻同步压缩一次（忽略阈值判定）。
// 返回 true = 压缩成功（本轮历史已替换为摘要，调用方重新组装后重试一次）。
//
// 每轮最多一次（与 2.3 共用 `compressAttempted`）；失败只记日志（主流程仍返回厂商原错误）。
func (tc *turnCtx) compressOnOverflow(ctx context.Context) bool {
	if tc == nil || tc.server == nil || tc.compressAttempted {
		return false
	}
	full, err := tc.buildAssembly()
	if err != nil {
		logf("[chonkpilot-server] compress: 超窗兜底前组装失败（session=%s）: %v\n", tc.req.Session, err)
		return false
	}
	// 组装本身可能已命中 2.3（估算口径低估真实总量）→ 已压缩，直接重试。
	if tc.compressed {
		return true
	}
	if tc.compressAttempted {
		return false
	}
	if full.LastTurnID == "" {
		return false
	}
	tc.compressAttempted = true
	summary, err := tc.compressHistory(ctx, full)
	if err != nil {
		logf("[chonkpilot-server] compress: 超窗兜底失败（session=%s）: %v\n", tc.req.Session, err)
		return false
	}
	tc.hist = []ChatMsg{summary}
	tc.turnTokens = nil
	tc.simplified, tc.simplifiedFrom, tc.compressed = false, "", true
	return true
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

// snapshotWriteBase 返回终态写快照的**基**（已提交快照消息）——
// 与本轮消息拼接后写入（`snapshot_turn` 仍 = 本轮）。
//
// 存在理由：压缩可能在本轮进行中回填快照；若终态仍用轮次开始时的 `tc.hist` 覆盖，压缩结果
// 会被抹掉。故仅当「当前快照的覆盖轮 == `tc.hist` 最后一个轮」时以已提交快照为基；其余
// （无快照 / 同轮继续 / 不衔接）→ false（回落 `tc.hist + cur`）。
//
// 四步策略（2026-11-14）：压缩成功后本轮 `tc.hist` 已替换为摘要、`tc.turnTokens` 清空 →
// 此处恒回落 `tc.hist + cur`（= 摘要 + 本轮消息），正是期望行为。
func (s *Server) snapshotWriteBase(tc *turnCtx) ([]ChatMsg, bool) {
	if s.cfg == nil || tc == nil || tc.req.Continue || len(tc.turnTokens) == 0 {
		return nil, false
	}
	last := tc.turnTokens[len(tc.turnTokens)-1].TurnID
	if last == "" {
		return nil, false
	}
	resp, err := s.cfg.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: tc.req.InstanceID, SessionID: tc.req.Session, Scope: tc.scope(),
		DialogID: s.dialogOf(tc.req.InstanceID, tc.req.Session, tc.scope()),
	})
	if err != nil || !resp.Found || resp.Snapshot.Turn != last {
		return nil, false
	}
	return chatMessagesFromFacade(resp.Snapshot.Messages), true
}

// ─── 端口实现（压缩）────────────────────────────────────────────

// compressSnapshotStore 是 `session.SnapshotStore` 的宿主实现（门面 SnapshotGet / SnapshotSet）。
type compressSnapshotStore struct {
	s          *Server
	instanceID string
	sessionID  string
	scope      facade.Scope
}

// snapshotStoreFor 按实例 / 会话构造快照端口（读写同一会话）。
func (s *Server) snapshotStoreFor(instanceID, sessionID string) session.SnapshotStore {
	return compressSnapshotStore{
		s: s, instanceID: instanceID, sessionID: sessionID,
		scope: s.scopeForSession(instanceID, sessionID),
	}
}

func (st compressSnapshotStore) Current(context.Context) (session.Snapshot, error) {
	resp, err := st.s.cfg.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: st.instanceID, SessionID: st.sessionID, Scope: st.scope,
		DialogID: st.s.dialogOf(st.instanceID, st.sessionID, st.scope),
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
		DialogID:   st.s.dialogOf(st.instanceID, st.sessionID, st.scope),
		Snapshot: facade.Snapshot{
			SessionID: st.sessionID, Turn: snap.Turn, Messages: facadeMessagesFromChat(snap.Messages),
		},
		Scope: st.scope,
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
