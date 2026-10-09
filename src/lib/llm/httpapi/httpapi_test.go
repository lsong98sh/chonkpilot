package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// fakePersist 在给定总线上注册最小 persist 应答（同主题 emit {req_id, ok, result}），
// 供不依赖 chonkpilot-data 的用例验证「上行 → 总线 → 回包」链路。
func fakePersist(t *testing.T, bus mq.Bus, subject string, result map[string]any) {
	t.Helper()
	_, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req struct {
			ReqID string `json:"req_id"`
			OK    *bool  `json:"ok"`
		}
		if err := json.Unmarshal(v.Payload, &req); err != nil || req.ReqID == "" || req.OK != nil {
			return nil // 只应答"请求"（应答载荷带 ok 字段 → 不再应答，防回环）
		}
		reply, _ := json.Marshal(map[string]any{"req_id": req.ReqID, "ok": true, "result": result})
		_ = bus.Emit(context.Background(), subject, reply)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// newTestServer 起一个真实监听的入口（127.0.0.1:0 → 随机端口）+ 真实进程内总线。
func newTestServer(t *testing.T, webRoot string) (*Server, mq.Bus, string) {
	t.Helper()
	return newTestServerOpts(t, Options{
		WorkDir: t.TempDir(),
		DataDir: t.TempDir(),
		WebRoot: webRoot,
	})
}

// newTestServerOpts 同上，但静态面等选项由调用方给出（覆盖内嵌静态面 WebFS 用例）。
func newTestServerOpts(t *testing.T, opts Options) (*Server, mq.Bus, string) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	opts.Bus = bus
	opts.Addr = "127.0.0.1:0"
	s := New(bus, opts)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, bus, "http://" + s.Addr()
}

// publish 发一次上行请求并解析结果信封。
func publish(t *testing.T, base, typ, payload string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"type": typ, "payload": payload})
	resp, err := http.Post(base+publishPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /publish: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /publish status=%d", resp.StatusCode)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode publish reply: %v", err)
	}
	return env
}

// ── SSE 客户端（测试用）──

type sseClient struct {
	lines chan string
	stop  context.CancelFunc
}

func openSSE(t *testing.T, base string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+eventsPath, nil)
	if err != nil {
		cancel()
		t.Fatalf("new events request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET /events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET /events status=%d", resp.StatusCode)
	}
	c := &sseClient{lines: make(chan string, 64), stop: cancel}
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			select {
			case c.lines <- sc.Text():
			default:
			}
		}
	}()
	// 等到 `: connected`（服务端已把本连接登记进广播表）。
	deadline := time.After(3 * time.Second)
	for {
		select {
		case line := <-c.lines:
			if strings.HasPrefix(line, ": connected") {
				return c
			}
		case <-deadline:
			t.Fatalf("SSE 未在超时内就绪")
		}
	}
}

func (c *sseClient) close() { c.stop() }

// nextData 等待一条 data 事件（可含子串 want；want 空 = 任意一条），超时返回 ""。
func (c *sseClient) nextData(want string, timeout time.Duration) string {
	deadline := time.After(timeout)
	for {
		select {
		case line := <-c.lines:
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			ev := strings.TrimPrefix(line, "data: ")
			if want == "" || strings.Contains(ev, want) {
				return ev
			}
		case <-deadline:
			return ""
		}
	}
}

// ── 静态面 / 引导 / shim ──

func TestStaticBootstrapAndShim(t *testing.T) {
	root := t.TempDir()
	index := "<html><head><title>t</title></head><body>CK-MARKER</body></html>"
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, base := newTestServer(t, root)

	// GET / → 200 + 关键标记（实例 id 注入 + shim 脚本标签）
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	raw, _ := readAll(resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status=%d", resp.StatusCode)
	}
	html := string(raw)
	for _, want := range []string{"CK-MARKER", `window.__chonkpilotInstanceId="` + s.InstanceID() + `"`, shimPath} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html 缺少标记 %q；got=%s", want, html)
		}
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("静态面应禁缓存，got=%q", resp.Header.Get("Cache-Control"))
	}

	// 资源可取
	resp2, err := http.Get(base + "/app.js")
	if err != nil {
		t.Fatalf("GET /app.js: %v", err)
	}
	b2, _ := readAll(resp2)
	resp2.Body.Close()
	if !strings.Contains(string(b2), "console.log") || !strings.Contains(resp2.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("app.js 未正确返回：ct=%q body=%q", resp2.Header.Get("Content-Type"), string(b2))
	}

	// GET /__ck_bridge.js → SSE 转投 shim（含 EventSource + emitRemote）
	resp3, err := http.Get(base + shimPath)
	if err != nil {
		t.Fatalf("GET shim: %v", err)
	}
	b3, _ := readAll(resp3)
	resp3.Body.Close()
	if !strings.Contains(string(b3), "EventSource") || !strings.Contains(string(b3), "emitRemote") {
		t.Fatalf("shim 内容不完整：%s", string(b3))
	}
}

