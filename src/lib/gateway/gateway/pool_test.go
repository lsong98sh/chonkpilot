// 按 workdir 隔离的连接池白盒测试（package mcpgateway 内部；2026-09-16）：
//   - isolate=true（stdio 缺省）：同 server 不同 workdir 各自独立子进程/管道，并发不互相阻塞；
//   - isolate=false：全 server 仅共享一条连接（现状）；
//   - workdir 空闲回收：超时无调用 → 关闭该 workdir 的连接/子进程并移出池，其它 workdir 不受影响；
//   - isolate=true 但调用上下文缺 workdir → 回落共享连接 + warn（不报错）；
//   - 旧配置（无 isolate 字段）行为 = 按 transport 推断（stdio → 隔离）。
//
// 下游 = 测试二进制自 re-exec 的 stdio MCP server（pid / slow 工具，见 async_test.go runStdioHelper）。
package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── 测试辅助 ─────────────────────────────────────────

// newIsolateTestProvider 建一个 stdio 下游 provider（测试二进制自 re-exec），
// isolate=nil 时按 entry 的 transport 推断；idleTTL = callTimeout（池回收 TTL 复用该项）。
func newIsolateTestProvider(t *testing.T, isolate *bool, callTimeout time.Duration, logf func(string, ...any)) *proxyProvider {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("skip：测试二进制路径不可用（%v）", err)
	}
	if logf == nil {
		logf = t.Logf
	}
	p, err := newProxyProvider(&ServerEntry{
		ID: "iso", Runtime: exe, Transport: "stdio", Isolate: isolate,
		Env: []string{"GATEWAY_STDIO_HELPER=1"},
	}, callTimeout, logf)
	if err != nil {
		t.Fatalf("newProxyProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.connect(ctx); err != nil { // 共享槽（控制面）建连
		t.Fatalf("connect: %v", err)
	}
	return p
}

// callFrom 从指定 workdir 调用下游工具，返回 "<pid>:<ms|空>" 原文与耗时。
// wd 为空 = 调用上下文不带 work_dir（回落共享槽路径）。
func callFrom(t *testing.T, p *proxyProvider, tool, wd string) (string, time.Duration) {
	t.Helper()
	text, elapsed, err := callFromErr(p, tool, wd)
	if err != nil {
		t.Fatalf("call %s（workdir=%q）: %v", tool, wd, err)
	}
	return text, elapsed
}

// callFromErr 同 callFrom 但不 Fatal（可安全用于测试 goroutine；instance 维度缺省）。
func callFromErr(p *proxyProvider, tool, wd string) (string, time.Duration, error) {
	return callScopedErr(p, tool, wd, "")
}

// callScopedErr 在调用上下文同时注入 (instance_id, work_dir) 后调用下游工具，返回原文与耗时
// （2026-09-19 缺口 1：连接池隔离键 = 二者组合）。
func callScopedErr(p *proxyProvider, tool, wd, inst string) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if wd != "" || inst != "" {
		ctx = withTurnContext(ctx, Context{WorkDir: wd, InstanceID: inst})
	}
	start := time.Now()
	res, err := p.Call(ctx, tool, map[string]any{})
	elapsed := time.Since(start)
	if err != nil {
		return "", elapsed, err
	}
	if len(res.Content) == 0 {
		return "", elapsed, fmt.Errorf("call %s 无内容: %v", tool, res)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return "", elapsed, fmt.Errorf("call %s 内容类型 = %T", tool, res.Content[0])
	}
	return strings.TrimSpace(tc.Text), elapsed, nil
}

// pidFrom 取指定 workdir（空 = 无 workdir）调用 pid 工具的子进程 pid。
func pidFrom(t *testing.T, p *proxyProvider, wd string) string {
	t.Helper()
	text, _ := callFrom(t, p, "pid", wd)
	return pidOf(text)
}

