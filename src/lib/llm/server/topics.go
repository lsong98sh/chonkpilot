package server

import (
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
)

// 主题（相对 kebab 串；总线命名空间前缀 "chonk." 在 mq 初始化（Options.Prefix）注入一次，
// 业务层不出现 chonk. 字面）。客户端方法面/事件按域映射（61-消息一览 §4：llm-* → session-*、
// tasks.* → task-* 等）；gateway 方法面 = mcp-<组>-<动作>（61-消息一览 §5）。
// instance-* 为 GUI 客户端实例消息（instance.Subject* 单一来源，见 chonkpilot-plugin/instance）。
const (
	// SubjectExit 实例退出（取消名下 running turn + 释放锁；注册表移除由 instanceManager 负责）。
	SubjectExit = instance.SubjectExit
	// SubjectDomainToolCall 是域工具执行回调主题（相对主题，chonk. 前缀由总线注入）：
	// gateway 命中注册域工具 → Emit {tool, args, context?} → 本模块订阅者执行后写回 v.Result。
	SubjectDomainToolCall = "domain-tool-call"
	// SubjectLLMSimple 无上下文单轮 LLM 方法面（相对主题 llm-simple；chonk. 前缀注入）。
	// 请求 {prompt[, system]} → 应答 {text}；无 session/turn 上下文（compress 摘要 / 网关
	// mcp_find 语义检索等"简单 prompt→结果"场景，61-消息一览 2026-09-08 需求更新）。
	SubjectLLMSimple = msgkeys.TopicLlmSimple
)

// domainTopics 内部消息名（方法/事件，连字符/点分隔）→ 相对域主题
// （61-消息一览 §4：llm-*/ask-user/turn-start → session-*，
// tasks.*/task-stop → task-*，prompt-optimise/optimised → prompt-*，tool-retry → tool-*，
// servers-*/server-starting → server-*）。data-* 数据面已由 persist 服务直接应答（本包不再路由）。
var domainTopics = map[string]string{
	// 方法面（客户端 → server；结果写 v.Result，无 .reply）
	msgkeys.TopicLlmStart:        "session-start",
	msgkeys.TopicLlmSend:         "session-send",
	msgkeys.TopicLlmInboxAdd:     "inbox-add",
	msgkeys.TopicLlmInboxList:    "inbox-list",
	msgkeys.TopicLlmInboxDiscard: "inbox-discard",
	msgkeys.TopicLlmInboxPromote: "inbox-promote",
	msgkeys.TopicLlmInboxRevert:  "inbox-revert",
	msgkeys.TopicLlmCancel:       "session-cancel",
	msgkeys.TopicAskUserReply:    "session-ask-reply",
	msgkeys.TopicTaskStop:        msgkeys.TopicTaskStop,
	msgkeys.TopicTaskBackground:  msgkeys.TopicTaskBackground,
	msgkeys.TopicTaskVerify:      msgkeys.TopicTaskVerify, // 一致性校验方法面（I-87）：主题名与内部名同名，非 data-* 域
	msgkeys.TopicToolRetry:       msgkeys.TopicToolRetry,
	msgkeys.TopicPromptOptimise:  msgkeys.TopicPromptOptimise,
	// 场景向导方法面（2026-10-04；agent-wizard* 同名主题，事件面由 GUI 桥在 init-data 检测后直接下发）
	msgkeys.TopicAgentWizardProbe:    msgkeys.TopicAgentWizardProbe,
	msgkeys.TopicAgentWizardCompose:  msgkeys.TopicAgentWizardCompose,
	msgkeys.TopicAgentWizardGenerate: msgkeys.TopicAgentWizardGenerate,
	msgkeys.TopicAgentWizardSkip:     msgkeys.TopicAgentWizardSkip,
	// 事件（server → 订阅者；键为前端/internal 名，非契约主题者保留字面量）
	"llm-receive":                msgkeys.TopicSessionReceive,
	"llm-complete":               msgkeys.TopicSessionComplete,
	"llm-retry":                  msgkeys.TopicSessionRetry,
	"llm-interrupted":            msgkeys.TopicSessionInterrupted,
	"ask-user":                   msgkeys.TopicSessionAsk,
	"turn-start":                 msgkeys.TopicSessionTurnStart,
	"inbox-changed":              msgkeys.TopicSessionInboxChanged,
	"tasks.started":              msgkeys.TopicTaskStarted,
	"tasks.updated":              msgkeys.TopicTaskUpdated,
	"tasks.done":                 msgkeys.TopicTaskDone,
	msgkeys.TopicServerStarting:  msgkeys.TopicServerStarting,
	msgkeys.TopicPromptOptimised: msgkeys.TopicPromptOptimised,
}

// domainSubject 内部消息名 → 相对主题（publish/reply/订阅统一入口）。
// 未知名回退原名（防御）。
func domainSubject(name string) string {
	if sub, ok := domainTopics[name]; ok {
		return sub
	}
	return name
}

// llm-send 的 type（进入 LLM 的统一消息）。
const (
	SendTypeTextUser   = "text-user"   // 用户消息（触发一轮 LLM）
	SendTypeToolResult = "tool-result" // 工具结果/ask 结果（喂回 LLM 循环）
	SendTypeTextNotify = "text-notify" // 异步工具通知（进 LLM 或提示）
)

// maxChatInputChars 是单条用户输入 / `next_run` 提示词的字符上限（Unicode 码点；INPUTVAL-1 §3.3，
// 2026-10-21 拍板：聊天输入 ≤ 16K 字符）。前端同源上限见 `utils/fieldRules.js`。
const maxChatInputChars = 16384

// llm-notify 的 type（LLM 返回内容广播给监听者）。
const (
	NotifyTypeReason   = "reason"    // 思考链增量
	NotifyTypeText     = "text"      // 正式回复增量
	NotifyTypeToolCall = "tool-call" // 工具请求 {tool_call_id, tool, arguments}
)
