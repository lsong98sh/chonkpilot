package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	mq "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
	msgkeys "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/topics"
)

// PluginMCPRegister 是 plugin.mcp.register 的载荷。
type PluginMCPRegister struct {
	URL       string            `json:"url"`
	Token     string            `json:"token"`
	HotTools  []string          `json:"hot_tools"`
	Mode      string            `json:"mode"` // http（默认）| tools
	Namespace string            `json:"namespace"`
	Category  string            `json:"category"`
	Timeout   int               `json:"timeout_sec"`
	Tools     []json.RawMessage `json:"tools"`
}

// PluginHooks 是宿主（PluginManager）注入的插件平台回调；nil 字段 = 对应能力不可用。
type PluginHooks struct {
	// RegisterMCP 把插件上报的 MCP 端点登记到 gateway（name 由宿主决定）。
	RegisterMCP func(ctx context.Context, pluginID string, req PluginMCPRegister) error
	// UnregisterMCP 注销插件的 MCP 端点（幂等）。
	UnregisterMCP func(ctx context.Context, pluginID string) error
	// RegisterUI 登记/覆盖插件 UI（视图/菜单/状态栏）。
	RegisterUI func(pluginID string, payload json.RawMessage)
	// Status 接收插件自报状态（running / degraded / error）。
	Status func(pluginID, state, detail string)
	// Publish 接收插件推送给自己 UI 的事件（topic 固定 plugin-ui-event）。
	Publish func(pluginID, topic string, payload json.RawMessage)
	// DataGuard 校验插件是否有权执行某个业务数据操作（manifest permissions）。
	DataGuard func(pluginID, resource, op string) error
	// Storage 是 storage.self 的宿主实现（插件私有 KV）。
	Storage func(pluginID, op string, params map[string]any) (any, error)
}

// SetPluginHooks 注入插件平台回调（可重复调用，后者覆盖）。
func (s *Server) SetPluginHooks(h PluginHooks) {
	s.pluginsMu.Lock()
	s.plugins = h
	s.pluginsMu.Unlock()
}

func (s *Server) pluginHooks() PluginHooks {
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	return s.plugins
}

// SessionByPluginID 返回指定插件的当前会话（同 id 多连接取第一个）。
func (s *Server) SessionByPluginID(pluginID string) *Session {
	if pluginID == "" {
		return nil
	}
	s.mu.Lock()
	list := s.sessionListLocked()
	s.mu.Unlock()
	for _, sess := range list {
		if sess.PluginID() == pluginID {
			return sess
		}
	}
	return nil
}

// RequestPlugin 向插件发一条反向请求（plugin.ui.rpc / plugin.command），并等待应答。
func (s *Server) RequestPlugin(ctx context.Context, pluginID, method string, payload any) (any, error) {
	sess := s.SessionByPluginID(pluginID)
	if sess == nil {
		return nil, errf(CodePluginUnregistered, "插件 %s 未连接", pluginID)
	}
	res, err := sess.conn.Call(ctx, method, payload)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, &Error{Code: CodeInternal, Msg: strings.Join(res.Errors, "; ")}
	}
	return res.Value, nil
}

func handleMCPRegister(ctx context.Context, sess *Session, args json.RawMessage) (any, error) {
	if sess.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in PluginMCPRegister
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.Mode == "" {
		in.Mode = "http"
	}
	if in.Mode == "stdio" {
		return nil, errf(CodeArgsInvalid, "插件捆绑 MCP 不支持 stdio（会与插件进程产生双所有者）")
	}
	if in.Mode == "http" {
		u, err := url.Parse(strings.TrimSpace(in.URL))
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" {
			return nil, errf(CodeArgsInvalid, "mcp.register: url 必须是 http://127.0.0.1:<port>")
		}
		if strings.TrimSpace(in.Token) == "" {
			return nil, errf(CodeArgsInvalid, "mcp.register: token 不能为空")
		}
	}
	h := sess.srv.pluginHooks()
	if h.RegisterMCP == nil {
		return nil, errf(CodeMethodNotFound, "宿主未启用插件 MCP 登记")
	}
	if err := h.RegisterMCP(ctx, sess.PluginID(), in); err != nil {
		return nil, &Error{Code: CodeInternal, Msg: err.Error()}
	}
	return map[string]any{"ok": true, "mode": in.Mode}, nil
}

