// downstream 连接装配白盒测试（package mcpgateway 内部）：
//   - transport 语义：http = streamable HTTP / sse = 旧式 SSE（各自 transport）；缺 url 前置报错；
//   - spawned stdio 子进程的 cwd 与 env 展开（%VAR% / ${VAR}）真实生效。
package mcpgateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeSpawnProbe 是 stdio helper 的探针落盘实现（runStdioHelper 在 GATEWAY_STDIO_PROBE
// 非空时调用）：把子进程工作目录与两个「变量展开后」的 env 值写入 <dir>/probe.txt，
// 供 TestSpawnedStdioCwdAndEnvExpansion 断言 cwd/env 展开真实生效。
func writeSpawnProbe(dir string) {
	wd, _ := os.Getwd()
	content := "cwd=" + wd +
		"\np1=" + os.Getenv("GATEWAY_PROBE_P1") +
		"\np2=" + os.Getenv("GATEWAY_PROBE_P2") + "\n"
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "probe.txt"), []byte(content), 0o644)
}

// probeFields 解析探针文件为 map（每行 k=v）。
func probeFields(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读探针失败（子进程未按预期落盘）: %v", err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if i := strings.Index(line, "="); i > 0 {
			out[line[:i]] = line[i+1:]
		}
	}
	return out
}

// TestBuildConnTransportSelection 校验 transport 语义收口：
// sse → SSEClientTransport（旧式 SSE）、http / 缺省+url → StreamableClientTransport；
// headers → 两分支均经自定义 http.Client 注入。
func TestBuildConnTransportSelection(t *testing.T) {
	conn, cmd, done, err := buildConn(&ServerEntry{ID: "s1", URL: "http://127.0.0.1:1/sse", Transport: "sse"})
	if err != nil {
		t.Fatalf("sse buildConn: %v", err)
	}
	if cmd != nil || done != nil {
		t.Fatalf("纯 url（proxied）不应有自持子进程: cmd=%v done=%v", cmd, done)
	}
	sc, ok := conn.(*sdkConn)
	if !ok {
		t.Fatalf("sse 连接应为 sdkConn，got %T", conn)
	}
	sseTr, ok := sc.tr.(*mcp.SSEClientTransport)
	if !ok {
		t.Fatalf("transport=sse 应使用 SSEClientTransport，got %T", sc.tr)
	}
	if sseTr.Endpoint != "http://127.0.0.1:1/sse" {
		t.Fatalf("sse endpoint 传递有误: %q", sseTr.Endpoint)
	}

	// 显式 http
	conn, _, _, err = buildConn(&ServerEntry{ID: "h1", URL: "http://127.0.0.1:2/mcp", Transport: "http"})
	if err != nil {
		t.Fatalf("http buildConn: %v", err)
	}
	if _, ok := conn.(*sdkConn).tr.(*mcp.StreamableClientTransport); !ok {
		t.Fatalf("transport=http 应使用 StreamableClientTransport，got %T", conn.(*sdkConn).tr)
	}

	// 缺省 + 仅 url → 推断 http（streamable）
	conn, _, _, err = buildConn(&ServerEntry{ID: "d1", URL: "http://127.0.0.1:3/mcp"})
	if err != nil {
		t.Fatalf("default buildConn: %v", err)
	}
	if _, ok := conn.(*sdkConn).tr.(*mcp.StreamableClientTransport); !ok {
		t.Fatalf("缺省 transport + url 应推断为 streamable http，got %T", conn.(*sdkConn).tr)
	}

	// headers → http 与 sse 分支均注入自定义 http.Client
	for _, tr := range []string{"http", "sse"} {
		conn, _, _, err := buildConn(&ServerEntry{
			ID: "hd", URL: "http://127.0.0.1:4/x", Transport: tr,
			Headers: map[string]string{"X-K": "v"},
		})
		if err != nil {
			t.Fatalf("%s+headers buildConn: %v", tr, err)
		}
		switch tt := conn.(*sdkConn).tr.(type) {
		case *mcp.StreamableClientTransport:
			if tt.HTTPClient == nil {
				t.Fatalf("%s：有 headers 时应注入自定义 http.Client", tr)
			}
		case *mcp.SSEClientTransport:
			if tt.HTTPClient == nil {
				t.Fatalf("%s：有 headers 时应注入自定义 http.Client", tr)
			}
		default:
			t.Fatalf("未预期的 transport 类型 %T", tt)
		}
	}
}

