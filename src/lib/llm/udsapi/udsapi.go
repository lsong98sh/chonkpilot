// Package udsapi 是「服务端 ↔ 本机薄壳客户端（flash）」的 **UDS 前门**（accept 侧）：
// 把 flash 的一条 UDS 连接（= 一个实例）桥接到服务端进程内总线。
//
// 设计见 docs/spec/10-architecture/19-多租户服务端形态.md §8.11 与
// docs/spec/60-reference/62-命令行与参数.md §3A：
//   - **instance ↔ 连接 1:1**：连接在 `instance-claim` 成功后绑定实例；**连接断开 = 该实例退出**
//     （补发 `instance-exit`；flash 不发布 `instance-heartbeat`）；
//   - **前门 = 本机信任 → 免鉴权**：claim 注入连接层信任标记 `__local=true`
//     （`src/lib/llm/server/claim.go` 的 `claimReq.Local`）→ work_dir 取客户端提议、免鉴权；
//   - **定向投递**：`instance-goto-top` 经**控制帧**直达目标实例连接（不下发前端）；
//   - `instance-bring-to-top` 由本前门处理（反查 `data_dir` 顶层实例 + 比对 `user`）。
//
// 上行帧语义**逐字对齐** chonkpilot-llm/httpapi 的 `POST /publish`：`{type,payload}` →
// 注入连接层身份 → 服务端进程内总线 → 应答 `{ok,result,errors}`。下行事件按 `instance_id`
// 路由到对应连接（无归属 → 广播到全部连接）。
//
// 消息面：**零新增主题/payload**（61-消息一览 为唯一准则）。
package udsapi

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/heartbeat"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/uds"
)

// Options 是前门构造参数。
type Options struct {
	// Bus 是服务端进程内总线（必填）。
	Bus mq.Bus
	// Path 是 UDS 监听路径；空 = uds.DefaultPath()。
	Path string
	// InstanceOf 取实例的 (work_dir, user)（未命中 ok=false）——供 `filesys.*` 注入 work_dir
	// 与 bring-to-top 比对 user。
	InstanceOf func(instanceID string) (workDir, user string, ok bool)
	// TopByDataDir 反查 `data_dir` 的**顶层实例** (instance_id, user)（未命中 ok=false）。
	TopByDataDir func(dataDir string) (instanceID, user string, ok bool)
	// Logf 诊断输出；nil = 走 log.Printf。
	Logf func(format string, args ...any)
}

// Gateway 是 UDS 前门。
type Gateway struct {
	opts Options
	srv  *uds.Server

	sub mq.Sub

	mu  sync.Mutex
	pub map[string][]pubStamp

	// conns 是当前**已绑定实例**的连接（instance_id → conn）；断开 / 被顶替时移除。
	// 用途：R7-B-1 —— 前门按连接代发保活心跳（flash 不自行发 instance-heartbeat）。
	connMu sync.Mutex
	conns  map[string]*uds.Conn

	// beatStop 保活心跳 goroutine 停止信号（Start 起、Stop 关；nil = 未启动）。
	beatStop chan struct{}
}

// keepaliveInterval 是前门代连接发布保活心跳的周期：取 heartbeat.Interval 的一半（15s），
// 对 heartbeat.Timeout（90s）留 2× 裕量（R7-B-1：flash 无客户端心跳，靠前门续期）。
var keepaliveInterval = heartbeat.Interval / 2

// New 构造前门（未监听）。
func New(opts Options) *Gateway {
	return &Gateway{opts: opts, pub: make(map[string][]pubStamp), conns: make(map[string]*uds.Conn)}
}

// Start 建立 UDS 监听 + 订阅总线（非阻塞）。
func (g *Gateway) Start() error {
	g.srv = uds.NewServer(uds.ServerOptions{
		Path:         g.opts.Path,
		OnRequest:    g.handle,
		OnDisconnect: g.onDisconnect,
		Logf:         g.logf,
	})
	if err := g.srv.Start(); err != nil {
		return err
	}
	sub, err := g.opts.Bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		g.forward(subject, v.Payload)
		return nil
	})
	if err != nil {
		_ = g.srv.Close()
		return err
	}
	g.sub = sub
	g.beatStop = g.startKeepalive()
	return nil
}

