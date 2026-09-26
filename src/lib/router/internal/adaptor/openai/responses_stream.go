// OpenAI Responses API 流式载荷解析（D-29）：语义化 SSE 事件 → canonical 事件所需的取值面。
//
// 说明：Responses 的 SSE 同时带 `event:` 行与 `data:` 载荷（载荷内的 `type` 与 `event:` 同名），
// 共享的 `adaptor.SSEDecoder` 只取 `data:` 载荷 → 本适配器按载荷内 `type` 分派（少一份状态）。
package openai

import (
	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// responsesEvent 是一条 Responses 流式载荷（各 `type` 取用各自字段；未用字段忽略）。
type responsesEvent struct {
	Type      string `json:"type"`
	ModelName string `json:"model"`     // 部分实现顶层回显模型名
	Delta     string `json:"delta"`     // output_text / reasoning_text / function_call_arguments 增量
	Arguments string `json:"arguments"` // function_call_arguments.done 的完整参数
	ItemID    string `json:"item_id"`   // 事件所属输出 item（function_call_arguments.* 用）
	Item      struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Response struct {
		Status            string          `json:"status"`
		Model             string          `json:"model"`
		Usage             *responsesUsage `json:"usage"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *responsesFailure `json:"error"`
	} `json:"response"`
}

// Model 取本事件携带的模型名（`response.model` 优先，回落顶层 `model`；都没有 → 空串）。
func (e responsesEvent) Model() string {
	if e.Response.Model != "" {
		return e.Response.Model
	}
	return e.ModelName
}

// itemKey 取事件所属输出 item 的定位键（`item_id` 优先，回落 `item.id`；都空 → 空串）。
// Responses 无 `index`：函数调用按 **item_id** 聚合，index 由首次出现顺序派生。
func (e responsesEvent) itemKey() string {
	if e.ItemID != "" {
		return e.ItemID
	}
	return e.Item.ID
}

// responsesFailure 是 `response.failed` 的错误详情（`code` 用于错误分类）。
type responsesFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// responsesUsage 是 Responses 的 usage 载荷（`response.completed` 内；LR-9 归一）。
type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// canon 归一到 canonical Usage（nil = 本事件无 usage）；口径与 LR-9 统一映射一致。
func (u *responsesUsage) canon() *canon.Usage {
	if u == nil {
		return nil
	}
	out := adaptor.NormalizeUsage(u.InputTokens, u.OutputTokens, u.TotalTokens,
		u.OutputTokensDetails.ReasoningTokens, u.InputTokensDetails.CachedTokens)
	return &out
}

// responsesIndex 把 Responses 的字符串定位键（item_id）映射为 canonical 的 int `Index`
// （首次出现顺序；无 `index` 的协议由本件派生，与 LR-6 聚合器对接）。
type responsesIndex struct {
	order map[string]int
	next  int
}

// newResponsesIndex 构造映射器。
func newResponsesIndex() *responsesIndex {
	return &responsesIndex{order: map[string]int{}}
}

// of 取（或分配）某 item_id 的 canonical Index；空键 → 追加到末尾（不覆盖已有条目）。
func (r *responsesIndex) of(itemID string) int {
	if idx, ok := r.order[itemID]; ok {
		return idx
	}
	idx := r.next
	r.next++
	if itemID != "" {
		r.order[itemID] = idx
	}
	return idx
}
