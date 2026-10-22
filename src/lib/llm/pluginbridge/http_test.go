package pluginbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/transport"
)

// startHTTPBridge 起一个同时开帧（临时 UDS）与 HTTP（随机 loopback 端口）前门的服务端。
func startHTTPBridge(t *testing.T, token string) *Server {
	t.Helper()
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	srv := New(Options{
		Bus:       bus,
		Addr:      filepath.Join(t.TempDir(), "plugin.sock"),
		Version:   "test",
		HTTPAddr:  "127.0.0.1:0",
		HTTPToken: token,
	})
	srv.Register("test.echo", func(_ context.Context, _ *Session, args json.RawMessage) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, errf(CodeArgsInvalid, "bad args")
		}
		return map[string]any{"echo": in.Text}, nil
	})
	if err := srv.Start(); err != nil {
		bus.Close()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(); _ = bus.Close() })
	return srv
}

func httpDo(t *testing.T, method, url, token, pluginID string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if pluginID != "" {
		req.Header.Set(httpHeaderPlugin, pluginID)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func httpHello(t *testing.T, base, token, id string) (string, string) {
	t.Helper()
	status, out := httpDo(t, http.MethodPost, base+httpHelloPath, token, "", map[string]any{"id": id})
	if status != http.StatusOK {
		t.Fatalf("hello status=%d body=%v", status, out)
	}
	pid, _ := out["plugin_id"].(string)
	iid, _ := out["instance_id"].(string)
	if pid != id || iid == "" {
		t.Fatalf("hello 应答不符: %v", out)
	}
	return pid, iid
}

// sseStream 读取 SSE 帧（跳过注释 / ping 行）并推入通道。
func sseStream(t *testing.T, req *http.Request) (<-chan transport.Frame, func()) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("events status=%d body=%s", resp.StatusCode, raw)
	}
	ch := make(chan transport.Frame, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var f transport.Frame
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f) != nil {
				continue
			}
			ch <- f
		}
	}()
	return ch, func() { _ = resp.Body.Close() }
}

func waitFrame(t *testing.T, ch <-chan transport.Frame, kind transport.Kind, name string, timeout time.Duration) transport.Frame {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				t.Fatalf("SSE 流已关闭（等待 %s/%s）", kind, name)
			}
			if f.Kind == kind && f.Name == name {
				return f
			}
		case <-deadline:
			t.Fatalf("等待 %s/%s 超时", kind, name)
		}
	}
}

// TestHTTPFrontDoorHelloAndCall：hello → 复用会话 → 业务方法 dispatch。
func TestHTTPFrontDoorHelloAndCall(t *testing.T) {
	srv := startHTTPBridge(t, "")
	base := "http://" + srv.HTTPAddr()

	pid, iid := httpHello(t, base, "", "test.http.bot")
	if !strings.HasPrefix(iid, "plugin:test.http.bot:") {
		t.Fatalf("instance_id=%q", iid)
	}

	// 空 body = {}（handler 解析出空 text）
	status, out := httpDo(t, http.MethodPost, base+httpPrefix+"test.echo", "", pid, nil)
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("echo status=%d body=%v", status, out)
	}
	result, _ := out["result"].(map[string]any)
	if result["echo"] != "" {
		t.Fatalf("空 body result=%v", out["result"])
	}

	status, out = httpDo(t, http.MethodPost, base+httpPrefix+"test.echo", "", pid, map[string]any{"text": "hi"})
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("echo status=%d body=%v", status, out)
	}
	result, _ = out["result"].(map[string]any)
	if result["echo"] != "hi" {
		t.Fatalf("echo result=%v", out["result"])
	}

	// 再次 hello 复用同一会话（instance_id 不变）
	_, iid2 := httpHello(t, base, "", "test.http.bot")
	if iid2 != iid {
		t.Fatalf("hello 未复用会话: %q != %q", iid2, iid)
	}

	// 未知方法 → HTTP 200 + ok:false
	status, out = httpDo(t, http.MethodPost, base+httpPrefix+"test.nope", "", pid, map[string]any{})
	if status != http.StatusOK || out["ok"] != false {
		t.Fatalf("unknown method status=%d body=%v", status, out)
	}
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 || !strings.Contains(errs[0].(string), CodeMethodNotFound) {
		t.Fatalf("unknown method errors=%v", out["errors"])
	}

	// 缺 X-Plugin-ID → 明确错误
	status, out = httpDo(t, http.MethodPost, base+httpPrefix+"test.echo", "", "", map[string]any{})
	if status != http.StatusBadRequest || out["ok"] != false {
		t.Fatalf("missing plugin id status=%d body=%v", status, out)
	}
}

