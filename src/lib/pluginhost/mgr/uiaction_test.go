package mgr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestUIActionPassThrough：动作面复用通用 RPC 信封（`plugin.ui.rpc` + `{method, params}`），
// 原样返回插件结果。
func TestUIActionPassThrough(t *testing.T) {
	var gotMethod, gotAction string
	var gotParams map[string]any
	m := New(Options{RequestPlugin: func(_ context.Context, id, method string, payload any) (any, error) {
		if id != "codebase" {
			t.Fatalf("id=%s", id)
		}
		gotMethod = method
		if pm, ok := payload.(map[string]any); ok {
			gotAction, _ = pm["method"].(string)
			gotParams, _ = pm["params"].(map[string]any)
		}
		return map[string]any{"ok": true}, nil
	}})
	m.plugins["codebase"] = runningPlugin("codebase", "1.0.0")

	res, err := m.UIAction(context.Background(), "codebase", "ui.ping", map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("UIAction: %v", err)
	}
	if gotMethod != MethodUIRPC || gotAction != "ui.ping" {
		t.Fatalf("method=%q action=%q", gotMethod, gotAction)
	}
	if v, _ := gotParams["a"].(int); v != 1 {
		t.Fatalf("params=%v", gotParams)
	}
	if v, _ := res.(map[string]any)["ok"].(bool); !v {
		t.Fatalf("result=%v", res)
	}
}

// TestUIActionErrors：空 action / 未登记 / 未就绪 / 通道未装配 → err（宿主映射 4xx/5xx）。
func TestUIActionErrors(t *testing.T) {
	m := New(Options{RequestPlugin: func(context.Context, string, string, any) (any, error) {
		return nil, errors.New("boom")
	}})
	if _, err := m.UIAction(context.Background(), "codebase", "  ", nil); err == nil {
		t.Fatal("空 action 应报错")
	}
	if _, err := m.UIAction(context.Background(), "ghost", "ui.ping", nil); err == nil {
		t.Fatal("未登记插件应报错")
	}

	m.plugins["codebase"] = &Plugin{ID: "codebase", Manifest: &Manifest{Version: "1.0.0"}, status: StatusStarting}
	if _, err := m.UIAction(context.Background(), "codebase", "ui.ping", nil); err == nil {
		t.Fatal("未就绪插件应报错")
	}

	m.plugins["codebase"].status = StatusRunning
	if _, err := m.UIAction(context.Background(), "codebase", "ui.ping", nil); err == nil {
		t.Fatal("插件返回错误应透传 err")
	}

	noChannel := New(Options{})
	noChannel.plugins["codebase"] = runningPlugin("codebase", "1.0.0")
	if _, err := noChannel.UIAction(context.Background(), "codebase", "ui.ping", nil); err == nil {
		t.Fatal("通道未装配应报错")
	}
}

// TestManifestSelfServe：`ui.selfServe` 落 pluginView.self_serve，且随 uiPayload 下发。
func TestManifestSelfServe(t *testing.T) {
	var m Manifest
	m.UI.SelfServe = true
	m.UI.Views = []View{{ID: "config", Entry: "webui/config.html"}}
	p := &Plugin{ID: "codebase", Manifest: &m, status: StatusRunning}

	v := (&Manager{}).view(p)
	if !v.SelfServe {
		t.Fatal("pluginView.self_serve 应为 true")
	}
	payload := string(m.uiPayload())
	if payload == "" || !strings.Contains(payload, `"self_serve":true`) {
		t.Fatalf("uiPayload=%s", payload)
	}
}
