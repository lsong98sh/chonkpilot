// Anthropic 请求编码（LR-4）：canonical Request → `/v1/messages` 线格式。
//
// 映射细则（与 canonical 的差异处置见包注释）：`system` 提升到顶层；消息内容一律为**内容块数组**；
// `role=tool` 并入 user 的 `tool_result` 块；相邻同角色合并；空块不发；工具参数解析为对象。
package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// 请求常量。
const (
	// apiVersion 是 Anthropic 必需的版本头（`anthropic-version`）。
	apiVersion = "2023-06-01"
	// defaultMaxTokens 是 `max_tokens` 缺省值（caps.MaxTokensRequired=true：缺值补缺省，
	// 不因缺参数而失败）。
	defaultMaxTokens = 4096
	// thinking 预算档位（Anthropic 要求 budget_tokens >= 1024 且 < max_tokens）。
	minThinkingBudget     = 1024
	defaultThinkingBudget = 4096
	thinkingBudgetHigh    = 8192
	thinkingBudgetMedium  = 4096
	thinkingBudgetLow     = 2048
	// thinkingEnabled 是 `thinking.type` 的取值。
	thinkingEnabled = "enabled"
	// toolChoiceAuto 是 `tool_choice.type` 的取值（默认行为，显式下发与 openai 侧一致）。
	toolChoiceAuto = "auto"
	// emptyInput 是工具调用无参数时的 `input`（Anthropic 要求对象）。
	emptyInput = `{}`
)

// 消息角色与内容块类型（wire 层）。
const (
	roleUser      = "user"
	roleAssistant = "assistant"

	blockText       = "text"
	blockImage      = "image"
	blockToolUse    = "tool_use"
	blockToolResult = "tool_result"

	imageSourceBase64 = "base64"
)

// messagesRequest 是 POST /v1/messages 的请求体。
type messagesRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	System      string        `json:"system,omitempty"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	ToolChoice  *toolChoice   `json:"tool_choice,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	Thinking    *thinking     `json:"thinking,omitempty"`
	Stream      bool          `json:"stream"`
}

// wireMessage 是 wire 层消息（内容恒为内容块数组）。
type wireMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock 是内容块并集：按 `type` 取用对应字段
// （`text` / `image` / `tool_use` / `tool_result` / `thinking`）。
type contentBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"` // type=text

	Source *imageSource `json:"source,omitempty"` // type=image

	ID    string          `json:"id,omitempty"`    // type=tool_use
	Name  string          `json:"name,omitempty"`  // type=tool_use
	Input json.RawMessage `json:"input,omitempty"` // type=tool_use（JSON 对象）

	ToolUseID string `json:"tool_use_id,omitempty"` // type=tool_result
	Content   string `json:"content,omitempty"`     // type=tool_result（文本结果）

	Thinking  string `json:"thinking,omitempty"`  // type=thinking（思考链文本）
	Signature string `json:"signature,omitempty"` // type=thinking（**必填**，D-31）
}

// imageSource 是图片源（base64 内联）。
type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// wireTool 是 wire 层工具定义（schema 字段名为 `input_schema`）。
type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// toolChoice 是 wire 层工具选择。
type toolChoice struct {
	Type string `json:"type"`
}

// thinking 是扩展思考开关（`budget_tokens` 由思考档位映射，见 thinkingBudget）。
type thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// buildRequest 把 canonical 请求编码为 `/v1/messages` 请求体。
// Model 为空 → 回落 `spec.DefaultModel`；BaseURL 为空 → invalid（明确报错，不发请求）。
func buildRequest(spec canon.Spec, req canon.Request) (*messagesRequest, *canon.Error) {
	if strings.TrimSpace(spec.BaseURL) == "" {
		return nil, canon.NewError(canon.ErrorInvalid, "provider 未配置 baseUrl")
	}
	model := req.Options.Model
	if model == "" {
		model = spec.DefaultModel
	}
	system, msgs, merr := buildMessages(req.Messages)
	if merr != nil {
		return nil, merr
	}
	maxTokens := defaultMaxTokens
	if req.Options.MaxTokens != nil && *req.Options.MaxTokens > 0 {
		maxTokens = *req.Options.MaxTokens
	}

	body := &messagesRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  msgs,
		Stream:    true,
	}
	if len(req.Tools) > 0 {
		body.Tools = buildTools(req.Tools)
		body.ToolChoice = &toolChoice{Type: toolChoiceAuto}
	}

	// thinking：启用时**不下发** temperature / top_p（Anthropic 不接受与扩展思考同用）；
	// 预算放不进 max_tokens（max_tokens 过小）→ 省略 thinking（软降级，请求仍可用）。
	thinkingOn := false
	if req.Options.Reasoning != nil {
		if budget, ok := thinkingBudget(req.Options.Reasoning.Effort, maxTokens); ok {
			body.Thinking = &thinking{Type: thinkingEnabled, BudgetTokens: budget}
			thinkingOn = true
		}
	}
	if !thinkingOn {
		body.Temperature = req.Options.Temperature
		body.TopP = req.Options.TopP
	}
	return body, nil
}

// thinkingBudget 按思考档位给出 `budget_tokens`：high/medium/low（空档按 medium），并夹到
// `max_tokens - 1`；夹后不足最小预算 → ok=false（调用方省略 thinking）。
func thinkingBudget(effort string, maxTokens int) (int, bool) {
	budget := defaultThinkingBudget
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "high":
		budget = thinkingBudgetHigh
	case "low":
		budget = thinkingBudgetLow
	case "medium":
		budget = thinkingBudgetMedium
	}
	if budget > maxTokens-1 {
		budget = maxTokens - 1
	}
	if budget < minThinkingBudget {
		return 0, false
	}
	return budget, true
}

