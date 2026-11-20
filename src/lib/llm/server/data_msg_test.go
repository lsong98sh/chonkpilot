// data-* 总线客户端测试 helper（原 config_test.go 迁移后保留）：server 测试经总线发
// data-* 请求等待 persist 应答（A1 迁库后由 chonkpilot-data/persist 服务面应答）。
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// registerTestInstance 注册测试实例（im 订阅 instance-register → s.im.Lookup；persist
// 自持实例视图 → data.Prj 可用）。t.Cleanup(data.Reset)：关闭 prj/prjusr 库连接缓存，
// 否则 TempDir 清理时被占用（用例经 data-prj-config-* 打开库后必现）。
func registerTestInstance(t *testing.T, s *Server) {
	t.Helper()
	_ = s.bus.Publish("instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	time.Sleep(30 * time.Millisecond)
	t.Cleanup(data.Reset)
}

// dataCall 发 data-* 请求并等结果（persist.reply 发布到同一 subject，订阅匹配 req_id/ok）。
// subject 形如 data-prj-config-save（相对主题，A2 正名；总线自动补 chonk. 前缀）。
func dataCall(t *testing.T, s *Server, subject string, payload map[string]any) map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 1)
	sub, err := s.bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		// 只捕获响应（有 ok 字段的为响应，无则为请求）
		if _, hasOK := m["ok"]; !hasOK {
			return nil
		}
		select {
		case ch <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	v := emitWait(t, s.bus, subject, jb(payload))
	// 失败信封（ok:false，persist failID）与超时（fire-and-forget 主题）都返回 err——
	// 失败信封的 body 落在 Result（{ok:false,error}），调用方按 ok 字段分支；
	// 超时 = 无应答（异常，旧语义同 fatal）。
	if v.Err() != nil {
		if m, ok := v.Result.(map[string]any); ok {
			if _, has := m["ok"]; has {
				return m // 失败信封：调用方判 ok=false
			}
		}
		t.Fatal(v.Err())
	}
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting reply for %s", subject)
		return nil
	}
}

// dataResult 取 reply 的 result 字段。
func dataResult(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in reply: %+v", m)
	}
	return res
}

func dataList(res map[string]any) []any {
	list, _ := res["list"].([]any)
	return list
}
