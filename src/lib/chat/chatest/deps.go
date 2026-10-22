// 便捷宿主装配（MOD-1 M8-C2）：一次构造全套内存 fake，减少测试样板。
package chatest

import (
	"io"
	"log/slog"

	chat "github.com/chonkpilot/chonkpilot-chat"
)

// Deps 是一整套内存 fake 宿主（字段可直接用于断言）。
type Deps struct {
	InstanceID string
	SessionID  string
	Context    *Context
	Store      *Store
	Sink       *Sink
	Clock      *Clock
	Inbox      *Inbox
	LLM        *LLM
	Tools      *Tools
	Catalog    *Catalog
}

// NewDeps 构造整套 fake 宿主：固定上下文 + 空 Store/Sink/Inbox/工具/目录 +
// 按调用序号预置的 LLM 脚本（scripts 可为空 → 每次 Step 返回 done(stop)）。
func NewDeps(instanceID, sessionID, systemPrompt string, messages []chat.Message, scripts ...[]chat.StepEvent) *Deps {
	return &Deps{
		InstanceID: instanceID,
		SessionID:  sessionID,
		Context:    NewContext(systemPrompt, messages),
		Store:      NewStore(),
		Sink:       NewSink(),
		Clock:      NewClock(),
		Inbox:      NewInbox(),
		LLM:        NewLLM(scripts...),
		Tools:      NewTools(),
		Catalog:    &Catalog{},
	}
}

// Engine 用本套 fake 装配 chat 引擎（Logger 丢弃，测试输出保持干净）。
func (d *Deps) Engine(cfg chat.RuntimeConfig) *chat.Runtime {
	return chat.New(chat.Deps{
		InstanceID: d.InstanceID,
		SessionID:  d.SessionID,
		Context:    d.Context,
		Store:      d.Store,
		Sink:       d.Sink,
		Tools:      d.Tools,
		Catalog:    d.Catalog,
		LLM:        d.LLM,
		Clock:      d.Clock,
		Inbox:      d.Inbox,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config:     cfg,
	})
}
