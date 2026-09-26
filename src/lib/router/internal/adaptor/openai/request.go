// OpenAI 兼容请求编码（LR-3）：canonical Request → chat completions 线格式。
//
// 字段映射（与 llm 侧既有线格式逐项对齐，便于 LR-11 替换时行为可比）：
//   - `Role` / `Kind` / `Content` / `ToolCallID` / `ToolCalls` / `Reasoning` 一一落到同名 wire 字段
//     （`Reasoning` → `reasoning_content`：DeepSeek 等兼容扩展；为空不下发）；
//   - 内容：纯文本 → `content` 字符串；含图片块 → 内容块数组（`text` + `image_url.url = data:<mime>;base64,…`）；
//   - `ToolDef.Parameters` 原样透传为 `tools[].function.parameters`（缺省补 `{"type":"object","properties":{}}`）。
package openai

import (
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// maxTokensParam 取值（Options.MaxTokensParam）。
const (
	// MaxTokensParamAuto 按模型名择一（缺省行为）。
	MaxTokensParamAuto = "auto"
	// MaxTokensParamLegacy 强制 `max_tokens`（老口径，多数兼容端点）。
	MaxTokensParamLegacy = "max_tokens"
	// MaxTokensParamCompletion 强制 `max_completion_tokens`（o 系 / gpt-5 系口径）。
	MaxTokensParamCompletion = "max_completion_tokens"
)

// toolCallTypeFunction 是 wire 层 tool_calls[].type 的固定取值。
const toolCallTypeFunction = "function"

// emptyToolParameters 是工具无参数 schema 时的缺省（OpenAI 要求 parameters 为对象）。
const emptyToolParameters = `{"type":"object","properties":{}}`

// chatRequest 是 POST /chat/completions 的请求体。
// 输出上限两个字段按模型择一写入（另一项 omitempty 不下发），见 maxTokensParam。
type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Stream          bool           `json:"stream"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
	Tools           []chatTool     `json:"tools,omitempty"`
	ToolChoice      string         `json:"tool_choice,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            *float64       `json:"top_p,omitempty"`
	MaxTokens       *int           `json:"max_tokens,omitempty"`
	MaxCompletion   *int           `json:"max_completion_tokens,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
}

// streamOptions 开启流内 usage（末片返回 token 统计）。
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatMessage 是 wire 层消息。
type chatMessage struct {
	Role             string         `json:"role"`
	Kind             string         `json:"kind,omitempty"`
	Content          any            `json:"content"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
}

// wireToolCall 是 wire 层工具调用（assistant 发起）。
type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

// wireFunction 是 wire 层函数名与参数（Arguments = 完整 JSON 串）。
type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatTool 是 wire 层工具定义。
type chatTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

// wireToolFunction 是 wire 层工具函数定义。
type wireToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// contentPart 是 wire 层内容块（多模态）。
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

// imageURL 是 wire 层图片引用（data URL）。
type imageURL struct {
	URL string `json:"url"`
}

// buildRequest 把 canonical 请求编码为 chat completions 请求体。
// Model 为空 → 回落 `spec.DefaultModel`；BaseURL 为空 → invalid（明确报错，不发请求）。
func buildRequest(spec canon.Spec, req canon.Request, caps canon.Caps, maxTokensParamOverride string) (*chatRequest, *canon.Error) {
	if strings.TrimSpace(spec.BaseURL) == "" {
		return nil, canon.NewError(canon.ErrorInvalid, "provider 未配置 baseUrl")
	}
	model := req.Options.Model
	if model == "" {
		model = spec.DefaultModel
	}
	body := &chatRequest{Model: model, Messages: buildMessages(req.Messages), Stream: true}
	if caps.UsageInStream {
		body.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if len(req.Tools) > 0 {
		body.Tools = buildTools(req.Tools)
		body.ToolChoice = "auto"
	}
	if req.Options.Temperature != nil {
		body.Temperature = req.Options.Temperature
	}
	if req.Options.TopP != nil {
		body.TopP = req.Options.TopP
	}
	if req.Options.Reasoning != nil {
		body.ReasoningEffort = req.Options.Reasoning.Effort
	}
	if req.Options.MaxTokens != nil {
		if maxTokensParam(model, maxTokensParamOverride) == MaxTokensParamCompletion {
			body.MaxCompletion = req.Options.MaxTokens
		} else {
			body.MaxTokens = req.Options.MaxTokens
		}
	}
	return body, nil
}

// maxTokensParam 选择输出上限的字段名：`override` 显式指定时直接采用；否则按模型名判定
// —— `o1*` / `o3*` / `o4*` / `gpt-5*`（新系，OpenAI 要求 `max_completion_tokens`）→ 新字段；
// 其余（兼容端点主流口径）→ `max_tokens`。
func maxTokensParam(model, override string) string {
	switch override {
	case MaxTokensParamLegacy, MaxTokensParamCompletion:
		return override
	}
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5"} {
		if strings.HasPrefix(m, prefix) {
			return MaxTokensParamCompletion
		}
	}
	return MaxTokensParamLegacy
}

// buildMessages 把 canonical 消息逐条映射为 wire 消息（顺序保持；空入参 → 空切片）。
func buildMessages(msgs []canon.Message) []chatMessage {
	out := make([]chatMessage, 0, len(msgs))
	for _, m := range msgs {
		wm := chatMessage{
			Role:             string(m.Role),
			Kind:             m.Kind,
			ToolCallID:       m.ToolCallID,
			ReasoningContent: m.Reasoning,
		}
		text, images := splitParts(m.Content)
		if len(images) == 0 {
			wm.Content = text
		} else {
			wm.Content = contentParts(text, images)
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]wireToolCall, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				calls = append(calls, wireToolCall{
					ID:       c.ID,
					Type:     toolCallTypeFunction,
					Function: wireFunction{Name: c.Name, Arguments: c.Arguments},
				})
			}
			wm.ToolCalls = calls
		}
		out = append(out, wm)
	}
	return out
}

// splitParts 拆出文本与图片块（非文本块的图片指针为空 → 忽略）。
func splitParts(parts []canon.Part) (text string, images []*canon.ImagePart) {
	var sb strings.Builder
	for i := range parts {
		p := parts[i]
		switch {
		case p.Type == canon.PartImage || p.Image != nil:
			if p.Image != nil {
				images = append(images, p.Image)
			}
		default:
			sb.WriteString(p.Text)
		}
	}
	return sb.String(), images
}

// contentParts 构造多模态内容块数组：文本块（非空才发）+ 各图片块（data URL 原样）。
func contentParts(text string, images []*canon.ImagePart) []contentPart {
	out := make([]contentPart, 0, len(images)+1)
	if text != "" {
		out = append(out, contentPart{Type: "text", Text: text})
	}
	for _, im := range images {
		out = append(out, contentPart{Type: "image_url", ImageURL: &imageURL{URL: im.DataURL}})
	}
	return out
}

// buildTools 把工具定义映射为 wire 形态（parameters 原样透传，缺省补空对象 schema）。
func buildTools(defs []canon.ToolDef) []chatTool {
	out := make([]chatTool, 0, len(defs))
	for _, d := range defs {
		params := d.Parameters
		if len(params) == 0 {
			params = json.RawMessage(emptyToolParameters)
		}
		out = append(out, chatTool{
			Type: toolCallTypeFunction,
			Function: wireToolFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  params,
			},
		})
	}
	return out
}
