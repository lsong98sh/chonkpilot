// Package bridgebus 把**宿主 mq 总线的一个受限主题子集**映射到插件桥能力面，
// 使原本「进程内 mq 插件」的实现（memory / history）可以**逐字复用**在进程外插件进程里
// —— 只改「跑在哪」，不改触发点、读取范围、门控、幂等、熔断/降级与数据落点。
//
// 语义映射（主题白名单见 routes；未列出的主题**拒绝**，不做隐式直通）：
//
//	宿主 → 插件（事件，无应答）  mq.On(subject)  ← 插件桥异步事件（plugin.register subscriptions）
//	宿主 → 插件（请求-应答）    mq.On(subject)  ← plugin.tool.bind（宿主把请求转发为
//	                                              plugin.tool.call，插件返回值/错误写回
//	                                              v.Result / v.Errors）
//	插件 → 宿主（data-*）       mq.Emit(...).Wait() → plugin/v1/data.read|write
//	                                              （调用方照旧按 req_id 关联：本包把宿主应答
//	                                              合成为 persist 同形的 {req_id, ok, result} 再派发）
//	插件 → 宿主（llm-simple）   mq.Emit(...).Wait() → plugin/v1/llm.step
//	插件 → 宿主（通知/工具面）   mq.Emit(...)        → plugin/v1/tool.notify、plugin/v1/tools.register|unregister
//
// 时间口径：data-* / tools.register / tools.unregister / tool-notify 单次上限 = 5s
// （= 改造前插件侧的 dataTimeout / dataclient.Timeout）；llm-simple 沿用调用方 ctx（插件侧
// 60s llmTimeout）；SDK Options.Timeout 需 >= 该上限（见两个插件 main）。
package bridgebus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// 单次桥调用上限（见包注释「时间口径」）。
const (
	dataTimeout = 5 * time.Second
)

// 主题种类。
type kind int

const (
	evtIn     kind = iota // 宿主 → 插件（事件，无应答）
	reqIn                 // 宿主 → 插件（请求-应答）
	dataRead              // 插件 → 宿主（数据读）
	dataWrite             // 插件 → 宿主（数据写）
	callOut               // 插件 → 宿主（方法面调用）
)

// route 是单条主题的路由声明。
type route struct {
	kind     kind
	resource string // dataRead / dataWrite：数据面 resource
	op       string // dataRead / dataWrite：数据面 op
	method   string // callOut：宿主方法名
	timeout  time.Duration
}

// routes 是**主题白名单**（= memory / history 改造前订阅与发布的全部 mq 主题；主题名与
// payload 均不变）。新增主题必须同步本表，否则 On/Emit 明确报错（不静默丢弃）。
var routes = map[string]route{
	// ── 宿主 → 插件（事件）——
	"session-complete":        {kind: evtIn},
	"instance-register":       {kind: evtIn},
	"instance-heartbeat":      {kind: evtIn},
	"instance-exit":           {kind: evtIn},
	"filesys.changed":         {kind: evtIn},
	"data-prj-config-refresh": {kind: evtIn},

	// ── 宿主 → 插件（请求-应答；plugin.tool.bind）——
	"memory.flush":          {kind: reqIn},
	"history-tool-call":     {kind: reqIn},
	"history-pre-tool-hook": {kind: reqIn},

	// ── 插件 → 宿主（数据面）——
	"data-prj-config-list":       {kind: dataRead, resource: "config", op: "prj.list", timeout: dataTimeout},
	"data-prj-config-load":       {kind: dataRead, resource: "config", op: "prj.get", timeout: dataTimeout},
	"data-prj-config-save":       {kind: dataWrite, resource: "config", op: "prj.set", timeout: dataTimeout},
	"data-user-config-load":      {kind: dataRead, resource: "config", op: "usr.get", timeout: dataTimeout},
	"data-session-get":           {kind: dataRead, resource: "session", op: "get", timeout: dataTimeout},
	"data-session-history":       {kind: dataRead, resource: "session", op: "history", timeout: dataTimeout},
	"data-session-load-messages": {kind: dataRead, resource: "session", op: "messages", timeout: dataTimeout},
	"data-memory-list":           {kind: dataRead, resource: "memory", op: "list", timeout: dataTimeout},
	"data-memory-read":           {kind: dataRead, resource: "memory", op: "read", timeout: dataTimeout},
	"data-memory-save":           {kind: dataWrite, resource: "memory", op: "write", timeout: dataTimeout},
	"data-memory-extract-load":   {kind: dataRead, resource: "memory", op: "extract.load", timeout: dataTimeout},
	"data-memory-extract-save":   {kind: dataWrite, resource: "memory", op: "extract.save", timeout: dataTimeout},

	// ── 插件 → 宿主（方法面）——
	"llm-simple":           {kind: callOut, method: "plugin/v1/llm.step"},
	"tool-notify":          {kind: callOut, method: "plugin/v1/tool.notify", timeout: dataTimeout},
	"mcp-tools-register":   {kind: callOut, method: "plugin/v1/tools.register", timeout: dataTimeout},
	"mcp-tools-unregister": {kind: callOut, method: "plugin/v1/tools.unregister", timeout: dataTimeout},
}

