package pluginbridge

import (
	"context"
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// mqMethod 是「对外方法 → 对内既有 MQ 主题」的映射。
type mqMethod struct {
	Method string
	Topic  string
}

// defaultMQMethods 是 v1 开放清单（对齐 61 主题；命名见设计方案 §4.3 / §11.7）。
// 只做白名单映射：未列出的方法不可达，不暴露任意主题。
var defaultMQMethods = []mqMethod{
	// LLM / 会话
	{"plugin/v1/llm.step", "llm-simple"},
	{"plugin/v1/session.start", "session-start"},
	{"plugin/v1/session.send", "session-send"},
	{"plugin/v1/session.cancel", "session-cancel"},
	{"plugin/v1/session.list", "data-session-list"},
	{"plugin/v1/session.get", "data-session-get"},
	{"plugin/v1/session.history", "data-session-history"},
	{"plugin/v1/session.content", "data-session-content"},
	{"plugin/v1/session.snapshot", "data-snapshot-get"},
	{"plugin/v1/session.rename", "data-session-title"},
	{"plugin/v1/session.delete", "data-session-delete"},
	{"plugin/v1/session.active", "data-session-active-set"},
	// 中途插话 inbox
	{"plugin/v1/session.inbox.add", "llm-inbox-add"},
	{"plugin/v1/session.inbox.list", "llm-inbox-list"},
	{"plugin/v1/session.inbox.discard", "llm-inbox-discard"},
	{"plugin/v1/session.inbox.promote", "llm-inbox-promote"},
	// 任务
	{"plugin/v1/task.tree", "data-tasktree-list"},
	{"plugin/v1/task.tasks", "data-tasktree-tasks"},
	{"plugin/v1/task.stop", "task-stop"},
	{"plugin/v1/task.background", "task-background"},
	{"plugin/v1/task.verify", "task-verify"},
	{"plugin/v1/tool.retry", "tool-retry"},
	{"plugin/v1/ask.reply", "ask-user-reply"},
	// 配置 / 只读
	{"plugin/v1/config.user.get", "data-user-config"},
	{"plugin/v1/config.project.get", "data-prj-config"},
	{"plugin/v1/config.filelist.list", "data-filelist"},
	{"plugin/v1/config.memory.list", "data-memory"},
	{"plugin/v1/config.mcp.list", "data-mcp"},
	{"plugin/v1/config.scenario.list", "data-scenario"},
	{"plugin/v1/config.prompt.get", "data-prompt"},
	{"plugin/v1/knowledge.list", "data-knowledge-list"},
	{"plugin/v1/knowledge.read", "data-knowledge-read"},
	// 文件
	{"plugin/v1/file.list", "filesys.list"},
	{"plugin/v1/file.read", "filesys.content"},
	{"plugin/v1/file.write", "filesys.create"},
	{"plugin/v1/file.mkdir", "filesys.mkdir"},
	{"plugin/v1/file.remove", "filesys.remove"},
	{"plugin/v1/file.rename", "filesys.rename"},
	{"plugin/v1/file.copy", "filesys.copy"},
	// 工具 / server
	{"plugin/v1/tools.list", "mcp-tools-list"},
	{"plugin/v1/tools.call", "mcp-tools-call"},
	{"plugin/v1/tools.wait", "mcp-tools-wait"},
	{"plugin/v1/tools.timeout", "mcp-tools-timeout"},
	{"plugin/v1/tools.register", "mcp-tools-register"},
	{"plugin/v1/tools.unregister", "mcp-tools-unregister"},
	// 资产（只读）：gateway 节点原语 / 注册资产（BRIDGE-2 T7：外部元工具 mcp_find/mcp_load 取数）。
	// prompts = prompt/skill 原语（skill 的 type 由条目 type 区分）；resources = 资源原语。
	{"plugin/v1/prompts.list", "mcp-prompts-list"},
	{"plugin/v1/prompts.get", "mcp-prompts-get"},
	{"plugin/v1/resources.list", "mcp-resources-list"},
	{"plugin/v1/resources.read", "mcp-resources-read"},
	{"plugin/v1/servers.list", "mcp-servers-list"},
	{"plugin/v1/servers.get", "mcp-servers-get"},
	{"plugin/v1/servers.register", "mcp-servers-register"},
	{"plugin/v1/servers.unregister", "mcp-servers-unregister"},
	{"plugin/v1/gateway.check", "mcp-gateway-check"},
	// 插件运维
	{"plugin/v1/memory.flush", "memory.flush"},
	{"plugin/v1/vfts.reindex", "vfts.reindex"},
	{"plugin/v1/vfts.dict.set", "vfts.dict.set"},
	{"plugin/v1/prompt.optimise", "prompt-optimise"},
}

// commandMethods 是「命令类」方法：默认不等待完成（ack 后由事件回传），
// 带 `request_id` 时仍等待结果（便于调用方拿校验错误）。
var commandMethods = map[string]bool{
	"plugin/v1/session.start":         true,
	"plugin/v1/session.send":          true,
	"plugin/v1/session.cancel":        true,
	"plugin/v1/session.rename":        true,
	"plugin/v1/session.delete":        true,
	"plugin/v1/session.active":        true,
	"plugin/v1/session.inbox.add":     true,
	"plugin/v1/session.inbox.discard": true,
	"plugin/v1/session.inbox.promote": true,
	"plugin/v1/task.stop":             true,
	"plugin/v1/task.background":       true,
	"plugin/v1/task.verify":           true,
	"plugin/v1/tool.retry":            true,
	"plugin/v1/ask.reply":             true,
	"plugin/v1/file.write":            true,
	"plugin/v1/file.mkdir":            true,
	"plugin/v1/file.remove":           true,
	"plugin/v1/file.rename":           true,
	"plugin/v1/file.copy":             true,
	"plugin/v1/tools.call":            true,
	"plugin/v1/tools.register":        true,
	"plugin/v1/tools.unregister":      true,
	"plugin/v1/servers.register":      true,
	"plugin/v1/servers.unregister":    true,
	"plugin/v1/memory.flush":          true,
	"plugin/v1/vfts.reindex":          true,
	"plugin/v1/vfts.dict.set":         true,
}

// asyncEmitter 是 mq 内部总线的可选异步派发能力。
type asyncEmitter interface {
	EmitAsync(ctx context.Context, subject string, payload any)
}

// registerMQCapabilities 把默认映射注册进 Registry。
func registerMQCapabilities(reg *Registry, bus mq.Bus) {
	if bus == nil {
		return
	}
	for _, m := range defaultMQMethods {
		m := m
		command := commandMethods[m.Method]
		reg.Register(m.Method, func(ctx context.Context, _ *Session, args json.RawMessage) (any, error) {
			// 命令类且未带 request_id → 两次 publish：立即 ack，完成/错误走事件主题。
			if command && !hasRequestID(args) {
				if ae, ok := bus.(asyncEmitter); ok {
					ae.EmitAsync(ctx, m.Topic, args)
				} else {
					go func() { _ = bus.Emit(ctx, m.Topic, args) }()
				}
				return map[string]any{"accepted": true, "topic": m.Topic}, nil
			}
			v := bus.Emit(ctx, m.Topic, args).Wait()
			if err := v.Err(); err != nil {
				return nil, &Error{Code: CodeInternal, Msg: err.Error()}
			}
			return v.Result, nil
		})
	}
}

func hasRequestID(args json.RawMessage) bool {
	if len(args) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil {
		return false
	}
	raw, ok := m["request_id"]
	return ok && len(raw) > 0 && string(raw) != `""` && string(raw) != "null"
}
