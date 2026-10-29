package server

import (
	"context"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
)

// 域工具执行回调（gateway tools/register handler_subject → 本模块订阅执行，写回 v.Result）
func (s *Server) subscribeAll() error {
	for _, sub := range []struct {
		subject string
		h       mq.VHandler
	}{
		{SubjectDomainToolCall, s.onDomainToolCall},
		{SubjectLLMSimple, s.onLLMSimple},
		{SubjectInstanceClaim, s.onInstanceClaim},         // 实例认领（请求-响应；61 §4.1 ①，阶段 2a）
		{SubjectLLMTestConnection, s.onLLMTestConnection}, // 设置→LLM「测试连接」只读探活（点分主题直通总线）
		{SubjectConfigSuggest, s.onConfigSuggest},         // 配置页/工具原语「AI 智能填充」（点分主题直通总线；AICFG-1）
		{domainSubject(msgkeys.TopicLlmStart), s.onLLMStart},
		{domainSubject(msgkeys.TopicLlmSend), s.onLLMSend},
		{domainSubject(msgkeys.TopicLlmInboxAdd), s.onInboxAdd},
		{domainSubject(msgkeys.TopicLlmInboxList), s.onInboxList},
		{domainSubject(msgkeys.TopicLlmInboxDiscard), s.onInboxDiscard},
		{domainSubject(msgkeys.TopicLlmInboxPromote), s.onInboxPromote},
		{domainSubject(msgkeys.TopicLlmCancel), s.onLLMCancel},
		{domainSubject(msgkeys.TopicAskUserReply), s.onAskUserReply},
		{domainSubject(msgkeys.TopicTaskStop), s.onTaskStop},
		{domainSubject(msgkeys.TopicTaskBackground), s.onTaskBackground},
		{domainSubject(msgkeys.TopicTaskVerify), s.onTaskVerify}, // 一致性校验方法面（I-87；只读，层为后端）
		{domainSubject(msgkeys.TopicToolRetry), s.onToolRetry},
		{domainSubject(msgkeys.TopicPromptOptimise), s.onPromptOptimise},
		// 单聊（子聊天）方法面（61 §4.3/§4.5；SUBCHAT-2 阶段 B-2）：清单 / 用户拍板结束。
		// 主题为无点分的单段 kebab（relative subject 与前端 type 同名，桥/入口已放行）。
		{domainSubject(msgkeys.TopicSubchatList), s.onSubchatList},
		{domainSubject(msgkeys.TopicSubchatFinish), s.onSubchatFinish},
		// 群聊「继续」续期（SUBCHAT-3 阶段 F，61 §4.2/§4.5）：同名单段主题。
		{domainSubject(msgkeys.TopicSubchatContinue), s.onSubchatContinue},
		// 场景向导方法面（2026-10-04）
		{domainSubject(msgkeys.TopicAgentWizardProbe), s.onAgentWizardProbe},
		{domainSubject(msgkeys.TopicAgentWizardCompose), s.onAgentWizardCompose},
		{domainSubject(msgkeys.TopicAgentWizardGenerate), s.onAgentWizardGenerate},
		{domainSubject(msgkeys.TopicAgentWizardSkip), s.onAgentWizardSkip},
	} {
		sh, err := s.bus.On(sub.subject, 0, sub.h)
		if err != nil {
			return err
		}
		s.subs = append(s.subs, sh)
	}
	for _, sub := range []struct {
		subject string
		h       mq.Handler
	}{
		{instance.SubjectRegister, s.onInstanceRegister},              // 用户/项目级 capability 根动态注册（21-llm-server；T-29）
		{SubjectExit, s.onExit},                                       // 退出 → 取消名下 running turn + 释放锁
		{msgkeys.TopicMcpTasksReport, s.onGatewayTaskDone},            // gateway 异步任务完成回报（mcp-tasks-report）→ 续轮/广播
		{msgkeys.TopicMcpGatewayChanged, s.onMCPChanged},              // 目录/接入变化（mcp-gateway-changed）→ 刷新工具缓存
		{msgkeys.TopicTaskDeleted, s.onTaskTreeDeleted},               // persist data-tasktree-delete 级联删除 → 内存同步（不复活）
		{msgkeys.TopicDataMemoryRefresh, s.onMemoryRefresh},           // 记忆域 save/delete 后广播（既有主题）→ 失效类别清单缓存（I-68 ②）
		{msgkeys.TopicDataUserConfigRefresh, s.onUserConfigRefresh},   // usr 配置 save/delete 后广播（既有主题）→ tool_async / llms 热生效
		{msgkeys.TopicDataMcpRefresh, s.onMCPConfigRefresh},           // MCP 四级文件配置 save/delete 后广播 → 下游 server 增量对账（保存即生效）
		{msgkeys.TopicDataPrjConfigRefresh, s.onPrjConfigRefresh},     // prj 配置 save/delete 后广播（既有主题）→ 执行配置热生效（P0-B：timeout_sec/max_concurrency/skip_dirs）
		{msgkeys.TopicDataPrjSecurityRefresh, s.onPrjSecurityRefresh}, // prj-security save/delete 后广播（既有主题）→ agentbox 允许目录热生效（security-* → mcp-server Config / gateway 上游下发）
	} {
		sh, err := s.bus.On(sub.subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
			sub.h(subj, v.Payload)
			return nil
		})
		if err != nil {
			return err
		}
		s.subs = append(s.subs, sh)
	}
	return nil
}
