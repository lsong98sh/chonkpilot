//	prompt-optimise 消息面：生成/优化提示词（对齐主仓库 pkg/ide/app GeneratePrompts /
//	OptimizeAgentPrompt 语义——LLM 只输出提示词文本，无解释、无 markdown）。
//
//	chonk.prompt-optimise{req_id, instance_id, type, content?} → server 订阅
//	type = tool_usage | summary | agent 等；content 存在 = 优化已有，不存在 = 全新生成。
//	完成后广播 chonk.prompt-optimised{instance_id, type, content}（instance+type 配对，
//	请求方凭配对关联自己的结果；失败时 content 为空 + error 字段）。
//
// 2026-09-08 收敛：生成/优化归属"一次性无上下文会话"入口（llmOnceSpec，同 llm-simple 语义）——
// 不再以独立 client 直开 usr 库解析 llms+defaultLLM；usr 配置经 data 门面（s.cfg）读取。
//
// 2026-09-25（SL-4 / 40-演进计划 §SL SL-C8）：provider 改为读**子系统默认 LLM** `llm.promptOptimise`
// （与 GUI 桥 src/lib/gui/bridge/optimize.go 统一；键缺失 / 空串已由数据层读侧回落 `defaultLLM`，
// SL-1，此处不再回落）。每次发起**现读配置**（SL-C9 热生效：按 turn 生效，不缓存进程级/包级）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-router"
)

// promptOptimiseReq 是 prompt-optimise 请求载荷。
type promptOptimiseReq struct {
	ReqID      string `json:"req_id"`
	InstanceID string `json:"instance_id"`
	Type       string `json:"type"`
	Content    string `json:"content,omitempty"`
}

// promptOptimiseMaxInflight 是 prompt-optimise 每实例在飞并发上限（B-24：原每次请求无条件开
// goroutine，无界；达上限 → 明确失败而非排队阻塞总线派发）。
const promptOptimiseMaxInflight = 2

// promptSemaphore 取（懒建）某实例的并发信号量（容量 promptOptimiseMaxInflight）。
func (s *Server) promptSemaphore(instanceID string) chan struct{} {
	s.promptSemMu.Lock()
	defer s.promptSemMu.Unlock()
	if s.promptSem == nil {
		s.promptSem = make(map[string]chan struct{})
	}
	sem := s.promptSem[instanceID]
	if sem == nil {
		sem = make(chan struct{}, promptOptimiseMaxInflight)
		s.promptSem[instanceID] = sem
	}
	return sem
}

// onPromptOptimise 受理提示词生成/优化：校验 type → 经一次性无上下文会话入口（llmOnceSpec，
// provider 由 promptOptimiseSpec 现读 `llm.promptOptimise`）后台生成，完成后广播 prompt-optimised。
// 每实例在飞并发受 promptOptimiseMaxInflight 限制（B-24）；超出 → 广播明确错误（不静默丢弃）。
func (s *Server) onPromptOptimise(ctx context.Context, subject string, v *mq.Value) error {
	var req promptOptimiseReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		return nil
	}
	if req.Type == "" {
		s.publishPromptOptimised(req.InstanceID, "", "", errors.New("prompt-optimise: type required"))
		return nil
	}
	instruction := optimiseInstruction(req.Type, req.Content)
	sem := s.promptSemaphore(req.InstanceID)
	select {
	case sem <- struct{}{}:
		go func() {
			defer func() { <-sem }()
			s.runPromptOptimise(req.InstanceID, req.Type, instruction)
		}()
	default:
		// 本实例在飞已达上限 → 明确失败（调用方据 error 提示稍后重试）。
		s.publishPromptOptimised(req.InstanceID, req.Type, "",
			errors.New("prompt-optimise: 本实例并发生成已达上限，请稍后重试"))
	}
	return nil
}

