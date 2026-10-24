// subject.go —— 插件对**宿主总线主题**的「请求-应答」绑定（plugin.tool.bind 的 SDK 侧）。
//
// 背景（2026-10-24，memory/history 外挂为独立 exe 插件）：这两个插件原本是进程内 mq
// 订阅者，其中一类订阅需要**写回结果**（memory.flush 的回执、history-tool-call 的工具回调、
// history-pre-tool-hook 的同步前置打点钩子）。`Subscribe` 只有 async 观察（无应答），
// 故复用宿主**既有**能力 `plugin.tool.bind`：宿主把该 MQ 主题上的请求转发为
// `plugin.tool.call`，插件返回值写回 `v.Result`，返回 error 写回 `v.Errors`
// （语义与进程内 `mq.On` 订阅者一致：请求方 await 结果，error 即失败）。
//
// **不新增 MQ 主题**：绑定的主题名由调用方给出（= 改造前插件订阅的相对主题）。
package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
)

// toolCallRequest 是宿主 `plugin.tool.bind` 绑定的主题被请求时下发的反向请求名。
const toolCallRequest = "plugin.tool.call"

// SubjectHandler 处理一条绑定主题上的请求：返回 (结果, nil) → 写回请求方 v.Result；
// 返回 error → 请求方收到错误（进程内语义 = 订阅 handler 返回 error）。
type SubjectHandler func(ctx context.Context, payload json.RawMessage) (any, error)

// BindSubject 把一条既有 MQ 主题绑定到本插件（宿主 plugin.tool.bind）。
//
// 绑定后：宿主在该主题收到请求 → `plugin.tool.call{subject, payload}` → fn(payload) →
// `{result: <返回值>}` 写回 → 请求方拿到 v.Result；fn 返回 error → v.Errors（请求方判定失败）。
// 同一插件可绑定多条主题；重复绑定同主题以最后一次的 fn 为准（宿主侧幂等）。
func (p *Plugin) BindSubject(subject string, fn SubjectHandler) error {
	if subject == "" || fn == nil {
		return errors.New("pluginsdk: BindSubject 需要 subject 与非空 handler")
	}
	p.mu.Lock()
	if p.subjects == nil {
		p.subjects = map[string]SubjectHandler{}
	}
	p.subjects[subject] = fn
	if _, ok := p.requests[toolCallRequest]; !ok {
		p.requests[toolCallRequest] = p.handleToolCall
	}
	p.mu.Unlock()
	if _, err := p.Call(context.Background(), "plugin.tool.bind", map[string]any{"subject": subject}); err != nil {
		return err
	}
	return nil
}

// handleToolCall 处理宿主经 plugin.tool.bind 转发的主题请求（按 subject 分派到已绑定的 fn）。
func (p *Plugin) handleToolCall(_ context.Context, in map[string]any) (any, error) {
	subject, _ := in["subject"].(string)
	p.mu.Lock()
	fn := p.subjects[subject]
	p.mu.Unlock()
	if fn == nil {
		return nil, errors.New("subject_not_bound: " + subject)
	}
	var body json.RawMessage
	if raw, ok := in["payload"]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		body = b
	}
	v, err := fn(context.Background(), body)
	if err != nil {
		return nil, err
	}
	return map[string]any{"result": v}, nil
}