func TestStaticDisabledWithoutWebRoot(t *testing.T) {
	_, _, base := newTestServer(t, "")
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("既无内嵌面又未配置 --web-root 应 503，got=%d", resp.StatusCode)
	}
}

// TestStaticEmbeddedFSAndWebRootOverride（2026-09-21）：静态面**内嵌面为默认**
// （browser 形态前端 go:embed 进 server exe → Options.WebFS），
// `--web-root` 显式指定时**覆盖**内嵌面；两路径都带 no-cache 头与 index.html 引导注入。
func TestStaticEmbeddedFSAndWebRootOverride(t *testing.T) {
	embedded := fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte("<html><head><title>e</title></head><body>EMB-MARKER</body></html>")},
		"assets/a.js": &fstest.MapFile{Data: []byte("console.log('emb')")},
	}
	s, _, base := newTestServerOpts(t, Options{
		WorkDir: t.TempDir(),
		DataDir: t.TempDir(),
		WebFS:   embedded,
	})

	// ① 未指定 --web-root → 走内嵌面（index 引导注入 + 资源可取 + no-cache）
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	raw, _ := readAll(resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("内嵌面 GET / status=%d body=%s", resp.StatusCode, raw)
	}
	html := string(raw)
	for _, want := range []string{"EMB-MARKER", `window.__chonkpilotInstanceId="` + s.InstanceID() + `"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("内嵌 index.html 缺少标记 %q；got=%s", want, html)
		}
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("内嵌面应禁缓存，got=%q", resp.Header.Get("Cache-Control"))
	}
	resp2, err := http.Get(base + "/assets/a.js")
	if err != nil {
		t.Fatalf("GET /assets/a.js: %v", err)
	}
	b2, _ := readAll(resp2)
	resp2.Body.Close()
	if !strings.Contains(string(b2), "console.log") {
		t.Fatalf("内嵌资源未正确返回：%q", string(b2))
	}

	// ② 内嵌面未命中的路径 → 404（不回落外部目录）
	resp3, err := http.Get(base + "/missing.js")
	if err != nil {
		t.Fatalf("GET /missing.js: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("内嵌面未命中应 404，got=%d", resp3.StatusCode)
	}

	// ③ 同时给 WebRoot（显式 --web-root）→ **外部目录覆盖内嵌面**
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"),
		[]byte("<html><head></head><body>EXT-MARKER</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, base2 := newTestServerOpts(t, Options{
		WorkDir: t.TempDir(),
		DataDir: t.TempDir(),
		WebRoot: root,
		WebFS:   embedded,
	})
	resp4, err := http.Get(base2 + "/")
	if err != nil {
		t.Fatalf("GET / (override): %v", err)
	}
	b4, _ := readAll(resp4)
	resp4.Body.Close()
	if !strings.Contains(string(b4), "EXT-MARKER") || strings.Contains(string(b4), "EMB-MARKER") {
		t.Fatalf("--web-root 应覆盖内嵌面：%s", string(b4))
	}
}

// ── 上行 → 总线 → 回包；总线事件 → SSE ──

func TestPublishHitsBusAndSSECarriesEvent(t *testing.T) {
	// 假 persist：应答 data-prj-config-list（同主题 emit {req_id, ok, result}）。
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><head></head></html>"), 0o644)
	s, bus, base := newTestServer(t, root)

	// 假 persist：应答 data-prj-config-list（同主题 emit {req_id, ok, result}）。
	fakePersist(t, bus, "data-prj-config-list", map[string]any{"list": map[string]any{"layout.sidebar": "300"}})

	// GET /events（先就绪，再触发事件）
	c := openSSE(t, base)
	defer c.close()

	// POST /publish（只读 data 面）→ 回包带 persist 结果
	env := publish(t, base, "data-prj-config-list", "{}")
	if env["ok"] != true {
		t.Fatalf("publish ok=false: %v", env)
	}
	res, _ := env["result"].(map[string]any)
	list, _ := res["list"].(map[string]any)
	if list["layout.sidebar"] != "300" {
		t.Fatalf("上行未经总线 persist 回包：%v", env)
	}

	// 总线事件（本实例归属）→ SSE 收到桥信封
	ev, _ := json.Marshal(map[string]any{"instance_id": s.InstanceID(), "marker": "E1"})
	_ = bus.Emit(context.Background(), "data-prj-config-refresh", ev)
	got := c.nextData("E1", 3*time.Second)
	if got == "" {
		t.Fatalf("SSE 未收到总线事件")
	}
	var envelope struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
		Source  string `json:"src"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("SSE 载荷不是桥信封：%s (%v)", got, err)
	}
	if envelope.Type != "data-prj-config-refresh" || envelope.Source != "mq" || !strings.Contains(envelope.Payload, "E1") {
		t.Fatalf("信封不符：%+v", envelope)
	}
}

