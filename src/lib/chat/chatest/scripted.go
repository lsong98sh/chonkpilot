// 脚本化 LLM / 工具 / 目录（MOD-1 M8-C2）：按调用序号或 CallID / Name 返回预置结果。
package chatest

import (
	"context"
	"sync"

	chat "github.com/chonkpilot/chonkpilot-chat"
)

// LLM 是脚本化 LLMClient：第 n 次 Step 返回 Scripts[n]（缺省 → done(stop)）；
// Errs[n] 非 nil 时该次调用直接返回错误。每次 StepRequest 按序记录。
type LLM struct {
	mu       sync.Mutex
	scripts  [][]chat.StepEvent
	errs     []error
	requests []chat.StepRequest
}

// NewLLM 按调用序号预置 step 脚本（缺省调用返回 done(stop)）。
func NewLLM(scripts ...[]chat.StepEvent) *LLM { return &LLM{scripts: scripts} }

// SetError 让第 idx 次（0 基）Step 返回错误。
func (l *LLM) SetError(idx int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for len(l.errs) <= idx {
		l.errs = append(l.errs, nil)
	}
	l.errs[idx] = err
}

// Step 返回预置流（缓冲满后关闭，同步可读）。
func (l *LLM) Step(_ context.Context, req chat.StepRequest, _ chat.StepOptions) (<-chan chat.StepEvent, error) {
	l.mu.Lock()
	l.requests = append(l.requests, req)
	idx := len(l.requests) - 1
	var script []chat.StepEvent
	if idx < len(l.scripts) {
		script = l.scripts[idx]
	}
	var stepErr error
	if idx < len(l.errs) {
		stepErr = l.errs[idx]
	}
	l.mu.Unlock()

	if stepErr != nil {
		return nil, stepErr
	}
	ch := make(chan chat.StepEvent, len(script)+1)
	if len(script) == 0 {
		ch <- chat.StepEvent{Type: chat.StepEventDone, Finish: "stop"}
	} else {
		for _, ev := range script {
			ch <- ev
		}
	}
	close(ch)
	return ch, nil
}

// Requests 返回收到的 StepRequest 快照（顺序 = 调用序号）。
func (l *LLM) Requests() []chat.StepRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]chat.StepRequest(nil), l.requests...)
}

// Text 构造「文本增量 + done(finish)」脚本。
func Text(text, finish string) []chat.StepEvent {
	return []chat.StepEvent{
		{Type: chat.StepEventDelta, Text: text},
		{Type: chat.StepEventDone, Finish: finish},
	}
}

// ToolStep 构造「单个工具调用 + done(tool_calls)」脚本。
func ToolStep(call chat.ToolCall) []chat.StepEvent {
	return []chat.StepEvent{
		{Type: chat.StepEventToolCall, ToolCall: &call},
		{Type: chat.StepEventDone, Finish: "tool_calls", ToolCalls: []chat.ToolCall{call}},
	}
}

// Tools 是脚本化 ToolInvoker：按 CallID → Name → Default 取 outcome；记录每次调用。
type Tools struct {
	mu      sync.Mutex
	byCall  map[string]chat.ToolOutcome
	byName  map[string]chat.ToolOutcome
	Default chat.ToolOutcome
	calls   []chat.ToolCall
}

// NewTools 构造空脚本工具。
func NewTools() *Tools {
	return &Tools{byCall: map[string]chat.ToolOutcome{}, byName: map[string]chat.ToolOutcome{}}
}

// OnCall 按 CallID 预置 outcome。
func (t *Tools) OnCall(callID string, outcome chat.ToolOutcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byCall == nil {
		t.byCall = map[string]chat.ToolOutcome{}
	}
	t.byCall[callID] = outcome
}

// OnName 按工具名预置 outcome（CallID 未命中时生效）。
func (t *Tools) OnName(name string, outcome chat.ToolOutcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byName == nil {
		t.byName = map[string]chat.ToolOutcome{}
	}
	t.byName[name] = outcome
}

// Dispatch 按脚本返回 outcome（未命中 → Default，零值 = 空内容同步成功）。
func (t *Tools) Dispatch(_ context.Context, call chat.ToolCall) (chat.ToolOutcome, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, call)
	if o, ok := t.byCall[call.ID]; ok {
		return o, nil
	}
	if o, ok := t.byName[call.Name]; ok {
		return o, nil
	}
	return t.Default, nil
}

// Cancel 恒返回未命中（测试不需要取消语义）。
func (t *Tools) Cancel(context.Context, string, string) (bool, error) { return false, nil }

// Detach 恒返回空 op id（测试不需要 detach 语义）。
func (t *Tools) Detach(context.Context, string, string) (string, error) { return "", nil }

// Calls 返回 Dispatch 调用快照（顺序）。
func (t *Tools) Calls() []chat.ToolCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]chat.ToolCall(nil), t.calls...)
}

// Catalog 是静态工具目录（Servers 恒空：服务目录属宿主管理面）。
type Catalog struct {
	Specs []chat.ToolSpec
}

// Servers 恒返回空（chat 会话端口不承载服务目录）。
func (c *Catalog) Servers(context.Context, string) ([]chat.ServerInfo, error) { return nil, nil }

// Tools 返回静态工具列表。
func (c *Catalog) Tools(context.Context, string) ([]chat.ToolSpec, error) { return c.Specs, nil }
