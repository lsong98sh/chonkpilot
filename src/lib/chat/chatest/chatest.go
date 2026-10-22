// Package chatest 是 chat 组件的内存测试宿主（MOD-1 M8-C2 / L2′）。
//
// 定位（专题 §3.10）：端口化的组件可以在**不引入 MQ / DB / 真实网络**的前提下跑接近
// 系统级的集成测试。本包只实现端口的内存 fake，**不接管生产**，也不被生产代码引用。
//
// 边界：只 import 标准库 + `chonkpilot-chat`；**禁止** import mq / msgkeys / router /
// gateway / data / facade / llm（组件边界断言见 chat 包的 deps_test.go 同一口径）。
//
// 能测：turn 状态机（多 step / 工具循环 / 续写）· 插话帧栈（inbox_insert / consume）·
// 异步工具回执（PendingWait）· 重放等价（事件序列 → `chat.Replay`）。
// 不能测（须宿主仓 MQ systest 覆盖）：消息形状与 61 契约 · 真实落库事务 · 窗口 / UI。
package chatest

import (
	"context"
	"sync"

	chat "github.com/chonkpilot/chonkpilot-chat"
)

// Context 是固定系统提示 + 初始消息的 ContextProvider（记录每次 Build 请求）。
type Context struct {
	SystemPrompt string
	Messages     []chat.Message
	Requests     []chat.ContextRequest
}

// NewContext 构造固定上下文。
func NewContext(systemPrompt string, messages []chat.Message) *Context {
	return &Context{SystemPrompt: systemPrompt, Messages: messages}
}

// Build 返回固定上下文（消息按副本返回，调用方修改不影响夹具）。
func (c *Context) Build(_ context.Context, req chat.ContextRequest) (chat.Context, error) {
	c.Requests = append(c.Requests, req)
	return chat.Context{
		SystemPrompt: c.SystemPrompt,
		Messages:     append([]chat.Message(nil), c.Messages...),
	}, nil
}

// Write 是一次写入记录（Kind = message / inbox；inbox 写入带 InboxID）。
type Write struct {
	Kind    string
	InboxID string
	Message chat.Message
}

// Store 是内存 TurnStore（并发安全；快照方法返回副本）。
type Store struct {
	mu        sync.Mutex
	messages  []chat.Message
	writes    []Write
	events    []chat.TurnEvent
	inbox     []string
	committed map[string]struct{}
}

// NewStore 构造空 Store。
func NewStore() *Store { return &Store{committed: map[string]struct{}{}} }

// SaveMessage 追加并记录一条消息。
func (s *Store) SaveMessage(_ context.Context, msg chat.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	s.writes = append(s.writes, Write{Kind: "message", Message: msg})
	return nil
}

// CommitInbox 同事务落消息 + 记录 inboxID；同一 inboxID 重复调用幂等（不重复落消息）。
func (s *Store) CommitInbox(_ context.Context, inboxID string, msg chat.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed == nil {
		s.committed = map[string]struct{}{}
	}
	if _, ok := s.committed[inboxID]; ok {
		return nil
	}
	s.committed[inboxID] = struct{}{}
	s.inbox = append(s.inbox, inboxID)
	s.messages = append(s.messages, msg)
	s.writes = append(s.writes, Write{Kind: "inbox", InboxID: inboxID, Message: msg})
	return nil
}

// AppendEvent 按序记录事件（Seq 原样保存，不自动改写）。
func (s *Store) AppendEvent(_ context.Context, ev chat.TurnEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

// Messages 返回落库消息快照。
func (s *Store) Messages() []chat.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]chat.Message(nil), s.messages...)
}

// Writes 返回写入记录快照（SaveMessage / CommitInbox 的调用顺序）。
func (s *Store) Writes() []Write {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Write(nil), s.writes...)
}

// Events 返回事件日志快照（按写入顺序）。
func (s *Store) Events() []chat.TurnEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]chat.TurnEvent(nil), s.events...)
}

// InboxCommits 返回已提交的 inboxID 快照（顺序）。
func (s *Store) InboxCommits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.inbox...)
}

// Sink 是记录型 EventSink（并发安全；按调用顺序投递）。
type Sink struct {
	mu     sync.Mutex
	events []chat.Event
}

// NewSink 构造空 Sink。
func NewSink() *Sink { return &Sink{} }

// Emit 记录一条对外事件。
func (s *Sink) Emit(_ context.Context, ev chat.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

// Events 返回事件快照。
func (s *Sink) Events() []chat.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]chat.Event(nil), s.events...)
}

// Inbox 是 FIFO 中途输入队列（并发安全；Poll 非阻塞）。
type Inbox struct {
	mu    sync.Mutex
	items []chat.InboxItem
}

// NewInbox 构造空取件队列。
func NewInbox() *Inbox { return &Inbox{} }

// Push 入队一条输入。
func (i *Inbox) Push(item chat.InboxItem) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.items = append(i.items, item)
}

// Poll 取队首（空 → ok=false），非阻塞。
func (i *Inbox) Poll(context.Context) (chat.InboxItem, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.items) == 0 {
		return chat.InboxItem{}, false, nil
	}
	item := i.items[0]
	i.items = i.items[1:]
	return item, true, nil
}

// Len 返回剩余条数。
func (i *Inbox) Len() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.items)
}

// Empty 报告队列是否为空（供断言）。
func (i *Inbox) Empty() bool { return i.Len() == 0 }
