// 流式 tool_call 增量聚合器（LR-6）—— 按 `index` 聚合，流结束才定稿。
//
// # 反面错法（本实现逐条规避；改动前先读这段，四条都踩过坑）
//
//  1. **按 `ID` 聚合** —— `id` 本身可能分片（首片可能为空、后续片才补齐），按 ID 匹配会漏并
//     把并行调用串成一条；本实现**只按 `index`**，`id` 仅在「首个非空值」落位。
//  2. **分片一到就 `json.Unmarshal`** —— `arguments` 会断在 JSON 中间（`{"path":"a.tx` / `t"}`），
//     一片一片解析必然失败；本实现只在 `Complete()`（流结束）时校验 JSON。
//  3. **空 `name` 覆盖已到 `name`** —— 后续分片只带 `arguments`（`name`/`id` 为空），无脑赋值会把
//     已落位的函数名冲成空串；本实现只在非空时覆盖。
//  4. **忽略 `index`** —— 并行工具调用（index 0/1）交叉到达，忽略 index 会把两个调用的参数拼在
//     一起；本实现每个 index 独立累加，结束时**按 `index` 升序**输出（与 docs/spec/40-roadmap/
//     40-演进计划.md §LR §6-1 一致；不依赖到达顺序 → 输出稳定）。
//
// # 输出形状（与 LR-5 冻结的七类事件对齐）
//
// D-32 语义收口：`*_delta` 一律只表示**增量**，**完整值**用 `tool_call`（`EvToolCall`）承载。
// 故**拼装完成**的工具调用以 `tool_call` 事件承载完整 `ToolCall{ID, Name, Arguments, Index}`：
// `Event{Type: EvToolCall, ToolCallFull: &ToolCall{…}}`。片段级事件**不发**（避免同一个调用发两条）。
// `EvToolCallDelta` / `ToolCallDelta` 仅保留「增量」类型语义（本批适配器不产生片段事件）。
package adaptor

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// emptyArguments 是「无参数工具调用」的定稿值（模型可能一个 arguments 分片都不发）。
const emptyArguments = "{}"

// ToolCallAccumulator 按 `index` 聚合流式 tool_call 增量。
//
// 非并发安全：一个流一个实例（适配器在 `Stream` 内自建）。
type ToolCallAccumulator struct {
	accs map[int]*toolCallAcc
}

// toolCallAcc 是单个 index 的累加态。
type toolCallAcc struct {
	id   string
	name string
	args string
}

// NewToolCallAccumulator 构造聚合器。
func NewToolCallAccumulator() *ToolCallAccumulator {
	return &ToolCallAccumulator{accs: map[int]*toolCallAcc{}}
}

// Add 合并一个增量分片：`id` / `name` 仅非空时覆盖；`arguments` 一律**追加**。
func (a *ToolCallAccumulator) Add(index int, id, name, argumentsDelta string) {
	acc := a.accs[index]
	if acc == nil {
		acc = &toolCallAcc{}
		a.accs[index] = acc
	}
	if id != "" {
		acc.id = id
	}
	if name != "" {
		acc.name = name
	}
	acc.args += argumentsDelta
}

// Set 以**完整值**收口某 index 的 arguments（`.done` / `output_item.done` 等携带完整 JSON 时用）：
// 覆盖此前的片段累加结果（完整值为权威）；`arguments` 为空 → 不动（不把已落位值冲成空）；
// index 未登记 → 自动建条目。`id` / `name` 不受影响。
func (a *ToolCallAccumulator) Set(index int, arguments string) {
	if arguments == "" {
		return
	}
	acc := a.accs[index]
	if acc == nil {
		acc = &toolCallAcc{}
		a.accs[index] = acc
	}
	acc.args = arguments
}

// Empty 报告是否有任何分片（无 → 该次调用无工具调用）。
func (a *ToolCallAccumulator) Empty() bool { return len(a.accs) == 0 }

// Complete 定稿：按 `index` **升序**返回拼装完成的工具调用。`arguments` 为空 → `{}`；
// 非合法 JSON → `*canon.Error{Kind: protocol}`（明确报错，不静默交付半截参数）。
func (a *ToolCallAccumulator) Complete() ([]canon.ToolCall, *canon.Error) {
	if a.Empty() {
		return nil, nil
	}
	indices := make([]int, 0, len(a.accs))
	for idx := range a.accs {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	out := make([]canon.ToolCall, 0, len(indices))
	for _, idx := range indices {
		acc := a.accs[idx]
		args := acc.args
		if args == "" {
			args = emptyArguments
		}
		if !json.Valid([]byte(args)) {
			return nil, canon.NewError(canon.ErrorProtocol,
				"tool_call["+strconv.Itoa(idx)+"] arguments 非合法 JSON: "+args)
		}
		out = append(out, canon.ToolCall{ID: acc.id, Name: acc.name, Arguments: args, Index: idx})
	}
	return out, nil
}

// Events 返回定稿后的 tool_call 事件（每个 index 一条，类型 = `EvToolCall` 携带**完整**
// `ToolCall`；无分片 → nil）。model 回填到事件（与同流其他事件口径一致）。
func (a *ToolCallAccumulator) Events(model string) ([]canon.Event, *canon.Error) {
	calls, err := a.Complete()
	if err != nil || len(calls) == 0 {
		return nil, err
	}
	evs := make([]canon.Event, 0, len(calls))
	for i := range calls {
		call := calls[i] // 取副本：事件持有独立指针（调用方不再依赖切片生命周期）
		evs = append(evs, canon.Event{
			Type:         canon.EvToolCall,
			Model:        model,
			ToolCallFull: &call,
		})
	}
	return evs, nil
}