// SSE instance 过滤：非本实例事件不投递（复用 acceptEventInstance 判据）。
func TestSSEFiltersForeignInstance(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><head></head></html>"), 0o644)
	s, bus, base := newTestServer(t, root)

	c := openSSE(t, base)
	defer c.close()

	// 他实例事件 → 不投递
	foreign, _ := json.Marshal(map[string]any{"instance_id": "other-instance", "marker": "FOREIGN"})
	_ = bus.Emit(context.Background(), "task-updated", foreign)
	if got := c.nextData("FOREIGN", 400*time.Millisecond); got != "" {
		t.Fatalf("外实例事件被投递：%s", got)
	}

	// 本实例事件 → 投递（并确认映射为前端 type）
	own, _ := json.Marshal(map[string]any{"instance_id": s.InstanceID(), "marker": "OWN"})
	_ = bus.Emit(context.Background(), "task-updated", own)
	got := c.nextData("OWN", 3*time.Second)
	if got == "" || !strings.Contains(got, `"type":"tasks.updated"`) {
		t.Fatalf("本实例事件未按映射投递：%s", got)
	}

	// 无归属字段事件 → 放行（data-*/filesys.* 语义）
	plain, _ := json.Marshal(map[string]any{"marker": "PLAIN"})
	_ = bus.Emit(context.Background(), "filesys.changed", plain)
	if got := c.nextData("PLAIN", 3*time.Second); got == "" {
		t.Fatalf("无归属事件未放行")
	}
}

// native 能力：明确「不支持」（不静默失败、不假成功）。
// 注：`gui.dir.open-dialog` 的**消息面**仍明确不支持（61 §1 result = `{path?}` 不变）；
// 其**等价面** = 非 MQ 的 `GET /dirs`（见 TestNativeDirOpenDialogEquivalent）。
func TestNativeCapabilitiesRejected(t *testing.T) {
	_, _, base := newTestServer(t, t.TempDir())
	for _, typ := range []string{
		"gui.dir.open-dialog", "gui.pick-executable", "gui.dir.open", "gui.console.open",
		"gui.reveal", "gui.open-with", "gui.capture", "gui.toolchain.detect",
		"gui.system.builtins", "gui.window.status", "gui.file.save",
	} {
		env := publish(t, base, typ, "{}")
		if env["ok"] != false {
			t.Fatalf("%s 应明确失败，got=%v", typ, env)
		}
		errs, _ := env["errors"].([]any)
		if len(errs) == 0 || !strings.Contains(errs[0].(string), "不支持") {
			t.Fatalf("%s 错误不明确：%v", typ, env)
		}
	}
}

