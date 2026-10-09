// OptimizeAgentPrompt 内部实现：AI 优化提示词（NoteEditor/ContextConfig「AI 优化」按钮）。
//
// 消息面契约（frontend/src/api/config.js optimizeAgentPrompt → gui.prompt-optimise）：
//
//	payload {title, useCase, prompt} → 立即返回 {ok, started}
//	流式结果经 mq 事件推送（各事件均带 instance_id，61-消息一览 §0 实例字段必带）：
//	  optimize-token  {content, instance_id}   逐 token 增量（前端边收边回显）
//	  optimize-done   {prompt, instance_id}    完整结果（成功收尾）
//	  optimize-error  {code, message, name?, status?, instance_id}  失败
//	    - code = 稳定静态错误码（前端 i18n 键 optimizeError.<code>，D-41）；
//	    - message = 兜底文案 + 动态详情（网络/端点原文，前端以 {message} 插值展示）；
//	    - name = provider 名（llm_not_found 插值）；status = HTTP 状态码串（llm_status_error 插值）。
//
// 2026-09-04：原 /call OptimizeAgentPrompt 注册已清零；callOptimizeAgentPrompt 保留为
// gui.prompt-optimise 消息面内部实现（guimsg.go），流式事件推送不变。
//
// 实现：读取用户配置的**子系统默认 LLM**（`llm.promptOptimise`，llmref 类型；12-数据层 / 40 §SL）——
// 键缺失 / 空串已由数据层读侧回落全局 `defaultLLM`（SL-1，此处不再回落）；值为 provider name
// （旧记录 int 索引经 resolveLLMRefIndex 折算）。按 OpenAI 兼容 /chat/completions 流式调用，
// 解析 SSE delta.content 转发。baseUrl 用户可配（兼容端点亦可）。
//
// SL-4（40-演进计划 §SL SL-C8）：本路径（GUI 桥）与 server 侧 `prompt-optimise`（llm/server/prompt.go）
// **统一读 `llm.promptOptimise`**；每次发起提示词优化**现读配置**（SL-C9 热生效，不缓存到
// 进程级/包级变量）。
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// optimizeLLM 命中的提示词优化 LLM（`llm.promptOptimise` 指向的 usr llms 记录）的快照
// （避免读配置对象歧义）。
type optimizeLLM struct {
	Name    string  `json:"name"`
	Model   string  `json:"model"`
	BaseURL string  `json:"baseUrl"`
	APIKey  string  `json:"apiKey"`
	Temp    float64 `json:"temperature"`
	// MaxOutputToken 最大输出 token（口径 Z1 改名，2026-09-25；请求体 max_tokens）。
	MaxOutputToken int `json:"maxOutputToken"`
	// MaxTokensLegacy 读时兼容（口径 Z1）：仅接收改名前的历史键 `maxTokens`（新写入不含该键）。
	MaxTokensLegacy int `json:"maxTokens"`
}

// optimizeError 提示词优化错误（D-41，2026-10-09 方案 A）：把**静态错误**与**动态详情**分离——
//   - Code：稳定静态错误码（snake_case），前端以 `optimizeError.<Code>` 翻译；
//   - Message：兜底文案 + 动态详情（网络/端点原文；前端以 `{message}` 插值展示，code 未识别时整串兜底）；
//   - Params：前端插值参数（如 name=provider 名、status=HTTP 状态码串）。
//
// 静态文案（原硬编码中文）全部经 Code 表达，en-US 才能翻译。
type optimizeError struct {
	Code    string
	Message string
	Params  map[string]any
}

func (e *optimizeError) Error() string { return e.Message }

// 错误码常量（稳定、snake_case；前端 src/frontend/src/locales/{zh-CN,en-US}/optimizeError.json 一一对应）。
const (
	optimizeCodePromptRequired     = "prompt_required"      // 入参 prompt 为空
	optimizeCodeConfigReadFailed   = "config_read_failed"   // 读取用户配置失败
	optimizeCodeNoLLMConfig        = "no_llm_config"        // 未配置任何 LLM
	optimizeCodeLLMNotSet          = "llm_not_set"          // 提示词优化 LLM 未设置
	optimizeCodeLLMNotFound        = "llm_not_found"        // 指定的 provider 名不存在（参数 name）
	optimizeCodeLLMConfigInvalid   = "llm_config_invalid"   // LLM 配置格式错误
	optimizeCodeLLMModelMissing    = "llm_model_missing"    // 默认 LLM 缺少 model
	optimizeCodeRequestBuildFailed = "request_build_failed" // 构建 HTTP 请求失败
	optimizeCodeLLMRequestFailed   = "llm_request_failed"   // 发起请求失败（网络/超时）
	optimizeCodeLLMStatusError     = "llm_status_error"     // 非 200（参数 status）
	optimizeCodeLLMEmptyResponse   = "llm_empty_response"   // 未返回内容
)

