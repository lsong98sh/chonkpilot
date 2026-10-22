// chat 离线重放影子（MOD-1 A4-P2）：turn 结束后用**已落库消息 + 已落库工具结果**重跑 chat 引擎，
// 比对消息序列。**零网络 / 零工具执行 / 零 DB 写 / 零 MQ**，默认关闭。
//
// 开关 = 环境变量 `CHONKPILOT_CHAT_SHADOW=1`（未设 / 其他值 = 关闭 → 零调用，行为与现在逐字节一致）。
//
// 语义：
//   - 上下文 = 生产同一份 `chatContextProvider.Build`（已压缩历史 + **本轮已落库用户输入** +
//     系统提示词）；本轮 assistant / tool 由引擎重新生成，故不在上下文里；
//   - LLM = `replayLLM`：按 role=assistant 消息顺序脚本化 step 响应（不访问网络；调用序号超出
//     脚本 → 显式错误，不静默返回空回复）；
//   - 工具 = `replayToolInvoker`：按 tool_call_id 返回已落库结果（不执行任何工具；缺失 → 明确
//     失败结果）；
//   - 落库 = `replayStore`：只记内存（不写 DB、不发 MQ），初始种子 = 本轮已落库用户输入。
//
// 比对口径：生产消息（`LoadMessages`，created_at 升序，只取 role / content / tool_call_id /
// tool_calls；忽略 created_at / id / kind / brief）与重放记录逐项 JSON 比较，最多 5 条差异摘要
// （空 = 等价）。**已知边界（如实登记）**：P2 只重建「一次完整轮次」——本轮中途插话 / 「继续」
// 等多段用户输入在重放里统一前置（同轮继续由 A4-P3 处理）；`notify`（工具通知）不进重放基线。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chonkpilot/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// chatShadowEnv 是离线重放影子的开关环境变量（`=1` 开启；未设 / 其他值 = 关闭）。
const chatShadowEnv = "CHONKPILOT_CHAT_SHADOW"

// shadowReplayCalls 记录 `shadowReplayTurn` 的调用次数（测试钩子：断言 env 未设时零调用）。
var shadowReplayCalls atomic.Int64

// chatShadowEnabled 报告离线重放影子是否开启（默认关闭；每次现读环境变量，不缓存）。
func chatShadowEnabled() bool { return os.Getenv(chatShadowEnv) == "1" }

// shadowReplayTurn 离线重放：用已落库消息重建本轮并比对（零网络 / 零工具执行 / 零 DB 写）。
// 返回 report = 差异摘要（空 = 等价）；err 仅用于调用方留痕。默认关闭。
func (s *Server) shadowReplayTurn(instanceID, sessionID, turnID string) (string, error) {
	shadowReplayCalls.Add(1)
	if s == nil || s.bus == nil {
		return "", errors.New("chat shadow replay: server 未装配")
	}
	if instanceID == "" || sessionID == "" || turnID == "" {
		return "", errors.New("chat shadow replay: 缺 instance_id / session_id / turn_id")
	}
	st := newSessionStore(s.bus, instanceID)
	prod := st.LoadMessages(turnID)
	if len(prod) == 0 {
		return "", nil
	}
	llm := newReplayLLM(prod)
	// 种子 = 本轮已落库用户输入（与 `Build` 同一过滤口径）：引擎只写 assistant / tool，
	// 比对基线需含输入侧，否则序列恒少开头的 user 消息。
	store := newReplayStore(chatTurnUserInputs(st, turnID))
	eng := chat.New(chat.Deps{
		InstanceID: instanceID,
		SessionID:  sessionID,
		Context:    newChatContextProvider(s, "", ""),
		Store:      store,
		Sink:       replaySink{},
		Tools:      newReplayToolInvoker(prod),
		Catalog:    replayCatalog{},
		LLM:        llm,
		Clock:      chatSystemClock{},
		Config: chat.RuntimeConfig{
			MaxSteps:      llm.stepCount() + 2,
			EventLogLevel: chat.TurnEventLogOutline,
			AutoContinue:  false,
			PendingWait:   0,
		},
	})
	if _, err := eng.RunTurn(context.Background(), turnID); err != nil {
		return "", err
	}
	return shadowReplayDiff(prod, store.messages()), nil
}