// browser 形态「最近项目」删除（gui.recent.remove）：仅改 usr config 自由键 recent_dirs
// （保序、只删该条），**不触碰项目目录 / 数据资产**。
func TestBrowserRecentRemove(t *testing.T) {
	_, bus, base := newTestServer(t, t.TempDir())
	fakePersist(t, bus, "data-user-config-load", map[string]any{
		"data": map[string]any{"recent_dirs": `["D:/a","D:/b"]`},
	})
	saved := make(chan map[string]any, 1)
	if _, err := bus.On("data-user-config-save", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req struct {
			ReqID string         `json:"req_id"`
			OK    *bool          `json:"ok"`
			Data  map[string]any `json:"data"`
		}
		if json.Unmarshal(v.Payload, &req) != nil || req.ReqID == "" || req.OK != nil {
			return nil
		}
		select {
		case saved <- req.Data:
		default:
		}
		reply, _ := json.Marshal(map[string]any{
			"req_id": req.ReqID, "ok": true, "result": map[string]any{"ok": true},
		})
		_ = bus.Emit(context.Background(), "data-user-config-save", reply)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	env := publish(t, base, "gui.recent.remove", `{"path":"D:/a"}`)
	if env["ok"] != true {
		t.Fatalf("gui.recent.remove 应 ok，got %+v", env)
	}
	select {
	case data := <-saved:
		if got, _ := data["recent_dirs"].(string); got != `["D:/b"]` {
			t.Fatalf("仅应删 D:/a 并保序，got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("未捕获 data-user-config-save 写入")
	}
}

// native 等价面：`GET /dirs` 返回本 instance 允许目录（work_dir 及子目录）。
func TestNativeDirOpenDialogEquivalent(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wd, "node_modules"), 0o755); err != nil {
		t.Fatal(err) // 重型目录应被跳过
	}
	bus, _ := mq.New(mq.Options{Prefix: "chonk."})
	defer bus.Close()
	s := New(bus, Options{Bus: bus, WorkDir: wd, Addr: "127.0.0.1:0"})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Shutdown(context.Background()) }()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + dirsPath)
	if err != nil {
		t.Fatalf("GET /dirs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /dirs status=%d", resp.StatusCode)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode /dirs: %v", err)
	}
	if filepath.ToSlash(wd) != env["work_dir"] {
		t.Fatalf("work_dir 不符：%v", env["work_dir"])
	}
	dirs, _ := env["dirs"].([]any)
	if len(dirs) == 0 {
		t.Fatalf("允许目录清单为空")
	}
	found := map[string]bool{}
	for _, d := range dirs {
		m, _ := d.(map[string]any)
		if p, _ := m["path"].(string); p != "" {
			found[p] = true
		}
	}
	for _, want := range []string{filepath.ToSlash(wd), filepath.ToSlash(filepath.Join(wd, "sub")), filepath.ToSlash(filepath.Join(wd, "sub", "deep"))} {
		if !found[want] {
			t.Fatalf("清单缺少 %q：%v", want, dirs)
		}
	}
	if found[filepath.ToSlash(filepath.Join(wd, "node_modules"))] {
		t.Fatalf("重型目录不应进入清单：%v", dirs)
	}
}

// ── 多客户端（fan-out 到本 instance 全部客户端；非本 instance 不投递；不重复/不串）──