func handleMCPUnegister(ctx context.Context, sess *Session, _ json.RawMessage) (any, error) {
	if sess.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	h := sess.srv.pluginHooks()
	if h.UnregisterMCP != nil {
		if err := h.UnregisterMCP(ctx, sess.PluginID()); err != nil {
			return nil, &Error{Code: CodeInternal, Msg: err.Error()}
		}
	}
	return map[string]any{"ok": true}, nil
}

func handleUIRegister(_ context.Context, sess *Session, args json.RawMessage) (any, error) {
	if sess.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	h := sess.srv.pluginHooks()
	if h.RegisterUI != nil {
		h.RegisterUI(sess.PluginID(), args)
	}
	return map[string]any{"ok": true}, nil
}

func handleStatusReport(_ context.Context, sess *Session, args json.RawMessage) (any, error) {
	if sess.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		State  string `json:"state"`
		Detail string `json:"detail"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.State == "" {
		in.State = "running"
	}
	if h := sess.srv.pluginHooks(); h.Status != nil {
		h.Status(sess.PluginID(), in.State, in.Detail)
	}
	return map[string]any{"ok": true}, nil
}

func handlePublish(_ context.Context, sess *Session, args json.RawMessage) (any, error) {
	if sess.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		Topic   string          `json:"topic"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Topic) != "plugin-ui-event" {
		return nil, errf(CodePermissionDenied, "plugin.publish 只允许 plugin-ui-event")
	}
	if h := sess.srv.pluginHooks(); h.Publish != nil {
		h.Publish(sess.PluginID(), in.Topic, in.Payload)
	}
	return map[string]any{"ok": true}, nil
}

// ── 业务数据面 ─────────────────────────

type dataRequest struct {
	Resource   string         `json:"resource"`
	Op         string         `json:"op"`
	InstanceID string         `json:"instance_id"`
	Params     map[string]any `json:"params"`
}

func handleDataRead(ctx context.Context, sess *Session, args json.RawMessage) (any, error) {
	return handleData(ctx, sess, args, false)
}

func handleDataWrite(ctx context.Context, sess *Session, args json.RawMessage) (any, error) {
	return handleData(ctx, sess, args, true)
}

func handleData(ctx context.Context, sess *Session, args json.RawMessage, write bool) (any, error) {
	if sess.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in dataRequest
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.Resource == "" || in.Op == "" {
		return nil, errf(CodeArgsInvalid, "data: resource/op 必填")
	}
	if in.InstanceID == "" {
		return nil, errf(CodeArgsInvalid, "data: instance_id 必填")
	}
	h := sess.srv.pluginHooks()
	if h.DataGuard != nil {
		if err := h.DataGuard(sess.PluginID(), in.Resource, in.Op); err != nil {
			return nil, &Error{Code: CodePermissionDenied, Msg: err.Error()}
		}
	}
	if in.Resource == "storage.self" {
		if h.Storage == nil {
			return nil, errf(CodeMethodNotFound, "宿主未启用插件私有存储")
		}
		v, err := h.Storage(sess.PluginID(), in.Op, in.Params)
		if err != nil {
			return nil, &Error{Code: CodeInternal, Msg: err.Error()}
		}
		return map[string]any{"ok": true, "result": v}, nil
	}
	topic, err := DataTopic(in.Resource, in.Op, write)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"instance_id": in.InstanceID}
	for k, v := range in.Params {
		payload[k] = v
	}
	res, err := EmitData(ctx, sess.srv.opts.Bus, topic, payload)
	if err != nil {
		return nil, &Error{Code: CodeInternal, Msg: err.Error()}
	}
	return map[string]any{"ok": true, "result": res}, nil
}