// TestBuildConnExplicitTransportNeedsURL：显式 http/sse 缺 url → 前置明确报错且**不 spawn**。
// runtime 故意填不存在的可执行：若仍先 spawn，会得到 "spawn ..." 错误而非 "needs url"。
func TestBuildConnExplicitTransportNeedsURL(t *testing.T) {
	for _, tr := range []string{"http", "sse"} {
		conn, cmd, done, err := buildConn(&ServerEntry{
			ID: "bad", Runtime: "chonkpilot-no-such-exe-xyz.exe", Transport: tr,
		})
		if err == nil {
			t.Fatalf("transport=%s 缺 url 应报错（conn=%v）", tr, conn)
		}
		if !strings.Contains(err.Error(), "needs url") {
			t.Fatalf("transport=%s 错误信息应指出缺 url，got %v", tr, err)
		}
		if strings.Contains(err.Error(), "spawn") {
			t.Fatalf("transport=%s 应在 spawn 前报错，got %v", tr, err)
		}
		if cmd != nil || done != nil {
			t.Fatalf("transport=%s 缺 url 不得拉起子进程", tr)
		}
	}
}

// TestProcessEnvExpansion：spawned 子进程 env 的 %VAR% / ${VAR} 展开（与 servers.list 同一
// expandVars）；未定义变量展开为空串（既有语义）；无 env → nil（继承父进程环境）。
func TestProcessEnvExpansion(t *testing.T) {
	t.Setenv("GATEWAY_ENV_PROBE", "V1")
	env := processEnv(&ServerEntry{Env: []string{
		"CP_PROBE_A=%GATEWAY_ENV_PROBE%",
		"CP_PROBE_B=${GATEWAY_ENV_PROBE}",
		"CP_PROBE_C=%GATEWAY_ENV_PROBE_MISSING%",
	}})
	m := map[string]string{}
	for _, kv := range env {
		if i := strings.Index(kv, "="); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	if m["CP_PROBE_A"] != "V1" || m["CP_PROBE_B"] != "V1" {
		t.Fatalf("env 未展开 %%VAR%%/${VAR}: A=%q B=%q", m["CP_PROBE_A"], m["CP_PROBE_B"])
	}
	if v, ok := m["CP_PROBE_C"]; !ok || v != "" {
		t.Fatalf("未定义变量应按既有语义展开为空串，got %q（存在=%v）", v, ok)
	}
	if _, ok := m["PATH"]; !ok {
		if _, ok2 := m["Path"]; !ok2 {
			t.Fatalf("应保留父进程环境基线（PATH/Path）")
		}
	}
	if processEnv(&ServerEntry{}) != nil {
		t.Fatalf("无 env 应返回 nil（继承父进程环境）")
	}
}

// TestSpawnedStdioCwdAndEnvExpansion：spawned stdio 子进程的 cwd 与 env 展开真实生效——
// 子进程（测试二进制自 re-exec）把 os.Getwd() 与展开后的 env 值落盘，断言 cwd = entry.Cwd。
func TestSpawnedStdioCwdAndEnvExpansion(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("skip：测试二进制路径不可用（%v）", err)
	}
	dir := t.TempDir() // 同时作为 entry.Cwd 与探针目录
	t.Setenv("GATEWAY_PROBE_DIR", dir)

	p, err := newProxyProvider(&ServerEntry{
		ID: "probe", Runtime: exe, Transport: "stdio", Cwd: dir,
		Env: []string{
			"GATEWAY_STDIO_HELPER=1",
			"GATEWAY_STDIO_PROBE=%GATEWAY_PROBE_DIR%",      // 探针目录（%VAR%）
			"GATEWAY_PROBE_P1=%GATEWAY_PROBE_DIR%\\p1.out", // %VAR%
			"GATEWAY_PROBE_P2=${GATEWAY_PROBE_DIR}/p2.out", // ${VAR}
		},
	}, 5*time.Second, t.Logf)
	if err != nil {
		t.Fatalf("newProxyProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := p.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatalf("stdio 下游未返回工具（helper 未按 stdio 服务）")
	}

	// 探针落盘位置本身 = %VAR% 展开正确（未展开会落到 <cwd>\%GATEWAY_PROBE_DIR%\probe.txt）
	got := probeFields(t, filepath.Join(dir, "probe.txt"))
	wantWd := filepath.Clean(dir)
	if !strings.EqualFold(filepath.Clean(got["cwd"]), wantWd) {
		t.Fatalf("stdio 子进程 cwd 未生效：got %q，want %q", got["cwd"], wantWd)
	}
	if got["p1"] != dir+`\p1.out` {
		t.Fatalf("%%VAR%% 未展开：got %q，want %q", got["p1"], dir+`\p1.out`)
	}
	if got["p2"] != dir+`/p2.out` {
		t.Fatalf("${VAR} 未展开：got %q，want %q", got["p2"], dir+`/p2.out`)
	}
	t.Logf("stdio 子进程 cwd=%s；env %%VAR%%→%s、${VAR}→%s 均已展开", got["cwd"], got["p1"], got["p2"])
}
