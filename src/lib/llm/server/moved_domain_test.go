package server

// 已迁 task 插件的四个会话编排工具的 **测试侧适配**（2026-11-08）。
//
// 生产路径 = task 插件工具 → 宿主 `plugin/v1/{ask.user,next.run,subchat.open|finish}`；
// 插件二进制不随 L1 单测启动，故测试用本文件模拟这段适配（与插件 handler 同语义）：
//   - ask_user  → 本 turn 的 ask 通道（ask_id/task_id 由 running 行 / 工具节点关联）
//   - next_run  → runNextRun（登记下一轮 inbox）
//   - subchat_* → execSubChatTool（会话创建 / lifecycle）
//
// 这样 L1 仍覆盖 turn 侧编排（节点、占位结果、答复续跑），插件侧由 task 插件自己的用例覆盖。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// movedDomainTestSubject 是测试网关回调主题（模拟 task 插件注册的回调）。
const movedDomainTestSubject = "test-moved-domain-call"

// movedDomainMsg 是 gateway 域工具回调载荷（`registered_provider` 的 turnContextPayload 形状）。
// 生产侧 `tool_result` 删除后 core 不再解析该载荷，测试适配保留最小解析。
type movedDomainMsg struct {
	Tool    string          `json:"tool"`
	Args    map[string]any  `json:"args"`
	Context *movedDomainCtx `json:"context"`
}

type movedDomainCtx struct {
	InstanceID  string `json:"instance_id"`
	Turn        string `json:"turn"`
	ToolCallID  string `json:"tool_call_id"`
	DisplayName string `json:"display_name"`
}

// isMovedDomainTool 判定工具是否为 2026-11-08 迁出 llm-server 的四个工具。
func isMovedDomainTool(name string) bool {
	switch name {
	case "ask_user", "next_run", "subchat_open", "subchat_finish":
		return true
	}
	return false
}

// execMovedDomainToolForTest 按宿主能力语义执行一次已迁工具（模拟插件适配层）。
// 返回 (结果文本, 是否工具级失败)；失败时把本 turn 的工具节点落 error（与迁移前
// domainExecute 的收尾同口径；tasks.done 幂等，后续 done 不会覆盖 error）。
func execMovedDomainToolForTest(s *Server, tc *turnCtx, callID, tool string, args map[string]any, purpose string) (string, bool) {
	if tc == nil {
		return "域工具必须经 server turn 调用（turn 上下文缺失）", true
	}
	var (
		text  string
		isErr bool
	)
	switch tool {
	case "ask_user":
		// 与插件 handler 同边界：空 question 是参数错误（未登记 ask）。
		if strings.TrimSpace(str(args["question"])) == "" {
			return "question 不能为空", true
		}
		taskID := ""
		if n := s.tasks.findByToolCall(tc.req.InstanceID, callID); n != nil {
			taskID = n.TaskID
		}
		askID := tc.askIDOf(callID)
		// 与宿主能力同序：占位结果先落库，再广播 ask（答复可就地覆盖，不被占位回填覆盖）。
		if askID != "" {
			tc.persistToolResultAsk(callID, askAsyncHandoffResult, "completed", nil, askID)
		}
		s.askWithID(tc.req.Turn, callID, args, taskID, askID)
		// S4：非阻塞占位结果；任务节点保持 running，答复到达才收尾。
		return askAsyncHandoffResult, false
	case "next_run":
		text, isErr = s.runNextRun(tc, args)
	case "subchat_open", "subchat_finish":
		text, isErr = s.execSubChatTool(tc, tool, args, purpose)
	default:
		return "未知已迁工具 " + tool, true
	}
	if isErr {
		if n := s.tasks.findByToolCall(tc.req.InstanceID, callID); n != nil {
			s.tasks.done(n.TaskID, TaskStateError, "", text)
		}
	}
	return text, isErr
}

// registerMigratedDomainTools 在**真实内嵌 gateway**（newTestServerMCP）上注册四个已迁工具，
// 回调转到 execMovedDomainToolForTest —— 模拟 task 插件的 tool adapter。
func registerMigratedDomainTools(t *testing.T, s *Server) {
	t.Helper()
	defs := []struct{ name, desc string }{
		{"ask_user", "向用户提问（测试适配）"},
		{"next_run", "登记下一轮排队输入（测试适配）"},
		{"subchat_open", "打开子聊天（测试适配）"},
		{"subchat_finish", "子聊天自报完成（测试适配）"},
	}
	for _, d := range defs {
		if _, err := s.emitGateway(context.Background(), "tools/register", map[string]any{
			"name":            d.name,
			"description":     d.desc,
			"handler_subject": movedDomainTestSubject,
			"owner":           "task",
			"hot":             true,
			"category":        "task",
		}); err != nil {
			t.Fatalf("tools/register(%s): %v", d.name, err)
		}
	}
	if _, err := s.bus.On(movedDomainTestSubject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var msg movedDomainMsg
		if err := json.Unmarshal(v.Payload, &msg); err != nil {
			return err
		}
		tc, callID, purpose := (*turnCtx)(nil), "", ""
		if msg.Context != nil {
			tc = s.lookupTurnIn(msg.Context.InstanceID, msg.Context.Turn)
			callID = msg.Context.ToolCallID
			purpose = msg.Context.DisplayName
		}
		text, _ := execMovedDomainToolForTest(s, tc, callID, msg.Tool, msg.Args, purpose)
		v.Result = map[string]any{
			"resultType": "complete",
			"content":    []any{map[string]any{"type": "text", "text": text}},
			"isError":    false, // 迁移前：next_run / subchat_* 的 isErr 不向 gateway 传递
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe(%s): %v", movedDomainTestSubject, err)
	}
	s.refreshTools()
}