// pidScoped 取指定 (instance, workdir) 调用 pid 工具的子进程 pid。
func pidScoped(t *testing.T, p *proxyProvider, wd, inst string) string {
	t.Helper()
	text, _, err := callScopedErr(p, "pid", wd, inst)
	if err != nil {
		t.Fatalf("pid 调用（instance=%q workdir=%q）: %v", inst, wd, err)
	}
	return pidOf(text)
}

// poolSize 返回当前池中 workdir 连接槽数量。
func poolSize(p *proxyProvider) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pool)
}

// waitPoolSize 轮询等待池大小达到 want（最多 5s）。
func waitPoolSize(t *testing.T, p *proxyProvider, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if poolSize(p) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("池大小未达期望：got %d, want %d", poolSize(p), want)
}

// ─── ① 两个 workdir 并发：各自独立子进程/管道，总耗时 ≈ 单次 ───

func TestIsolateParallelWorkdirsIndependentPipes(t *testing.T) {
	t.Setenv("GATEWAY_STDIO_SLOW_MS", "800")
	p := newIsolateTestProvider(t, boolPtr(true), 30*time.Second, t.Logf)
	if !p.isolate {
		t.Fatalf("isolate=true 未生效（p.isolate=false）")
	}

	// 预热两个 workdir 的隔离槽（进程启动/握手耗时不计入并发比较）
	pidA := pidFrom(t, p, `E:\proj\aa`)
	pidB := pidFrom(t, p, `E:\proj\bb`)
	pidShared := pidFrom(t, p, "")
	if poolSize(p) != 2 {
		t.Fatalf("池应有 2 个 workdir 槽，got %d", poolSize(p))
	}
	if pidA == pidB {
		t.Fatalf("不同 workdir 应各持独立子进程/管道（同 pid=%s）", pidA)
	}
	if pidA == pidShared || pidB == pidShared {
		t.Fatalf("共享槽应与隔离槽不同进程：shared=%s A=%s B=%s", pidShared, pidA, pidB)
	}
	// 池复用：同 workdir 再次调用仍是同一子进程
	if again := pidFrom(t, p, `E:\proj\aa`); again != pidA {
		t.Fatalf("同 workdir 应复用连接：pid %s → %s", pidA, again)
	}

	// 单次（串行）耗时基线
	_, single := callFrom(t, p, "slow", `E:\proj\aa`)

	// 两个不同 workdir 并发（若共享单管道会被串行化 → 总耗时 ≈ 2×）
	var wg sync.WaitGroup
	results := make([]string, 2)
	var callErrs [2]error
	for i, wd := range []string{`E:\proj\aa`, `E:\proj\bb`} {
		wg.Add(1)
		go func(i int, wd string) {
			defer wg.Done()
			results[i], _, callErrs[i] = callFromErr(p, "slow", wd)
		}(i, wd)
	}
	parallelStart := time.Now()
	wg.Wait()
	total := time.Since(parallelStart)
	for i, err := range callErrs {
		if err != nil {
			t.Fatalf("并发调用[%d]失败: %v", i, err)
		}
	}

	t.Logf("单次 slow=%v（A=%s）；两 workdir 并发总耗时=%v（A=%s, B=%s）",
		single, pidOf(results[0]), total, pidOf(results[0]), pidOf(results[1]))
	if pidOf(results[0]) == pidOf(results[1]) {
		t.Fatalf("并发调用未隔离到不同子进程：%s vs %s", results[0], results[1])
	}
	if total > single+single/2 {
		t.Fatalf("两个 workdir 的调用被串行阻塞：单次=%v，并发总耗时=%v（应 ≈ 单次）", single, total)
	}
}

// ─── ② isolate=false：仅一条共享连接（现状） ───

