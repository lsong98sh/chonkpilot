// Package adaptor 是 router 的协议适配层（LR-3 OpenAI 兼容 / LR-4 Anthropic 在此落地）。
//
// 可见性：本包位于 `router/internal/` 之下 → **只允许 `router/**` import**；llm / gui / server /
// desktop 等消费方无法 import 适配器，只能经 router 根包的公开 API 使用。
//
// 分层：
//
//   - 本文件 = 协议适配器契约 `Adapter`（router 根包的适配器表**直接持有本接口**）；
//   - `sse.go` = 三协议共用的 SSE 行解析（`data:` 前缀 / 多行 / 跨 chunk 半包 / `[DONE]` 终止）；
//   - `errors.go` = 错误体识别（JSON `error.message` 与非 JSON / HTML 原样兜底）+ LR-8 的状态码 /
//     传输错误分类与 `Retry-After` 解析；
//   - `exchange.go` = 出网与流式读取共用件（首字节超时 / chunk 间隔超时 / 非 SSE 响应兜底）；
//   - `toolcall.go` = LR-6 按 `index` 的 tool_call 增量聚合器；
//   - `usage.go` = LR-9 的 usage 归一唯一入口（`NormalizeUsage`：三协议 → canonical `Usage`）；
//   - `degrade.go` = LR-7 声明式降级（硬不支持 → 明确报错；软不支持 → 显式省略）；
//   - `fixture.go` + `testdata/*.sse.json` = 可复用的 SSE 分片测试素材格式与加载器（各协议白盒共用）；
//   - `openai/` = OpenAI 家族两协议（`openai.go` = chat completions；`responses.go` = Responses API）；
//     `anthropic/` = `/v1/messages`；`echo/` = **内置兜底**（不发 HTTP，D-30）。
//
// **本包只 import 标准库与 `internal/canon`**（canonical 类型定义处）——**不 import router 根包**
// （否则根包持有适配器表即构成 import 环）。
package adaptor

import (
	"context"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// Adapter 是协议适配器契约（router 根包的适配器表按本接口持有与分派）。
//
// 契约：
//
//   - Stream 把事件**按序**写入 out 并返回；**不关闭 out**（由 Router 在 Stream 返回后关闭）；
//   - 适配器级失败用返回值报错：*canon.Error = 已分类（推荐），其它 error 由 Router 归为 protocol；
//   - Stream 必须响应 ctx 取消（写 out 时以 select 兼顾 ctx.Done）；
//   - 终态事件（done）在本协议流结束时**只投一次**；失败路径**不投**终态事件，改为返回错误
//     （Router 据此投唯一一条 error 事件）。
type Adapter interface {
	// Protocol 返回归一协议名（canon.ProtocolOpenAI / ProtocolResponses / ProtocolAnthropic）。
	Protocol() string
	// Caps 返回本协议的能力矩阵（LR-7 的声明式降级据此进行）。
	Caps() canon.Caps
	// Stream 组装请求 → 发送 → 解析流 → 按序写 canonical 事件到 out。
	Stream(ctx context.Context, spec canon.Spec, req canon.Request, out chan<- canon.Event) error
}

// Emit 按序写一个事件并兼顾 ctx 取消（适配器写 out 的唯一出口）。
// 消费方已离开（ctx 取消）→ 返回 protocol 类错误，**且不投事件**（Router 侧不会再投事件）。
//
// 先判 `ctx.Err()` 再 `select`：仅靠 `select` 的两路就绪是**随机**的（缓冲通道有空位 + ctx 已取消
// 时可能写入成功），会让「已取消 → 不投事件」的契约时好时坏；先判一次使该契约确定成立。
func Emit(ctx context.Context, out chan<- canon.Event, ev canon.Event) error {
	if err := ctx.Err(); err != nil {
		return canon.NewError(canon.ErrorProtocol, err.Error())
	}
	select {
	case out <- ev:
		return nil
	case <-ctx.Done():
		return canon.NewError(canon.ErrorProtocol, ctx.Err().Error())
	}
}