// Stop 退订 + 关闭监听（总线由调用方持有，本包不 Close）。
func (g *Gateway) Stop() {
	if g.beatStop != nil {
		close(g.beatStop)
		g.beatStop = nil
	}
	if g.sub != nil {
		_ = g.sub.Unsubscribe()
		g.sub = nil
	}
	if g.srv != nil {
		_ = g.srv.Close()
	}
}

// startKeepalive 起保活心跳 goroutine（周期 keepaliveInterval），返回停止信号。
// R7-B-1：flash 实例只 claim 时刷新一次 LastBeat，无客户端心跳 → 分离形态 sweep 90s 后会
// 误回收长会话；前门按**仍持有连接的实例**代发 `instance-heartbeat{instance_id}`（既有主题/
// payload，61 §4.1）续期——连接断开即停止续期，与「UDS 连接断开 = 实例退出」语义对齐。
func (g *Gateway) startKeepalive() chan struct{} {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(keepaliveInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				g.beatConnections()
			}
		}
	}()
	return stop
}

// beatConnections 为全部**已绑定实例**发布一次 `instance-heartbeat{instance_id}`（续期）。
func (g *Gateway) beatConnections() {
	g.connMu.Lock()
	ids := make([]string, 0, len(g.conns))
	for id := range g.conns {
		ids = append(ids, id)
	}
	g.connMu.Unlock()
	for _, id := range ids {
		g.publish(msgkeys.TopicInstanceHeartbeat, map[string]any{msgkeys.FieldInstanceId: id})
	}
}

// SendControl 定向投递一条控制帧到目标实例连接（供托盘 / 其它服务端组件复用）。
func (g *Gateway) SendControl(instanceID, typ string, data any) bool {
	if g.srv == nil {
		return false
	}
	return g.srv.SendControl(instanceID, typ, data)
}

// Connected 报告指定实例当前是否持有 **UDS 前门连接**（只读，无副作用）。
// 用途：托盘「列举实例」判形态 —— flash 经 UDS 前门接入（`Connected=true`）；
// browser 走 httpapi、无 UDS 连接（`Connected=false`）。前门未启动 → false。
func (g *Gateway) Connected(instanceID string) bool {
	if g.srv == nil {
		return false
	}
	return g.srv.Connected(instanceID)
}

// ── 上行处理 ──

// handle 处理一次上行请求。
func (g *Gateway) handle(c *uds.Conn, typ string, payload json.RawMessage) uds.Response {
	switch typ {
	case msgkeys.TopicInstanceClaim:
		return g.handleClaim(c, payload)
	case msgkeys.TopicInstanceBringToTop:
		return g.handleBringToTop(c, payload)
	}
	// 普通上行：以**本连接实例**为准注入 instance_id（客户端不得自报其它实例）。
	injected, err := g.inject(typ, payload, c.Instance())
	if err != nil {
		return uds.Response{OK: false, Errors: []string{err.Error()}}
	}
	result, errs := g.publishV(typ, injected)
	return uds.Response{OK: len(errs) == 0, Result: result, Errors: errStrings(errs)}
}