// callOptimizeAgentPrompt 立即返回（流式在后台 goroutine 推送事件）。
func callOptimizeAgentPrompt(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	var req struct {
		Title   string `json:"title"`
		UseCase string `json:"useCase"`
		Prompt  string `json:"prompt"`
	}
	unmarshalParams(params, &req)
	if strings.TrimSpace(req.Prompt) == "" {
		b.emitOptimizeError(&optimizeError{Code: optimizeCodePromptRequired, Message: "prompt required"})
		return json.Marshal(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldStarted: false})
	}
	llm, oerr := activeLLM(b)
	if oerr != nil {
		b.emitOptimizeError(oerr)
		return json.Marshal(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldStarted: false})
	}
	go b.optimizeStream(req.Title, req.UseCase, req.Prompt, llm)
	return json.Marshal(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldStarted: true})
}

// emitOptimizeError 下发 optimize-error 事件（同步路径用；instance_id 必带，61 §0）。params 可选键
// 直接并入 payload（如 name/status），供前端 `optimizeError.<code>` 插值。
func (b *Bridge) emitOptimizeError(e *optimizeError) {
	payload := map[string]any{
		msgkeys.FieldCode:       e.Code,
		msgkeys.FieldMessage:    e.Message,
		msgkeys.FieldInstanceId: b.instanceID,
	}
	for k, v := range e.Params {
		payload[k] = v
	}
	b.EmitFrontend(msgkeys.TopicOptimizeError, jsonEnvelope(payload))
}

// activeLLM 取用户配置的**提示词优化 LLM**（`llm.promptOptimise` = provider name；旧记录 int 索引
// 兼容；见 §resolveLLMRefIndex）。键缺失 / 空串已由数据层读侧回落 `defaultLLM`（SL-1）。
// 提示词优化只支持 usr llms 中的真实 provider：取值空串或已失效（改名/删除）→ 明确报错，
// 不猜测端点、不发请求。
func activeLLM(b *Bridge) (optimizeLLM, *optimizeError) {
	cfg, err := readUserConfig(b)
	if err != nil {
		return optimizeLLM{}, &optimizeError{Code: optimizeCodeConfigReadFailed, Message: err.Error()}
	}
	llms := asAnySlice(cfg["llms"])
	if len(llms) == 0 {
		return optimizeLLM{}, &optimizeError{Code: optimizeCodeNoLLMConfig, Message: "未配置 LLM（请在 用户配置 → LLM 中添加）"}
	}
	idx, oerr := resolveLLMRefIndex(cfg["llm.promptOptimise"], llms)
	if oerr != nil {
		return optimizeLLM{}, oerr
	}
	raw, _ := json.Marshal(llms[idx])
	var llm optimizeLLM
	if err := json.Unmarshal(raw, &llm); err != nil {
		return optimizeLLM{}, &optimizeError{Code: optimizeCodeLLMConfigInvalid, Message: err.Error()}
	}
	if strings.TrimSpace(llm.Model) == "" {
		return optimizeLLM{}, &optimizeError{Code: optimizeCodeLLMModelMissing, Message: "默认 LLM 缺少 model"}
	}
	// 读时兼容（口径 Z1）：旧键 maxTokens（改名前的历史配置）→ 当作 maxOutputToken。
	if llm.MaxOutputToken == 0 {
		llm.MaxOutputToken = llm.MaxTokensLegacy
	}
	return llm, nil
}

// resolveLLMRefIndex 解析 llmref 取值（`llm.promptOptimise`；同 defaultLLM 形态）→ usr llms 下标
// （2026-09-15：llmref 改为 name 字符串，读侧兼容旧 int 索引；折算口径与 data.LLMRefName 对齐，
// 见 src/lib/data/llmref.go）：
//   - 数字（旧记录 int 索引；含 persist 自动计算的 0/-1）→ 越界/负数按既有语义回落首个可用 LLM（下标 0）；
//   - 字符串 → 按 provider name 精确匹配 usr llms；
//     未命中：空串（未设置） / 已失效名 → 错误（含原因）。
//   - 其他（键缺失等）→ 回落下标 0（既有行为）。
func resolveLLMRefIndex(v any, llms []any) (int, *optimizeError) {
	if name, ok := v.(string); ok {
		if name == "" {
			return 0, &optimizeError{Code: optimizeCodeLLMNotSet, Message: "提示词优化 LLM 未设置，需选择已配置的 LLM"}
		}
		for i, item := range llms {
			m, _ := item.(map[string]any)
			if n, _ := m["name"].(string); n == name {
				return i, nil
			}
		}
		return 0, &optimizeError{
			Code:    optimizeCodeLLMNotFound,
			Message: fmt.Sprintf("提示词优化 LLM「%s」不存在（请在 用户配置 → LLM 中重新设置）", name),
			Params:  map[string]any{msgkeys.FieldName: name},
		}
	}
	// 数字（旧记录 int 索引；含 persist 自动计算的 0/-1）：**三种形态都认** —— 门面 inline 绑定给出
	// 内核 int（readUserConfig 用 Atoi 还原），序列化绑定（MQ/JSON）给出 float64，int64 为中间形态。
	// 越界/负数/键缺失 → 既有语义回落首个可用 LLM（下标 0）。
	switch n := v.(type) {
	case int:
		if n >= 0 && n < len(llms) {
			return n, nil
		}
	case int64:
		if i := int(n); i >= 0 && i < len(llms) {
			return i, nil
		}
	case float64:
		if i := int(n); i >= 0 && i < len(llms) {
			return i, nil
		}
	}
	return 0, nil
}

