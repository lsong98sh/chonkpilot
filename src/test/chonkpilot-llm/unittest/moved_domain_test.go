package llmtest

// 已迁 task 插件的四个会话编排工具的 **L2 测试适配**（2026-11-08）。
//
// 生产路径 = task 插件工具 → 插件桥 `plugin/v1/{ask.user,next.run,subchat.open|finish}`。
// L2 不启动插件二进制；本文件启动真实插件桥 + 帧客户端，并把四个工具注册到 gateway：
// 工具回调（handler_subject）→ 帧客户端调宿主能力 → 结果写回工具结果。与插件实现同链路。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	llmserver "github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/transport"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

const movedDomainCallSubject = "test-moved-domain-call"

// startMovedDomainPlugin 启动插件桥、接上 server 能力面，并把四个工具注册到内嵌 gateway。
func startMovedDomainPlugin(t *testing.T, bus mq.Bus, s *llmserver.Server) {
	t.Helper()
	pb := pluginbridge.New(pluginbridge.Options{
		Bus: bus, Addr: filepath.Join(t.TempDir(), "plugin.sock"), Version: "test",
	})
	if err := pb.Start(); err != nil {
		t.Fatalf("pluginbridge start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Close() })
	s.SetPluginRegistry(pb.Registry())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: pb.Addr()})
	if err != nil {
		t.Fatalf("plugin bridge dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "test.moved-domain"}); err != nil || !res.OK {
		t.Fatalf("plugin.hello: %+v %v", res, err)
	}

	if _, err := bus.On(movedDomainCallSubject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var msg struct {
			Tool    string         `json:"tool"`
			Args    map[string]any `json:"args"`
			Context *struct {
				InstanceID string `json:"instance_id"`
				Turn       string `json:"turn"`
				ToolCallID string `json:"tool_call_id"`
				Purpose    string `json:"purpose"`
			} `json:"context"`
		}
		if err := json.Unmarshal(v.Payload, &msg); err != nil {
			return err
		}
		in := map[string]any{"args": msg.Args}
		if msg.Context != nil {
			in["instance_id"] = msg.Context.InstanceID
			in["turn"] = msg.Context.Turn
			in["tool_call_id"] = msg.Context.ToolCallID
			in["purpose"] = msg.Context.Purpose
		}
		method := map[string]string{
			"ask_user":       "plugin/v1/ask.user",
			"next_run":       "plugin/v1/next.run",
			"subchat_open":   "plugin/v1/subchat.open",
			"subchat_finish": "plugin/v1/subchat.finish",
		}[msg.Tool]
		if method == "" {
			v.Result = map[string]any{"resultType": "complete", "isError": true,
				"content": []any{map[string]any{"type": "text", "text": "未知已迁工具 " + msg.Tool}}}
			return nil
		}
		res, err := cli.Call(context.Background(), method, in)
		text, isErr := "", false
		if err != nil || !res.OK {
			text, isErr = "插件能力调用失败", true
			if err != nil {
				text = err.Error()
			}
		} else if m, ok := res.Value.(map[string]any); ok {
			text, _ = m["text"].(string)
			isErr, _ = m["is_error"].(bool)
		}
		// 迁移前：next_run / subchat_* 的 isErr 不向 gateway 传递（由结果文本表达）。
		if msg.Tool != "ask_user" {
			isErr = false
		}
		v.Result = map[string]any{
			"resultType": "complete",
			"content":    []any{map[string]any{"type": "text", "text": text}},
			"isError":    isErr,
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe(%s): %v", movedDomainCallSubject, err)
	}

	schema := `{"type":"object","properties":{}}`
	for _, d := range []struct{ name, desc string }{
		{"ask_user", "向用户提问（L2 测试适配）"},
		{"next_run", "登记下一轮排队输入（L2 测试适配）"},
		{"subchat_open", "打开子聊天（L2 测试适配）"},
		{"subchat_finish", "子聊天自报完成（L2 测试适配）"},
	} {
		if err := bus.Emit(ctx, "mcp-tools-register", map[string]any{
			"name":            d.name,
			"description":     d.desc,
			"schema":          schema,
			"handler_subject": movedDomainCallSubject,
			"owner":           "task",
			"hot":             true,
			"category":        "task",
		}).Wait().Err(); err != nil {
			t.Fatalf("tools/register(%s): %v", d.name, err)
		}
	}
	// 让 server 的工具缓存刷新（mcp-gateway-changed → onMCPChanged → gc.ListTools）。
	if err := bus.Emit(context.Background(), "mcp-gateway-changed", map[string]any{}).Wait().Err(); err != nil {
		t.Fatalf("mcp-gateway-changed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
}