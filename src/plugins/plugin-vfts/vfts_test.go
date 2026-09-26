package vfts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

func TestQueryToolDefinitions(t *testing.T) {
	if len(queryTools) != 1 {
		t.Fatalf("expected 1 query tool, got %d", len(queryTools))
	}

	expectedNames := map[string]bool{
		"vfts_query": true,
	}

	for _, tool := range queryTools {
		if tool.name == "" {
			t.Error("found tool with empty name")
			continue
		}
		if !expectedNames[tool.name] {
			t.Errorf("unexpected tool name: %q", tool.name)
		}
		delete(expectedNames, tool.name)

		if tool.description == "" {
			t.Errorf("tool %q has empty description", tool.name)
		}

		// Verify no workdir in props (plugin removes it)
		if _, hasWorkdir := tool.props["workdir"]; hasWorkdir {
			t.Errorf("tool %q should not have workdir parameter", tool.name)
		}
	}

	if len(expectedNames) > 0 {
		missing := make([]string, 0, len(expectedNames))
		for n := range expectedNames {
			missing = append(missing, n)
		}
		t.Fatalf("missing tools: %v", missing)
	}
}

func TestSchemaJSON(t *testing.T) {
	tool := &gatewayTool{
		name:        "test_tool",
		description: "test description",
		props: map[string]any{
			"match": map[string]any{"type": "string", "description": "match query"},
			"topK":  map[string]any{"type": "integer", "description": "max results"},
		},
	}

	schema := tool.schemaJSON()
	if schema == "" {
		t.Fatal("schemaJSON returned empty string")
	}

	// Should be valid JSON
	if schema[0] != '{' {
		t.Errorf("expected JSON object, got: %s", schema)
	}

	// Should contain properties
	if !contains(schema, "match") || !contains(schema, "topK") {
		t.Errorf("schema should contain properties: %s", schema)
	}

	// Should NOT contain required (plugin tools have no required for workdir)
	if contains(schema, "required") {
		t.Errorf("schema should not contain required field: %s", schema)
	}
}

func TestToolReply(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		r := toolReply("hello world", false)
		if r["resultType"] != "complete" {
			t.Errorf("expected resultType=complete, got %v", r["resultType"])
		}
		if r["isError"] != false {
			t.Errorf("expected isError=false, got %v", r["isError"])
		}
		content, ok := r["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("expected content array with 1 item, got %v", r["content"])
		}
		item, ok := content[0].(map[string]any)
		if !ok || item["type"] != "text" || item["text"] != "hello world" {
			t.Errorf("unexpected content item: %v", content[0])
		}
	})

	t.Run("error", func(t *testing.T) {
		r := toolReply("error msg", true)
		if r["isError"] != true {
			t.Errorf("expected isError=true, got %v", r["isError"])
		}
		content := r["content"].([]any)
		item := content[0].(map[string]any)
		if item["text"] != "error msg" {
			t.Errorf("expected text=error msg, got %v", item["text"])
		}
	})
}