// handleClaim 处理 instance-claim：注入连接层信任（`__local=true`）→ 免鉴权 + work_dir 取提议；
// 成功后绑定 连接 ↔ 实例。
func (g *Gateway) handleClaim(c *uds.Conn, payload json.RawMessage) uds.Response {
	m := decodeMap(payload)
	if m == nil {
		m = map[string]any{}
	}
	m["__local"] = true // 连接层信任标记（非消息面字段；与 instance_id/token 同口径）
	injected := encodeMap(m)
	result, errs := g.publishV(msgkeys.TopicInstanceClaim, injected)
	if len(errs) > 0 {
		return uds.Response{OK: false, Result: result, Errors: errStrings(errs)}
	}
	if id := resultInstanceID(result); id != "" {
		// R7-D-10：同连接改绑新实例 id → 先清理旧 id 的幽灵登记（否则保活心跳 / 定向事件
		// 仍按旧 id 路由到本连接）。正常 flash 客户端每连接仅 claim 一次，此分支仅兜底。
		if prev := c.Instance(); prev != "" && prev != id {
			g.connMu.Lock()
			if g.conns[prev] == c {
				delete(g.conns, prev)
			}
			g.connMu.Unlock()
			g.publish(msgkeys.TopicInstanceExit, map[string]any{msgkeys.FieldInstanceId: prev})
		}
		c.SetInstance(id)
		g.connMu.Lock()
		g.conns[id] = c
		g.connMu.Unlock()
	}
	return uds.Response{OK: true, Result: result}
}

// handleBringToTop 处理 instance-bring-to-top（61 §4.1 ⑥）：反查 `data_dir` 顶层实例，
// 比对发起方实例的 `user`：一致 → 定向投递 `instance-goto-top`；不一致 / 未命中 → 明确失败。
func (g *Gateway) handleBringToTop(c *uds.Conn, payload json.RawMessage) uds.Response {
	if g.opts.TopByDataDir == nil || g.opts.InstanceOf == nil {
		return forbidden()
	}
	m := decodeMap(payload)
	dataDir, _ := m[msgkeys.InstanceBringToTopPayloadDataDir].(string)
	fromID := c.Instance()
	if fromID == "" || strings.TrimSpace(dataDir) == "" {
		return forbidden()
	}
	_, fromUser, ok := g.opts.InstanceOf(fromID)
	if !ok {
		return forbidden()
	}
	topID, topUser, ok := g.opts.TopByDataDir(dataDir)
	if !ok {
		return forbidden()
	}
	if !strings.EqualFold(strings.TrimSpace(topUser), strings.TrimSpace(fromUser)) {
		g.logf("[udsapi] instance-bring-to-top: 跨用户拒绝 from=%s(%s) target=%s(%s)",
			fromID, fromUser, topID, topUser)
		return forbidden()
	}
	// R7-B-6：SendControl 未命中（目标无 UDS 连接）→ 明确失败，不得回 {ok:true} 假成功。
	if !g.srv.SendControl(topID, msgkeys.TopicInstanceGotoTop, nil) {
		g.logf("[udsapi] instance-bring-to-top: 目标实例无活动连接 top=%s", topID)
		return forbidden()
	}
	return uds.Response{OK: true, Result: map[string]any{msgkeys.FieldOk: true}}
}

// forbidden 返回 instance-forbidden（61 §4.6：不跳登录，前端提示换目录、继续选）。
func forbidden() uds.Response {
	return uds.Response{OK: false, Errors: []string{"instance-forbidden"}}
}

// inject 注入连接层身份：`instance_id` 以**本连接实例**为准（客户端自报其它 → 覆盖；缺失 → 拒绝）。
// 另：`filesys.*` 注入本实例 `work_dir`（与 httpapi 的 bindFilesys 同口径：客户端不得自报目录）。
func (g *Gateway) inject(typ string, payload json.RawMessage, instanceID string) (json.RawMessage, error) {
	m := decodeMap(payload)
	if m == nil {
		m = map[string]any{}
	}
	if instanceID == "" {
		return nil, errInstanceRequired
	}
	reported, _ := m[msgkeys.FieldInstanceId].(string)
	if reported != instanceID {
		if reported != "" {
			g.logf("[udsapi] instance_id forged attempt: topic=%s reported=%q overridden->%q", typ, reported, instanceID)
		}
		m[msgkeys.FieldInstanceId] = instanceID
	}
	if strings.HasPrefix(typ, "filesys.") && g.opts.InstanceOf != nil {
		if wd, _, ok := g.opts.InstanceOf(instanceID); ok && wd != "" {
			m["work_dir"] = wd
		}
	}
	return encodeMap(m), nil
}

