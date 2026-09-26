// 能力矩阵（LR-1 类型；LR-7 的声明式降级消费本结构）。
package canon

// Caps 是一个协议 / provider 的能力声明（false = 不支持，适配器据此降级或明确报错）。
type Caps struct {
	Stream            bool // 流式输出
	Tools             bool // 工具调用
	ToolChoice        bool // tool_choice 参数
	ParallelToolCalls bool // 并行工具调用
	Reasoning         bool // 思考链（reasoning / thinking）
	ReasoningEffort   bool // 思考强度档位
	Images            bool // 图片（多模态输入）
	TopP              bool // top_p 参数
	MaxTokensRequired bool // max_tokens 必填（如 Anthropic）
	UsageInStream     bool // 流内 usage
	SystemRole        bool // 独立 system 角色；false → system 提示需并入首条 user（或走协议专用字段）
}