func TestStrval(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{"", ""},
		{true, "true"},
		{false, "false"},
		{nil, ""},
		{42, "42"},
		{3.14, "3.14"},
	}
	for _, tt := range tests {
		got := strval(tt.in)
		if got != tt.want {
			t.Errorf("strval(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveExe(t *testing.T) {
	// Save and restore env
	oldEnv := os.Getenv("VFTS_EXE")
	defer os.Setenv("VFTS_EXE", oldEnv)
	os.Unsetenv("VFTS_EXE")

	const exeName = "chonkpilot-vfts-mcp-server.exe"

	t.Run("explicit path", func(t *testing.T) {
		dir := t.TempDir()
		exePath := filepath.Join(dir, exeName)
		os.WriteFile(exePath, []byte("dummy"), 0o644)

		got, err := resolveExe(exePath)
		if err != nil {
			t.Fatalf("resolveExe failed: %v", err)
		}
		if got == "" {
			t.Fatal("expected non-empty path")
		}
	})

	t.Run("env var", func(t *testing.T) {
		dir := t.TempDir()
		exePath := filepath.Join(dir, exeName)
		os.WriteFile(exePath, []byte("dummy"), 0o644)
		os.Setenv("VFTS_EXE", exePath)
		defer os.Unsetenv("VFTS_EXE")

		got, err := resolveExe("")
		if err != nil {
			t.Fatalf("resolveExe failed: %v", err)
		}
		if got == "" {
			t.Fatal("expected non-empty path")
		}
	})

	t.Run("same dir as caller", func(t *testing.T) {
		// This test is tricky because it depends on the test binary location.
		// We just verify it doesn't panic and returns an error (since test binary
		// is probably not in the same dir as the engine).
		_, err := resolveExe("")
		// It may or may not find the engine, but shouldn't panic
		_ = err
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestIdleReclaimDue：空闲引擎子进程回收判定（childIdleTimeout 接线）。
func TestIdleReclaimDue(t *testing.T) {
	now := time.Now()
	old := now.Add(-10 * time.Minute)
	cases := []struct {
		name    string
		active  bool
		last    time.Time
		timeout time.Duration
		want    bool
	}{
		{"活跃中不回收", true, old, 5 * time.Minute, false},
		{"从未活跃不回收", false, time.Time{}, 5 * time.Minute, false},
		{"空闲未超阈值不回收", false, now.Add(-1 * time.Minute), 5 * time.Minute, false},
		{"空闲超阈值回收", false, old, 5 * time.Minute, true},
	}
	for _, tc := range cases {
		if got := idleReclaimDue(tc.active, tc.last, now, tc.timeout); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// TestPhaseStatusJSON：进度快照 payload（state/phase/done/total）。
func TestPhaseStatusJSON(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(phaseStatusJSON(phaseIndex, 3, 10)), &m); err != nil {
		t.Fatal(err)
	}
	if m["state"] != "indexing" || m["phase"] != "index" ||
		m["progressDone"].(float64) != 3 || m["progressTotal"].(float64) != 10 {
		t.Fatalf("unexpected phase status JSON: %v", m)
	}
}

// TestReadEngineProgress：读取引擎落盘 meta.json 的进度（进度推送源）。
func TestReadEngineProgress(t *testing.T) {
	wd := t.TempDir()
	if _, _, _, ok := readEngineProgress(wd); ok {
		t.Fatal("meta.json 不存在时应返回 ok=false")
	}
	dir := filepath.Join(wd, ".chonkpilot", "vfts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"),
		[]byte(`{"state":"indexing","progressDone":7,"progressTotal":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state, done, total, ok := readEngineProgress(wd)
	if !ok || state != "indexing" || done != 7 || total != 42 {
		t.Fatalf("readEngineProgress = (%q,%d,%d,%v)", state, done, total, ok)
	}
}

// TestPollEngineProgressSequence：进度序列——随 meta.json 变化按序回调，state!=indexing 不再回调。
func TestPollEngineProgressSequence(t *testing.T) {
	wd := t.TempDir()
	metaPath := filepath.Join(wd, ".chonkpilot", "vfts", "meta.json")
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMeta := func(state string, done, total int) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"state": state, "progressDone": done, "progressTotal": total})
		if err := os.WriteFile(metaPath, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeMeta("indexing", 0, 10)

	updates := make(chan string, 16)
	stop := make(chan struct{})
	go pollEngineProgress(stop, wd, 5*time.Millisecond, func(done, total int) {
		select {
		case updates <- fmt.Sprintf("%d/%d", done, total):
		default:
		}
	})
	defer close(stop)

	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case got := <-updates:
				if got == want {
					return
				}
			case <-time.After(20 * time.Millisecond):
			}
		}
		t.Fatalf("未观测到进度更新 %s", want)
	}
	wait("0/10")
	writeMeta("indexing", 3, 10)
	wait("3/10")
	writeMeta("indexing", 50, 120)
	wait("50/120")

	// 完成（state=ready）后不再推送进度
	writeMeta("ready", 120, 120)
	time.Sleep(50 * time.Millisecond)
	select {
	case got := <-updates:
		t.Fatalf("ready 后不应再推送进度，却收到 %s", got)
	default:
	}
}

// TestRebuildDebouncerCoalesces：去抖器——窗口内多次 schedule（同 key）只触发一次；
// 单次 schedule 仍必触发（不丢重建）。
func TestRebuildDebouncerCoalesces(t *testing.T) {
	d := newRebuildDebouncer(30 * time.Millisecond)
	var mu sync.Mutex
	fired := 0
	d.schedule("wd-a", func() { mu.Lock(); fired++; mu.Unlock() })
	d.schedule("wd-a", func() { mu.Lock(); fired++; mu.Unlock() })
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	got := fired
	mu.Unlock()
	if got != 1 {
		t.Fatalf("窗口内两次 schedule 应只触发一次，实际 %d 次", got)
	}

	// 单次 schedule 必触发一次
	d.schedule("wd-b", func() { mu.Lock(); fired++; mu.Unlock() })
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	got = fired
	mu.Unlock()
	if got != 2 {
		t.Fatalf("单次 schedule 应触发一次（累计 2），实际 %d 次", got)
	}
}

// newTestVftsWithLog 构造测试插件（引擎路径不存在 → 后台重建快速失败），并返回日志快照读取函数。
func newTestVftsWithLog(window time.Duration) (*Vfts, func() []string) {
	p := New(Options{Exe: "no-such-vfts-engine.exe", RebuildDebounce: window})
	var mu sync.Mutex
	var lines []string
	p.logf = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	return p, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

// TestOnPrjConfigRefreshIndexKeysCoalesce：一次保存写 exts + skip-dirs 两键（= 两次
// data-prj-config-refresh）→ 插件侧只重建一次；单键变更仍重建一次。
func TestOnPrjConfigRefreshIndexKeysCoalesce(t *testing.T) {
	refresh := func(p *Vfts, id string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"id": id, "op": "save"})
		if err := p.onPrjConfigRefresh(context.Background(), "", &mq.Value{Payload: payload}); err != nil {
			t.Fatalf("onPrjConfigRefresh(%s): %v", id, err)
		}
	}
	rebuilds := func(lines []string) int {
		n := 0
		for _, l := range lines {
			if strings.Contains(l, "强制重建索引") {
				n++
			}
		}
		return n
	}

	// 一次保存两键：两次 refresh → 去抖合并为一次重建
	p, dump := newTestVftsWithLog(30 * time.Millisecond)
	p.works["/wd-coalesce"] = &workRec{workDir: "/wd-coalesce", refs: 1, enabled: true}
	refresh(p, extsKey)
	refresh(p, skipDirsKey)
	time.Sleep(200 * time.Millisecond)
	if n := rebuilds(dump()); n != 1 {
		t.Fatalf("一次保存两键应只重建一次，实际 %d 次：%v", n, dump())
	}

	// 单键变更：仍必须重建一次（不得"改了不重建"）
	p2, dump2 := newTestVftsWithLog(30 * time.Millisecond)
	p2.works["/wd-single"] = &workRec{workDir: "/wd-single", refs: 1, enabled: true}
	refresh(p2, extsKey)
	time.Sleep(200 * time.Millisecond)
	if n := rebuilds(dump2()); n != 1 {
		t.Fatalf("单键变更应重建一次，实际 %d 次：%v", n, dump2())
	}

	// 未启用的 workdir：不触发重建
	p3, dump3 := newTestVftsWithLog(30 * time.Millisecond)
	p3.works["/wd-off"] = &workRec{workDir: "/wd-off", refs: 1, enabled: false}
	refresh(p3, extsKey)
	time.Sleep(200 * time.Millisecond)
	if n := rebuilds(dump3()); n != 0 {
		t.Fatalf("未启用的 workdir 不应重建，实际 %d 次：%v", n, dump3())
	}
}

// TestSweepIdleClientReclaimAndLazyRebuild：清扫回收语义——有活跃实例不回收（并刷新活跃基准）、
// 空闲未超阈值不回收、空闲超阈值回收共享 client；回收后经 sharedClient 懒重建（新实例）。
func TestSweepIdleClientReclaimAndLazyRebuild(t *testing.T) {
	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	r := &workRec{workDir: "/wd"}
	p.works["/wd"] = r
	old := p.sharedClient() // 懒建基准实例（newClient 惰性，不 spawn）

	// 有活跃实例（refs>0）：无论空闲多久都不回收，且刷新 lastActiveAt
	r.refs = 1
	p.mu.Lock()
	p.lastActiveAt = time.Now().Add(-2 * childIdleTimeout)
	p.mu.Unlock()
	p.sweepIdleClient()
	if got := p.sharedClient(); got != old {
		t.Fatal("有活跃实例时不应回收共享 client")
	}
	p.mu.Lock()
	last := p.lastActiveAt
	p.mu.Unlock()
	if time.Since(last) > time.Second {
		t.Fatalf("活跃实例存在时应刷新 lastActiveAt，实际距今 %v", time.Since(last))
	}

	// 引用归零但刚活跃过：未超阈值 → 不回收
	r.refs = 0
	p.sweepIdleClient()
	if got := p.sharedClient(); got != old {
		t.Fatal("空闲未超阈值不应回收共享 client")
	}

	// 空闲超阈值：回收（client 置空 + 关闭会话）
	p.mu.Lock()
	p.lastActiveAt = time.Now().Add(-childIdleTimeout - time.Second)
	p.mu.Unlock()
	p.sweepIdleClient()
	p.clientMu.Lock()
	got := p.client
	p.clientMu.Unlock()
	if got != nil {
		t.Fatal("空闲超阈值应回收共享 client")
	}

	// 回收后懒重建：新实例非 nil 且不同于被回收实例
	rebuilt := p.sharedClient()
	if rebuilt == nil || rebuilt == old {
		t.Fatal("回收后应懒重建新的共享 client 实例")
	}
}

// newStubGatewayBus 建内存总线并冒充 gateway 方法面（mcp-tools-register/unregister 应答），
// 注销请求的工具名追加到 unregistered（互斥锁保护，供测试读取）。
func newStubGatewayBus(t *testing.T, mu *sync.Mutex, unregistered *[]string) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	reg, err := bus.On(subjectToolRegister, 0, func(_ context.Context, _ string, v *mq.Value) error {
		v.Result = map[string]any{"ok": true}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", subjectToolRegister, err)
	}
	unreg, err := bus.On(subjectToolUnregister, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(v.Payload, &m)
		mu.Lock()
		*unregistered = append(*unregistered, m.Name)
		mu.Unlock()
		v.Result = map[string]any{"ok": true}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", subjectToolUnregister, err)
	}
	t.Cleanup(func() { _ = reg.Unsubscribe(); _ = unreg.Unsubscribe(); _ = bus.Close() })
	return bus
}

// TestNoHeartbeatTimeoutUnregister：合并单进程形态不做心跳超时退出判定——实例心跳陈旧（远超原
// 90s 阈值）且周期维护（tick）已跑多轮后，实例绑定与 gateway 工具注册面必须保持不变
// （回归：无心跳发布方时 GUI 启动 90s 后查询工具被误注销）。
func TestNoHeartbeatTimeoutUnregister(t *testing.T) {
	if heartbeatJudged {
		t.Skip("分离形态（-tags split）启用心跳超时判定 → 本用例只适用合并单进程形态（见 sweep_split_test.go）")
	}
	var mu sync.Mutex
	var unregistered []string
	bus := newStubGatewayBus(t, &mu, &unregistered)

	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	p.deps.Bus = bus
	wd := "/wd-stale"
	p.insts["i1"] = &instRec{workdir: wd, last: time.Now().Add(-time.Hour)} // 心跳陈旧
	p.works[wd] = &workRec{workDir: wd, refs: 1, enabled: true}
	p.syncTools() // 实例在位 + 已启用 → 注册查询工具
	p.mu.Lock()
	registered := p.registered
	p.mu.Unlock()
	if !registered {
		t.Fatal("setup：启用中的实例应已注册查询工具")
	}

	// 周期维护多轮（15s/轮；原 90s 超时判定第 6 轮即命中）
	for i := 0; i < 8; i++ {
		p.tick()
	}

	p.mu.Lock()
	_, alive := p.insts["i1"]
	registered = p.registered
	refs := p.works[wd].refs
	p.mu.Unlock()
	if !alive || refs != 1 {
		t.Fatalf("心跳陈旧不应移除实例：alive=%v refs=%d", alive, refs)
	}
	if !registered {
		t.Fatal("心跳陈旧不应注销 gateway 查询工具")
	}
	mu.Lock()
	got := append([]string(nil), unregistered...)
	mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("心跳陈旧不应产生注销调用：%v", got)
	}
}

// TestExplicitExitUnregisters：显式 instance-exit（GUI/CLI 退出注销）仍走 instanceGone →
// 引用归零 → 注销 gateway 查询工具（移除超时判定后该清理路径不回归）。
func TestExplicitExitUnregisters(t *testing.T) {
	var mu sync.Mutex
	var unregistered []string
	bus := newStubGatewayBus(t, &mu, &unregistered)

	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	p.deps.Bus = bus
	wd := "/wd-exit"
	p.insts["i1"] = &instRec{workdir: wd, last: time.Now()}
	p.works[wd] = &workRec{workDir: wd, refs: 1, enabled: true}
	p.syncTools()

	p.onInstanceExit("", []byte(`{"instance_id":"i1"}`))

	p.mu.Lock()
	_, alive := p.insts["i1"]
	registered := p.registered
	owner := p.regOwner
	p.mu.Unlock()
	if alive || registered || owner != "" {
		t.Fatalf("显式退出应清理实例与工具面：alive=%v registered=%v owner=%q", alive, registered, owner)
	}
	mu.Lock()
	got := append([]string(nil), unregistered...)
	mu.Unlock()
	if len(got) != len(queryTools) {
		t.Fatalf("显式退出应注销 %d 个查询工具，实际 %d：%v", len(queryTools), len(got), got)
	}
}