func TestIsolateFalseSharesSingleConn(t *testing.T) {
	p := newIsolateTestProvider(t, boolPtr(false), 30*time.Second, t.Logf)
	if p.isolate {
		t.Fatalf("isolate=false 显式设置应生效（p.isolate=true）")
	}
	pidA := pidFrom(t, p, `E:\proj\aa`)
	pidB := pidFrom(t, p, `E:\proj\bb`)
	pidShared := pidFrom(t, p, "")
	if pidA != pidB || pidA != pidShared {
		t.Fatalf("isolate=false 应全 server 共享单连接：A=%s B=%s shared=%s", pidA, pidB, pidShared)
	}
	if n := poolSize(p); n != 0 {
		t.Fatalf("isolate=false 不应建 pool 条目，got %d", n)
	}
	t.Logf("isolate=false：A/B/无 workdir 均命中同一子进程 pid=%s，pool=%d", pidA, poolSize(p))
}

// ─── ③ 空闲回收：超时无调用 → 关闭该 workdir 的子进程并移出池，其它 workdir 不受影响 ───

func TestIsolateIdleReclaimPerWorkdir(t *testing.T) {
	const ttl = 1 * time.Second // 复用 Params.CallTimeout 作为池空闲 TTL（扫描间隔 = TTL/2）
	p := newIsolateTestProvider(t, boolPtr(true), ttl, t.Logf)

	pidA := pidFrom(t, p, `E:\proj\aa`)
	pidB := pidFrom(t, p, `E:\proj\bb`)
	if poolSize(p) != 2 {
		t.Fatalf("池应有 2 个槽，got %d", poolSize(p))
	}

	// A 保持活跃（每 300ms 一次调用刷新 lastUsed），B 空闲 → B 应被回收而 A 不受影响
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(300 * time.Millisecond):
				_, _, _ = callFromErr(p, "pid", `E:\proj\aa`)
			}
		}
	}()

	waitPoolSize(t, p, 1) // 只剩 A（B 已回收）
	pidA2 := pidFrom(t, p, `E:\proj\aa`)
	if pidA2 != pidA {
		t.Fatalf("A 的隔离槽被误回收：pid %s → %s", pidA, pidA2)
	}
	close(stop)
	<-done
	// B 下次调用按 workdir 懒建 → 新子进程
	pidB2 := pidFrom(t, p, `E:\proj\bb`)
	if pidB2 == pidB {
		t.Fatalf("B 回收后应懒建新子进程：pid 仍为 %s", pidB2)
	}
	t.Logf("空闲回收：pool 2 → 1（B pid %s→%s 重建，A pid %s 未受影响）", pidB, pidB2, pidA)
}

// ─── ④ isolate=true 但 workdir 缺失 → 回落共享连接 + warn（不报错） ───