// optimizeStream 流式调用 LLM 并把增量推给前端（独立 goroutine，不阻塞桥返回）。
func (b *Bridge) optimizeStream(title, useCase, prompt string, llm optimizeLLM) {
	emit := func(typ string, payload map[string]any) {
		payload["instance_id"] = b.instanceID
		b.EmitFrontend(typ, jsonEnvelope(payload))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := strings.TrimRight(llm.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if !strings.HasSuffix(base, "/chat/completions") {
		base += "/chat/completions"
	}

	sys := "You are a prompt engineering assistant. Improve the given prompt for clarity, precision and effectiveness. Keep the user's intent and language. Output ONLY the improved prompt text, no explanations."
	user := prompt
	if title != "" || useCase != "" {
		parts := []string{}
		if title != "" {
			parts = append(parts, "Title: "+title)
		}
		if useCase != "" {
			parts = append(parts, "Use case: "+useCase)
		}
		user = strings.Join(parts, "\n") + "\n\nPrompt:\n" + prompt
	}

	reqBody := map[string]any{
		"model": llm.Model,
		"messages": []map[string]string{
			{"role": "system", "content": sys},
			{"role": "user", "content": user},
		},
		"stream":      true,
		"temperature": llm.Temp,
	}
	// max_tokens=0 表示未配置 → **省略该字段**（交由服务端默认），不可原样发 0
	// （部分 OpenAI 兼容端点会把 0 解读为「不输出任何 token」而立即空返回）。
	if llm.MaxOutputToken > 0 {
		reqBody["max_tokens"] = llm.MaxOutputToken
	}
	body, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		emit(msgkeys.TopicOptimizeError, map[string]any{
			msgkeys.FieldCode:    optimizeCodeRequestBuildFailed,
			msgkeys.FieldMessage: err.Error(),
		})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if llm.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+llm.APIKey)
	}
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		// message 仅保留动态详情（网络/超时原文），静态前缀由 code 键化（前端以 {message} 插值）。
		emit(msgkeys.TopicOptimizeError, map[string]any{
			msgkeys.FieldCode:    optimizeCodeLLMRequestFailed,
			msgkeys.FieldMessage: err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		emit(msgkeys.TopicOptimizeError, map[string]any{
			msgkeys.FieldCode:    optimizeCodeLLMStatusError,
			msgkeys.FieldStatus:  fmt.Sprintf("%d", resp.StatusCode),
			msgkeys.FieldMessage: strings.TrimSpace(string(raw)),
		})
		return
	}

	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content == "" {
				continue
			}
			sb.WriteString(ch.Delta.Content)
			emit(msgkeys.TopicOptimizeToken, map[string]any{msgkeys.FieldContent: ch.Delta.Content})
			if ch.FinishReason == "stop" {
				emit(msgkeys.TopicOptimizeDone, map[string]any{msgkeys.FieldPrompt: sb.String()})
				return
			}
		}
	}
	if sb.Len() > 0 {
		emit(msgkeys.TopicOptimizeDone, map[string]any{msgkeys.FieldPrompt: sb.String()})
	} else {
		emit(msgkeys.TopicOptimizeError, map[string]any{
			msgkeys.FieldCode:    optimizeCodeLLMEmptyResponse,
			msgkeys.FieldMessage: "LLM 未返回内容",
		})
	}
}

// jsonEnvelope 序列化事件 payload（对齐前端 mq emitRemote 载荷为 JSON 字符串）。
func jsonEnvelope(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// unmarshalParams 兼容单对象参数（含误传单元素数组包两层；原 primitives.go，A5b 随
// knowledge 本地实现删除后并入唯一调用方 optimize.go）。
func unmarshalParams(params []json.RawMessage, out any) {
	if len(params) == 0 {
		return
	}
	raw := params[0]
	if len(raw) > 0 && raw[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
			raw = arr[0]
		}
	}
	_ = json.Unmarshal(raw, out)
}