// shadowReplayIfEnabled 是生产触发点（唯一）：turn 正常终态收尾后按环境变量决定是否异步重放。
// 未设 env → **直接返回（零调用）**；已开 → 后台 goroutine，panic 兜底、日志只经 `logf`、
// **不发布任何 MQ 消息**（失败只留痕，不影响生产轮次）。
func (tc *turnCtx) shadowReplayIfEnabled() {
	if tc == nil || tc.server == nil || !chatShadowEnabled() {
		return
	}
	instanceID, sessionID, turnID := tc.req.InstanceID, tc.req.Session, tc.req.Turn
	go func() {
		defer recoverLog("chat shadow replay")
		report, err := tc.server.shadowReplayTurn(instanceID, sessionID, turnID)
		if err != nil {
			logf("[chonkpilot-server] chat 影子重放失败（instance=%s session=%s turn=%s）: %v\n",
				instanceID, sessionID, turnID, err)
			return
		}
		if report != "" {
			logf("[chonkpilot-server] chat 影子重放有差异（instance=%s session=%s turn=%s）:\n%s\n",
				instanceID, sessionID, turnID, report)
			return
		}
		logf("[chonkpilot-server] chat 影子重放等价（instance=%s session=%s turn=%s）\n",
			instanceID, sessionID, turnID)
	}()
}

// ─── replayLLM：按已落库 assistant 消息脚本化 step ──────────────────────

// replayStep 是一条脚本化 assistant 响应。
type replayStep struct {
	text      string
	reasoning string
	calls     []chat.ToolCall
	finish    string
}

// replayLLM 是 `chat.LLMClient` 的离线实现：不发请求，按调用序号回放脚本。
type replayLLM struct {
	mu    sync.Mutex
	steps []replayStep
	next  int
}

// newReplayLLM 由已落库消息生成脚本：扫描 role=assistant（按 created_at 顺序），每条一 step；
// Finish = 有 tool_calls → "tool_calls"，否则 "stop"。
func newReplayLLM(msgs []ChatMsg) *replayLLM {
	steps := make([]replayStep, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "assistant" || (m.Content == "" && len(m.ToolCalls) == 0) {
			continue
		}
		finish := "stop"
		if len(m.ToolCalls) > 0 {
			finish = "tool_calls"
		}
		steps = append(steps, replayStep{
			text: m.Content, reasoning: m.Reasoning,
			calls: append([]chat.ToolCall(nil), m.ToolCalls...), finish: finish,
		})
	}
	return &replayLLM{steps: steps}
}

// stepCount 返回脚本条数（调用方据此上界 MaxSteps）。
func (l *replayLLM) stepCount() int {
	if l == nil {
		return 0
	}
	return len(l.steps)
}

// Step 返回下一次脚本响应（channel 语义与宿主 adapter 一致：发完即关）。
// 调用序号超出脚本 → Done.Err 显式错误（不静默返回空回复）。
func (l *replayLLM) Step(_ context.Context, _ chat.StepRequest, _ chat.StepOptions) (<-chan chat.StepEvent, error) {
	l.mu.Lock()
	idx := l.next
	l.next++
	l.mu.Unlock()

	out := make(chan chat.StepEvent, 4)
	if idx >= len(l.steps) {
		out <- chat.StepEvent{Type: chat.StepEventDone, Err: fmt.Errorf(
			"chat shadow replay: LLM 脚本已用尽（第 %d 次调用，脚本 %d 条）", idx+1, len(l.steps))}
		close(out)
		return out, nil
	}
	st := l.steps[idx]
	if st.reasoning != "" {
		out <- chat.StepEvent{Type: chat.StepEventReasoning, Text: st.reasoning}
	}
	// 引擎的正文只从 delta 累加（`Done.Text` 不参与拼接）→ 正文以一条 delta 发出；
	// Done 仍按契约携带同一份 Text（诊断 / 与宿主 adapter 同形）。
	if st.text != "" {
		out <- chat.StepEvent{Type: chat.StepEventDelta, Text: st.text}
	}
	out <- chat.StepEvent{Type: chat.StepEventDone, Text: st.text, ToolCalls: st.calls, Finish: st.finish}
	close(out)
	return out, nil
}

