package uds

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Request 是一次上行请求（服务端 OnRequest 入参）。
type Request struct {
	ID      int64
	Type    string
	Payload json.RawMessage
}

// Response 是一次上行应答（`{ok,result,errors}` 信封，61 §9.1）。
type Response struct {
	OK     bool
	Result any
	Errors []string
}

// ServerOptions 是前门构造参数。
type ServerOptions struct {
	// Path 是监听路径；空 = DefaultPath()。
	Path string
	// OnRequest 处理一次上行请求并返回应答（必填）。
	OnRequest func(c *Conn, typ string, payload json.RawMessage) Response
	// OnDisconnect 连接断开（客户端退出 / 网络错误）——调用方据此清理（如补发 instance-exit）。
	OnDisconnect func(c *Conn)
	// Logf 诊断输出；nil = 不输出。
	Logf func(format string, args ...any)
}

// Server 是 UDS 前门（accept 侧）：一个监听 socket，多个连接（一条连接 = 一个实例）。
type Server struct {
	opts ServerOptions
	path string

	mu         sync.Mutex
	ln         net.Listener
	conns      map[*Conn]struct{}
	byInstance map[string]*Conn
	closed     bool
}

// NewServer 构造前门（未监听）。
func NewServer(opts ServerOptions) *Server {
	p := opts.Path
	if p == "" {
		p = DefaultPath()
	}
	return &Server{
		opts:       opts,
		path:       p,
		conns:      make(map[*Conn]struct{}),
		byInstance: make(map[string]*Conn),
	}
}

// Path 返回监听路径。
func (s *Server) Path() string { return s.path }

// Start 建立监听并接受连接（非阻塞）。
// 监听失败 → 返回错误（**仅作异常告警**，**不作**「已有 server 在跑」的单例依据 —— 见下）。
func (s *Server) Start() error {
	if s.opts.OnRequest == nil {
		return errors.New("uds: OnRequest required")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("uds: mkdir %s: %w", filepath.Dir(s.path), err)
	}
	// 清理**陈旧** socket 文件（上次异常退出遗留）。注意：Windows 下先 `os.Remove` 再
	// `net.Listen` 同一路径**重复绑定不失败**（会顶掉旧 socket 路径），故 **bind 结果不能作
	// 进程单例判据**；server 单例由 `chonkpilot-lib/procsingleton`（命名互斥体）判定。
	_ = os.Remove(s.path)
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("uds: listen %s: %w", s.path, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	go s.acceptLoop(ln)
	return nil
}

// acceptLoop 接受连接并为每条连接起读循环。
func (s *Server) acceptLoop(ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			s.logf("uds: accept: %v", err)
			return
		}
		c := &Conn{srv: s, nc: nc}
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		go c.serve()
	}
}

// Close 关闭监听与全部连接（幂等）。
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	conns := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, c := range conns {
		c.close()
	}
	_ = os.Remove(s.path)
	return nil
}

// SendEvent 把一条**下行事件**定向投递到指定实例的连接（未命中 → false）。
func (s *Server) SendEvent(instanceID, typ string, payload any) bool {
	c := s.connOf(instanceID)
	if c == nil {
		return false
	}
	return c.SendEvent(typ, payload) == nil
}

// SendControl 把一条**定向控制帧**投递到指定实例的连接（未命中 → false；不下发前端）。
func (s *Server) SendControl(instanceID, typ string, data any) bool {
	c := s.connOf(instanceID)
	if c == nil {
		return false
	}
	return c.SendControl(typ, data) == nil
}

// Connected 报告指定实例当前是否绑定有**活动连接**（只读，无副作用）。
// 判据 = 该实例 id 是否已在 连接↔实例 映射中（claim 成功时绑定、断开时解绑）。
// 用途：托盘「列举实例」区分形态 —— flash 经 UDS 前门接入（连接在）· browser 走 httpapi（无连接）。
func (s *Server) Connected(instanceID string) bool {
	return s.connOf(instanceID) != nil
}

// Broadcast 把一条**无归属事件**投递到全部连接（如 `server-starting`；61 §4.1：无 instance_id 的广播事件）。
func (s *Server) Broadcast(typ string, payload any) {
	s.mu.Lock()
	conns := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.SendEvent(typ, payload)
	}
}

// connOf 按实例 id 取连接（未绑定 / 已断开 → nil）。
func (s *Server) connOf(instanceID string) *Conn {
	if instanceID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byInstance[instanceID]
}

// bind 绑定 连接 ↔ 实例（1:1；重复绑定同 id 覆盖）。
func (s *Server) bind(c *Conn, instanceID string) {
	if instanceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 同一实例只允许一条连接：新连接顶替旧连接（旧连接由调用方决定是否关闭 —— 此处仅解绑映射）。
	s.byInstance[instanceID] = c
}

// unbind 解绑连接（断开时）。
func (s *Server) unbind(c *Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
	for id, cc := range s.byInstance {
		if cc == c {
			delete(s.byInstance, id)
		}
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}

// ── 连接 ──

// Conn 是一条 UDS 连接（accept 侧）。
type Conn struct {
	srv *Server
	nc  net.Conn

	writeMu sync.Mutex
	mu      sync.RWMutex
	instID  string
	closed  bool
}

// SetInstance 绑定本连接的实例 id（由 OnRequest 在 instance-claim 成功后调用）。
func (c *Conn) SetInstance(instanceID string) {
	if instanceID == "" {
		return
	}
	c.mu.Lock()
	c.instID = instanceID
	c.mu.Unlock()
	c.srv.bind(c, instanceID)
}

// Instance 返回本连接绑定的实例 id（未绑定 = ""）。
func (c *Conn) Instance() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.instID
}

// SendEvent 向本连接写一条下行事件帧。
func (c *Conn) SendEvent(typ string, payload any) error {
	return c.write(eventFrame{K: frameEvent, Type: typ, Payload: payloadBytes(payload), Src: "mq"})
}

// SendControl 向本连接写一条定向控制帧。
func (c *Conn) SendControl(typ string, data any) error {
	return c.write(controlFrame{K: frameControl, Type: typ, Data: payloadBytes(data)})
}

// write 写一帧（行分隔 JSON；写锁串行化，避免并发交错）。
func (c *Conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return net.ErrClosed
	}
	_, err = c.nc.Write(b)
	return err
}

// serve 读循环：逐行解析上行帧 → OnRequest → 写回应答。
func (c *Conn) serve() {
	r := bufio.NewReaderSize(c.nc, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			c.handleLine(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.srv.logf("uds: conn read: %v", err)
			}
			break
		}
	}
	c.srv.unbind(c)
	c.close()
	if c.srv.opts.OnDisconnect != nil {
		c.srv.opts.OnDisconnect(c)
	}
}

// handleLine 解析一行并处理（非 req 帧 / 解析失败 → 忽略）。
func (c *Conn) handleLine(line []byte) {
	var f requestFrame
	if json.Unmarshal(line, &f) != nil || f.K != frameRequest {
		return
	}
	resp := c.srv.opts.OnRequest(c, f.Type, f.Payload)
	_ = c.write(responseFrame{K: frameResponse, ID: f.ID, OK: resp.OK, Result: resp.Result, Errors: resp.Errors})
}

// close 关闭底层连接（幂等）。
func (c *Conn) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	_ = c.nc.Close()
}