// Options 是插件桥能力面（生产实现 = pluginsdk.Plugin 的对应方法，见两个插件 main）。
type Options struct {
	// Read / Write 调 plugin/v1/data.read|write（宿主返回 {ok, result} 信封）。
	Read  func(ctx context.Context, resource, op, instanceID string, params map[string]any) (json.RawMessage, error)
	Write func(ctx context.Context, resource, op, instanceID string, params map[string]any) (json.RawMessage, error)
	// Call 调宿主方法（plugin/v1/llm.step 等）。
	Call func(ctx context.Context, method string, args any) (json.RawMessage, error)
	// Subscribe 订阅总线事件（异步、无应答）。
	Subscribe func(pattern string, fn func(ctx context.Context, topic string, payload []byte))
	// Bind 绑定一条需要应答的主题（plugin.tool.bind）；fn 的返回值写回请求方 v.Result，
	// error 写回 v.Errors。
	Bind func(subject string, fn func(ctx context.Context, payload json.RawMessage) (any, error)) error
}

// Bus 是「进程内 mq.Bus → 插件桥」的适配器（实现 mq.Bus）。
//
// 派发内核 = 本地 mq.Bus（`mq.New(Options{})`）：保留 order/通配/共享 Value 语义，
// 使改造前插件代码的 On/Emit 用法完全不变。
type Bus struct {
	local mq.Bus
	opts  Options

	mu         sync.Mutex
	registered map[string]bool
	subErr     map[string]error

	queue chan item
	quit  chan struct{}
}

type item struct {
	subject string
	payload []byte
}

// New 构建适配器并启动事件派发协程（事件不阻塞插件桥读循环）。
func New(local mq.Bus, opts Options) *Bus {
	b := &Bus{
		local:      local,
		opts:       opts,
		registered: map[string]bool{},
		subErr:     map[string]error{},
		queue:      make(chan item, 1024),
		quit:       make(chan struct{}),
	}
	go b.loop()
	return b
}

// loop 串行派发宿主事件（保持同主题先后顺序；不阻塞插件桥读循环）。
func (b *Bus) loop() {
	for {
		select {
		case <-b.quit:
			return
		case it := <-b.queue:
			b.hintInstance(it.payload)
			_ = b.local.Emit(context.Background(), it.subject, it.payload)
		}
	}
}

// ─── mq.Bus 实现 ─────────────────────────────────────────

// Prefix 返回本地派发总线的前缀（未注入 = 空串）。
func (b *Bus) Prefix() string { return b.local.Prefix() }

// On 订阅主题（语义同 mq.Bus.On；宿主侧按 routes 声明事件订阅或 plugin.tool.bind）。
func (b *Bus) On(subject string, order int, h mq.VHandler) (mq.Sub, error) {
	r, ok := routes[subject]
	if !ok {
		return nil, fmt.Errorf("bridgebus: 主题 %s 未在桥接映射表中（拒绝隐式直通）", subject)
	}
	if err := b.ensure(subject, r); err != nil {
		return nil, err
	}
	return b.local.On(subject, order, h)
}

// Subscribe v1 兼容订阅（等价 On(subject, 0, 包装)；插件未使用）。
func (b *Bus) Subscribe(subject string, h mq.Handler) (mq.Sub, error) {
	r, ok := routes[subject]
	if !ok {
		return nil, fmt.Errorf("bridgebus: 主题 %s 未在桥接映射表中（拒绝隐式直通）", subject)
	}
	if err := b.ensure(subject, r); err != nil {
		return nil, err
	}
	return b.local.Subscribe(subject, h)
}

