// config.suggest 方法面：AI 智能填充（AICFG-1，2026-10-16）。
//
// 背景：配置页（LLM / MCP）与工具（原语）编辑页**全手填**——用户要配一个模型得自己知道
// model/baseUrl/上下文窗口/协议/能力标志一整套参数；要新建工具得自己写契约 md。本方法面
// 让用户用自然语言描述需求 → LLM 产出**结构化字段建议** → 只回填表单**待保存态**（不落库）。
//
// 契约（61-消息一览 §1「点分相对主题直通总线服务」）：
//
//	chonk.config.suggest{instance_id?, kind, text, current?}
//	  → 同步应答 {ok, fields:{...}, notes?, error?:{kind, message}}
//
//	kind    = "llm" | "mcp" | "tool"（目标表单种类；决定白名单与指令模板）
//	text    = 用户自然语言（必填）
//	current = 当前表单值（可选；供增量补全/保持已填项）
//
// 要点：
//   - **provider 选择口径统一**：与 prompt-optimise 一致，读**子系统默认 LLM** `llm.promptOptimise`
//     （缺失/空串由数据层读侧回落 `defaultLLM`）；**现读配置、热生效**，不缓存到进程级。
//   - **一次性、非流式**：结构化 JSON 用一次性返回最简单（流式半截 JSON 无展示价值），
//     经一次性无上下文会话入口（llmOnceSpec，自带 60s 超时，不悬挂）。
//   - **输出解析**：严格 `json.Unmarshal`；解析失败/无任何可填字段 → 稳定错误码 `parse_failed`。
//   - **白名单**：fields **只含该 kind 的白名单字段**（§4）——`apiKey` / `level` / `enabled` 等
//     禁填字段**绝不代填、绝不回传**（即便模型输出也丢弃；`current` 上传时同样剔除）。
//   - **失败恒走 result 信封**（恒返回 nil，不抛总线错误，前端据 `result.ok` 分流）——形同
//     llm.test-connection。应答与日志经 `maskLLMSecrets` 脱敏（上游回显密钥不外泄）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// SubjectConfigSuggest 配置建议方法面的相对主题。**点分形态**：GUI 桥与服务端上行入口按
// 「点分相对主题直通总线服务」原样注入总线（61-消息一览 §1），browser 形态需在
// `allowedDottedPrefixes` 显式放行（publish.go）。
const SubjectConfigSuggest = msgkeys.TopicConfigSuggest

// configSuggestKind* 目标表单种类（决定白名单与指令；入参校验白名单）。
const (
	configSuggestKindLLM  = "llm"
	configSuggestKindMCP  = "mcp"
	configSuggestKindTool = "tool"
)

// configSuggestKindInvalid 入参不合法（kind 未知 / text 为空）：与 llm.test-connection 同分类。
const configSuggestKindInvalid = llmTestKindInvalid

// configSuggestKindParse 模型输出不可解析（非合法 JSON / 无任何可填字段）：稳定错误码。
const configSuggestKindParse ErrKind = "parse_failed"

// configSuggestReq 是配置建议请求载荷（字段名与前端表单同形）。
type configSuggestReq struct {
	InstanceID string         `json:"instance_id,omitempty"`
	Kind       string         `json:"kind"`
	Text       string         `json:"text"`
	Current    map[string]any `json:"current,omitempty"`
}

// configSuggestWhitelist 各 kind 允许回填的字段（§4 白名单，逐字对照设计稿）：
//   - llm：✅ name/model/protocol/thinking/reasoningEffort/capabilities；
//     ⚠️ baseUrl/maxContextToken/maxOutputToken/temperature/maxToolIterations；🚫 apiKey。
//   - mcp：✅ name/transport/description/url；⚠️ runtime/args/env/namespace/timeout；
//     🚫 level/enabled/isolate/sandbox/hot_tools。
//   - tool：✅ title/description/parameters/content；✅/⚠️ meta；⚠️ refs/delegate。
//
// **不在本表的字段（尤其 apiKey）一律丢弃**——模型即便输出也不回传。
var configSuggestWhitelist = map[string][]string{
	configSuggestKindLLM: {
		"name", "model", "protocol", "baseUrl",
		"maxContextToken", "maxOutputToken", "thinking", "reasoningEffort",
		"capabilities", "temperature", "maxToolIterations",
	},
	configSuggestKindMCP: {
		"name", "transport", "description", "url",
		"runtime", "args", "env", "namespace", "timeout",
	},
	configSuggestKindTool: {
		"title", "description", "parameters", "content", "meta", "refs", "delegate",
	},
}

