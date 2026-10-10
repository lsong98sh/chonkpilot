// remote_bus_test.go — flash 远程总线模式（flash 薄壳）的桥接缝验证：
// **桥 + UDS 客户端（`uds.Client` 作总线）↔ UDS 前门**，不经内嵌 server/filesys。
//
// 覆盖（见 src/lib/gui/remote.go 与 19 §8.11）：
//  1. 桥启动即发 `instance-register`（经 UDS 到达 server）；连接须先 `instance-claim` 绑定实例；
//  2. 上行 `data-*` 经总线转发到 server 并经**事件回投**取回结果（persist 应答路径）；
//  3. 下行事件（`session-receive`）经 UDS → 桥 → `window.mq.emitRemote`（前端 type `llm-receive`）。
package bridge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/uds"
)

// fakeFront 是最小 UDS 前门：claim 绑定实例；data-* 经**事件回投**应答（模拟 persist reply）；
// 其余上行直答 OK。记录收到的上行主题供断言。
type fakeFront struct {
	srv      *uds.Server
	mu       sync.Mutex
	subjects []string
}

func startFakeFront(t *testing.T, path string) *fakeFront {
	t.Helper()
	f := &fakeFront{}
	f.srv = uds.NewServer(uds.ServerOptions{
		Path: path,
		OnRequest: func(c *uds.Conn, typ string, payload json.RawMessage) uds.Response {
			f.mu.Lock()
			f.subjects = append(f.subjects, typ)
			f.mu.Unlock()
			switch typ {
			case msgkeys.TopicInstanceClaim:
				c.SetInstance("i1")
				return uds.Response{OK: true, Result: map[string]any{
					msgkeys.InstanceClaimResultInstanceId: "i1",
					msgkeys.InstanceClaimResultWorkDir:    "C:/wd",
					msgkeys.InstanceClaimResultDataDir:    "C:/wd/.chonkpilot",
				}}
			case "data-demo-load":
				var m struct {
					ReqID string `json:"req_id"`
				}
				_ = json.Unmarshal(payload, &m)
				// persist 应答路径：与请求同主题的事件（携带 req_id），dataViaPersist 据此收敛。
				_ = c.SendEvent("data-demo-load", map[string]any{
					"req_id": m.ReqID, "ok": true,
					"result": map[string]any{"list": []any{"x"}},
				})
				return uds.Response{OK: true}
			default:
				return uds.Response{OK: true}
			}
		},
	})
	if err := f.srv.Start(); err != nil {
		t.Fatalf("uds server start: %v", err)
	}
	t.Cleanup(func() { _ = f.srv.Close() })
	return f
}

func (f *fakeFront) sawSubject(subject string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subjects {
		if s == subject {
			return true
		}
	}
	return false
}

func (f *fakeFront) waitSubject(t *testing.T, subject string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.sawSubject(subject) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("前门未收到上行主题 %q（已收 %v）", subject, f.subjects)
}

func TestRemoteBusBridgeOverUDS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	f := startFakeFront(t, path)

	cli, err := uds.Dial(uds.ClientOptions{Path: path})
	if err != nil {
		t.Fatalf("uds dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	// 1) 连接先 claim（绑定实例）——否则其后 instance-register 缺连接身份。
	if v := cli.Emit(context.Background(), msgkeys.TopicInstanceClaim, map[string]any{
		"user": "u", "work_dir": "C:/wd",
	}).Wait(); v.Err() != nil {
		t.Fatalf("instance-claim: %v", v.Err())
	}

	var (
		evalMu  sync.Mutex
		scripts []string
	)
	br := bridge.New("i1", "C:/wd", "C:/wd/.chonkpilot", func(script string) {
		evalMu.Lock()
		scripts = append(scripts, script)
		evalMu.Unlock()
	}, cli) // 关键：总线 = uds.Client（远程总线模式）
	if err := br.Start(); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	t.Cleanup(br.CloseInstance)

	// 2) 桥启动即发 instance-register（经 UDS 到前门）。
	f.waitSubject(t, msgkeys.TopicInstanceRegister)

	// 3) 上行 data-* 经总线转发 → server 事件回投 → 桥收敛结果。
	res, errs := br.PublishEvent("data-demo-load", `{}`)
	if len(errs) > 0 {
		t.Fatalf("data-demo-load errs: %v", errs)
	}
	m, _ := res.(map[string]any)
	if m == nil || m["list"] == nil {
		t.Fatalf("data-demo-load result = %#v（应经 UDS 事件回投取回）", res)
	}

	// 4) 下行事件（session-receive）→ 桥 → window.mq.emitRemote（前端 type llm-receive）。
	if !f.srv.SendEvent("i1", msgkeys.TopicSessionReceive, map[string]any{"instance_id": "i1", "session": "s"}) {
		t.Fatal("SendEvent 未命中实例连接")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		evalMu.Lock()
		joined := strings.Join(scripts, "\n")
		evalMu.Unlock()
		if strings.Contains(joined, "llm-receive") && strings.Contains(joined, "emitRemote") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	evalMu.Lock()
	defer evalMu.Unlock()
	t.Fatalf("未收到转发给前端的事件脚本（llm-receive）；已收 %v", scripts)
}

// 编译期断言：uds.Client 满足 mq.Bus（桥的总线接口）——远程总线模式的类型前提。
var _ mq.Bus = (*uds.Client)(nil)

// TestBridgeInstanceRegisterCarriesUser：桥启动即发的 `instance-register` 必须携带**必填**
// `user`（61 §4.1 ⑤；`instance-bring-to-top` 跨用户判定依赖该字段）。
func TestBridgeInstanceRegisterCarriesUser(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	got := make(chan map[string]any, 1)
	sub, err := bus.On(msgkeys.TopicInstanceRegister, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		select {
		case got <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("bus.On: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	br := bridge.New("i1", "C:/wd", "C:/wd/.chonkpilot", func(string) {}, bus)
	if err := br.Start(); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	select {
	case m := <-got:
		if s, _ := m[msgkeys.InstanceRegisterEventUser].(string); strings.TrimSpace(s) == "" {
			t.Fatalf("instance-register 缺必填 user: %#v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 instance-register")
	}
}