// Publish v1 兼容发布（fire-and-forget）。
func (b *Bus) Publish(subject string, payload []byte) error {
	b.Emit(context.Background(), subject, payload)
	return nil
}

// Emit 发布/请求：按 routes 走插件桥；Value 语义与进程内总线一致。
func (b *Bus) Emit(ctx context.Context, subject string, payload any) *mq.Future {
	r, ok := routes[subject]
	if !ok {
		return errorFuture(fmt.Errorf("bridgebus: 主题 %s 未在桥接映射表中（拒绝隐式直通）", subject))
	}
	switch r.kind {
	case evtIn, reqIn:
		// 插件自身发布同主题（历史用法：无）；保持本地派发语义。
		return b.local.Emit(ctx, subject, payload)
	case dataRead, dataWrite:
		return b.emitData(ctx, subject, r, payload)
	default:
		return b.emitCall(ctx, subject, r, payload)
	}
}

// Close 停止派发协程并关闭本地总线。
func (b *Bus) Close() error {
	select {
	case <-b.quit:
	default:
		close(b.quit)
	}
	return b.local.Close()
}

// ─── 宿主 → 插件 ─────────────────────────────────────────

// ensure 首次向宿主注册该主题（事件订阅 / 请求-应答绑定），幂等。
func (b *Bus) ensure(subject string, r route) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.registered[subject] {
		return b.subErr[subject]
	}
	b.registered[subject] = true
	switch r.kind {
	case evtIn:
		if b.opts.Subscribe == nil {
			b.subErr[subject] = fmt.Errorf("bridgebus: 未装配事件订阅能力")
			return b.subErr[subject]
		}
		b.opts.Subscribe(subject, func(_ context.Context, topic string, payload []byte) {
			b.enqueue(topic, payload)
		})
	case reqIn:
		if b.opts.Bind == nil {
			b.subErr[subject] = fmt.Errorf("bridgebus: 未装配主题绑定能力")
			return b.subErr[subject]
		}
		if err := b.opts.Bind(subject, func(ctx context.Context, payload json.RawMessage) (any, error) {
			b.hintInstance(payload)
			v := b.local.Emit(ctx, subject, payload).Wait()
			if err := v.Err(); err != nil {
				return nil, err
			}
			return v.Result, nil
		}); err != nil {
			b.subErr[subject] = err
			return err
		}
	}
	return nil
}

// enqueue 投递宿主事件（有界队列；满则直接异步派发，不丢事件、不阻塞读循环）。
// instance 补登记与业务派发都在 loop（/ 兜底 goroutine）里做，避免占用插件桥读循环。
func (b *Bus) enqueue(subject string, payload []byte) {
	select {
	case b.queue <- item{subject: subject, payload: payload}:
	default:
		go func() {
			b.hintInstance(payload)
			_ = b.local.Emit(context.Background(), subject, payload)
		}()
	}
}

// hintInstance 用载荷里已有的 instance_id + work_dir 补一次本地 instance-register。
//
// 目的（**只改跑在哪带来的启动时差**，不改任何业务语义）：进程内插件在宿主启动早期即订阅
// instance-register，必然收到；进程外插件进程启动/重启晚于该事件时本地实例视图会为空，
// 使 history 的打点门控与 memory 的手动沉淀锁键失效。这里只在**插件本来就会收到的载荷**
// （session-complete 顶层、history 前置钩子 context）带齐两字段时，向本地实例视图补一次
// 登记（instance.Manager.HandleRegister 幂等刷新；不含 data_dir/client_type，二者两个插件都
// 不使用）。正常路径下等同于重复登记同值，无副作用。
func (b *Bus) hintInstance(payload []byte) {
	if len(payload) == 0 {
		return
	}
	var m struct {
		InstanceID string `json:"instance_id"`
		WorkDir    string `json:"work_dir"`
		Context    *struct {
			InstanceID string `json:"instance_id"`
			WorkDir    string `json:"work_dir"`
		} `json:"context"`
	}
	if json.Unmarshal(payload, &m) != nil {
		return
	}
	id, wd := m.InstanceID, m.WorkDir
	if c := m.Context; c != nil {
		if id == "" {
			id = c.InstanceID
		}
		if wd == "" {
			wd = c.WorkDir
		}
	}
	if id == "" || wd == "" {
		return
	}
	_ = b.local.Emit(context.Background(), "instance-register", map[string]any{
		"instance_id": id, "work_dir": wd,
	})
}