func TestMultiClientFanoutAndInstanceFilter(t *testing.T) {
	s, bus, base := newTestServer(t, t.TempDir())
	c1 := openSSE(t, base)
	defer c1.close()
	c2 := openSSE(t, base)
	defer c2.close()

	// ① 同一实例事件 → 两个客户端各收到一份（fan-out）
	own, _ := json.Marshal(map[string]any{"instance_id": s.InstanceID(), "marker": "FAN"})
	_ = bus.Emit(context.Background(), "task-updated", own)
	for i, c := range []*sseClient{c1, c2} {
		got := c.nextData("FAN", 3*time.Second)
		if got == "" || !strings.Contains(got, `"type":"tasks.updated"`) {
			t.Fatalf("客户端 #%d 未收到本实例事件：%q", i+1, got)
		}
		// ② 不重复投递：同一事件不再到达第二次
		if dup := c.nextData("FAN", 300*time.Millisecond); dup != "" {
			t.Fatalf("客户端 #%d 收到重复事件：%s", i+1, dup)
		}
	}

	// ③ 非本实例事件 → 两个客户端都不投递
	foreign, _ := json.Marshal(map[string]any{"instance_id": "other-instance", "marker": "FOREIGN"})
	_ = bus.Emit(context.Background(), "task-updated", foreign)
	for i, c := range []*sseClient{c1, c2} {
		if got := c.nextData("FOREIGN", 400*time.Millisecond); got != "" {
			t.Fatalf("客户端 #%d 收到外实例事件：%s", i+1, got)
		}
	}

	// ④ 任一客户端的操作不影响另一个：客户端 A 发只读 tools-list（请求不得回投为事件）
	env := publish(t, base, "tools-list", `{"instance_id":`+strconvQuote(s.InstanceID())+`}`)
	if env["ok"] != true {
		t.Fatalf("tools-list ok=false: %v", env)
	}
	for i, c := range []*sseClient{c1, c2} {
		if got := c.nextData("mcp-tools-list", 400*time.Millisecond); got != "" {
			t.Fatalf("客户端 #%d 收到他人请求回投（串流）：%s", i+1, got)
		}
	}

	// ⑤ 其后本实例事件仍正常 fan-out（证明 ④ 未破坏任一客户端）
	after, _ := json.Marshal(map[string]any{"instance_id": s.InstanceID(), "marker": "AFTER"})
	_ = bus.Emit(context.Background(), "task-updated", after)
	for i, c := range []*sseClient{c1, c2} {
		if got := c.nextData("AFTER", 3*time.Second); got == "" {
			t.Fatalf("客户端 #%d 在④之后未收到事件", i+1)
		}
	}
}

// ── filesys 托管：服务端应答 filesys.*，且 work_dir 由服务端绑定（不被浏览器覆盖）──

func TestFilesysHostedAndWorkDirBound(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("hello-ck"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 他目录（模拟浏览器自报 work_dir / 越界目标）
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "SECRET.txt"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	bus, _ := mq.New(mq.Options{Prefix: "chonk."})
	defer bus.Close()
	s := New(bus, Options{Bus: bus, WorkDir: wd, Addr: "127.0.0.1:0"})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Shutdown(context.Background()) }()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// ① 服务端可应答 filesys.list；且自报 work_dir=other 被覆盖 → 列的是服务端 workDir
	env := publish(t, ts.URL, "filesys.list", `{"work_dir":`+strconvQuote(other)+`,"path":""}`)
	if env["ok"] != true {
		t.Fatalf("filesys.list ok=false（服务端未托管 filesys？）：%v", env)
	}
	res, _ := env["result"].(map[string]any)
	if res == nil {
		t.Fatalf("filesys.list result 为空：%v", env)
	}
	if res["path"] != wd {
		t.Fatalf("work_dir 未被服务端绑定：path=%v want=%v", res["path"], wd)
	}
	names := childNames(res)
	if names["SECRET.txt"] {
		t.Fatalf("越权：他目录内容被列出：%v", names)
	}
	if !names["a.txt"] || !names["sub"] {
		t.Fatalf("服务端 workDir 条目缺失：%v", names)
	}

	// ② 绝对越界 path → filesys 第二道拒绝（forbidden）
	env2 := publish(t, ts.URL, "filesys.list", `{"path":`+strconvQuote(other)+`}`)
	if env2["ok"] != false {
		t.Fatalf("越界 path 应拒绝：%v", env2)
	}

	// ③ 读内容（真实条目）
	env3 := publish(t, ts.URL, "filesys.content", `{"path":"a.txt"}`)
	if env3["ok"] != true {
		t.Fatalf("filesys.content ok=false: %v", env3)
	}
	res3, _ := env3["result"].(map[string]any)
	if res3["content"] != "hello-ck" {
		t.Fatalf("内容不符：%v", res3)
	}

	// ④ 写操作：dir 越界（他目录）→ 拒绝且不落盘
	env4 := publish(t, ts.URL, "filesys.create", `{"dir":`+strconvQuote(other)+`,"name":"evil.txt","content":"x"}`)
	if env4["ok"] != false {
		t.Fatalf("越界写入应拒绝：%v", env4)
	}
	if _, err := os.Stat(filepath.Join(other, "evil.txt")); err == nil {
		t.Fatalf("越界写入落盘了")
	}
	// ⑤ 写操作：服务端 workDir 内 → 成功
	env5 := publish(t, ts.URL, "filesys.create", `{"dir":`+strconvQuote(wd)+`,"name":"new.txt","content":"ok"}`)
	if env5["ok"] != true {
		t.Fatalf("workDir 内写入失败：%v", env5)
	}
	if b, err := os.ReadFile(filepath.Join(wd, "new.txt")); err != nil || string(b) != "ok" {
		t.Fatalf("写入未落盘：%v %q", err, string(b))
	}
}

