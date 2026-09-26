// 声明式降级（LR-7）—— 请求用到 provider 不支持的能力时的**显式**处置，纯标准库实现。
//
// 策略表（**硬 / 软的判据**：会使请求语义改变或丢内容的 → 硬，明确报错；仅影响采样细节、省略即
// 回落上游默认的 → 软，就地剔除**且必须在本表可读**，绝不静默丢字段）：
//
//	能力            触发条件                        caps 为 false 时
//	Tools           req.Tools 非空                  硬：Error{invalid}（报错，不降级）
//	Images          消息含 image 块                 硬：Error{invalid}（报错，不降级）
//	Reasoning       Options.Reasoning 非空          软：清零（省略字段 → 上游默认）
//	ReasoningEffort Options.Reasoning 非空          软：清零（同上；仅 Anthropic 支持档位）
//	TopP            Options.TopP 非空               软：清零
//	MaxTokensRequired Options.MaxTokens 为空        caps 为 true → 各适配器补缺省（见 defaultMaxTokens）
//	UsageInStream   —                               caps 为 false → 编码器不下发 stream_options
//	Stream          —                               非流式协议（Caps.Stream=false）不适配本 Router 契约
//	                                                （Router 只提供流式 Call）→ 由适配器自报，不在本表
//	ToolChoice / ParallelToolCalls                  canonical `Request` 无对应字段（只表达必需项）→ 不参与降级
//
// 适配器在自己的 `Stream` 入口调用本函数；返回 nil 表示请求已按 caps 可发。
package adaptor

import "github.com/chonkpilot/chonkpilot-router/internal/canon"

// Degrade 按 caps 检查并就地剔除（软）请求中不支持的能力；硬不支持 → 返回分类错误。
// req 为适配器 `Stream` 的**入参副本**（canon.Request 按值传入），故就地清零不会影响调用方。
func Degrade(caps canon.Caps, req *canon.Request) *canon.Error {
	if !caps.Tools && len(req.Tools) > 0 {
		return canon.NewError(canon.ErrorInvalid, "该 provider 协议不支持工具调用（tools）")
	}
	if !caps.Images && hasImage(req.Messages) {
		return canon.NewError(canon.ErrorInvalid, "该 provider 协议不支持图片（多模态输入）")
	}
	if req.Options.Reasoning != nil && (!caps.Reasoning || !caps.ReasoningEffort) {
		req.Options.Reasoning = nil // 软降级：省略思考参数（不回传错误、不静默改造消息）
	}
	if req.Options.TopP != nil && !caps.TopP {
		req.Options.TopP = nil // 软降级：省略 top_p
	}
	return nil
}

// hasImage 报告消息序列中是否含图片块。
func hasImage(msgs []canon.Message) bool {
	for _, m := range msgs {
		for _, p := range m.Content {
			if p.Type == canon.PartImage || p.Image != nil {
				return true
			}
		}
	}
	return false
}
