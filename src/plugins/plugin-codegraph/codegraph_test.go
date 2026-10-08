package codegraph

import (
	"context"
	"encoding/json"
	"errors"
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
	if len(queryTools) != 8 {
		t.Fatalf("expected 8 query tools, got %d", len(queryTools))
	}

	expectedNames := map[string]bool{
		"codegraph_symbol_search":        true,
		"codegraph_get_symbol_info":      true,
		"codegraph_callers":              true,
		"codegraph_callees":              true,
		"codegraph_get_dependency_graph": true,
		"codegraph_find_circular_deps":   true,
		"codegraph_analyze_complexity":   true,
		"codegraph_get_module_summary":   true,
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
			"query": map[string]any{"type": "string", "description": "search query"},
			"limit": map[string]any{"type": "integer", "description": "max results"},
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
	if !contains(schema, "query") || !contains(schema, "limit") {
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

func TestResolveExe(t *testing.T) {
	// Save and restore env
	oldEnv := os.Getenv("CODEGRAPH_EXE")
	defer os.Setenv("CODEGRAPH_EXE", oldEnv)
	os.Unsetenv("CODEGRAPH_EXE")

	const exeName = "chonkpilot-codegraph-mcp-server.exe"

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
		os.Setenv("CODEGRAPH_EXE", exePath)
		defer os.Unsetenv("CODEGRAPH_EXE")

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

// TestRunWithRetry：编排重试次数语义（T-11 失败重试）。
func TestRunWithRetry(t *testing.T) {
	cases := []struct {
		name      string
		attempts  int
		fails     int
		wantCalls int
		wantErr   bool
	}{
		{"首次成功", 3, 0, 1, false},
		{"重试后成功", 3, 2, 3, false},
		{"耗尽仍失败", 3, 9, 3, true},
		{"attempts<=0 视为 1", 0, 1, 1, true},
		{"attempts=1 不重试", 1, 5, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := runWithRetry(context.Background(), tc.attempts, 0, func() error {
				calls++
				if calls <= tc.fails {
					return errors.New("boom")
				}
				return nil
			})
			if calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestIdleReclaimDue：空闲引擎子进程回收判定（T-18 childIdleTimeout 接线）。
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

// TestReadEngineProgress：读取引擎落盘 meta.json 的进度（T-11 进度推送源）。
func TestReadEngineProgress(t *testing.T) {
	wd := t.TempDir()
	if _, _, _, ok := readEngineProgress(wd); ok {
		t.Fatal("meta.json 不存在时应返回 ok=false")
	}
	dir := filepath.Join(wd, ".chonkpilot", "codegraph")
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
	metaPath := filepath.Join(wd, ".chonkpilot", "codegraph", "meta.json")
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
	writeMeta("indexing", 10, 10)
	wait("10/10")

	// 完成（state=ready）后不再推送进度
	writeMeta("ready", 10, 10)
	time.Sleep(80 * time.Millisecond)
	select {
	case got := <-updates:
		t.Fatalf("state=ready 不应再推送进度：%s", got)
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

	d.schedule("wd-b", func() { mu.Lock(); fired++; mu.Unlock() })
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	got = fired
	mu.Unlock()
	if got != 2 {
		t.Fatalf("单次 schedule 应触发一次（累计 2），实际 %d 次", got)
	}
}

// newTestCodegraphWithLog 构造测试插件（引擎路径不存在 → 后台重建快速失败），
// 并返回日志快照读取函数（用于统计强制重建次数）。
func newTestCodegraphWithLog(window time.Duration) (*Codegraph, func() []string) {
	p := New(Options{Exe: "no-such-codegraph-engine.exe", RebuildDebounce: window})
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

// TestOnPrjConfigRefreshIndexKeysIdempotent：索引配置保存幂等 + 去抖合并——
// ① 相同值重复保存 → 不重建（0 次）；② 真变更 → 重建 1 次；
// ③ 一次保存写 exts + skip-dirs 两键 → 合并为 1 次；④ 未启用的 workdir 不重建；
// ⑤ 显式删键（回落默认集）→ 重建 1 次。
func TestOnPrjConfigRefreshIndexKeysIdempotent(t *testing.T) {
	refresh := func(p *Codegraph, id string, list map[string]any, op string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"id": id, "op": op, "list": list})
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

	// ① 相同值重复保存：同值 refresh → 不重建
	p, dump := newTestCodegraphWithLog(30 * time.Millisecond)
	p.works["/wd-same"] = &workRec{workDir: "/wd-same", refs: 1, enabled: true,
		cfgExts: "go,js", cfgSkipDirs: "node_modules", cfgSeen: true}
	refresh(p, extsKey, map[string]any{extsKey: "go,js"}, "save")
	refresh(p, extsKey, map[string]any{extsKey: "go,js"}, "save")
	refresh(p, skipDirsKey, map[string]any{skipDirsKey: "node_modules"}, "save")
	time.Sleep(150 * time.Millisecond)
	if n := rebuilds(dump()); n != 0 {
		t.Fatalf("相同值重复保存不应重建，实际 %d 次：%v", n, dump())
	}

	// ② 真变更 → 重建 1 次
	p2, dump2 := newTestCodegraphWithLog(30 * time.Millisecond)
	p2.works["/wd-change"] = &workRec{workDir: "/wd-change", refs: 1, enabled: true,
		cfgExts: "go,js", cfgSeen: true}
	refresh(p2, extsKey, map[string]any{extsKey: "go,py,ts"}, "save")
	time.Sleep(150 * time.Millisecond)
	if n := rebuilds(dump2()); n != 1 {
		t.Fatalf("真实变更应重建一次，实际 %d 次：%v", n, dump2())
	}

	// ③ 一次保存写两键（两次 refresh）→ 去抖合并为一次重建
	p3, dump3 := newTestCodegraphWithLog(30 * time.Millisecond)
	p3.works["/wd-coalesce"] = &workRec{workDir: "/wd-coalesce", refs: 1, enabled: true,
		cfgExts: "go,js", cfgSkipDirs: "node_modules", cfgSeen: true}
	refresh(p3, extsKey, map[string]any{extsKey: "go,py", skipDirsKey: "node_modules"}, "save")
	refresh(p3, skipDirsKey, map[string]any{extsKey: "go,py", skipDirsKey: "dist"}, "save")
	time.Sleep(150 * time.Millisecond)
	if n := rebuilds(dump3()); n != 1 {
		t.Fatalf("一次保存两键应只重建一次，实际 %d 次：%v", n, dump3())
	}

	// ④ 未启用的 workdir：不重建
	p4, dump4 := newTestCodegraphWithLog(30 * time.Millisecond)
	p4.works["/wd-off"] = &workRec{workDir: "/wd-off", refs: 1, enabled: false, cfgSeen: true}
	refresh(p4, extsKey, map[string]any{extsKey: "go,py"}, "save")
	time.Sleep(150 * time.Millisecond)
	if n := rebuilds(dump4()); n != 0 {
		t.Fatalf("未启用的 workdir 不应重建，实际 %d 次：%v", n, dump4())
	}

	// ⑤ 显式删键（= 回落引擎默认集）→ 重建 1 次
	p5, dump5 := newTestCodegraphWithLog(30 * time.Millisecond)
	p5.works["/wd-del"] = &workRec{workDir: "/wd-del", refs: 1, enabled: true,
		cfgExts: "go,js", cfgSeen: true}
	refresh(p5, extsKey, map[string]any{}, "delete")
	time.Sleep(150 * time.Millisecond)
	if n := rebuilds(dump5()); n != 1 {
		t.Fatalf("删键应重建一次，实际 %d 次：%v", n, dump5())
	}
}

// TestNormalizeAction：codegraph.action 取值归一（大小写/空白容错；未知/空 → 忽略）。
func TestNormalizeAction(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"clear", actionClear},
		{"CLEAR", actionClear},
		{" retry ", actionRetry},
		{"rebuild", actionRebuild},
		{"", ""},
		{"   ", ""},
		{"purge", ""},
	}
	for _, tc := range cases {
		if got := normalizeAction(tc.in); got != tc.want {
			t.Errorf("normalizeAction(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestOnActionSignalRoutes：codegraph.action 信号路由——clear → 清除路径；retry → 降级全量重建；
// rebuild → 强制重建；未知值 / 删键 / 未启用 workdir → 不动作（零新增消息主题）。
func TestOnActionSignalRoutes(t *testing.T) {
	refresh := func(p *Codegraph, op string, list map[string]any) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"id": actionKey, "op": op, "list": list})
		if err := p.onPrjConfigRefresh(context.Background(), "", &mq.Value{Payload: payload}); err != nil {
			t.Fatalf("onPrjConfigRefresh(%s): %v", actionKey, err)
		}
	}
	count := func(lines []string, sub string) int {
		n := 0
		for _, l := range lines {
			if strings.Contains(l, sub) {
				n++
			}
		}
		return n
	}

	// clear → 清除索引产物
	p1, dump1 := newTestCodegraphWithLog(30 * time.Millisecond)
	p1.works["/wd-clear"] = &workRec{workDir: "/wd-clear", refs: 1, enabled: true}
	refresh(p1, "save", map[string]any{actionKey: "clear"})
	if n := count(dump1(), "清除索引产物"); n != 1 {
		t.Fatalf("clear 应触发一次清除，实际 %d 次：%v", n, dump1())
	}

	// retry → 引擎无失败明细 → 降级为全量重建
	p2, dump2 := newTestCodegraphWithLog(30 * time.Millisecond)
	p2.works["/wd-retry"] = &workRec{workDir: "/wd-retry", refs: 1, enabled: true}
	refresh(p2, "save", map[string]any{actionKey: "retry"})
	if n := count(dump2(), "降级为全量重建"); n != 1 {
		t.Fatalf("retry 应降级为一次全量重建，实际 %d 次：%v", n, dump2())
	}

	// rebuild → 强制全量重建
	p3, dump3 := newTestCodegraphWithLog(30 * time.Millisecond)
	p3.works["/wd-rebuild"] = &workRec{workDir: "/wd-rebuild", refs: 1, enabled: true}
	refresh(p3, "save", map[string]any{actionKey: "rebuild"})
	if n := count(dump3(), "强制重建索引"); n != 1 {
		t.Fatalf("rebuild 应强制重建一次，实际 %d 次：%v", n, dump3())
	}

	// 未知值 / 删键 / 空值 / 未启用 workdir → 不动作
	p4, dump4 := newTestCodegraphWithLog(30 * time.Millisecond)
	p4.works["/wd-off"] = &workRec{workDir: "/wd-off", refs: 1, enabled: false}
	p4.works["/wd-on"] = &workRec{workDir: "/wd-on", refs: 1, enabled: true}
	refresh(p4, "save", map[string]any{actionKey: "purge"})
	refresh(p4, "delete", map[string]any{})
	refresh(p4, "save", map[string]any{actionKey: ""})
	time.Sleep(60 * time.Millisecond)
	for _, sub := range []string{"清除索引产物", "强制重建索引", "降级为全量重建"} {
		if n := count(dump4(), sub); n != 0 {
			t.Fatalf("未知值/删键不应动作（%s），实际 %d 次：%v", sub, n, dump4())
		}
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

// ─── 多 workdir（T-12 / P2-4：每 workdir 独立 client）────────────

// stubEngine 是引擎客户端桩（实现 engineClient）：记录调用（workdir|工具名）并按预置应答，
// 用于断言多 workdir 的调用路由与状态隔离（无需真实引擎子进程）。
type stubEngine struct {
	mu       sync.Mutex
	calls    []string          // 形如 "/wd-a|codegraph_status"
	lastArgs map[string]any    // 最近一次调用参数（断言 workdir 注入）
	statuses []string          // codegraph_status 依次应答（末项粘滞）
	replies  map[string]string // 其它工具固定应答
	closed   int
}

func (s *stubEngine) call(_ context.Context, name string, args map[string]any) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wd, _ := args["workdir"].(string)
	s.calls = append(s.calls, wd+"|"+name)
	s.lastArgs = args
	if name == "codegraph_status" {
		if len(s.statuses) == 0 {
			return "", false, errors.New("stub: 未预置 codegraph_status 应答")
		}
		out := s.statuses[0]
		if len(s.statuses) > 1 {
			s.statuses = s.statuses[1:]
		}
		return out, false, nil
	}
	if out, ok := s.replies[name]; ok {
		return out, false, nil
	}
	return "", false, fmt.Errorf("stub: 未预置 %s 应答", name)
}

func (s *stubEngine) close() {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
}

// snapshot 返回调用序列与 close 次数的快照。
func (s *stubEngine) snapshot() ([]string, int, map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...), s.closed, s.lastArgs
}

// toolNamesOf 从 "<workdir>|<工具名>" 序列中取出该 workdir 的工具名序列（并断言无串台）。
func toolNamesOf(t *testing.T, calls []string, wd string) []string {
	t.Helper()
	var out []string
	for _, c := range calls {
		gotWd, name, ok := strings.Cut(c, "|")
		if !ok {
			t.Fatalf("非法调用记录 %q", c)
		}
		if gotWd != wd {
			t.Fatalf("workdir 串台：期望 %s，实际调用 %s", wd, c)
		}
		out = append(out, name)
	}
	return out
}

// newStubPersistBus 冒充 persist 数据面：应答 data-prj-config-load（恒回空串 = 引擎默认集）
// 与 data-prj-config-save（按 "instance_id|key" 记录到 saved）。应答形态对齐
// chonkpilot-data/persist：把 {req_id, ok, result} 作为**消息**回发到请求同一主题
// （插件 dataclient.Emit 按 req_id 关联收敛；带 ok 的载荷即应答，订阅侧跳过以防回环）。
func newStubPersistBus(t *testing.T, mu *sync.Mutex, saved map[string]string) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	respond := func(subject string, result map[string]any, reqID string) {
		go func() {
			_ = bus.Emit(context.Background(), subject, map[string]any{
				"req_id": reqID, "ok": true, "result": result,
			})
		}()
	}
	onReq := func(_ context.Context, subject string, v *mq.Value) error {
		var m struct {
			OK         *bool  `json:"ok"` // 已带 ok = 应答消息（跳过）
			ReqID      string `json:"req_id"`
			InstanceID string `json:"instance_id"`
			Data       struct {
				ID    string `json:"id"`
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"data"`
		}
		if err := json.Unmarshal(v.Payload, &m); err != nil || m.OK != nil {
			return nil
		}
		switch subject {
		case subjectPrjConfigLoad:
			respond(subject, map[string]any{"data": ""}, m.ReqID)
		case subjectPrjConfigSave:
			mu.Lock()
			saved[m.InstanceID+"|"+m.Data.Key] = m.Data.Value
			mu.Unlock()
			respond(subject, map[string]any{}, m.ReqID)
		}
		return nil
	}
	load, err := bus.On(subjectPrjConfigLoad, 0, onReq)
	if err != nil {
		t.Fatalf("subscribe %s: %v", subjectPrjConfigLoad, err)
	}
	save, err := bus.On(subjectPrjConfigSave, 0, onReq)
	if err != nil {
		t.Fatalf("subscribe %s: %v", subjectPrjConfigSave, err)
	}
	t.Cleanup(func() { _ = load.Unsubscribe(); _ = save.Unsubscribe(); _ = bus.Close() })
	return bus
}

// TestClientForPerWorkdirIdentity：clientFor 语义——同一 workdir 复用同一引擎子进程 client，
// 不同 workdir 各自独立 client（P2-4 解除「全局单份」）。
func TestClientForPerWorkdirIdentity(t *testing.T) {
	p := New(Options{Exe: "dummy-not-spawned.exe"})
	a1 := p.clientFor("/wd-a")
	a2 := p.clientFor("/wd-a")
	b := p.clientFor("/wd-b")

	if a1 != a2 {
		t.Fatal("同一 workdir 应复用同一引擎子进程 client")
	}
	if a1 == b {
		t.Fatal("不同 workdir 必须各自独立 client（每 workdir 独立引擎子进程）")
	}
	if _, ok := a1.(*client); !ok {
		t.Fatalf("默认实现应为真实引擎 client，实际 %T", a1)
	}
	p.clientMu.Lock()
	n := len(p.clients)
	p.clientMu.Unlock()
	if n != 2 {
		t.Fatalf("clients 应含 2 个 workdir，实际 %d", n)
	}
}

// TestSweepIdleClientPerWorkdir：空闲回收按 workdir 独立判定——无活跃实例且空闲超
// childIdleTimeout 的 workdir 子进程被回收；活跃 workdir（即使基准陈旧）与未超阈值的空闲
// workdir（新起子进程）不被回收；多轮周期维护（tick）后活跃 workdir 仍存活。
func TestSweepIdleClientPerWorkdir(t *testing.T) {
	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	idle, active, fresh := &stubEngine{}, &stubEngine{}, &stubEngine{}
	stale := time.Now().Add(-childIdleTimeout - time.Minute)
	p.clients["/wd-idle"] = &clientRec{c: idle, lastUsed: stale}
	p.clients["/wd-active"] = &clientRec{c: active, lastUsed: stale}
	p.clients["/wd-fresh"] = &clientRec{c: fresh, lastUsed: time.Now()}
	p.works["/wd-active"] = &workRec{workDir: "/wd-active", refs: 1, enabled: true}

	p.sweepIdleClient()

	p.clientMu.Lock()
	_, idleAlive := p.clients["/wd-idle"]
	_, activeAlive := p.clients["/wd-active"]
	_, freshAlive := p.clients["/wd-fresh"]
	p.clientMu.Unlock()
	if idleAlive {
		t.Fatal("无活跃实例且空闲超阈值的 workdir 子进程应被回收")
	}
	if !activeAlive {
		t.Fatal("有活跃实例的 workdir 子进程不应被回收")
	}
	if !freshAlive {
		t.Fatal("空闲未超阈值的 workdir 子进程不应被回收")
	}
	if _, closed, _ := idle.snapshot(); closed != 1 {
		t.Fatalf("被回收的 client 应 close 一次，实际 %d", closed)
	}
	if _, closed, _ := active.snapshot(); closed != 0 {
		t.Fatalf("活跃 workdir 的 client 不应被 close，实际 %d", closed)
	}

	// 多轮周期维护：活跃 workdir 仍存活（基准被刷新），已回收的不再出现
	for i := 0; i < 8; i++ {
		p.tick()
	}
	p.clientMu.Lock()
	_, activeAlive = p.clients["/wd-active"]
	_, idleAlive = p.clients["/wd-idle"]
	p.clientMu.Unlock()
	if !activeAlive || idleAlive {
		t.Fatalf("多轮 tick 后状态异常：activeAlive=%v idleAlive=%v", activeAlive, idleAlive)
	}
}

// TestMultiWorkdirIndexAndQueryIsolation：两个 workdir 各自独立引擎子进程——
// ① 各自索引（configure/initialize/status 只抵达自己的子进程，互不串台）；
// ② 索引状态各自回写（codegraph.status 落到各自实例的 prj 库，值互不覆盖）；
// ③ 查询按调用实例路由到该 workdir 的独立子进程（LLM 不传 workdir，由插件注入）。
func TestMultiWorkdirIndexAndQueryIsolation(t *testing.T) {
	var mu sync.Mutex
	saved := map[string]string{}
	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	p.deps.Bus = newStubPersistBus(t, &mu, saved)

	const wdA, wdB = "/wd-a", "/wd-b"
	newEngine := func(wd string, files int, symbol string) *stubEngine {
		return &stubEngine{
			statuses: []string{
				`{"state":"not_initialized","workdir":"` + wd + `"}`,
				fmt.Sprintf(`{"state":"ready","workdir":"%s","files":%d}`, wd, files),
			},
			replies: map[string]string{
				"codegraph_configure":     `{"ok":true}`,
				"codegraph_initialize":    fmt.Sprintf(`{"ok":true,"files":%d}`, files),
				"codegraph_symbol_search": fmt.Sprintf(`{"workdir":"%s","symbols":[{"name":"%s"}]}`, wd, symbol),
			},
		}
	}
	engA, engB := newEngine(wdA, 3, "AlphaOnly"), newEngine(wdB, 7, "BetaOnly")
	p.clients[wdA] = &clientRec{c: engA, lastUsed: time.Now()}
	p.clients[wdB] = &clientRec{c: engB, lastUsed: time.Now()}
	p.insts["i-a"] = &instRec{workdir: wdA, last: time.Now()}
	p.insts["i-b"] = &instRec{workdir: wdB, last: time.Now()}
	p.works[wdA] = &workRec{workDir: wdA, refs: 1, enabled: true}
	p.works[wdB] = &workRec{workDir: wdB, refs: 1, enabled: true}

	p.ensureWorkspace(wdA, false)
	p.ensureWorkspace(wdB, false)

	// ① 各自索引：调用序列一致且无串台（toolNamesOf 同时断言 workdir 归属）
	wantIdx := []string{"codegraph_configure", "codegraph_status", "codegraph_initialize", "codegraph_status"}
	callsA, _, _ := engA.snapshot()
	callsB, _, _ := engB.snapshot()
	for _, c := range []struct {
		calls []string
		wd    string
	}{{callsA, wdA}, {callsB, wdB}} {
		got := toolNamesOf(t, c.calls, c.wd)
		if strings.Join(got, ",") != strings.Join(wantIdx, ",") {
			t.Fatalf("workdir %s 索引调用序列 %v，期望 %v", c.wd, got, wantIdx)
		}
	}

	// ② 状态各自回写：各自文件数正确且互不覆盖
	mu.Lock()
	stA, stB := saved["i-a|"+statusKey], saved["i-b|"+statusKey]
	mu.Unlock()
	if !strings.Contains(stA, wdA) || !strings.Contains(stA, `"files":3`) {
		t.Fatalf("i-a 的 codegraph.status 应反映 %s 的索引结果，实际 %q", wdA, stA)
	}
	if !strings.Contains(stB, wdB) || !strings.Contains(stB, `"files":7`) {
		t.Fatalf("i-b 的 codegraph.status 应反映 %s 的索引结果，实际 %q", wdB, stB)
	}
	if strings.Contains(stA, wdB) || strings.Contains(stB, wdA) {
		t.Fatalf("两个 workdir 的状态串台：stA=%q stB=%q", stA, stB)
	}

	// ③ 查询：LLM 不传 workdir → 按调用实例路由到该 workdir 的独立子进程
	v := &mq.Value{Payload: []byte(`{"tool":"codegraph_symbol_search","args":{"query":"AlphaOnly"},"context":{"instance_id":"i-a"}}`)}
	if err := p.onToolCall(context.Background(), toolCallSubject, v); err != nil {
		t.Fatalf("onToolCall: %v", err)
	}
	res, _ := v.Result.(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("应答 content 异常：%v", v.Result)
	}
	item, _ := content[0].(map[string]any)
	if text, _ := item["text"].(string); !strings.Contains(text, "AlphaOnly") {
		t.Fatalf("查询应答应来自 %s 的引擎，实际 %v", wdA, text)
	}
	callsA, _, lastArgsA := engA.snapshot()
	callsB, _, lastArgsB := engB.snapshot()
	if len(callsB) != len(wantIdx) {
		t.Fatalf("查询不应触达其它 workdir 的引擎：%v", callsB)
	}
	if lastArgsB["workdir"] != wdB {
		t.Fatalf("%s 的引擎最后一次调用应为索引流程：%v", wdB, lastArgsB)
	}
	got := toolNamesOf(t, callsA[len(wantIdx):], wdA)
	if len(got) != 1 || got[0] != "codegraph_symbol_search" {
		t.Fatalf("%s 应收到一次查询调用，实际 %v", wdA, got)
	}
	if lastArgsA["workdir"] != wdA || lastArgsA["query"] != "AlphaOnly" {
		t.Fatalf("查询参数应注入 workdir=%s 且保留 query，实际 %v", wdA, lastArgsA)
	}
}

// ─── 索引配置（codegraph.skip-dirs / codegraph.stack-gitignore）────────────

// splitRules 是**测试专用**助手：生产侧 skip-dirs 解析已统一由
// github.com/chonkpilot/chonkpilot-ignore 的 ignore.ConfigOptions 完成（本插件不再自解析），
// 此处保留一份同名实现以锁定"保序且保留重复项"的解析语义。
func splitRules(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

// TestSplitRules：skip-dirs 解析——按逗号/分号/换行分隔、去空白；
// **保序且保留重复项**（gitignore 语义下顺序有意义：'!' 取反 + 后一条覆盖前一条）。
func TestSplitRules(t *testing.T) {
	got := splitRules("dist/, !dist/keep.txt\nlogs;  \n!logs/a,dist/")
	want := []string{"dist/", "!dist/keep.txt", "logs", "!logs/a", "dist/"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("splitRules = %v，期望 %v", got, want)
	}
	if got := splitRules("  \n , ; "); got != nil {
		t.Fatalf("全空白应无规则，实际 %v", got)
	}
}

// newStubPersistBusValues 冒充 persist 的 data-prj-config-load：按 key 返回预置值。
func newStubPersistBusValues(t *testing.T, values map[string]string) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	onReq := func(_ context.Context, subject string, v *mq.Value) error {
		var m struct {
			OK    *bool  `json:"ok"` // 已带 ok = 应答消息（跳过）
			ReqID string `json:"req_id"`
			Data  struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(v.Payload, &m); err != nil || m.OK != nil || subject != subjectPrjConfigLoad {
			return nil
		}
		go func() {
			_ = bus.Emit(context.Background(), subject, map[string]any{
				"req_id": m.ReqID, "ok": true, "result": map[string]any{"data": values[m.Data.ID]},
			})
		}()
		return nil
	}
	sub, err := bus.On(subjectPrjConfigLoad, 0, onReq)
	if err != nil {
		t.Fatalf("subscribe %s: %v", subjectPrjConfigLoad, err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe(); _ = bus.Close() })
	return bus
}

// TestReadIndexConfig：项目级索引配置读取——skip-dirs 按 gitignore 规则**原样透传**
// （不折名、不丢 '!'/glob），stack-gitignore 透传为布尔，并播种幂等比较基线。
func TestReadIndexConfig(t *testing.T) {
	const rawRules = "dist/\n!dist/keep.txt, logs"
	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	p.deps.Bus = newStubPersistBusValues(t, map[string]string{
		extsKey:           "go, js, go",
		skipDirsKey:       rawRules,
		stackGitignoreKey: "true",
	})
	p.insts["i1"] = &instRec{workdir: "/wd"}
	p.works["/wd"] = &workRec{workDir: "/wd", refs: 1, enabled: true}

	exts, rules, stack := p.readIndexConfig("/wd")
	if strings.Join(exts, ",") != "go,js" {
		t.Fatalf("exts = %v，期望 [go js]（去重保序）", exts)
	}
	want := []string{"dist/", "!dist/keep.txt", "logs"}
	if strings.Join(rules, "|") != strings.Join(want, "|") {
		t.Fatalf("rules = %v，期望原样透传 %v", rules, want)
	}
	if !stack {
		t.Fatal("stack-gitignore=true 应透传为 true")
	}
	if r := p.works["/wd"]; !r.cfgSeen || r.cfgExts != "go, js, go" || r.cfgSkipDirs != rawRules || r.cfgStack != "true" {
		t.Fatalf("比较基线播种错误：%+v", r)
	}

	// 缺省（键不存在）→ 空规则 + stack=false
	p.deps.Bus = newStubPersistBusValues(t, map[string]string{extsKey: "go"})
	exts2, rules2, stack2 := p.readIndexConfig("/wd")
	if exts2 == nil || rules2 == nil || len(rules2) != 0 || stack2 {
		t.Fatalf("缺省配置应回落空切片 + stack=false，实际 exts=%v rules=%v stack=%v", exts2, rules2, stack2)
	}
}