// TestHTTPFrontDoorSSE：Broadcast → evt；ShutdownPlugin → ctl。
func TestHTTPFrontDoorSSE(t *testing.T) {
	srv := startHTTPBridge(t, "")
	base := "http://" + srv.HTTPAddr()
	pid, _ := httpHello(t, base, "", "test.http.sse")

	req, err := http.NewRequest(http.MethodGet, base+httpEventsPath, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(httpHeaderPlugin, pid)
	frames, stop := sseStream(t, req)
	defer stop()

	srv.Broadcast("server-ready", map[string]any{"form": "test"})
	f := waitFrame(t, frames, transport.KindEvt, "server-ready", 3*time.Second)
	var payload map[string]any
	if err := json.Unmarshal(f.Payload, &payload); err != nil || payload["form"] != "test" {
		t.Fatalf("server-ready payload=%s err=%v", f.Payload, err)
	}

	if !srv.ShutdownPlugin(pid) {
		t.Fatal("ShutdownPlugin 未找到 HTTP 会话")
	}
	f = waitFrame(t, frames, transport.KindCtl, "plugin.shutdown", 3*time.Second)
	if err := json.Unmarshal(f.Payload, &payload); err != nil || payload["grace_ms"] != float64(5000) {
		t.Fatalf("shutdown payload=%s err=%v", f.Payload, err)
	}
}

// TestHTTPFrontDoorToken：缺失 / 错误 → 401；正确 → 200。
func TestHTTPFrontDoorToken(t *testing.T) {
	srv := startHTTPBridge(t, "s3cret-token")
	base := "http://" + srv.HTTPAddr()

	status, out := httpDo(t, http.MethodPost, base+httpHelloPath, "", "", map[string]any{"id": "tok.bot"})
	if status != http.StatusUnauthorized || out["ok"] != false {
		t.Fatalf("缺失 token status=%d body=%v", status, out)
	}
	status, out = httpDo(t, http.MethodPost, base+httpHelloPath, "wrong", "", map[string]any{"id": "tok.bot"})
	if status != http.StatusUnauthorized || out["ok"] != false {
		t.Fatalf("错误 token status=%d body=%v", status, out)
	}
	pid, _ := httpHello(t, base, "s3cret-token", "tok.bot")

	status, out = httpDo(t, http.MethodPost, base+httpPrefix+"test.echo", "wrong", pid, map[string]any{"text": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("业务调用错误 token status=%d body=%v", status, out)
	}
	status, out = httpDo(t, http.MethodPost, base+httpPrefix+"test.echo", "s3cret-token", pid, map[string]any{"text": "ok"})
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("业务调用正确 token status=%d body=%v", status, out)
	}
}

// TestHTTPFrontDoorRejectsNonLoopback：非 loopback 绑定 → Start 返回错误。
func TestHTTPFrontDoorRejectsNonLoopback(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()
	srv := New(Options{
		Bus:      bus,
		Addr:     filepath.Join(t.TempDir(), "plugin.sock"),
		HTTPAddr: "0.0.0.0:0",
	})
	if err := srv.Start(); err == nil {
		_ = srv.Close()
		t.Fatal("非 loopback HTTPAddr 应启动失败")
	}
}

// TestHTTPFrontDoorClose：close 幂等；Server.Close 后端口不再可用。
func TestHTTPFrontDoorClose(t *testing.T) {
	srv := startHTTPBridge(t, "")
	base := "http://" + srv.HTTPAddr()
	pid, _ := httpHello(t, base, "", "test.http.close")

	status, out := httpDo(t, http.MethodPost, base+httpClosePath, "", pid, nil)
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("close status=%d body=%v", status, out)
	}
	status, out = httpDo(t, http.MethodPost, base+httpClosePath, "", pid, nil)
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("close 幂等 status=%d body=%v", status, out)
	}
	// 会话已关闭 → 业务调用要求重新 hello
	status, out = httpDo(t, http.MethodPost, base+httpPrefix+"test.echo", "", pid, map[string]any{"text": "x"})
	if status != http.StatusBadRequest || out["ok"] != false {
		t.Fatalf("close 后调用 status=%d body=%v", status, out)
	}

	_ = srv.Close()
	_ = srv.Close() // 幂等

	req, _ := http.NewRequest(http.MethodPost, base+httpHelloPath, strings.NewReader(`{"id":"x"}`))
	cli := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	if _, err := cli.Do(req); err == nil {
		t.Fatal("Server.Close 后 HTTP 端口仍可用")
	}
}
