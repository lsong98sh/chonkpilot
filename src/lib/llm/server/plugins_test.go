package server

// 内嵌插件集成测试（28-plugins：Options.Plugins 在 Start 全部就绪后广播
// server-starting；compress 走 llm-complete → 写快照 → llm-compress → 压缩回写的全链路）。

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
	"github.com/chonkpilot/chonkpilot-plugin-history"
)

// pluginEnv 与 newTestServer 相同，但装配 Options.Plugins（内嵌插件集成测试专用）。
func pluginEnv(t *testing.T, llmSrv *httptest.Server, plugins []plugin.Hook, preStart func(mq.Bus)) *Server {
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
	s.locksDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if preStart != nil {
		preStart(bus)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// TestPluginsLoadedServerStarting：Options.Plugins 装配（compress + history）→ Start 全部
// 成功后才广播 server-starting（插件加载完成 = 就绪信号）。
func TestPluginsLoadedServerStarting(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	started := make(chan map[string]any, 1)
	pluginEnv(t, llm, []plugin.Hook{compress.New(compress.DefaultOptions(), inline.New(nil)), history.New()},
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
}

// TestCompressPluginEndToEnd：llm-complete → 写快照 → llm-compress → compress 插件
// 判定（有简化区 + 简化区 token 达 T）→ 注入 Summarize（server 当前 LLM）→ 回写快照
// （system 摘要 + 保留段；snapshot_turn 不变）。验证 compress 插件全链路生效。
//
// 需求同步（2026-09-24 A）：`compress_token_threshold` 改为**简化区**阈值（原「全快照门控」作废）
// —— 保留最后 1 轮（N=1）时简化区 = 首轮（回显短文本 ~11 token），故 T 取 1 才能触发；
// 首轮单独完成时**无简化区**（保留段=全量）→ 不压缩。
func TestCompressPluginEndToEnd(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := pluginEnv(t, llm, []plugin.Hook{compress.New(compress.Options{RetainTurns: 1, TokenMax: 1}, inline.New(nil))}, nil)
	// 第一轮：仅 1 轮 → **无简化区**（保留段=全量）→ 不压缩
	startTurn(t, s, "s-plg", "t1")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-plg", "turn": "t1",
		"type": "text-user", "content": "first round",
	}))
	evs := collectTurn(t, s.bus, "t1", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("turn1 no complete: %+v", evs)
	}
	waitTurnGone(t, s, "t1")
	time.Sleep(200 * time.Millisecond) // 等 turn1 的 llm-compress 处理完（无变更跳过）

	// 第二轮：2 轮 → 保留最后 1 轮，简化区 = 首轮（token ≥ T=1）→ 压缩首轮
	big := strings.Repeat("Z", 400)
	startTurn(t, s, "s-plg", "t2")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-plg", "turn": "t2",
		"type": "text-user", "content": big,
	}))
	evs = collectTurn(t, s.bus, "t2", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("turn2 no complete: %+v", evs)
	}
	waitTurnGone(t, s, "t2")

	// compress 插件异步处理：轮询快照直到首条变为 system 摘要
	deadline := time.Now().Add(5 * time.Second)
	var snap map[string]any
	for time.Now().Before(deadline) {
		cur := snapshotOf(t, s, "s-plg")
		if cur != nil {
			if hist, ok := cur["history"].([]any); ok && len(hist) > 0 {
				if m0, ok := hist[0].(map[string]any); ok && str(m0["role"]) == "system" {
					snap = cur
					break
				}
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	if snap == nil {
		t.Fatal("compress 插件未回写 system 摘要快照")
	}
	hist, _ := snap["history"].([]any)
	if len(hist) == 0 {
		t.Fatal("compress 插件快照为空")
	}
	m0 := hist[0].(map[string]any)
	if str(m0["role"]) != "system" {
		t.Fatalf("compress 插件未回写 system 摘要快照: %+v", snap)
	}
	if !strings.Contains(str(m0["content"]), "[已压缩早前对话]") {
		t.Fatalf("摘要消息格式不符: %q", str(m0["content"]))
	}
	if str(snap["snapshot_turn"]) != "t2" {
		t.Fatalf("压缩后 snapshot_turn 应保持 t2: %+v", snap)
	}
	// 保留段 = 第二轮完整 turn（user big + assistant 回显）→ 共 3 条
	if len(hist) != 3 {
		t.Fatalf("保留段不符: %+v", snap)
	}
	m1 := hist[1].(map[string]any)
	if str(m1["role"]) != "user" || str(m1["content"]) != big {
		t.Fatalf("保留段不符: %+v", snap)
	}
}