// onConfigSuggest 受理配置建议方法面：校验入参 → 现读 provider 一次性生成 → 严格解析并过滤白名单
// → 同步写回结果。恒返回 nil（结果走 v.Result 信封），失败不抛总线错误。
func (s *Server) onConfigSuggest(_ context.Context, _ string, v *mq.Value) error {
	req, err := parseConfigSuggestReq(v.Payload)
	if err != nil {
		v.Result = configSuggestFail(configSuggestKindInvalid, err.Error())
		return nil
	}
	v.Result = s.runConfigSuggest(req)
	return nil
}

// parseConfigSuggestReq 解析并校验载荷：kind ∈ {llm,mcp,tool}、text 非空（缺 → invalid）。
func parseConfigSuggestReq(payload []byte) (*configSuggestReq, error) {
	var req configSuggestReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, errors.New("payload 不是合法 JSON 对象")
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Text = strings.TrimSpace(req.Text)
	switch req.Kind {
	case configSuggestKindLLM, configSuggestKindMCP, configSuggestKindTool:
	default:
		return nil, fmt.Errorf("kind 须为 llm/mcp/tool 之一，实得 %q", req.Kind)
	}
	if req.Text == "" {
		return nil, errors.New("text 不能为空")
	}
	return &req, nil
}

// runConfigSuggest 现读 provider → 一次性生成 → 严格解析 → 组装结果信封（成功/失败同形）。
func (s *Server) runConfigSuggest(req *configSuggestReq) map[string]any {
	instruction := configSuggestInstruction(req.Kind, req.Text, req.Current)
	raw, err := s.llmOnceSpec(s.promptOptimiseSpec(req.InstanceID), "", instruction)
	if err != nil {
		return configSuggestFailFromErr(err)
	}
	fields, notes, err := parseConfigSuggestOutput(req.Kind, raw)
	if err != nil {
		return configSuggestFail(configSuggestKindParse, err.Error())
	}
	out := map[string]any{"ok": true, "fields": fields}
	if notes != "" {
		out["notes"] = notes
	}
	return out
}

// parseConfigSuggestOutput 严格解析模型输出为 JSON 对象，并**只保留该 kind 的白名单字段**：
//   - 非 JSON / 顶层非对象 → parse 错误；
//   - 非白名单字段（含 apiKey）与空值 → 丢弃；
//   - `notes`（string）单独取出作为结果注记（不进 fields）；
//   - 过滤后无任何字段 → parse 错误（不部分回填空对象）。
func parseConfigSuggestOutput(kind, raw string) (map[string]any, string, error) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, "", errors.New("模型返回内容不是合法 JSON 对象")
	}
	allow := map[string]bool{}
	for _, k := range configSuggestWhitelist[kind] {
		allow[k] = true
	}
	fields := map[string]any{}
	for k, val := range obj {
		if k == "notes" {
			continue // 注记走 result.notes，不进 fields
		}
		if !allow[k] {
			continue // 非白名单字段（含 apiKey）一律丢弃
		}
		if val == nil {
			continue
		}
		if str, ok := val.(string); ok && strings.TrimSpace(str) == "" {
			continue
		}
		fields[k] = val
	}
	if len(fields) == 0 {
		return nil, "", errors.New("模型未返回任何可填字段")
	}
	notes, _ := obj["notes"].(string)
	return fields, strings.TrimSpace(notes), nil
}

