// Package sessionmq 把 Session 组件事件映射为**既有 MQ 主题**（SESSION-COMP-1 P4）。
//
// 为什么在 `appwire`（装配层）而不是组件内：组件只发 `component.Event`，不认识主题名；
// 且 `chonkpilot-session` 的依赖方向断言（`deps_test.go`）禁止其依赖 MQ 客户端。
// 本包是**唯一映射点**（宿主装配时 `component.New(Options{Store: <持久化端口>, Sink: sessionmq.New(...)})`）。
//
// 映射（只发既有主题，不新增）：
//
//	DialogCreated  → session-new{instance_id, session_id, dialog_id}
//	SessionCreated → session-new{instance_id, session_id, parent_session_id, dialog_id}
//	TitleChanged   → data-session-title-changed{session_id, title, dialog_id?}（不带 instance_id = 全局）
//	其余事件        → 内部事件（MessageAppended / TurnOpened / TurnClosed / SnapshotSet / Finished /
//	                DialogDeleted）当前**无对应对外主题**（61 未定义）→ 不投递。
//
// 事件丢失不影响一致性（数据面为准，2G §4.3）；本包不 panic、不阻塞。
package sessionmq

import (
	"context"
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-session/component"
)

// Sink 是 component.Sink 的 MQ 实现（每实例一个：`instance_id` 随事件载荷下发）。
type Sink struct {
	bus        mq.Bus
	instanceID string
}

// New 构造 Sink（bus 为宿主实际总线；nil = 不投递）。
func New(bus mq.Bus, instanceID string) *Sink {
	return &Sink{bus: bus, instanceID: instanceID}
}

// Emit 实现 component.Sink（同步、只入队；异常不影响组件提交）。
func (s *Sink) Emit(ev component.Event) {
	if s == nil || s.bus == nil {
		return
	}
	defer func() { _ = recover() }()
	switch ev.Kind {
	case component.EventDialogCreated:
		s.emit(msgkeys.TopicSessionNew, map[string]any{
			"instance_id": s.instanceID,
			"session_id":  ev.SessionID,
			"dialog_id":   ev.DialogID,
		})
	case component.EventSessionCreated:
		payload := map[string]any{
			"instance_id": s.instanceID,
			"session_id":  ev.SessionID,
			"dialog_id":   ev.DialogID,
		}
		if ev.ParentID != "" {
			payload["parent_session_id"] = ev.ParentID
		}
		s.emit(msgkeys.TopicSessionNew, payload)
	case component.EventTitleChanged:
		payload := map[string]any{"session_id": ev.SessionID, "title": ev.Title}
		if ev.DialogID != "" {
			payload["dialog_id"] = ev.DialogID
		}
		s.emit(msgkeys.TopicDataSessionTitleChanged, payload)
	}
}

// emit 序列化并发布（失败静默：事件非一致性来源）。
func (s *Sink) emit(subject string, payload map[string]any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = s.bus.Emit(context.Background(), subject, b)
}