// ─── replayToolInvoker：按 tool_call_id 返回已落库结果 ────────────────────

// replayToolInvoker 是 `chat.ToolInvoker` 的离线实现：不执行任何工具，只查已落库结果。
type replayToolInvoker struct {
	results map[string]chat.ToolResult
}

// newReplayToolInvoker 由已落库 role=tool 消息建 tool_call_id → 结果映射。
func newReplayToolInvoker(msgs []ChatMsg) *replayToolInvoker {
	results := make(map[string]chat.ToolResult, len(msgs))
	for _, m := range msgs {
		if m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		results[m.ToolCallID] = replayToolResult(m.ToolCallID, m.Content)
	}
	return &replayToolInvoker{results: results}
}

// replayToolResult 还原一条已落库工具结果：
//   - `LoadMessages` 读侧已把 `{call,result,async}` 归一为 LLM 视角的裸结果文本 → **解析失败
//     即回退原始 content**（正常路径）；
//   - 若 content 仍为原始 JSON 形态 → 取 `result.content`，并按 `result.status`
//     （failed / cancelled / interrupted）判 `IsError`。
func replayToolResult(callID, content string) chat.ToolResult {
	res := chat.ToolResult{CallID: callID, Content: content}
	tc, ok := persist.ParseToolContent(content)
	if !ok || tc.Result == nil {
		return res
	}
	res.Content = tc.Result.Content
	switch tc.Result.Status {
	case "failed", "cancelled", "interrupted":
		res.IsError = true
	}
	return res
}

// Dispatch 返回映射结果；缺失 → `IsError=true` 的明确失败结果（不静默成功）。
func (t *replayToolInvoker) Dispatch(_ context.Context, call chat.ToolCall) (chat.ToolOutcome, error) {
	if res, ok := t.results[call.ID]; ok {
		return chat.ToolOutcome{Result: res}, nil
	}
	return chat.ToolOutcome{Result: chat.ToolResult{
		CallID:  call.ID,
		IsError: true,
		Content: fmt.Sprintf("错误: 离线重放缺失工具结果（tool_call_id=%s）", call.ID),
	}}, nil
}

// Cancel 离线重放无在飞执行 → 恒 false。
func (t *replayToolInvoker) Cancel(context.Context, string, string) (bool, error) {
	return false, nil
}

// Detach 离线重放无在飞执行 → 空 task_id。
func (t *replayToolInvoker) Detach(context.Context, string, string) (string, error) {
	return "", nil
}

// ─── replayStore：内存 TurnStore（不写 DB）─────────────────────────────

// replayStore 是 `chat.TurnStore` 的内存实现：消息 / 事件 / inbox 提交只记内存。
type replayStore struct {
	mu     sync.Mutex
	msgs   []chat.Message
	events []chat.TurnEvent
	inbox  []string
}

// newReplayStore 构造内存 store（seed = 比对基线的输入侧消息，见 shadowReplayTurn）。
func newReplayStore(seed []ChatMsg) *replayStore {
	msgs := make([]chat.Message, 0, len(seed))
	msgs = append(msgs, seed...)
	return &replayStore{msgs: msgs}
}

func (r *replayStore) SaveMessage(_ context.Context, msg chat.Message) error {
	r.appendMessage(msg)
	return nil
}

