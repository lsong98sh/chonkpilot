// chat 工具端口宿主适配器（MOD-1 M5b-②）：ToolCatalog / ToolInvoker。
//
// 与 `chatshadow.go`（LLMClient / TurnStore / EventSink）同属宿主装配层：
// **本文件不改生产路径**——现有 `turnCtx` 的工具循环保持原样；本适配器供影子运行 /
// 后续切换使用。策略类判断（白名单 / hot / denyTools / 缺省强制异步）仍归宿主，通过钩子注入；
// chat 内核只拿到「已过滤的下发面」与「已定好的执行结果」。
package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-chat"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// ─── ToolCatalog ─────────────────────────────────────────────────────

// chatToolCatalog 把宿主工具目录接到 `chat.ToolCatalog`。
//
// `list(instanceID)` 必须返回**宿主已过滤后的下发面**（hot 过滤 / agent 白名单 /
// denyTools 与生产 `turnCtx.llmTools` 同口径）；chat 不认识这些策略。
type chatToolCatalog struct {
	s    *Server
	list func(instanceID string) []ToolDef
}

// newChatToolCatalog 构造目录适配器（list 为 nil → 空目录）。
func newChatToolCatalog(s *Server, list func(instanceID string) []ToolDef) chat.ToolCatalog {
	return &chatToolCatalog{s: s, list: list}
}

// Servers 返回服务目录视图。MCP 服务列表是管理面 / UI 关注点，不在 chat 端口的
// 会话语义内（chat 只用 Tools），故恒空——不是遗漏。
func (c *chatToolCatalog) Servers(context.Context, string) ([]chat.ServerInfo, error) {
	return nil, nil
}

// Tools 把 ToolDef 转成 chat.ToolSpec（Parameters → InputSchema JSON 原文）。
func (c *chatToolCatalog) Tools(_ context.Context, instanceID string) ([]chat.ToolSpec, error) {
	if c == nil || c.list == nil {
		return nil, nil
	}
	defs := c.list(instanceID)
	out := make([]chat.ToolSpec, 0, len(defs))
	for _, d := range defs {
		spec := chat.ToolSpec{Name: d.Name, Description: d.Description, Meta: d.Meta}
		if d.Parameters != nil {
			if b, err := json.Marshal(d.Parameters); err == nil {
				spec.InputSchema = b
			}
		}
		out = append(out, spec)
	}
	return out, nil
}

// ─── ToolInvoker ─────────────────────────────────────────────────────

// chatToolInvoker 把 `chat.ToolInvoker` 接到 gateway 调用面 / ask 通道 / 执行池控制面。
//
// 一次 turn 绑一个实例（workDir / dataDir / topSession / parent 由构造方给定）：
//   - Dispatch：gateway `mcp-tools-call` → 同步结果；`structuredContent.status=pending`
//     或 ask_user → `Pending{OpID}`（何时回填、如何续轮归宿主，chat 只挂起）；
//   - Cancel / Detach：执行池控制面（`execSink`），op 句柄 = gateway task_id。
type chatToolInvoker struct {
	s          *Server
	instanceID string
	sessionID  string
	turnID     string
	workDir    string
	dataDir    string
	topSession string
	parent     string

	// args 可选：调用级参数改写（生产 = `turnCtx.gatewayArgs`：缺省强制 `async=always`，
	// 进程内编排工具与契约声明超时裁决点的工具除外；见 turn.go gatewayArgs）。
	args func(name string, args map[string]any) map[string]any
	// policy 可选：分发前硬拒（生产 = `toolAllowed` 白名单 + `mcp_invoke` 旁路加固）；
	// 返回非空文本 = 拒绝并把该文本作为失败结果回喂（不落 gateway）。
	policy func(name string, args map[string]any) string
	// ask 可选：ask_user 通道（生产 = `Server.ask`）；返回 opID（ask_id）。
	// nil = 不特判，按普通工具下发 gateway。
	ask func(call chat.ToolCall, args map[string]any) string
}

// newChatToolInvoker 构造工具执行适配器（策略钩子随后按需赋值）。
func newChatToolInvoker(s *Server, instanceID, sessionID, turnID, workDir, dataDir string) *chatToolInvoker {
	return &chatToolInvoker{
		s: s, instanceID: instanceID, sessionID: sessionID, turnID: turnID,
		workDir: workDir, dataDir: dataDir,
	}
}

// Dispatch 执行一次工具调用。工具级失败（含策略拒绝 / gateway 报错）编码为
// `Result.IsError`；返回 error 仅表示适配器自身不可用。
func (t *chatToolInvoker) Dispatch(ctx context.Context, call chat.ToolCall) (chat.ToolOutcome, error) {
	name := call.Name
	args := parseArgs(call.Arguments)
	canon := strings.TrimPrefix(name, "self_")

	if t.policy != nil {
		if msg := t.policy(name, args); msg != "" {
			return chat.ToolOutcome{Result: chat.ToolResult{
				CallID: call.ID, Content: msg, IsError: true,
			}}, nil
		}
	}
	if t.ask != nil && isAskTool(canon) {
		return chat.ToolOutcome{Pending: true, OpID: t.ask(call, args)}, nil
	}

	callArgs := args
	if t.args != nil {
		callArgs = t.args(name, args)
	}
	res, taskID, err := t.s.gc.Call(ctx, name, callArgs, mcpgateway.Context{
		WorkDir: t.workDir, DataDir: t.dataDir,
		Session: t.sessionID, Turn: t.turnID,
		InstanceID: t.instanceID, ToolCallID: call.ID,
		TopSession: t.topSession, Parent: t.parent,
	})
	if err != nil {
		return chat.ToolOutcome{Result: chat.ToolResult{
			CallID: call.ID, Content: "错误: " + err.Error(), IsError: true,
		}}, nil
	}
	if taskID != "" {
		// 转异步：宿主登记 task_id → 挂起本轮，回执到达后由宿主重建实例继续（M6 恢复）。
		return chat.ToolOutcome{Pending: true, OpID: taskID}, nil
	}
	out := chat.ToolResult{CallID: call.ID, Content: resultTextFromMap(res)}
	if resultStatus(res) == "cancelled" {
		// 超时裁决取消：结果照常回喂，终态语义由宿主按 _meta.status 判定（I-62）。
		out.Meta = map[string]any{"status": "cancelled"}
	}
	return chat.ToolOutcome{Result: out}, nil
}

// Cancel 取消在飞执行（op 句柄 = gateway task_id；空 instanceID 回落到本轮实例）。
func (t *chatToolInvoker) Cancel(_ context.Context, instanceID, opID string) (bool, error) {
	if t.s.execSink == nil {
		return false, nil
	}
	if instanceID == "" {
		instanceID = t.instanceID
	}
	return t.s.execSink.CancelExec(instanceID, opID)
}

// Detach 把在飞执行转后台（返回 gateway task_id）。
func (t *chatToolInvoker) Detach(_ context.Context, instanceID, opID string) (string, error) {
	if t.s.execSink == nil {
		return "", nil
	}
	if instanceID == "" {
		instanceID = t.instanceID
	}
	return t.s.execSink.DetachExec(instanceID, opID)
}

// 编译期断言：工具端口两个适配器。
var (
	_ chat.ToolCatalog = (*chatToolCatalog)(nil)
	_ chat.ToolInvoker = (*chatToolInvoker)(nil)
)