func TestIsolateMissingWorkdirFallsBackShared(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	logf := func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	p := newIsolateTestProvider(t, boolPtr(true), 30*time.Second, logf)

	shared := pidFrom(t, p, "")
	again := pidFrom(t, p, "") // 第二次不应重复告警
	if shared != again {
		t.Fatalf("缺 workdir 应回落同一共享连接：%s vs %s", shared, again)
	}
	if n := poolSize(p); n != 0 {
		t.Fatalf("缺 workdir 不应建 pool 条目，got %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	warnCount := 0
	for _, l := range logs {
		if strings.Contains(l, "no work_dir") && strings.Contains(l, "fallback to shared conn") {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Fatalf("warn 应恰好一次（回落后不刷屏），got %d：%v", warnCount, logs)
	}
	t.Logf("缺 workdir：回落共享连接 pid=%s，warn=%d 条：%s", shared, warnCount, logs)
}

// ─── ⑤ 旧配置（无 isolate 字段）：按 transport 推断，stdio 走隔离 ───

func TestIsolateDefaultInferredByTransport(t *testing.T) {
	cases := []struct {
		name string
		e    ServerEntry
		want bool
	}{
		{"旧配置 stdio（仅 runtime）", ServerEntry{ID: "s", Runtime: "x"}, true},
		{"旧配置 stdio（显式 transport）", ServerEntry{ID: "s", Runtime: "x", Transport: "stdio"}, true},
		{"旧配置 http（仅 url）", ServerEntry{ID: "h", URL: "http://x"}, false},
		{"旧配置 http（显式）", ServerEntry{ID: "h", URL: "http://x", Transport: "http"}, false},
		{"旧配置 sse", ServerEntry{ID: "e", URL: "http://x", Transport: "sse"}, false},
		{"显式 true（http）", ServerEntry{ID: "h", URL: "http://x", Isolate: boolPtr(true)}, true},
		{"显式 false（stdio）", ServerEntry{ID: "s", Runtime: "x", Isolate: boolPtr(false)}, false},
	}
	for _, c := range cases {
		if got := c.e.IsolateEnabled(); got != c.want {
			t.Fatalf("%s：IsolateEnabled()=%v, want %v", c.name, got, c.want)
		}
	}

	// 旧配置（无 isolate 键）的 stdio server：运行期确按 workdir 隔离（不同 workdir 不同进程）
	p := newIsolateTestProvider(t, nil, 30*time.Second, t.Logf)
	if !p.isolate {
		t.Fatalf("旧配置 stdio 应推断为隔离（p.isolate=false）")
	}
	pidA := pidFrom(t, p, `E:\proj\aa`)
	pidB := pidFrom(t, p, `E:\proj\bb`)
	if pidA == pidB {
		t.Fatalf("旧配置 stdio 应隔离：不同 workdir 同 pid=%s", pidA)
	}
	t.Logf("旧配置 stdio：A=%s B=%s（隔离生效）", pidA, pidB)
}

// ─── 补充：servers.list `isolate=` 解析 + Invalidate 覆盖全池 ───

func TestParseServersListIsolateAndInvalidatePool(t *testing.T) {
	entries, err := parseServersList([]byte(
		"mcp.alias=sa\nsa.runtime=node\nsa.args=a.js\nsa.isolate=true\n" +
			"mcp.alias=sb\nsb.runtime=node\nsb.args=b.js\nsb.isolate=false\n" +
			"mcp.alias=sc\nsc.runtime=node\nsc.args=c.js\n"))
	if err != nil {
		t.Fatalf("parseServersList: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if !entries[0].IsolateEnabled() || entries[0].Isolate == nil {
		t.Fatalf("sa 应显式 isolate=true：%+v", entries[0])
	}
	if entries[1].IsolateEnabled() || entries[1].Isolate == nil {
		t.Fatalf("sb 应显式 isolate=false：%+v", entries[1])
	}
	if entries[2].Isolate != nil || !entries[2].IsolateEnabled() {
		t.Fatalf("sc 缺 isolate 字段 → nil 并按 transport 推断（stdio→true）：%+v", entries[2])
	}

	// Invalidate（取消 → kill + respawn）须覆盖池中全部 workdir 槽
	p := newIsolateTestProvider(t, boolPtr(true), 30*time.Second, t.Logf)
	pidA := pidFrom(t, p, `E:\proj\aa`)
	pidB := pidFrom(t, p, `E:\proj\bb`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rebuilt, err := p.Invalidate(ctx)
	if err != nil || !rebuilt {
		t.Fatalf("Invalidate: rebuilt=%v err=%v", rebuilt, err)
	}
	if n := poolSize(p); n != 0 {
		t.Fatalf("Invalidate 后池应清空，got %d", n)
	}
	newA := pidFrom(t, p, `E:\proj\aa`)
	newB := pidFrom(t, p, `E:\proj\bb`)
	if newA == pidA || newB == pidB {
		t.Fatalf("Invalidate 应重建全部 workdir 连接：A %s→%s, B %s→%s", pidA, newA, pidB, newB)
	}
	t.Logf("Invalidate 覆盖全池：A %s→%s, B %s→%s，pool 清空后按 workdir 懒建", pidA, newA, pidB, newB)
}

// ─── 补充：servers/register 载荷承载 isolate（含旧载荷兼容）───

func TestRegisterSpecCarriesIsolate(t *testing.T) {
	var on regMsg
	if err := json.Unmarshal([]byte(`{"name":"a","mcp_server":{"runtime":"node","args":["a.js"],"isolate":false}}`), &on); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if on.MCPServer == nil || on.MCPServer.Isolate == nil || *on.MCPServer.Isolate {
		t.Fatalf("mcp_server.isolate=false 应落到 *bool：%+v", on.MCPServer)
	}
	var legacy regMsg
	if err := json.Unmarshal([]byte(`{"name":"a","mcp_server":{"runtime":"node","args":["a.js"]}}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	// 旧载荷（无 isolate 键）→ nil → 按 transport 推断（stdio → 隔离）
	entry := ServerEntry{ID: "a", Runtime: "node", Args: []string{"a.js"}, Isolate: legacy.MCPServer.Isolate}
	if !entry.IsolateEnabled() {
		t.Fatalf("旧载荷 stdio 应推断为隔离：%+v", entry)
	}
	t.Logf("register 载荷：isolate=false → %v；旧载荷（无键） stdio → IsolateEnabled=%v", *on.MCPServer.Isolate, entry.IsolateEnabled())
}

// pidOf 从 "<pid>[:<ms>]" 取 pid 部分。
func pidOf(s string) string {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i]
	}
	return s
}

// ─── ⑥ 同 workdir、不同 instance：隔离键含 instance → 各自子进程；作用域取消互不影响（缺口 1）───

func TestIsolateSameWorkdirDifferentInstances(t *testing.T) {
	const wd = `E:\proj\shared`
	// 池键必须含 instance 维度（否则同 workdir 多 instance 共享槽）
	if poolKeyFor("ins-a", wd) == poolKeyFor("ins-b", wd) {
		t.Fatalf("连接池 key 必须含 instance_id（缺口 1）：%q == %q", poolKeyFor("ins-a", wd), poolKeyFor("ins-b", wd))
	}
	if poolKeyFor("ins-a", wd) == poolKeyFor("", wd) {
		t.Fatal("无 instance 的旧键不应与有 instance 的键相同")
	}
	if poolKeyFor("", wd) != wd {
		t.Fatalf("instance 为空应回退纯 workdir 键（旧语义）：%q", poolKeyFor("", wd))
	}

	p := newIsolateTestProvider(t, boolPtr(true), 30*time.Second, t.Logf)
	pidA := pidScoped(t, p, wd, "ins-a")
	pidB := pidScoped(t, p, wd, "ins-b")
	if pidA == pidB {
		t.Fatalf("同 workdir 不同 instance 应各持独立下游子进程/会话，got 同 pid=%s", pidA)
	}
	if n := poolSize(p); n != 2 {
		t.Fatalf("池应有 2 个 (instance, workdir) 槽，got %d", n)
	}
	// 池复用：同 (instance, workdir) 再次调用仍是同一子进程
	if again := pidScoped(t, p, wd, "ins-a"); again != pidA {
		t.Fatalf("同 (instance, workdir) 应复用连接：pid %s → %s", pidA, again)
	}

	// 作用域取消：只作废/重建 ins-a 的槽 → ins-b 的连接与进程不受影响（取消不再互相打断）
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ok, err := p.InvalidateScoped(ctx, "ins-a", wd)
	if err != nil || !ok {
		t.Fatalf("InvalidateScoped(ins-a): ok=%v err=%v", ok, err)
	}
	if n := poolSize(p); n != 1 {
		t.Fatalf("作用域作废后应只剩 ins-b 的槽，got %d", n)
	}
	if got := pidScoped(t, p, wd, "ins-b"); got != pidB {
		t.Fatalf("ins-b 的连接不应被 ins-a 的取消影响：pid %s → %s", pidB, got)
	}
	newA := pidScoped(t, p, wd, "ins-a")
	if newA == pidA {
		t.Fatalf("ins-a 的槽应已被打断并按需重建：pid 仍为 %s", newA)
	}
	t.Logf("同 workdir 两 instance：A pid %s→%s（作用域取消后重建），B pid %s 全程未受影响；pool=%d",
		pidA, newA, pidB, poolSize(p))
}
