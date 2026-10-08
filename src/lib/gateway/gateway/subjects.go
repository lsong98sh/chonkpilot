// 主题空间与消息类型（对齐 26-mcp-gateway / 61-消息一览 §5）。
// 2026-09-06 主题统一（gateway 能力面重构）：全部方法面与通知归单一 **mcp 域**——
// 方法调用相对主题 = mcp-<组>-<动作>（方法名 / → -：tools/list → mcp-tools-list）；
// 通知 = mcp-tasks-report（任务完成回报）/ mcp-gateway-changed（目录/接入变化）。
// 2026-09-18：`mcp-tasks-status|result|list|cancel` 与 `mcp-tools-background` 方法面**已移除**
// （用户决定）——取消/转后台改由任务层 + 进程内 sink（`Gateway.CancelExec` / `DetachExec`），
// 异步结果与状态以 message 表 + 任务层为准；**通知面 mcp-tasks-report 保留**。
// 2026-09-05 方法面与 mq 内核对称 promise 化：Emit/On 同一条方法主题（无 -reply 主题、
// payload 无 req_id）——订阅者（gateway）执行后写回 v.Result，发送方 await 同一主题取回；
// 失败以 error 返回（mq 收集进 v.Errors，发送方经 v.Err() 读取）。
// chonk. 命名空间前缀在 mq 总线初始化时（Options.Prefix）注入一次，本模块只写相对主题
// （mcp-*），经带前缀总线自动落在 chonk.mcp-* 域。
//
// 事件面（2026-08-30 §9.9 决策修订：任务编排提升 server，gateway 降级纯执行层）：
//   - 任务完成回报只发 server（task-report，订阅方收敛为 server，不对前端广播任务事件；
//     任务树/级联/状态事件由 server 编排后广播，26-mcp-gateway/21-llm-server）
//   - 目录/接入变化 = 单一 mcp-changed（2026-09-05 合并 tool-changed ∪ server-status-changed；
//     2026-09-06 更名 mcp-gateway-changed 入 mcp 域）
package mcpgateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

const (
	// SubjectTaskReport 是异步任务完成回报主题（B1 分拆后独立主题；§9.9）：
	// 仅回报 server（订阅方收敛为 server），不对前端广播任务事件——
	// 任务树/级联/状态事件由 server 编排后广播（task-done/task-started/task-updated）。
	// payload：{task_id, tool, state, result_summary, instance_id, session?, turn?, tool_call_id?,
	//          top_session?, parent?}（后两项 = I-90 增补的任务树归属，缺省为空）
	SubjectTaskReport = msgkeys.TopicMcpTasksReport

	// SubjectMCPChanged 是目录/接入变化通知（2026-09-05 合并 tool-changed ∪ server-status-changed；
	// 2026-09-06 归 mcp 域更名）：
	// tool/resource/skill/prompt/agent/mcp-server 任一变化（注册/注销/接入/状态/重载）触发；
	// payload：{kind: tool|resource|skill|prompt|agent|server, name?, status?, reason?, instance_id}
	// kind=server 的 status ∈ connected|failed|starting|stopped|unregistered|reloaded
	//（starting=spawned 拉起中；stopped=unregister 停 spawned；failed 可带 reason=退出原因）。
	SubjectMCPChanged = msgkeys.TopicMcpGatewayChanged

	// SubjectToolsTimeout 是「到达超时、待用户裁决」下行事件（统一异步模型 2026-09-13）：
	// manual/never 的任务化调用到达超时点时发往前端（fire-and-forget），由用户从 options 中裁决；
	// 裁决前任务保持 running——**不自动失败、不自动转后台、不重跑**。
	// options 由模式决定：manual = [detach, cancel]，never = [wait, cancel]。
	// payload：{instance_id, tool_call_id, task_id, tool, reason:"timeout", timeout_s,
	//          options:[detach,cancel]|[wait,cancel]}
	SubjectToolsTimeout = msgkeys.TopicMcpToolsTimeout
)

// 资产/注册 kind（61-消息一览 §5.1.1 2026-09-05 定稿，2026-09-06 分组化后仍作内部目录
// 分类与通知 kind）：register/unregister 按原语分组入口（tools/prompts/resources/servers），
// skill = prompts 资产的类别，不独立成组。
//
// 25 §5/T2（2026-09-25）：**KindAgent 已撤出资产面**（不再 register / 不进 find|load|prompts-list）
// —— 常量保留以免大改，但不再出现在任何资产列举/检索面。
const (
	KindServer   = "server"
	KindTool     = "tool"
	KindAgent    = "agent"
	KindPrompt   = "prompt"
	KindSkill    = "skill"
	KindResource = "resource"
)

// methodSubject 返回统一 mcp 域相对主题（方法名 / → -，前缀 mcp-）：
// tools/list → mcp-tools-list；prompts/get → mcp-prompts-get；
// servers/register → mcp-servers-register；gateway/check → mcp-gateway-check。
func methodSubject(method string) string {
	return "mcp-" + strings.ReplaceAll(method, "/", "-")
}

// Context 是消息统一上下文载荷（§3.4）。
type Context struct {
	WorkDir    string `json:"work_dir"`
	DataDir    string `json:"data_dir,omitempty"`
	Session    string `json:"session,omitempty"`
	Turn       string `json:"turn,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"` // LLM tool-call 关联 id（server 层层传递，2026-08-29）
	// TopSession / Parent 是任务树归属上下文（I-90 增补，2026-09-18 用户确认）：
	// 由 llm 在发起 tools/call 时下发 → gateway 记入执行池 Task → 随 mcp-tasks-report 回报，
	// 使任务层可在「未登记」时**独立建节点**（见 chonkpilot-task/layer.go applyReport）。
	TopSession string `json:"top_session,omitempty"` // 该调用所属主会话（top_session）
	Parent     string `json:"parent,omitempty"`      // 已登记的 llm 侧节点 task_id（无则空）
}

// CallReq 是 tools/call 载荷（无 req_id；响应经同主题 v.Result）。
type CallReq struct {
	Context
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ListReq 是 tools/list 载荷（无 req_id；响应经同主题 v.Result）。
type ListReq struct {
	Context
	Cursor string `json:"cursor,omitempty"`
}

// ServerReq 是 servers/list 载荷（无 req_id；响应经同主题 v.Result）。
type ServerReq struct {
	Context
	Server string `json:"server,omitempty"`
}

// RegisterReq 是 mcp-register / mcp-unregister 载荷（无 req_id；响应经同主题 v.Result）。
type RegisterReq struct {
	Context
	Name      string `json:"name"`
	URL       string `json:"url"`
	Transport string `json:"transport,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// MethodError 是方法调用失败（订阅者以 error 返回 → mq 收集进 v.Errors；
// 发送方 await 后经 v.Err() 取回）。Code 为 JSON-RPC 风格错误码（0 = 普通失败）。
type MethodError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *MethodError) Error() string { return e.Message }

// methodError 构造 *MethodError（gateway handler 失败返回用）。
func methodError(code int, format string, args ...any) error {
	return &MethodError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// resultJSON 把方法结果 map 归一为 JSON 形态（marshal 后再 unmarshal 成纯 map/[]any）——
// 等价旧 -reply 载荷经 JSON 编解码后的契约：发送方（gwclient/测试）一律按 JSON 结构消费
// v.Result（mcp.Tool / mcp.TextContent 等原生结构不可直接按 map 断言）。
func resultJSON(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return m
	}
	return out
}

// marshal 序列化消息（错误返回 nil）。
func marshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
