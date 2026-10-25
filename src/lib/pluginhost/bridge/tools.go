package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	transport "github.com/lsong98sh/chonkpilot-plugin-framework/protocol"
	mq "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
)

// toolCallSeq 生成跨进程工具调用的 call_id（取消帧定位用；进程内单调递增即可）。
var toolCallSeq atomic.Int64

// toolCallTimeout 是外部工具处理一次调用的上限（工具契约允许 tool_result 等待较长时间；
// 与 server-tools 自身的 10 分钟执行 ctx 对齐，留余量）。契约显式 timeout=0/-1（无上限）
// 时**不施加**该上限（载荷带 no_timeout，见 bindToolSubject）。var 便于测试覆写。
var toolCallTimeout = 11 * time.Minute

// toolOwner 是某个 handler_subject 的插件归属。
type toolOwner struct {
	sess *Session
	sub  mq.Sub
}

// bindToolSubject 把某个 bus 主题绑定到插件：总线请求 → `plugin.tool.call` → 插件结果写回。
func (s *Server) bindToolSubject(sess *Session, subject string) error {
	if s.opts.Bus == nil {
		return errf(CodeInternal, "未装配消息总线")
	}
	s.toolMu.Lock()
	if old, ok := s.tools[subject]; ok && old.sess != sess {
		s.toolMu.Unlock()
		return errf(CodePluginUnauthorized, "主题 %s 已被其他插件占用", subject)
	}
	if s.tools == nil {
		s.tools = make(map[string]*toolOwner)
	}
	if _, exists := s.tools[subject]; exists {
		s.toolMu.Unlock()
		return nil
	}
	s.toolMu.Unlock()

	sub, err := s.opts.Bus.On(subject, 0, func(ctx context.Context, _ string, v *mq.Value) error {
		// 载荷原样转发给插件；补 call_id（取消帧按此 id 定位处理器）——
		// 追加字段对旧插件向后兼容（未知键忽略）。
		var body map[string]any
		_ = json.Unmarshal(v.Payload, &body)
		if body == nil {
			body = map[string]any{}
		}
		callID := fmt.Sprintf("tc-%d", toolCallSeq.Add(1))
		body["call_id"] = callID
		noTimeout, _ := body["no_timeout"].(bool)
		// no_timeout（契约 timeout=0/-1 = 无上限）→ 不设 11 分钟兜底，仅由取消收敛。
		callCtx, cancel := ctx, context.CancelFunc(func() {})
		if !noTimeout {
			callCtx, cancel = context.WithTimeout(ctx, toolCallTimeout)
		}
		defer cancel()
		res, cerr := sess.conn.Call(callCtx, "plugin.tool.call", map[string]any{
			"subject": subject,
			"payload": body,
		})
		// 取消/超时：通知插件侧中断处理器（tool_result 轮询等长操作即时收敛）。
		if cerr != nil && callCtx.Err() != nil {
			raw, _ := json.Marshal(map[string]any{"call_id": callID})
			_ = sess.conn.Send(transport.Frame{
				Kind: transport.KindCtl, Name: "plugin.tool.cancel", Payload: raw,
			})
		}
		if cerr != nil {
			v.Errors = append(v.Errors, cerr)
			return nil
		}
		if !res.OK {
			msg := "tool call failed"
			if len(res.Errors) > 0 {
				msg = res.Errors[0]
			}
			v.Errors = append(v.Errors, &Error{Code: CodeInternal, Msg: msg})
			return nil
		}
		var out struct {
			Result any `json:"result"`
		}
		raw, _ := json.Marshal(res.Value)
		_ = json.Unmarshal(raw, &out)
		v.Result = out.Result
		return nil
	})
	if err != nil {
		return err
	}
	s.toolMu.Lock()
	s.tools[subject] = &toolOwner{sess: sess, sub: sub}
	s.toolMu.Unlock()
	return nil
}

// unbindToolSession 在插件断开时清理其全部工具绑定。
func (s *Server) unbindToolSession(sess *Session) {
	s.toolMu.Lock()
	var subs []mq.Sub
	for subject, owner := range s.tools {
		if owner.sess == sess {
			subs = append(subs, owner.sub)
			delete(s.tools, subject)
		}
	}
	s.toolMu.Unlock()
	for _, sub := range subs {
		_ = sub.Unsubscribe()
	}
}

func handleToolBind(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		Subject string `json:"subject"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.Subject == "" {
		return nil, errf(CodeArgsInvalid, "plugin.tool.bind 需要 subject")
	}
	if err := s.srv.bindToolSubject(s, in.Subject); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "subject": in.Subject}, nil
}

func handleToolUnbind(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	var in struct {
		Subject string `json:"subject"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	s.srv.toolMu.Lock()
	owner := s.srv.tools[in.Subject]
	if owner != nil && owner.sess == s {
		delete(s.srv.tools, in.Subject)
	}
	s.srv.toolMu.Unlock()
	if owner != nil && owner.sess == s {
		_ = owner.sub.Unsubscribe()
	}
	return map[string]any{"ok": true}, nil
}