// runPromptOptimise 后台执行一次性无上下文 LLM 生成并广播 prompt-optimised
// （错误同样广播，content 为空；llmOnceSpec 自带 60s 超时，生成不得悬挂）。
// provider 由 promptOptimiseSpec 现读 `llm.promptOptimise` 决定（SL-4）。
func (s *Server) runPromptOptimise(instanceID, typ, instruction string) {
	text, err := s.llmOnceSpec(s.promptOptimiseSpec(instanceID), "", instruction)
	if err != nil {
		s.publishPromptOptimised(instanceID, typ, "", err)
		return
	}
	s.publishPromptOptimised(instanceID, typ, text, nil)
}

// promptOptimiseSpec 现读 usr 配置，按**子系统默认 LLM** `llm.promptOptimise` 折算 provider →
// router.Spec（SL-4 / 40-演进计划 §SL SL-C8：与 GUI 桥 activeLLM 统一读同一键）。
//
//   - 键缺失 / 空串 → 数据层读侧已回落全局 `defaultLLM`（SL-1）→ 此处**不再回落**；
//   - 值为旧 int 索引 → 经 `data.LLMRefName` 折算为 `llms[idx]` 的 provider name
//     （server 的 provider 选择只认 name，直接透传 int 会静默退化为 exe 默认）；
//   - 值为 provider name → 经 providerFromConfig 命中 usr llms（未命中 → 内置兜底保留名 / nil）；
//   - 未配置 / 读取失败 → exe flags 隐含默认（与改前 `s.llm` 回落口径一致，零行为回归）。
//
// 每次发起**现读配置**（SL-C9 热生效：按 turn 生效，不得缓存到进程级/包级变量）。
func (s *Server) promptOptimiseSpec(instanceID string) router.Spec {
	if s.cfg == nil {
		return s.exeDefaultSpec()
	}
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] promptOptimiseSpec: UserConfigGet failed: %v\n", err)
		return s.exeDefaultSpec()
	}
	cfg := res.Config // 可能为 nil（无配置）：LLMRefName → "" → providerFromConfig → nil → exe 默认
	name := data.LLMRefName(cfg["llm.promptOptimise"], cfg["llms"])
	return s.specFor(providerFromConfig(cfg, name))
}

// publishPromptOptimised 广播 prompt-optimised{instance_id, type, content}；err 非 nil 时
// content 为空并附带 error 字段（订阅方凭 instance+type 配对关联）。
func (s *Server) publishPromptOptimised(instanceID, typ, content string, err error) {
	payload := map[string]any{"instance_id": instanceID, "type": typ, "content": content}
	if err != nil {
		payload["error"] = err.Error()
	}
	s.publish(msgkeys.TopicPromptOptimised, payload)
}

// optimiseInstruction 构造 LLM 生成/优化指令（语义对齐主仓库 OptimizeAgentPrompt：
// 专家提示词工程师角色 + 仅输出优化后的提示词文本，无解释、无 markdown 格式）。
func optimiseInstruction(typ, content string) string {
	desc := promptTypeDesc(typ)
	if content != "" {
		return fmt.Sprintf(`You are an expert prompt engineer. Optimize the following %s to be more effective, clear, and actionable.

Current Content:
%s

Please provide an improved version of the prompt. Return ONLY the optimized prompt text, no explanations or markdown formatting. The prompt should be concise yet comprehensive, with clear instructions for the AI.`, desc, content)
	}
	return fmt.Sprintf(`You are an expert prompt engineer. Write a %s from scratch.

Please provide the complete prompt. Return ONLY the prompt text, no explanations or markdown formatting. The prompt should be concise yet comprehensive, with clear instructions for the AI.`, desc)
}

// promptTypeDesc 把 type 映射为 LLM 指令中的提示词类型描述（未知 type 回落通用提示词）。
func promptTypeDesc(typ string) string {
	switch typ {
	case "tool_usage":
		return "tool usage guide (instructions describing how the AI should use its available tools)"
	case "summary":
		return "conversation summary prompt (template guiding the AI to compress long conversations)"
	case "agent":
		return "AI agent system prompt (defining the AI assistant's role, capabilities and behavioral constraints)"
	}
	return "AI prompt"
}
