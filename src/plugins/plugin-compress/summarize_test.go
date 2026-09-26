package compress

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// newUserConfigAPI 起一个绑定临时 usr 库的 data 门面（隔离用户配置；用例结束关库再删目录）。
func newUserConfigAPI(t *testing.T, bus mq.Bus) facade.API {
	t.Helper()
	api := inline.NewWithOptions(bus, persist.Options{
		UsrPath: filepath.Join(t.TempDir(), "usr.db"), AppDir: t.TempDir(),
	})
	t.Cleanup(data.Reset)
	return api
}

// TestSummarizeCarriesInstanceID：经 llm-simple 请求摘要时必须带 instance_id
// （61-消息一览 §0 实例字段必带），由触发事件 session-compress 载荷透传。
func TestSummarizeCarriesInstanceID(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()
	got := make(chan map[string]any, 1)
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		select {
		case got <- req:
		default:
		}
		v.Result = map[string]any{"text": "SUMMARY"}
		return nil
	}); err != nil {
		t.Fatalf("subscribe llm-simple: %v", err)
	}

	c := New(DefaultOptions(), inline.New(bus))
	c.deps = plugin.Deps{Bus: bus}
	if _, err := c.summarize("ins-1", "sys-prompt", "", "hello"); err != nil {
		t.Fatalf("summarize: %v", err)
	}
	select {
	case req := <-got:
		if req["instance_id"] != "ins-1" {
			t.Fatalf("llm-simple 请求缺 instance_id: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到 llm-simple 请求")
	}
}

// TestResolveSubsystemLLMAndPayload（SL-3 / SL-C2）：压缩子系统读 usr `llm.compress` →
// 随 llm-simple 请求带 `llm`（provider name）；删键 → 回落 defaultLLM（数据层读侧已回落）。
func TestResolveSubsystemLLMAndPayload(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()
	api := newUserConfigAPI(t, bus)
	if _, err := api.UserConfigSet(facade.UserConfigSetRequest{Entries: map[string]any{
		"llms": []any{
			map[string]any{"name": "prov-a", "model": "mA"},
			map[string]any{"name": "prov-b", "model": "mB"},
		},
		"defaultLLM":   "prov-a",
		"llm.compress": "prov-b",
	}}); err != nil {
		t.Fatalf("UserConfigSet: %v", err)
	}

	got := make(chan map[string]any, 1)
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		select {
		case got <- req:
		default:
		}
		v.Result = map[string]any{"text": "SUMMARY"}
		return nil
	}); err != nil {
		t.Fatalf("subscribe llm-simple: %v", err)
	}

	c := New(DefaultOptions(), api)
	c.deps = plugin.Deps{Bus: bus}
	if llm := c.resolveSubsystemLLM("ins-1", "", ""); llm != "prov-b" {
		t.Fatalf("resolveSubsystemLLM = %q, want prov-b（usr llm.compress）", llm)
	}
	if _, err := c.summarize("ins-1", "sys-prompt", "prov-b", "hello"); err != nil {
		t.Fatalf("summarize: %v", err)
	}
	select {
	case req := <-got:
		if req["llm"] != "prov-b" {
			t.Fatalf("llm-simple 请求应带 llm=prov-b：%+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到 llm-simple 请求")
	}

	// 删显式键 → 回落 defaultLLM（回落归数据层读侧，插件不重做）
	if _, err := api.UserConfigDelete(facade.UserConfigDeleteRequest{Keys: []string{"llm.compress"}}); err != nil {
		t.Fatalf("UserConfigDelete: %v", err)
	}
	if llm := c.resolveSubsystemLLM("ins-1", "", ""); llm != "prov-a" {
		t.Fatalf("删键后 resolveSubsystemLLM = %q, want prov-a（回落 defaultLLM）", llm)
	}
}

// TestSummarizePayloadByteEquivalentWithoutSubsystemLLM（SL-C7 兼容硬要求）：未配置子系统 LLM
// （且无任何 llms → defaultLLM 无可用）→ 不传 `llm`，llm-simple 载荷与改前**逐字节等价**。
func TestSummarizePayloadByteEquivalentWithoutSubsystemLLM(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()
	api := newUserConfigAPI(t, bus) // 空 usr 配置

	var raw []byte
	if _, err := bus.On("llm-simple", 0, func(_ context.Context, _ string, v *mq.Value) error {
		raw = append([]byte(nil), v.Payload...)
		v.Result = map[string]any{"text": "SUMMARY"}
		return nil
	}); err != nil {
		t.Fatalf("subscribe llm-simple: %v", err)
	}

	c := New(DefaultOptions(), api)
	c.deps = plugin.Deps{Bus: bus}
	llm := c.resolveSubsystemLLM("ins-1", "", "")
	if llm != "" {
		t.Fatalf("无可用 LLM 应不指定：%q", llm)
	}
	if _, err := c.summarize("ins-1", "sys-prompt", llm, "hello"); err != nil {
		t.Fatalf("summarize: %v", err)
	}
	const want = `{"instance_id":"ins-1","prompt":"hello","system":"sys-prompt"}`
	if string(raw) != want {
		t.Fatalf("未指定子系统 LLM 时载荷须逐字节等价：\n got=%s\nwant=%s", raw, want)
	}
}

// TestDefaultOptionsMirror：「上下文管理」页（ContextConfig.vue）的默认初值与之同源——断言压缩
// 三项阈值的后端权威默认（改动本常量须同步 chonkpilot-gui/frontend/src/views/settings/
// ContextConfig.vue 的 keepFullMaxTurns/keepFullMaxTokens/compressTokenThreshold 初值；
// `keep_full_max_turns` 侧镜像断言见 chonkpilot-llm/server TestSystemDefaultParamsMirror）。
func TestDefaultOptionsMirror(t *testing.T) {
	opts := DefaultOptions()
	if opts.RetainTurns != 10 || opts.KeepFullTokens != 24000 || opts.TokenMax != 20000 {
		t.Fatalf("压缩默认 = %+v，want {RetainTurns:10, KeepFullTokens:24000, TokenMax:20000}", opts)
	}
}