func (r *replayStore) CommitInbox(_ context.Context, inboxID string, msg chat.Message) error {
	r.mu.Lock()
	r.inbox = append(r.inbox, inboxID)
	r.mu.Unlock()
	r.appendMessage(msg)
	return nil
}

func (r *replayStore) AppendEvent(_ context.Context, ev chat.TurnEvent) error {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	return nil
}

// appendMessage 追加一条消息（ToolCalls 拷贝，避免与引擎共享底层数组）。
func (r *replayStore) appendMessage(msg chat.Message) {
	msg.ToolCalls = append([]chat.ToolCall(nil), msg.ToolCalls...)
	r.mu.Lock()
	r.msgs = append(r.msgs, msg)
	r.mu.Unlock()
}

// messages 返回已记录消息的快照（含 seed）。
func (r *replayStore) messages() []chat.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]chat.Message(nil), r.msgs...)
}

// ─── 空目录 / 空事件出口 ──────────────────────────────────────────────

// replayCatalog 是空工具目录（重放不依赖下发面）。
type replayCatalog struct{}

func (replayCatalog) Servers(context.Context, string) ([]chat.ServerInfo, error) { return nil, nil }
func (replayCatalog) Tools(context.Context, string) ([]chat.ToolSpec, error)     { return nil, nil }

// replaySink 是空事件出口（离线重放**不发任何 MQ 消息**）。
type replaySink struct{}

func (replaySink) Emit(context.Context, chat.Event) {}

// ─── 比对 ────────────────────────────────────────────────────────────

// shadowMaxDiffs 是差异摘要上限（与 `chat.Verify` 同口径：最多展示 5 处）。
const shadowMaxDiffs = 5

// shadowMsgView 是比对口径（只取 role / content / tool_call_id / tool_calls）。
type shadowMsgView struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []shadowCallView `json:"tool_calls,omitempty"`
}

// shadowCallView 是工具调用的比对口径（忽略 Context 等非协议字段）。
type shadowCallView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// shadowReplayDiff 比对生产消息与重放消息（逐项 JSON、顺序敏感），返回最多 5 条差异摘要；
// 空 = 等价。
func shadowReplayDiff(prod, replay []chat.Message) string {
	diffs := make([]string, 0, shadowMaxDiffs)
	n := len(prod)
	if len(replay) > n {
		n = len(replay)
	}
	for i := 0; i < n && len(diffs) < shadowMaxDiffs; i++ {
		switch {
		case i >= len(prod):
			diffs = append(diffs, fmt.Sprintf("重放多出第 %d 条：%s", i+1, shadowMessageJSON(replay[i])))
		case i >= len(replay):
			diffs = append(diffs, fmt.Sprintf("重放缺少第 %d 条：%s", i+1, shadowMessageJSON(prod[i])))
		default:
			want, got := shadowMessageJSON(prod[i]), shadowMessageJSON(replay[i])
			if want != got {
				diffs = append(diffs, fmt.Sprintf("第 %d 条不一致：生产=%s 重放=%s", i+1, want, got))
			}
		}
	}
	if len(prod) != len(replay) && len(diffs) < shadowMaxDiffs {
		diffs = append(diffs, fmt.Sprintf("消息条数不同：生产=%d 重放=%d", len(prod), len(replay)))
	}
	return strings.Join(diffs, "\n")
}

// shadowMessageJSON 把一条消息按比对口径编码（编码失败 → 退化打印，不 panic）。
func shadowMessageJSON(m chat.Message) string {
	v := shadowMsgView{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
	for _, c := range m.ToolCalls {
		v.ToolCalls = append(v.ToolCalls, shadowCallView{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(b)
}

// 编译期断言：离线重放的三个端口实现满足 chat 端口。
var (
	_ chat.LLMClient   = (*replayLLM)(nil)
	_ chat.ToolInvoker = (*replayToolInvoker)(nil)
	_ chat.TurnStore   = (*replayStore)(nil)
	_ chat.ToolCatalog = replayCatalog{}
	_ chat.EventSink   = replaySink{}
)
