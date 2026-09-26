// OpenAI Responses API 请求编码（D-29）：canonical Request → `/responses` 线格式。
//
// 字段映射（与 llm 侧既有 `llm_responses.go` 的 `responsesRequest` 逐项对齐）：
//   - 历史**最前**的连续 `role=system` 消息 → 顶层 `instructions`（多段以空行拼接）；其余 system 保留
//     为 message item（与 llm 侧同口径，不擅自吞并）；
//   - `input` 输入 item：message（`input_text` / `output_text` / `input_image`）·
//     function_call（`call_id` / `name` / `arguments`）· function_call_output（`call_id` / `output`）·
//     reasoning（`reasoning_text` 内容块）；
//   - 工具定义为**扁平**形态 `{type:function, name, description, parameters}`（无 `function` 嵌套层）；
//   - `stream:true`（恒发）；`reasoning:{effort}`（非 chat 的顶层 `reasoning_effort`）；
//   - `max_output_tokens`（非 `max_tokens`）。
package openai

import (
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// responsesRequest 是 POST /responses 的请求体。
type responsesRequest struct {
	Model           string              `json:"model"`
	Instructions    string              `json:"instructions,omitempty"`
	Input           []responsesInput    `json:"input"`
	Stream          bool                `json:"stream"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	ToolChoice      string              `json:"tool_choice,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	TopP            *float64            `json:"top_p,omitempty"`
	MaxOutputTokens *int                `json:"max_output_tokens,omitempty"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
}

// responsesInput 是输入 item（并集，按 `type` 取用字段）。
type responsesInput struct {
	Type    string               `json:"type"`
	Role    string               `json:"role,omitempty"`      // message
	Content []responsesInputPart `json:"content,omitempty"`   // message / reasoning
	CallID  string               `json:"call_id,omitempty"`   // function_call / function_call_output
	Name    string               `json:"name,omitempty"`      // function_call
	Args    string               `json:"arguments,omitempty"` // function_call
	Output  string               `json:"output,omitempty"`    // function_call_output
}

// responsesInputPart 是 message（或 reasoning）的内容块。
type responsesInputPart struct {
	Type     string `json:"type"` // input_text | output_text | input_image | reasoning_text
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// responsesTool 是**扁平**工具定义（Responses 无 `function` 嵌套层）。
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// responsesReasoning 是思考强度（`effort` 取值与 chat 的 `reasoning_effort` 同词表）。
type responsesReasoning struct {
	Effort string `json:"effort"`
}

// buildResponsesRequest 把 canonical 请求编码为 `/responses` 请求体。
// Model 为空 → 回落 `spec.DefaultModel`；BaseURL 为空 / 无任何可发送内容 → invalid（明确报错，不发请求）。
func buildResponsesRequest(spec canon.Spec, req canon.Request) (*responsesRequest, *canon.Error) {
	if strings.TrimSpace(spec.BaseURL) == "" {
		return nil, canon.NewError(canon.ErrorInvalid, "provider 未配置 baseUrl")
	}
	model := req.Options.Model
	if model == "" {
		model = spec.DefaultModel
	}
	instructions, input, ierr := buildResponsesInput(req.Messages)
	if ierr != nil {
		return nil, ierr
	}

	body := &responsesRequest{Model: model, Instructions: instructions, Input: input, Stream: true}
	if len(req.Tools) > 0 {
		body.Tools = buildResponsesTools(req.Tools)
		body.ToolChoice = "auto"
	}
	if req.Options.Temperature != nil {
		body.Temperature = req.Options.Temperature
	}
	if req.Options.TopP != nil {
		body.TopP = req.Options.TopP
	}
	if req.Options.MaxTokens != nil {
		body.MaxOutputTokens = req.Options.MaxTokens
	}
	if req.Options.Reasoning != nil {
		body.Reasoning = &responsesReasoning{Effort: req.Options.Reasoning.Effort}
	}
	return body, nil
}

// buildResponsesInput 把 canonical 消息序列映射为 `instructions` + 输入 item 列表。
//
// 映射：最前连续 system → instructions；assistant 的 `Reasoning` → reasoning item（其 content 归
// `reasoning_text` 块）、`ToolCalls` → function_call item、正文 → message item（`output_text` 块）；
// `role=tool` → function_call_output item；其余（含 user）→ message item（`input_text` +
// `input_image` 块）。无任何 item 且无 instructions → invalid（不发空请求）。
//
// 与 llm 侧单点差异：**空内容块不发**（`messageItem` 在无文本且无图片时返回 ok=false）——
// 避免下发 `{"type":"input_text","text":""}` 触发上游校验（llm 侧会发空文本块）。
func buildResponsesInput(msgs []canon.Message) (string, []responsesInput, *canon.Error) {
	var sys []string
	leadingSys := true // 仍在最前的 system 段内（仅该段吸收为 instructions）
	input := make([]responsesInput, 0, len(msgs))

	for i := range msgs {
		m := msgs[i]
		if leadingSys && m.Role == canon.RoleSystem {
			if text, _ := splitParts(m.Content); strings.TrimSpace(text) != "" {
				sys = append(sys, strings.TrimSpace(text))
			}
			continue
		}
		leadingSys = false

		switch m.Role {
		case canon.RoleSystem:
			appendMessageItem(&input, string(m.Role), m.Content)
		case canon.RoleTool:
			text, _ := splitParts(m.Content)
			input = append(input, responsesInput{Type: itemFunctionCallOutput, CallID: m.ToolCallID, Output: text})
		case canon.RoleAssistant:
			if m.Reasoning != "" {
				input = append(input, responsesInput{
					Type:    itemReasoning,
					Content: []responsesInputPart{{Type: partReasoningText, Text: m.Reasoning}},
				})
			}
			for _, c := range m.ToolCalls {
				input = append(input, responsesInput{Type: itemFunctionCall, CallID: c.ID, Name: c.Name, Args: c.Arguments})
			}
			appendMessageItem(&input, string(canon.RoleAssistant), m.Content)
		default: // user（及未知角色）
			appendMessageItem(&input, string(m.Role), m.Content)
		}
	}
	if len(input) == 0 && len(sys) == 0 {
		return "", nil, canon.NewError(canon.ErrorInvalid, "无可发送的消息（instructions 与 input 皆空）")
	}
	return strings.Join(sys, "\n\n"), input, nil
}

// appendMessageItem 追加一个 message item（无可发内容块 → 不追加，见 buildResponsesInput 注）。
func appendMessageItem(input *[]responsesInput, role string, parts []canon.Part) {
	if it, ok := messageItem(role, parts); ok {
		*input = append(*input, it)
	}
}

// messageItem 构造 message item：assistant 用 `output_text`，其余用 `input_text`；图片块 → `input_image`
// （data URL 原样）；空文本块不发 —— 无任何内容块 → ok=false（调用方跳过该消息）。
func messageItem(role string, parts []canon.Part) (responsesInput, bool) {
	ctype := partInputText
	if role == string(canon.RoleAssistant) {
		ctype = partOutputText
	}
	text, images := splitParts(parts)
	content := make([]responsesInputPart, 0, len(images)+1)
	if text != "" {
		content = append(content, responsesInputPart{Type: ctype, Text: text})
	}
	for _, im := range images {
		content = append(content, responsesInputPart{Type: partInputImage, ImageURL: im.DataURL})
	}
	if len(content) == 0 {
		return responsesInput{}, false
	}
	return responsesInput{Type: itemMessage, Role: role, Content: content}, true
}

// buildResponsesTools 把工具定义映射为**扁平**形态（schema 缺省补空对象 schema）。
func buildResponsesTools(defs []canon.ToolDef) []responsesTool {
	out := make([]responsesTool, 0, len(defs))
	for _, d := range defs {
		schema := d.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(emptyToolParameters)
		}
		out = append(out, responsesTool{Type: toolCallTypeFunction, Name: d.Name, Description: d.Description, Parameters: schema})
	}
	return out
}