// childNames 取 filesys.list result.children 的名字集合。
func childNames(res map[string]any) map[string]bool {
	out := map[string]bool{}
	arr, _ := res["children"].([]any)
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				out[n] = true
			}
		}
	}
	return out
}

// strconvQuote JSON 字符串字面量（测试拼载荷用）。
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestPublishDataBindsInstanceID：data-* 上行**强制绑定**本实例的 instance_id——
// 浏览器端自报的他实例 id 被覆盖（不能借 payload 越权读写其它实例的数据面）。
func TestPublishDataBindsInstanceID(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><head></head></html>"), 0o644)
	s, bus, base := newTestServer(t, root)

	// 捕获到达总线的 data-* 请求（同时按协议应答，避免 persist 路径等待超时）。
	got := make(chan map[string]any, 1)
	_, err := bus.On("data-session-list", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req struct {
			ReqID      string `json:"req_id"`
			OK         *bool  `json:"ok"`
			InstanceID string `json:"instance_id"`
		}
		if json.Unmarshal(v.Payload, &req) != nil || req.ReqID == "" || req.OK != nil {
			return nil // 只处理请求，不处理应答（防回环）
		}
		select {
		case got <- map[string]any{"instance_id": req.InstanceID}:
		default:
		}
		reply, _ := json.Marshal(map[string]any{"req_id": req.ReqID, "ok": true, "result": map[string]any{}})
		_ = bus.Emit(context.Background(), "data-session-list", reply)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	env := publish(t, base, "data-session-list", `{"instance_id":"evil-instance"}`)
	if env["ok"] != true {
		t.Fatalf("data-session-list ok=false: %v", env)
	}
	select {
	case m := <-got:
		if m["instance_id"] != s.InstanceID() {
			t.Fatalf("data-* 的 instance_id 应被绑定为本实例：got=%v want=%v", m["instance_id"], s.InstanceID())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未捕获到达总线的 data-session-list 请求")
	}
}

// TestPublishBodyLimit B-31：/publish 请求体超上限（4MiB）→ 413（复用 {ok:false,errors}
// 信封）；上限在 isLoginTopic 分支前生效（登录面同样受限）。
func TestPublishBodyLimit(t *testing.T) {
	_, _, base := newTestServer(t, t.TempDir())

	big := strings.Repeat("a", (4<<20)+4096)
	body, _ := json.Marshal(map[string]string{"type": "login-in", "payload": big})
	resp, err := http.Post(base+publishPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /publish: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求体应 413，got %d", resp.StatusCode)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode 413 信封: %v", err)
	}
	if env["ok"] != false {
		t.Fatalf("413 信封应 ok=false：%v", env)
	}
}

// 上行白名单：白名单外点分主题拒绝；纯前端单字事件静默放行（不注入总线）。
func TestPublishWhitelist(t *testing.T) {
	_, bus, base := newTestServer(t, t.TempDir())

	env := publish(t, base, "other.topic", "{}")
	if env["ok"] != false {
		t.Fatalf("白名单外点分主题应拒绝：%v", env)
	}

	// 纯前端事件（单字，不在方法表白名单）→ ok、不注入总线
	seen := make(chan struct{}, 1)
	_, _ = bus.On("session-changed", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		select {
		case seen <- struct{}{}:
		default:
		}
		return nil
	})
	env2 := publish(t, base, "session-changed", `{"session_id":"s1"}`)
	if env2["ok"] != true {
		t.Fatalf("纯前端事件应放行：%v", env2)
	}
	select {
	case <-seen:
		t.Fatalf("纯前端事件不应注入总线")
	case <-time.After(200 * time.Millisecond):
	}
}