var errInstanceRequired = &injectErr{"instance_id required"}

type injectErr struct{ msg string }

func (e *injectErr) Error() string { return e.msg }

// ── 下行 ──

// forward 把总线事件按 instance_id 路由到对应连接（无归属 → 广播）。
func (g *Gateway) forward(subject string, payload []byte) {
	if g.self(subject, payload) {
		return
	}
	if g.srv == nil {
		return
	}
	inst := eventInstanceID(payload)
	if inst == "" {
		g.srv.Broadcast(subject, payload)
		return
	}
	g.srv.SendEvent(inst, subject, payload)
}

// onDisconnect 连接断开 → 该实例退出（补发 `instance-exit`；61 §4.1 ③）。
// R7-B-2：仅当本连接仍是该实例**当前绑定**（未被新连接顶替）才补发 exit —— 否则第二连接
// 同 id 顶替、旧连接断开时会误杀新连接的活实例。
func (g *Gateway) onDisconnect(c *uds.Conn) {
	id := c.Instance()
	if id == "" {
		return
	}
	g.connMu.Lock()
	if g.conns[id] == c { // 仅移除**本连接**的登记（被顶替的旧连接不得删掉新连接的登记）
		delete(g.conns, id)
	}
	g.connMu.Unlock()
	if g.srv == nil || !g.srv.IsCurrent(c) {
		return
	}
	g.publish(msgkeys.TopicInstanceExit, map[string]any{msgkeys.FieldInstanceId: id})
}

// ── 发布（含防环）──

func (g *Gateway) publish(subject string, payload any) {
	if g.opts.Bus == nil {
		return
	}
	raw := payloadBytes(payload)
	g.mark(subject, raw)
	if v := g.opts.Bus.Emit(context.Background(), subject, raw).Wait(); v.Err() != nil {
		g.logf("[udsapi] publish %s: %v", subject, v.Err())
	}
}

func (g *Gateway) publishV(subject string, payload any) (any, []error) {
	raw := payloadBytes(payload)
	g.mark(subject, raw)
	v := g.opts.Bus.Emit(context.Background(), subject, raw).Wait()
	return v.Result, v.Errors
}

type pubStamp struct {
	fp string
	at time.Time
}

func (g *Gateway) mark(subject string, payload []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	list := append(g.pub[subject], pubStamp{fp: fingerprint(subject, payload), at: time.Now()})
	if len(list) > 2 {
		list = list[len(list)-2:]
	}
	g.pub[subject] = list
}

func (g *Gateway) self(subject string, payload []byte) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	list, ok := g.pub[subject]
	if !ok {
		return false
	}
	fp := fingerprint(subject, payload)
	now := time.Now()
	out := list[:0]
	hit := false
	for _, st := range list {
		if now.Sub(st.at) >= time.Second {
			continue
		}
		if !hit && st.fp == fp {
			hit = true
			continue
		}
		out = append(out, st)
	}
	if len(out) == 0 {
		delete(g.pub, subject)
	} else {
		g.pub[subject] = out
	}
	return hit
}

// ── 工具 ──

func (g *Gateway) logf(format string, args ...any) {
	if g.opts.Logf != nil {
		g.opts.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// fingerprint 计算一次发布事件的指纹（subject + 载荷哈希）——防环用，与 httpapi 同口径。
func fingerprint(subject string, payload []byte) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(subject))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return strconv.FormatUint(h.Sum64(), 16)
}

func decodeMap(payload json.RawMessage) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return nil
	}
	return m
}

func encodeMap(m map[string]any) json.RawMessage {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return raw
}

func payloadBytes(payload any) []byte {
	switch p := payload.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return p
	case []byte:
		return p
	case string:
		return []byte(p)
	default:
		raw, _ := json.Marshal(p)
		return raw
	}
}

func resultInstanceID(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := m[msgkeys.FieldInstanceId].(string)
	return id
}

func eventInstanceID(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &probe) != nil {
		return ""
	}
	return probe.InstanceID
}

func errStrings(errs []error) []string {
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}
