package pluginbridge

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// fakeKVResponder 模拟 persist 的「同主题应答」：收到请求（无顶层 ok）→ 按 req_id 回
// {req_id, ok, result}；应答本身（有顶层 ok）忽略，避免自环。
func fakeKVResponder(t *testing.T, bus mq.Bus, topic string, result map[string]any) {
	t.Helper()
	_, err := bus.On(topic, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var env struct {
			ReqID string          `json:"req_id"`
			OK    *bool           `json:"ok"`
			Data  json.RawMessage `json:"data"`
		}
		if json.Unmarshal(v.Payload, &env) != nil || env.OK != nil {
			return nil
		}
		out, _ := json.Marshal(map[string]any{"req_id": env.ReqID, "ok": true, "result": result})
		go func() { _ = bus.Emit(context.Background(), topic, out) }()
		return nil
	})
	if err != nil {
		t.Fatalf("订阅 %s: %v", topic, err)
	}
}

// TestConfigProjectCapabilityRoundTrip：`plugin/v1/config.project.*` 必须走「请求 + 同主题应答」
// 面（raw Emit 的 v.Result 恒空 → 读写都拿不到结果）。本用例锁定该口径（PLUGIN-UI-1 第三段）。
func TestConfigProjectCapabilityRoundTrip(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()

	fakeKVResponder(t, bus, msgkeys.TopicDataPrjConfigLoad, map[string]any{"data": "v1"})
	fakeKVResponder(t, bus, msgkeys.TopicDataPrjConfigSave, map[string]any{"ok": true, "id": "k"})

	reg := NewRegistry()
	registerMQCapabilities(reg, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h, ok := reg.Lookup("plugin/v1/config.project.get")
	if !ok {
		t.Fatal("未注册 plugin/v1/config.project.get")
	}
	res, err := h(ctx, nil, json.RawMessage(`{"instance_id":"ins-1","data":{"id":"k"}}`))
	if err != nil {
		t.Fatalf("config.project.get: %v", err)
	}
	m, _ := res.(map[string]any)
	if m["data"] != "v1" {
		t.Fatalf("config.project.get 结果 = %+v，期望 data=v1（应答未关联 req_id？）", res)
	}

	h, ok = reg.Lookup("plugin/v1/config.project.set")
	if !ok {
		t.Fatal("未注册 plugin/v1/config.project.set")
	}
	res, err = h(ctx, nil, json.RawMessage(`{"instance_id":"ins-1","data":{"entries":{"a":"b"}}}`))
	if err != nil {
		t.Fatalf("config.project.set: %v", err)
	}
	m, _ = res.(map[string]any)
	if m["ok"] != true {
		t.Fatalf("config.project.set 结果 = %+v（命令类被当成 fire-and-forget？）", res)
	}
}

// ── usr 层插件配置能力（`plugin/v1/config.user.*`，2026-10-24）───────────────
//
// 口径：键前缀由桥会话身份推导（`plugins.<插件id>.`），越界整批不写；读侧只回自己的键。

// newUserConfigTestSession 构造一个已 hello 的桥会话（只有身份参与能力判定）。
func newUserConfigTestSession(pluginID string) *Session {
	return &Session{pluginID: pluginID}
}

// recordTopic 记录某主题收到的**请求**载荷（无顶层 ok 的才是请求；应答忽略）。
func recordTopic(t *testing.T, bus mq.Bus, topic string) func() map[string]any {
	t.Helper()
	var mu sync.Mutex
	var last map[string]any
	if _, err := bus.On(topic, 50, func(_ context.Context, _ string, v *mq.Value) error {
		var env map[string]any
		if json.Unmarshal(v.Payload, &env) != nil {
			return nil
		}
		if _, isReply := env["ok"]; isReply {
			return nil
		}
		mu.Lock()
		last = env
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("订阅 %s: %v", topic, err)
	}
	return func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// TestConfigUserGetNarrowsToOwnKeys：load 只回调用方自己的 `plugins.<id>.*`（别家插件 / llms /
// 普通 usr 键一律不返回）；单键读回值串。
func TestConfigUserGetNarrowsToOwnKeys(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()

	fakeKVResponder(t, bus, msgkeys.TopicDataUserConfigLoad, map[string]any{"data": map[string]any{
		"plugins.browser.api-key": "sk-real",
		"plugins.browser.width":   "1280",
		"plugins.desktop.dir":     `E:\shots`,
		"theme":                   "dark",
		"llms":                    []any{},
	}})

	reg := NewRegistry()
	registerMQCapabilities(reg, bus)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess := newUserConfigTestSession("browser")

	h, ok := reg.Lookup("plugin/v1/config.user.load")
	if !ok {
		t.Fatal("未注册 plugin/v1/config.user.load")
	}
	res, err := h(ctx, sess, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("config.user.load: %v", err)
	}
	got, _ := res.(map[string]any)["data"].(map[string]any)
	if len(got) != 2 {
		t.Fatalf("load 应只回自己的 2 个键，实际 %+v", got)
	}
	if got["plugins.browser.width"] != "1280" || got["plugins.browser.api-key"] != "sk-real" {
		t.Fatalf("load 值不符：%+v", got)
	}

	// 单键读（`{id}`）：回该键值串（未配置 → 空串）
	h, _ = reg.Lookup("plugin/v1/config.user.get")
	res, err = h(ctx, sess, json.RawMessage(`{"id":"plugins.browser.width"}`))
	if err != nil {
		t.Fatalf("config.user.get: %v", err)
	}
	if v, _ := res.(map[string]any)["data"].(string); v != "1280" {
		t.Fatalf("单键读 = %+v，期望 data=1280", res)
	}
	res, err = h(ctx, sess, json.RawMessage(`{"id":"plugins.browser.height"}`))
	if err != nil {
		t.Fatalf("config.user.get(未配置): %v", err)
	}
	if v, _ := res.(map[string]any)["data"].(string); v != "" {
		t.Fatalf("未配置键应回空串：%+v", res)
	}
}

// TestConfigUserSetPrefixGuard：写自己的键 → 落数据面（flat 键 + values）；写别家 / 非法前缀 →
// `config_key_forbidden` 且**不发**数据面请求（整批不写）；空 entries / 超长值 → `args_invalid`。
func TestConfigUserSetPrefixGuard(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()

	fakeKVResponder(t, bus, msgkeys.TopicDataUserConfigSave, map[string]any{"ok": true, "id": "user_config"})
	saved := recordTopic(t, bus, msgkeys.TopicDataUserConfigSave)

	reg := NewRegistry()
	registerMQCapabilities(reg, bus)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess := newUserConfigTestSession("websearch")

	h, ok := reg.Lookup("plugin/v1/config.user.set")
	if !ok {
		t.Fatal("未注册 plugin/v1/config.user.set")
	}
	if _, err := h(ctx, sess, json.RawMessage(`{"entries":{"plugins.websearch.api-key":"sk-abc","plugins.websearch.count":"10"}}`)); err != nil {
		t.Fatalf("config.user.set: %v", err)
	}
	body := saved()
	if body == nil {
		t.Fatal("写自己的键应发数据面 save 请求")
	}
	if body["plugins.websearch.api-key"] != "sk-abc" || body["plugins.websearch.count"] != "10" {
		t.Fatalf("落库载荷不符：%+v", body)
	}
	if _, hasEntries := body["entries"]; hasEntries {
		t.Fatalf("数据面载荷应为 flat 键（不是 entries 包装）：%+v", body)
	}

	// 越界键（别家插件）→ config_key_forbidden，且整批不写
	_, err = h(ctx, sess, json.RawMessage(`{"entries":{"plugins.browser.width":"1920"}}`))
	if codeOf(err) != CodeConfigKeyForbidden {
		t.Fatalf("越界键应 config_key_forbidden，实际 %v", err)
	}
	if body := saved(); body["plugins.browser.width"] != nil {
		t.Fatalf("越界时不得发数据面请求（整批不写）：%+v", body)
	}

	// 非 plugins.* 前缀 → 同样拒绝
	if _, err := h(ctx, sess, json.RawMessage(`{"entries":{"theme":"dark"}}`)); codeOf(err) != CodeConfigKeyForbidden {
		t.Fatalf("theme 应被拒，实际 %v", err)
	}
	// `plugins.<别家 id>.` 与 `plugins.` 直接前缀亦拒绝
	for _, key := range []string{"plugins.desktop.x", "plugins.", "pluginsx.y"} {
		args, _ := json.Marshal(map[string]any{"entries": map[string]any{key: "1"}})
		if _, err := h(ctx, sess, args); codeOf(err) != CodeConfigKeyForbidden {
			t.Fatalf("键 %q 应被拒，实际 %v", key, err)
		}
	}

	// 空 entries / 缺 entries / 超长值 → args_invalid
	for _, raw := range []string{
		`{"entries":{}}`,
		`{}`,
		`{"entries":{"plugins.websearch.api-key":"` + strings.Repeat("a", maxPluginConfigValueChars+1) + `"}}`,
		`{"entries":{"plugins.websearch.api-key":null}}`,
	} {
		if _, err := h(ctx, sess, json.RawMessage(raw)); codeOf(err) != CodeArgsInvalid {
			t.Fatalf("payload %s 应 args_invalid，实际 %v", raw, err)
		}
	}

	// 单键形态 `{id,value}` 与 entries 等价
	if _, err := h(ctx, sess, json.RawMessage(`{"id":"plugins.websearch.count","value":20}`)); err != nil {
		t.Fatalf("单键写: %v", err)
	}
	if body := saved(); body["plugins.websearch.count"] != "20" {
		t.Fatalf("单键写入载荷不符：%+v", body)
	}
}

// TestConfigUserDeletePrefixGuard：删自己的键 → 数据面 delete（`{id}`）；别家键 → 拒绝。
func TestConfigUserDeletePrefixGuard(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()

	fakeKVResponder(t, bus, msgkeys.TopicDataUserConfigDelete, map[string]any{"ok": true})
	deleted := recordTopic(t, bus, msgkeys.TopicDataUserConfigDelete)

	reg := NewRegistry()
	registerMQCapabilities(reg, bus)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess := newUserConfigTestSession("task")

	h, ok := reg.Lookup("plugin/v1/config.user.delete")
	if !ok {
		t.Fatal("未注册 plugin/v1/config.user.delete")
	}
	if _, err := h(ctx, sess, json.RawMessage(`{"id":"plugins.task.max"}`)); err != nil {
		t.Fatalf("config.user.delete: %v", err)
	}
	if body := deleted(); body["id"] != "plugins.task.max" {
		t.Fatalf("删除载荷不符：%+v", body)
	}
	if _, err := h(ctx, sess, json.RawMessage(`{"id":"plugins.mcp.x"}`)); codeOf(err) != CodeConfigKeyForbidden {
		t.Fatalf("别家键应被拒，实际 %v", err)
	}
	if _, err := h(ctx, sess, json.RawMessage(`{}`)); codeOf(err) != CodeArgsInvalid {
		t.Fatalf("缺 id 应 args_invalid，实际 %v", err)
	}
}

// TestConfigUserRequiresHello：未 hello（无插件身份）→ plugin_unregistered（不能推导键前缀）。
func TestConfigUserRequiresHello(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()
	reg := NewRegistry()
	registerMQCapabilities(reg, bus)
	h, _ := reg.Lookup("plugin/v1/config.user.load")
	if _, err := h(context.Background(), newUserConfigTestSession(""), json.RawMessage(`{}`)); codeOf(err) != CodePluginUnregistered {
		t.Fatalf("未 hello 应 plugin_unregistered，实际 %v", err)
	}
}

// TestConfigUserSetThenGetRoundTrip：能力往返——set 写自己键 → get 读回一致（有状态假数据面）。
func TestConfigUserSetThenGetRoundTrip(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	defer bus.Close()

	// 有状态假数据面：save 落库 / load 回全量（能力面按身份收窄）。
	var mu sync.Mutex
	stored := map[string]any{}
	reply := func(topic string, reqID string, result map[string]any) {
		out, _ := json.Marshal(map[string]any{"req_id": reqID, "ok": true, "result": result})
		go func() { _ = bus.Emit(context.Background(), topic, out) }()
	}
	subscribeRequest := func(topic string, fn func(reqID string, env map[string]any)) {
		if _, err := bus.On(topic, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var env map[string]any
			if json.Unmarshal(v.Payload, &env) != nil {
				return nil
			}
			if _, isReply := env["ok"]; isReply {
				return nil
			}
			reqID, _ := env["req_id"].(string)
			fn(reqID, env)
			return nil
		}); err != nil {
			t.Fatalf("订阅 %s: %v", topic, err)
		}
	}
	subscribeRequest(msgkeys.TopicDataUserConfigSave, func(reqID string, env map[string]any) {
		mu.Lock()
		for k, v := range env {
			if k == "req_id" || k == "instance_id" {
				continue
			}
			stored[k] = v
		}
		mu.Unlock()
		reply(msgkeys.TopicDataUserConfigSave, reqID, map[string]any{"ok": true})
	})
	subscribeRequest(msgkeys.TopicDataUserConfigLoad, func(reqID string, _ map[string]any) {
		mu.Lock()
		data := make(map[string]any, len(stored)+1)
		for k, v := range stored {
			data[k] = v
		}
		mu.Unlock()
		data["plugins.other.x"] = "越界值" // 收窄断言：不应出现在应答里
		reply(msgkeys.TopicDataUserConfigLoad, reqID, map[string]any{"data": data})
	})

	reg := NewRegistry()
	registerMQCapabilities(reg, bus)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess := newUserConfigTestSession("browser")

	set, _ := reg.Lookup("plugin/v1/config.user.set")
	if _, err := set(ctx, sess, json.RawMessage(`{"entries":{"plugins.browser.width":"1920","plugins.browser.debug":"true"}}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	get, _ := reg.Lookup("plugin/v1/config.user.get")
	for key, want := range map[string]string{
		"plugins.browser.width": "1920",
		"plugins.browser.debug": "true",
	} {
		args, _ := json.Marshal(map[string]any{"id": key})
		res, err := get(ctx, sess, args)
		if err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		if got, _ := res.(map[string]any)["data"].(string); got != want {
			t.Fatalf("往返不一致：%s = %q，期望 %q", key, got, want)
		}
	}
	// 越界键不在应答内（收窄）
	res, err := get(ctx, sess, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("get(子树): %v", err)
	}
	sub, _ := res.(map[string]any)["data"].(map[string]any)
	if _, leaked := sub["plugins.other.x"]; leaked {
		t.Fatalf("别家插件键不应出现在应答：%+v", sub)
	}
}
