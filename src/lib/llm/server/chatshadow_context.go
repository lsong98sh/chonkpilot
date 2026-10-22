// chat 上下文端口宿主适配器（MOD-1 M6d）：ContextProvider。
//
// 与 `chatshadow.go`（LLMClient / TurnStore / EventSink）、`chatshadow_tool.go`
// （ToolCatalog / ToolInvoker）同属宿主装配层：**本文件不改生产路径**——现有 `turnCtx`
// 的历史组装保持原样；本适配器供影子运行 / 后续切换使用。
//
// 归宿主的部分（chat 内核不读库、不压缩、不拼提示词）：
//   - 已压缩历史：`sessionStore.BuildContextTokens`（快照 + 最近 N 轮，`exclude_turn` 排除当前轮）；
//   - 系统提示词：`resolveTurnScenario`（全局 / 目录 / 场景 / agent / 会话注入五层）。
//
// 约定：
//   - `Build` 每轮（turn）只调用一次；同一轮内的后续 step 复用同一份上下文；
//   - `Messages` = 已压缩历史（`exclude_turn` 排除当前轮）+ **当前轮已落库的用户输入**
//     （A4-P2 修正：引擎要求「完整上下文含本轮用户输入」）；**不含**当前轮的 assistant / tool
//     消息——本轮由引擎重新生成（离线重放只重建一次完整轮次）；
//   - `includeSnapshot` 即生产 `newTurnCtx` 的 `!req.Continue`：同轮次继续时关闭快照分支，
//     `SetIncludeSnapshot(false)` 为其预留（当前无生产接线）；
//   - 生产「同轮次继续」（同轮内已存在 assistant / tool 消息）由宿主把既有 assistant / tool
//     也放进初始 `Messages`（A4-P3 处理）；P2 离线重放不覆盖该场景；
//   - `memoryGuide` / `assetGuide` 等 **per-step 注入** 仍由宿主后续步骤负责，本适配器不实现；
//   - 系统提示词只经 `chat.Context.SystemPrompt` 返回，**不重复塞进 `Messages`**（chat 负责装配）。
package server

import (
	"context"
	"errors"

	"github.com/chonkpilot/chonkpilot-chat"
)

// chatContextProvider 把宿主「读库 + 压缩 + 提示词拼装」接到 `chat.ContextProvider`。
type chatContextProvider struct {
	s          *Server
	scenarioID string
	agent      string
	// includeSnapshot = 是否走快照前缀（默认 true；同轮次继续 = false，见文件头）。
	includeSnapshot bool
}

// newChatContextProvider 构造上下文适配器（`scenarioID` / `agent` 为空 = 请求侧未指定，
// 由会话行固定参数补位，与生产 resolveTurnScenario 同口径）。
func newChatContextProvider(s *Server, scenarioID, agent string) *chatContextProvider {
	return &chatContextProvider{s: s, scenarioID: scenarioID, agent: agent, includeSnapshot: true}
}

// SetIncludeSnapshot 覆盖快照开关（同轮次继续语义：与生产 `!req.Continue` 同口径）。
func (p *chatContextProvider) SetIncludeSnapshot(on bool) {
	p.includeSnapshot = on
}

// Build 组装本轮已拼好的上下文：已压缩历史 + **当前轮已落库的用户输入** + 系统提示词。
//
// 历史经 `BuildContextTokens`（`exclude_turn` = 当前轮）取，随后按 created_at 顺序追加当前轮
// `role=user` 的用户输入（kind = text / insert / continue；`notify` 等工具通知不算用户输入）——
// 引擎要求 `Context.Messages` 是「完整上下文含本轮用户输入」，缺本轮用户输入会丢首步输入。
// 当前轮的 assistant / tool **不在此处**：它们由引擎在本轮重新生成（离线重放语义）。
func (p *chatContextProvider) Build(_ context.Context, req chat.ContextRequest) (chat.Context, error) {
	if p == nil || p.s == nil {
		return chat.Context{}, errors.New("chatContextProvider: server 未装配")
	}
	if req.InstanceID == "" {
		return chat.Context{}, errors.New("chatContextProvider: 缺 instance_id")
	}
	if req.SessionID == "" {
		return chat.Context{}, errors.New("chatContextProvider: 缺 session_id")
	}
	st := newSessionStore(p.s.bus, req.InstanceID)
	// exclude_turn = 当前轮：历史不含本轮；本轮的**用户输入**在下方单独追加（不重复注入）。
	hist, _ := st.BuildContextTokens(req.SessionID, req.TurnID, p.includeSnapshot)
	cur := chatTurnUserInputs(st, req.TurnID)
	msgs := make([]ChatMsg, 0, len(hist)+len(cur))
	msgs = append(msgs, hist...)
	msgs = append(msgs, cur...)
	sc := p.s.resolveTurnScenario(req.InstanceID, req.SessionID, p.scenarioID, p.agent)
	return chat.Context{SystemPrompt: sc.sysPrompt, Messages: msgs}, nil
}

// chatTurnUserInputs 读当前轮**已落库的用户输入**（created_at 升序）：`role=user` 且
// kind ∈ {"", text, insert, continue}。`notify`（工具通知）与 assistant / tool 不计入。
//
// 供 `Build`（上下文组装）与离线重放（A4-P2 的比对基线）共用同一过滤口径。
func chatTurnUserInputs(st *sessionStore, turnID string) []ChatMsg {
	if st == nil || turnID == "" {
		return nil
	}
	all := st.LoadMessages(turnID)
	out := make([]ChatMsg, 0, len(all))
	for _, m := range all {
		if m.Role != "user" {
			continue
		}
		switch m.Kind {
		case "", "text", "insert", "continue":
			out = append(out, m)
		}
	}
	return out
}

// 编译期断言：上下文适配器满足 chat 端口。
var _ chat.ContextProvider = (*chatContextProvider)(nil)