// ─── 插件 → 宿主 ─────────────────────────────────────────

// emitData 把 data-* 请求转成 plugin/v1/data.* 并把宿主应答合成 persist 同形信封派发回本地
// （数据面调用方按 req_id 关联，见 dataclient.Emit / memory.request）。
func (b *Bus) emitData(ctx context.Context, subject string, r route, payload any) *mq.Future {
	body := payloadMap(payload)
	instanceID := strval(body["instance_id"])
	params := make(map[string]any, len(body))
	for k, v := range body {
		if k == "instance_id" {
			continue
		}
		params[k] = v // 透传 data / req_id 等（MQ 载荷逐字不变）
	}
	cctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	var raw json.RawMessage
	var err error
	if r.kind == dataRead {
		raw, err = b.opts.Read(cctx, r.resource, r.op, instanceID, params)
	} else {
		raw, err = b.opts.Write(cctx, r.resource, r.op, instanceID, params)
	}
	_ = b.local.Emit(context.Background(), subject, replyEnvelope(body, raw, err))
	f := mq.NewFuture(&mq.Value{Subject: subject})
	f.Resolve()
	return f
}

// emitCall 把方法面请求转成 plugin/v1/* 调用；返回值写入 v.Result，错误写入 v.Errors
// （与进程内 mq.Emit(...).Wait() 的读取方式一致）。
func (b *Bus) emitCall(ctx context.Context, subject string, r route, payload any) *mq.Future {
	body := payloadMap(payload)
	cctx := ctx
	cancel := context.CancelFunc(func() {})
	if r.timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	raw, err := b.opts.Call(cctx, r.method, body)
	cancel()
	v := &mq.Value{Subject: subject}
	if err != nil {
		v.Errors = append(v.Errors, err)
	} else if len(raw) > 0 && string(raw) != "null" {
		var out any
		if json.Unmarshal(raw, &out) == nil {
			v.Result = out
		}
	}
	f := mq.NewFuture(v)
	f.Resolve()
	return f
}

// replyEnvelope 把宿主数据面应答合成为 persist 同形的 {req_id, ok, result|error}。
// 失败（桥调用错误 / 宿主 ok=false）→ ok=false + error；成功 → ok=true + result。
func replyEnvelope(req map[string]any, raw json.RawMessage, err error) map[string]any {
	env := map[string]any{"req_id": strval(req["req_id"])}
	if err != nil {
		env["ok"] = false
		env["error"] = err.Error()
		return env
	}
	var wrap struct {
		OK     *bool  `json:"ok"`
		Result any    `json:"result"`
		Error  string `json:"error"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &wrap)
	}
	if wrap.OK != nil && !*wrap.OK {
		env["ok"] = false
		if wrap.Error != "" {
			env["error"] = wrap.Error
		}
		return env
	}
	env["ok"] = true
	if wrap.Result != nil {
		env["result"] = wrap.Result
	}
	return env
}

// payloadMap 把 Emit 载荷归一为 map（map / JSON 文本 / nil）。
func payloadMap(payload any) map[string]any {
	switch p := payload.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		out := make(map[string]any, len(p))
		for k, v := range p {
			out[k] = v
		}
		return out
	case []byte:
		var out map[string]any
		if json.Unmarshal(p, &out) == nil && out != nil {
			return out
		}
	case string:
		var out map[string]any
		if json.Unmarshal([]byte(p), &out) == nil && out != nil {
			return out
		}
	}
	return map[string]any{}
}

// strval 任意值 → 字符串（配置/载荷值归一，口径同 plugin/dataclient.Strval）。
func strval(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// errorFuture 返回一个已完成的失败 Future（主题未映射等装配错误）。
func errorFuture(err error) *mq.Future {
	v := &mq.Value{}
	v.Errors = append(v.Errors, err)
	f := mq.NewFuture(v)
	f.Resolve()
	return f
}
