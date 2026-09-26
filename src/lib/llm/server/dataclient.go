// 数据面请求-响应客户端（A3 总线化：server 对 chonkpilot-data 的一切访问经总线 persist，
// 不再直连 prj/usr 库）。persist 应答发布到请求**同一相对主题**（A2 正名，§3 头），载荷
// {req_id, ok, result} / {req_id, ok:false, error}；请求带 req_id 并按 req_id 匹配应答
// （忽略他人请求/应答与失败回复）。超时统一 dataTimeout。
package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// dataTimeout 数据面请求超时（persist 应答同步：同进程合并形态微秒级；给分离形态留裕量）。
const dataTimeout = 3 * time.Second

// dataRequest 发 data-<domain>-<op> 请求并等 result（失败/超时 → error）。
func dataRequest(bus mq.Bus, subject string, req map[string]any) (map[string]any, error) {
	reqID := newID()
	req["req_id"] = reqID
	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != reqID || m.OK == nil {
			return nil // 忽略他人请求/应答与失败回复
		}
		if !*m.OK {
			msg := m.Error
			if msg == "" {
				msg = "persist error"
			}
			select {
			case done <- reply{err: &dataError{msg: msg}}:
			default:
			}
			return nil
		}
		select {
		case done <- reply{result: m.Result}:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()

	b, _ := json.Marshal(req)
	if f := bus.Emit(context.Background(), subject, b); f.Wait().Err() != nil {
		return nil, f.Wait().Err()
	}
	select {
	case r := <-done:
		return r.result, r.err
	case <-time.After(dataTimeout):
		return nil, &dataError{msg: subject + " via persist timeout"}
	}
}

// dataError 是数据面失败（含 persist 错误 / 超时）。
type dataError struct{ msg string }

func (e *dataError) Error() string { return e.msg }