// llm-start 拆两步（session-start + session-send，注入 instance_id）。
func TestLLMStartSplit(t *testing.T) {
	s, bus, base := newTestServer(t, t.TempDir())

	got := make(chan map[string]any, 1)
	_, _ = bus.On("session-send", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil && m["type"] == "text-user" {
			select {
			case got <- m:
			default:
			}
		}
		return nil
	})
	env := publish(t, base, "llm-start", `{"session":"s1","q":"hi"}`)
	if env["ok"] != true {
		t.Fatalf("llm-start ok=false: %v", env)
	}
	select {
	case m := <-got:
		if m["instance_id"] != s.InstanceID() || m["content"] != "hi" || m["session"] != "s1" {
			t.Fatalf("session-send 载荷不符：%v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("未发出 session-send")
	}
}

// gui.init-data：只读文件树 + workDir（服务端 --work-dir 派生）。
func TestGUIInitDataReadOnly(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wd, "a.txt"), []byte("x"), 0o644)
	bus, _ := mq.New(mq.Options{Prefix: "chonk."})
	defer bus.Close()
	fakePersist(t, bus, "data-prj-config-list", map[string]any{"list": map[string]any{"layout.sidebar": "280"}})
	fakePersist(t, bus, "data-user-config-load", map[string]any{"data": map[string]any{"theme": "dark"}})
	s := New(bus, Options{Bus: bus, WorkDir: wd, DataDir: t.TempDir(), Addr: "127.0.0.1:0"})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Shutdown(context.Background()) }()

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	env := publish(t, ts.URL, "gui.init-data", "{}")
	if env["ok"] != true {
		t.Fatalf("init-data ok=false: %v", env)
	}
	res, _ := env["result"].(map[string]any)
	if res["workDir"] != wd {
		t.Fatalf("workDir 不符：%v", res["workDir"])
	}
	nodes, _ := res["treeData"].([]any)
	if len(nodes) == 0 {
		t.Fatalf("treeData 为空（应读到 work-dir 一层）")
	}
	layout, _ := res["layout"].(map[string]any)
	if layout["sidebar"] != "280" {
		t.Fatalf("layout 未从数据面读回：%v", res["layout"])
	}
	ui, _ := res["ui"].(map[string]any)
	if ui["theme"] != "dark" {
		t.Fatalf("ui 未从数据面读回：%v", res["ui"])
	}
}

// 目录穿越防护。
func TestStaticPathTraversal(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(secret, []byte("SECRET"), 0o644)
	_, _, base := newTestServer(t, root)

	resp, err := http.Get(base + "/..%2f..%2fsecret.txt")
	if err != nil {
		t.Fatalf("GET traversal: %v", err)
	}
	raw, _ := readAll(resp)
	resp.Body.Close()
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("目录穿越未被拦住：%s", string(raw))
	}
}

func readAll(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}

// TestMarkPublishedConcurrentSameSubject B-26：并发两次 markPublished 同 subject 后，
// 两份指纹均能在 1s 窗口内命中（单槽 pubStamp 会被后写覆盖 → 先写事件经 bus 回环时
// isSelfPublished 匹配失败 → SSE 重复投递一次）；未发布过的同名主题载荷不吞、命中一次性消耗。
func TestMarkPublishedConcurrentSameSubject(t *testing.T) {
	s := New(nil, Options{})
	const subject = "test/evt"
	payloads := [][]byte{[]byte(`{"n":1}`), []byte(`{"n":2}`)}
	var wg sync.WaitGroup
	wg.Add(len(payloads))
	for _, p := range payloads {
		go func(p []byte) {
			defer wg.Done()
			s.markPublished(subject, p)
		}(p)
	}
	wg.Wait()
	if s.isSelfPublished(subject, []byte(`{"n":3}`)) {
		t.Fatal("未发布过的载荷不应判为自发布")
	}
	for i, p := range payloads {
		if !s.isSelfPublished(subject, p) {
			t.Fatalf("第 %d 份指纹应在 1s 窗口内命中", i+1)
		}
	}
	if s.isSelfPublished(subject, payloads[0]) {
		t.Fatal("命中应一次性消耗，不应二次命中")
	}
}
