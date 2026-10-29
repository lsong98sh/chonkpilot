package server

// 内嵌插件装配测试（28-plugins：Options.Plugins 在 Start 全部就绪后广播
// server-starting）。
//
// 2026-10-27（旧路径退场）：`src/plugins/plugin-compress`（原内嵌 Hook 实现，exe/别名期产物）
// 已删除 —— 压缩自 SESSION-1 起内建在会话侧（`compress.go` + `chonkpilot-session` 压缩内核），
// 其全链路由 `compress_test.go` / `compress_notice_test.go` 覆盖。本文件只保留**装配机制**断言，
// 用本地 fake hook 代替原 compress 插件。

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
)

// fakeHook 是最小内嵌插件（只用于验证「全部 Start 成功后才广播 server-starting」）。
type fakeHook struct {
	name    string
	started *bool
}

func (h fakeHook) Name() string { return h.name }

func (h fakeHook) Start(hook.Deps) error {
	if h.started != nil {
		*h.started = true
	}
	return nil
}

// pluginEnv 与 newTestServer 相同，但装配 Options.Plugins（内嵌插件集成测试专用）。
func pluginEnv(t *testing.T, llmSrv *httptest.Server, plugins []hook.Hook, preStart func(mq.Bus)) *Server {
	t.Helper()
	data.Reset()
	testWorkDir = t.TempDir()
	t.Cleanup(func() { testWorkDir = "" })
	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	s := New(bus, Options{LLMBase: llmSrv.URL, LLMModel: "mock", DisableMCP: true,
		UsrPath: t.TempDir() + "/usr.db", Plugins: plugins})
	fakeGateway(bus, s, 150*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if preStart != nil {
		preStart(bus)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// data 层共享连接关闭须在 s.Stop 之后、TempDir 清理之前（LIFO），否则 Windows 库句柄未释放。
	t.Cleanup(closeTestDataStore)
	t.Cleanup(s.Stop)
	return s
}

// TestPluginsLoadedServerStarting：Options.Plugins 装配（fake hook）→ Start 全部
// 成功后才广播 server-starting（插件加载完成 = 就绪信号）。
// 注：history / codegraph / vfts 自 PLUGIN-SPLIT-1 起为**进程外插件**，
// 不再是宿主编译期内嵌 Hook。
func TestPluginsLoadedServerStarting(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	started := make(chan map[string]any, 1)
	hookStarted := false
	pluginEnv(t, llm, []hook.Hook{fakeHook{name: "fake", started: &hookStarted}},
		func(bus mq.Bus) {
			_, _ = bus.On("server-starting", 0, func(_ context.Context, _ string, v *mq.Value) error {
				var m map[string]any
				_ = json.Unmarshal(v.Payload, &m)
				started <- m
				return nil
			})
		})
	select {
	case m := <-started:
		if m["started_at"] == nil {
			t.Fatalf("server-starting 缺 started_at: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("插件装配后未广播 server-starting")
	}
	if !hookStarted {
		t.Fatal("内嵌插件 Start 未被调用")
	}
}