// EmitData 调一次数据面主题并等待其应答。
//
// 数据面（persist）采用「同主题应答」：请求带 `req_id`，应答 = {req_id, ok, result|error}
// 再次发布到同一主题（见 data/persist envelope）。故这里先挂一个临时订阅（order 100，
// 在业务订阅者之后执行）收应答，再 Emit 请求；首个匹配 req_id 的应答即结果。
func EmitData(ctx context.Context, bus mq.Bus, topic string, payload map[string]any) (any, error) {
	reqID, _ := payload["req_id"].(string)
	if reqID == "" {
		reqID = fmt.Sprintf("plg-%d", time.Now().UnixNano())
		payload["req_id"] = reqID
	}
	replies := make(chan map[string]any, 4)
	sub, err := bus.On(topic, 100, func(_ context.Context, _ string, v *mq.Value) error {
		var env struct {
			ReqID  string          `json:"req_id"`
			OK     *bool           `json:"ok"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		if json.Unmarshal(v.Payload, &env) != nil || env.OK == nil {
			return nil // 请求载荷（无顶层 ok）不处理
		}
		if env.ReqID != "" && env.ReqID != reqID {
			return nil
		}
		out := map[string]any{"ok": *env.OK}
		if len(env.Result) > 0 && string(env.Result) != "null" {
			var r any
			if json.Unmarshal(env.Result, &r) == nil {
				out["result"] = r
			}
		}
		if env.Error != "" {
			out["error"] = env.Error
		}
		select {
		case replies <- out:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := bus.Emit(ctx, topic, payload).Wait().Err(); err != nil {
		return nil, err
	}
	select {
	case r := <-replies:
		if ok, _ := r["ok"].(bool); !ok {
			return nil, fmt.Errorf("%v", r["error"])
		}
		return r["result"], nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("data: %s 无应答", topic)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DataTopic 把「资源.操作」映射到既有数据面主题（闭集；未列出 = 不支持）。
func DataTopic(resource, op string, write bool) (string, error) {
	key := resource + "." + op
	table := map[string]string{
		"session.list":          msgkeys.TopicDataSessionList,
		"session.get":           msgkeys.TopicDataSessionGet,
		"session.history":       msgkeys.TopicDataSessionHistory,
		"session.content":       msgkeys.TopicDataSessionContent,
		"session.context":       msgkeys.TopicDataSessionContext,
		"session.messages":      msgkeys.TopicDataSessionLoadMessages,
		"session.snapshot.get":  msgkeys.TopicDataSnapshotGet,
		"session.snapshot.set":  msgkeys.TopicDataSnapshotSet,
		"session.summary.set":   msgkeys.TopicDataSessionSetSummary,
		"session.inbox.list":    msgkeys.TopicLlmInboxList,
		"session.inbox.add":     msgkeys.TopicLlmInboxAdd,
		"session.inbox.discard": msgkeys.TopicLlmInboxDiscard,
		"session.inbox.promote": msgkeys.TopicLlmInboxPromote,
		"config.prj.get":        msgkeys.TopicDataPrjConfigLoad,
		"config.prj.list":       msgkeys.TopicDataPrjConfigList,
		"config.prj.set":        msgkeys.TopicDataPrjConfigSave,
		"config.prj.delete":     msgkeys.TopicDataPrjConfigDelete,
		"config.usr.get":        msgkeys.TopicDataUserConfigLoad,
		"prompt.get":            msgkeys.TopicDataPromptLoad,
		"prompt.set":            msgkeys.TopicDataPromptSave,
		"filelist.list":         msgkeys.TopicDataFilelistList,
		"filelist.put":          msgkeys.TopicDataFilelistPut,
		"filelist.del":          msgkeys.TopicDataFilelistDel,
		"memory.list":           msgkeys.TopicDataMemoryList,
		"memory.read":           msgkeys.TopicDataMemoryRead,
		"memory.write":          msgkeys.TopicDataMemorySave,
		// 提取进度专用表（memory 沉淀的锚点推进；2026-10-24 外挂为独立插件 exe 后开放，
		// 主题与载荷不变，见 61-消息一览）。
		"memory.extract.load": msgkeys.TopicDataMemoryExtractLoad,
		"memory.extract.save": msgkeys.TopicDataMemoryExtractSave,
		"knowledge.list":      msgkeys.TopicDataKnowledgeList,
		"knowledge.read":      msgkeys.TopicDataKnowledgeRead,
		"filesys.list":        "filesys.list",
		"filesys.read":        "filesys.content",
		"filesys.write":       "filesys.create",
		"filesys.mkdir":       "filesys.mkdir",
		"filesys.remove":      "filesys.remove",
		"filesys.rename":      "filesys.rename",
		"filesys.copy":        "filesys.copy",
		"task.tree":           msgkeys.TopicDataTasktreeList,
		"task.tasks":          msgkeys.TopicDataTasktreeTasks,
		"task.stop":           msgkeys.TopicTaskStop,
		"task.background":     msgkeys.TopicTaskBackground,
		"task.verify":         msgkeys.TopicTaskVerify,
	}
	if topic, ok := table[key]; ok {
		return topic, nil
	}
	if write {
		return "", errf(CodeArgsInvalid, "未知数据写操作 %s", key)
	}
	return "", errf(CodeArgsInvalid, "未知数据读操作 %s", key)
}