// configSuggestFailFromErr 把生成中的错误转结果信封：*LLMError 用其分类，**一次性调用超时**
// （§6：生成耗时较长 → 明确错误）归 `timeout`，其余按协议错误。
func configSuggestFailFromErr(err error) map[string]any {
	var le *LLMError
	if errors.As(err, &le) {
		return configSuggestFail(le.Kind, le.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return configSuggestFail(ErrTimeout, "config.suggest 生成超时: "+err.Error())
	}
	return configSuggestFail(ErrProtocol, err.Error())
}

// configSuggestFail 组装失败结果信封：kind 为稳定分类（invalid/parse_failed/LLM 既有分类），
// message 与主路径同形（`[kind] <msg>`，前端复用 errorMessage 分类映射），并**强制脱敏**。
func configSuggestFail(kind ErrKind, msg string) map[string]any {
	if kind == "" {
		kind = ErrProtocol
	}
	logf("[chonkpilot-server] config.suggest ok=false kind=%s message=%s\n",
		kind, maskLLMSecrets(msg, ""))
	return map[string]any{
		"ok":     false,
		"fields": map[string]any{},
		"error": map[string]any{
			"kind":    string(kind),
			"message": maskLLMSecrets(llmErr(kind, msg).Error(), ""),
		},
	}
}

// configSuggestInstruction 按 kind 拼指令：字段清单 + 禁止项 + 当前值 + 仅输出 JSON 的硬要求
// （对齐 optimiseInstruction 写法：只输出结果、无解释）。
func configSuggestInstruction(kind, text string, current map[string]any) string {
	var b strings.Builder
	b.WriteString("You are a configuration assistant. Based on the user's description, " +
		"suggest values for the form as a single JSON object.\n\n")
	b.WriteString("Allowed fields (ONLY these):\n")
	b.WriteString(configSuggestFieldSpec(kind))
	b.WriteString("\nNever output secrets (apiKey, tokens, passwords). " +
		"Omit any field you are unsure about.\n")
	if cur, ok := configSuggestCurrentJSON(kind, current); ok {
		b.WriteString("\nCurrent form values (only override when the description asks for it):\n")
		b.WriteString(cur)
		b.WriteString("\n")
	}
	b.WriteString("\nUser description:\n")
	b.WriteString(text)
	b.WriteString("\n\nReturn ONLY a single valid JSON object (no markdown fences, no explanations).")
	return b.String()
}

// configSuggestFieldSpec 返回该 kind 的字段清单（LLM 指令用；与白名单同源语义）。
func configSuggestFieldSpec(kind string) string {
	switch kind {
	case configSuggestKindLLM:
		return strings.Join([]string{
			"- name (string): provider display name",
			`- model (string): model id`,
			`- protocol (string): "openai" (Chat Completions) or "responses" (Responses API)`,
			"- baseUrl (string): API endpoint base URL",
			"- maxContextToken (integer): context window size in tokens",
			"- maxOutputToken (integer): max output tokens",
			"- thinking (boolean): enable reasoning",
			`- reasoningEffort (string): "high" or "max"`,
			`- capabilities (array of strings): subset of ["reasoning","vision"]`,
			"- temperature (number): 0..2",
			"- maxToolIterations (integer): max tool-call rounds",
		}, "\n")
	case configSuggestKindMCP:
		return strings.Join([]string{
			"- name (string): server name, ^[A-Za-z][A-Za-z0-9_]*$",
			`- transport (string): "stdio" or "http" or "sse"`,
			"- description (string): human-readable description",
			"- url (string): endpoint for http/sse transport",
			"- runtime (string): executable for stdio transport",
			"- args (array of strings): command-line args for stdio",
			`- env (array of strings): "KEY=VALUE" entries for stdio`,
			"- namespace (string): tool name prefix",
			"- timeout (number): request timeout in seconds",
		}, "\n")
	case configSuggestKindTool:
		return strings.Join([]string{
			"- title (string): contract title",
			"- description (string): one-paragraph summary",
			"- parameters (string): JSON Schema text for the tool parameters",
			"- content (string): markdown body following the type outline",
			"- meta (object): meta key/value map (e.g. runtime/args/output/category/async/hot)",
			`- refs (object): referenced names, e.g. {"skills":[],"tools":[],"resources":[]}`,
			"- delegate (string): delegation condition (agent type only)",
		}, "\n")
	}
	return ""
}

// configSuggestCurrentJSON 把当前表单值序列化为 JSON 供指令嵌入（**只保留该 kind 白名单字段**，
// 避免把 apiKey 等禁填项送到上游）；为空 → ok=false（不嵌入）。
func configSuggestCurrentJSON(kind string, current map[string]any) (string, bool) {
	if len(current) == 0 {
		return "", false
	}
	allow := map[string]bool{}
	for _, k := range configSuggestWhitelist[kind] {
		allow[k] = true
	}
	safe := map[string]any{}
	for k, v := range current {
		if !allow[k] || v == nil {
			continue
		}
		safe[k] = v
	}
	if len(safe) == 0 {
		return "", false
	}
	raw, err := json.Marshal(safe)
	if err != nil {
		return "", false
	}
	return string(raw), true
}