// buildMessages 把 canonical 消息映射为 wire 消息，并返回顶层 `system` 文本（多段以空行拼接）。
//
// 角色映射：system → 顶层 system；assistant → assistant；tool → **user**（tool_result 块）；
// 其余（含 user）→ user。相邻同角色合并（tool_result 并入 user 后保持交替语义）；
// 空内容块不发；无任何可发消息 / 首条非 user → invalid（Anthropic 要求，明确报错不擅自改造）。
func buildMessages(msgs []canon.Message) (string, []wireMessage, *canon.Error) {
	var systemParts []string
	out := make([]wireMessage, 0, len(msgs))
	for i := range msgs {
		m := msgs[i]
		switch m.Role {
		case canon.RoleSystem:
			if text := partsText(m.Content); text != "" {
				systemParts = append(systemParts, text)
			}
		case canon.RoleAssistant:
			blocks, err := assistantBlocks(m)
			if err != nil {
				return "", nil, err
			}
			out = appendBlocks(out, roleAssistant, blocks...)
		case canon.RoleTool:
			out = appendBlocks(out, roleUser, contentBlock{
				Type:      blockToolResult,
				ToolUseID: m.ToolCallID,
				Content:   partsText(m.Content),
			})
		default: // user（及未知角色）→ user 消息
			blocks, err := userBlocks(m)
			if err != nil {
				return "", nil, err
			}
			out = appendBlocks(out, roleUser, blocks...)
		}
	}
	if len(out) == 0 {
		return "", nil, canon.NewError(canon.ErrorInvalid, "无可发送的消息（system 之外全部为空）")
	}
	if out[0].Role != roleUser {
		return "", nil, canon.NewError(canon.ErrorInvalid, "Anthropic 要求 messages 以 user 角色起首")
	}
	return strings.Join(systemParts, "\n\n"), out, nil
}

// appendBlocks 追加内容块：与上一条同角色 → 合并进该条；空块序列 → 不产生消息
// （Anthropic 拒绝空 content）。
func appendBlocks(msgs []wireMessage, role string, blocks ...contentBlock) []wireMessage {
	if len(blocks) == 0 {
		return msgs
	}
	if n := len(msgs); n > 0 && msgs[n-1].Role == role {
		msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
		return msgs
	}
	return append(msgs, wireMessage{Role: role, Content: blocks})
}

// userBlocks 构造 user 侧内容块（text / image；空块不发）。
func userBlocks(m canon.Message) ([]contentBlock, *canon.Error) {
	blocks := make([]contentBlock, 0, len(m.Content))
	for _, p := range m.Content {
		switch {
		case p.Image != nil:
			mediaType, data, ok := parseDataURL(p.Image.DataURL)
			if !ok {
				return nil, canon.NewError(canon.ErrorInvalid, "图片块的 DataURL 非法（需 data:<mime>;base64,…）")
			}
			blocks = append(blocks, contentBlock{
				Type:   blockImage,
				Source: &imageSource{Type: imageSourceBase64, MediaType: mediaType, Data: data},
			})
		case p.Text != "":
			blocks = append(blocks, contentBlock{Type: blockText, Text: p.Text})
		}
	}
	return blocks, nil
}

// assistantBlocks 构造 assistant 侧内容块：thinking + text + tool_use。
//
// 思考链（`Message.Reasoning`）**回传需配对签名**（D-31，Anthropic 扩展思考）：`ReasoningSignature`
// 非空才发 `thinking` 块（缺签名的 thinking 块会被上游 400 拒），且思考块须在正文之前；仅有
// `Reasoning` 而无签名 → 不发（宁可少回传，不让整轮请求失败）。
func assistantBlocks(m canon.Message) ([]contentBlock, *canon.Error) {
	blocks := make([]contentBlock, 0, len(m.Content)+len(m.ToolCalls)+1)
	if m.Reasoning != "" && m.ReasoningSignature != "" {
		blocks = append(blocks, contentBlock{
			Type:      contentBlockThinking,
			Thinking:  m.Reasoning,
			Signature: m.ReasoningSignature,
		})
	}
	if text := partsText(m.Content); text != "" {
		blocks = append(blocks, contentBlock{Type: blockText, Text: text})
	}
	for _, c := range m.ToolCalls {
		input := json.RawMessage(strings.TrimSpace(c.Arguments))
		if len(input) == 0 {
			input = json.RawMessage(emptyInput)
		}
		if !json.Valid(input) {
			return nil, canon.NewError(canon.ErrorProtocol,
				"assistant tool_call "+c.Name+" 的 arguments 非合法 JSON: "+c.Arguments)
		}
		blocks = append(blocks, contentBlock{Type: blockToolUse, ID: c.ID, Name: c.Name, Input: input})
	}
	return blocks, nil
}

// buildTools 把工具定义映射为 wire 形态（schema → `input_schema`，缺省补空对象 schema）。
func buildTools(defs []canon.ToolDef) []wireTool {
	out := make([]wireTool, 0, len(defs))
	for _, d := range defs {
		schema := d.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, wireTool{Name: d.Name, Description: d.Description, InputSchema: schema})
	}
	return out
}

// partsText 拼接文本块（图片块与空块跳过）。
func partsText(parts []canon.Part) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Image == nil && p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// parseDataURL 拆 `data:<mime>;base64,<data>`；非 base64 data URL → ok=false。
func parseDataURL(s string) (mediaType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(s, prefix) {
		return "", "", false
	}
	meta, payload, found := strings.Cut(strings.TrimPrefix(s, prefix), ",")
	if !found || !strings.HasSuffix(meta, ";base64") {
		return "", "", false
	}
	mediaType = strings.TrimSuffix(meta, ";base64")
	if mediaType == "" || payload == "" {
		return "", "", false
	}
	return mediaType, payload, true
}
