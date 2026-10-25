package bridge

import (
	"encoding/json"
	"sync"

	transport "github.com/lsong98sh/chonkpilot-plugin-framework/protocol"
)

// Filter 是订阅过滤键；空 / "*" = 任意。
type Filter struct {
	InstanceID string `json:"instance_id,omitempty"`
	ProjectID  string `json:"project_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Direction  string `json:"direction,omitempty"`
}

// Subscription 是一条订阅声明。v1 只有 async 观察。
type Subscription struct {
	ID      string `json:"id,omitempty"`
	Pattern string `json:"pattern"`
	Mode    string `json:"mode,omitempty"`
	Filter  Filter `json:"filter,omitempty"`
}

// FilterRegistration 是 before-hook 注册项（执行链在 B1 落地）。
type FilterRegistration struct {
	Stage    string   `json:"stage"`
	Priority int      `json:"priority,omitempty"`
	Fields   []string `json:"fields,omitempty"`
	ID       string   `json:"id,omitempty"`
}

// Session 是一条插件连接 + 身份 + 订阅表。
type Session struct {
	srv  *Server
	conn transport.Conn

	mu         sync.RWMutex
	pluginID   string
	instanceID string
	features   map[string]any
	subs       []Subscription
	filters    []FilterRegistration

	outQ      chan transport.Frame
	reqQ      chan transport.Frame
	done      chan struct{}
	closeOnce sync.Once
}

func newSession(srv *Server, c transport.Conn) *Session {
	return &Session{
		srv:  srv,
		conn: c,
		outQ: make(chan transport.Frame, 256),
		reqQ: make(chan transport.Frame, 64),
		done: make(chan struct{}),
		subs: nil,
	}
}

// PluginID 返回插件自报 id（hello 前为空）。
func (s *Session) PluginID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pluginID
}

// InstanceID 返回服务端分配的插件实例 id。
func (s *Session) InstanceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.instanceID
}

// Features 返回插件注册特性（只读快照）。
func (s *Session) Features() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]any, len(s.features))
	for k, v := range s.features {
		out[k] = v
	}
	return out
}

// Emit 向插件推一条事件（有界队列；满则丢帧，不阻塞总线）。
func (s *Session) Emit(name string, payload any) {
	raw := rawJSON(payload)
	f := transport.Frame{Kind: transport.KindEvt, Name: name, Payload: raw}
	select {
	case s.outQ <- f:
	default:
		s.srv.logf("pluginbridge: 慢插件 %s，丢弃事件 %s", s.PluginID(), name)
	}
}

// Ctl 向插件推一条控制帧（与 Emit 同结构、同一有界队列；满则丢帧，不阻塞总线）。
func (s *Session) Ctl(name string, payload any) {
	raw := rawJSON(payload)
	f := transport.Frame{Kind: transport.KindCtl, Name: name, Payload: raw}
	select {
	case s.outQ <- f:
	default:
		s.srv.logf("pluginbridge: 慢插件 %s，丢弃控制帧 %s", s.PluginID(), name)
	}
}

func (s *Session) serve() {
	go s.writer()
	go s.worker()
	for {
		f, err := s.conn.Recv()
		if err != nil {
			s.Close()
			return
		}
		if f.Kind != transport.KindReq {
			continue
		}
		select {
		case s.reqQ <- f:
		case <-s.done:
			return
		default:
			s.srv.logf("pluginbridge: 插件 %s 请求队列满，拒绝 %s", s.PluginID(), f.Name)
			_ = s.conn.Send(transport.Frame{
				Kind: transport.KindRes, ID: f.ID,
				Errors: []string{CodePluginDisabled + ": request queue full"},
			})
		}
	}
}

// maxConcurrentRequests 是单个插件会话**在飞**请求上限（并发派发）。
//
// 2026-10-24（memory/history 外挂为独立 exe 插件）：本会话此前由**单个** worker 串行处理
// 插件发来的每个请求 → 插件侧原本并行的多次调用（如 memory 按类别并行读记忆 + 重写）
// 在宿主侧被串成一条线，后发调用会先耗尽自身 ctx 预算（llm-simple 60s）→ 与进程内语义不等价。
// 这里改为**并发派发**（上限 32 = 与 reqQ 同量级，避免无界 goroutine）：
// 每个请求独立执行、结果按帧 ID 各自回写；插件顺序调用（上一个 Call 返回后再发下一个）时
// 行为与串行完全一致（并发只发生在插件**自己**并发发起的场景）。
const maxConcurrentRequests = 32

func (s *Session) worker() {
	sem := make(chan struct{}, maxConcurrentRequests)
	for {
		select {
		case <-s.done:
			return
		case f := <-s.reqQ:
			select {
			case sem <- struct{}{}:
				go func(f transport.Frame) {
					defer func() { <-sem }()
					s.handle(f)
				}(f)
			case <-s.done:
				return
			}
		}
	}
}

func (s *Session) writer() {
	for {
		select {
		case <-s.done:
			return
		case f := <-s.outQ:
			if err := s.conn.Send(f); err != nil {
				s.Close()
				return
			}
		}
	}
}

func (s *Session) handle(f transport.Frame) {
	res := transport.Frame{Kind: transport.KindRes, ID: f.ID}
	v, err := s.srv.dispatch(s.srv.ctx, s, f.Name, f.Payload)
	if err != nil {
		res.Errors = []string{err.Error()}
		s.srv.audit(s, f.Name, false, codeOf(err))
	} else {
		res.OK = true
		res.Result = v
		s.srv.audit(s, f.Name, true, "")
	}
	_ = s.conn.Send(res)
	if f.Name == "plugin.exit" {
		s.Close()
	}
}

// matches 判定一条总线事件是否应投递给本会话。
func (s *Session) matches(subject string, payload []byte) bool {
	env := parseEnvelope(payload)
	s.mu.RLock()
	subs := s.subs
	s.mu.RUnlock()
	for _, sub := range subs {
		if sub.Mode != "" && sub.Mode != "async" {
			continue
		}
		if !matchSubject(sub.Pattern, subject) {
			continue
		}
		if !filterMatch(sub.Filter, env) {
			continue
		}
		return true
	}
	return false
}

// Close 关闭会话（幂等）：断连、注销、停止 worker。
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
		s.srv.unbindToolSession(s)
		s.srv.removeSession(s)
	})
}

type envelope struct {
	InstanceID string `json:"instance_id"`
	ProjectID  string `json:"project_id"`
	SessionID  string `json:"session_id"`
	Direction  string `json:"direction"`
	Src        string `json:"src"`
}

func parseEnvelope(payload []byte) envelope {
	var e envelope
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &e)
	}
	if e.Direction == "" {
		if e.Src == "gui" {
			e.Direction = "gui"
		} else {
			e.Direction = "server"
		}
	}
	return e
}

func filterMatch(f Filter, e envelope) bool {
	if !keyMatch(f.InstanceID, e.InstanceID) {
		return false
	}
	if !keyMatch(f.ProjectID, e.ProjectID) {
		return false
	}
	if !keyMatch(f.SessionID, e.SessionID) {
		return false
	}
	if !keyMatch(f.Direction, e.Direction) {
		return false
	}
	return true
}

func keyMatch(want, got string) bool {
	return want == "" || want == "*" || want == got
}

func rawJSON(v any) json.RawMessage {
	switch p := v.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return p
	case []byte:
		return p
	case string:
		return json.RawMessage(p)
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return nil
		}
		return b
	}
}
