// 进程内 meta / domain 工具面的**规格期望值**测试（25 §0② · 18 §3.6）。
//
// 期望值来源：docs/spec/20-modules/25-mcp-server.md §0②/§3.6、
// docs/spec/20-modules/21-llm-server.md（ask_user 节点保持 running、答复回填收尾）、
// docs/spec/10-architecture/18-工具异步超时与取消.md §3.6。
//
// 驱动方式：dialConn 夹具把 fake transport.Conn 交给 Run，断言**调用顺序 / 桥参数 /
// 结果文本与结构**；期望值写死，不从实现 helper 反推。
package toolmeta

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/transport"
)

// withFakeDial 把 Run → 插件桥拨号替换为给定 fake Conn（仅测试夹具，不改业务语义）。
func withFakeDial(t *testing.T, cli transport.Conn) {
	t.Helper()
	old := dialConn
	dialConn = func(context.Context, Bridge) (transport.Conn, error) { return cli, nil }
	t.Cleanup(func() { dialConn = old })
}

// snapshot 返回本次连接上已发生的调用（顺序 + 载荷）。
func (c *fakeConn) snapshot() []recordedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]recordedCall, len(c.records))
	copy(out, c.records)
	return out
}

// methods 返回调用顺序（方法名序列）。
func (c *fakeConn) methods() []string {
	recs := c.snapshot()
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Method
	}
	return out
}

// payloadOf 返回第 i 次调用的载荷（map 形态，非 map 直接 fail）。
func payloadOf(t *testing.T, recs []recordedCall, i int) map[string]any {
	t.Helper()
	if i >= len(recs) {
		t.Fatalf("调用次数不足：want >= %d, got %d（%v）", i+1, len(recs), recs)
	}
	m, ok := recs[i].Payload.(map[string]any)
	if !ok {
		t.Fatalf("第 %d 次调用载荷不是对象：%T %#v", i, recs[i].Payload, recs[i].Payload)
	}
	return m
}

// assertMethods 断言调用顺序逐字相等。
func assertMethods(t *testing.T, cli *fakeConn, want []string) {
	t.Helper()
	got := cli.methods()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("调用顺序 = %v, want %v", got, want)
	}
}

// ── domain：ask_user ───────────────────────────────────────

// ask_user 成功：task.node.start → ask.user，立即返回占位文本；**不 finishNode** ——
// 规格 21 §ask_user：任务节点保持 running，终态由答复回填（llm-server tasks.done）收尾。
func TestRunDomainAskUserOrderAndText(t *testing.T) {
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		if method == "plugin/v1/task.node.start" {
			return transport.Result{OK: true, Value: map[string]any{"task_id": "node-1"}}
		}
		return transport.Result{OK: true, Value: map[string]any{}}
	}}
	withFakeDial(t, cli)

	args := map[string]any{"question": "继续吗？", "options": []any{"是", "否"}}
	cx := Context{
		InstanceID: "ins-1", Session: "s-1", Turn: "turn-1",
		ToolCallID: "call-1", DisplayName: "确认继续", WorkDir: `C:\w`,
	}
	res := Run(context.Background(), GroupDomain, "ask_user", args, cx, Bridge{Addr: "fake"})

	if res.IsError {
		t.Fatalf("ask_user 成功不应 isError: %+v", res)
	}
	if res.Text != "已向用户提问，等待回答" {
		t.Fatalf("ask_user 结果文本 = %q", res.Text)
	}
	assertMethods(t, cli, []string{
		"plugin/v1/task.node.start",
		"plugin/v1/ask.user",
	})

	recs := cli.snapshot()
	start := payloadOf(t, recs, 0)
	wantStart := map[string]any{
		"instance_id":  "ins-1",
		"turn":         "turn-1",
		"tool":         "ask_user",
		"tool_call_id": "call-1",
		"display_name":      "确认继续",
		"args":         args,
	}
	if !reflect.DeepEqual(start, wantStart) {
		t.Fatalf("task.node.start 载荷 = %#v, want %#v", start, wantStart)
	}

	ask := payloadOf(t, recs, 1)
	wantAsk := map[string]any{
		"turn":         "turn-1",
		"tool_call_id": "call-1",
		"task_id":      "node-1",
		"args":         args,
	}
	if !reflect.DeepEqual(ask, wantAsk) {
		t.Fatalf("ask.user 载荷 = %#v, want %#v", ask, wantAsk)
	}
	if n := cli.callCountOf("plugin/v1/task.node.done"); n != 0 {
		t.Fatalf("ask_user 成功不得收尾节点（答复回填收尾），task.node.done 调用 %d 次", n)
	}
}

// ask_user 负例：建节点失败 / 提问通道失败 —— 精确文本 + 失败时 finishNode(error)。
func TestRunDomainAskUserFailures(t *testing.T) {
	t.Run("turn 上下文缺失", func(t *testing.T) {
		cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
			if method == "plugin/v1/task.node.start" {
				return transport.Result{OK: false, Errors: []string{"turn not found"}}
			}
			return transport.Result{OK: true, Value: map[string]any{}}
		}}
		withFakeDial(t, cli)

		res := Run(context.Background(), GroupDomain, "ask_user", map[string]any{}, Context{}, Bridge{})
		if !res.IsError || res.Text != "域工具必须经 server turn 调用（turn 上下文缺失）" {
			t.Fatalf("结果 = %+v", res)
		}
		assertMethods(t, cli, []string{"plugin/v1/task.node.start"})
	})

	t.Run("ask 通道失败 → 节点 error", func(t *testing.T) {
		cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
			switch method {
			case "plugin/v1/task.node.start":
				return transport.Result{OK: true, Value: map[string]any{"task_id": "node-1"}}
			case "plugin/v1/ask.user":
				return transport.Result{OK: false, Errors: []string{"ask channel closed"}}
			}
			return transport.Result{OK: true, Value: map[string]any{}}
		}}
		withFakeDial(t, cli)

		res := Run(context.Background(), GroupDomain, "ask_user", map[string]any{"question": "q"}, Context{}, Bridge{})
		if !res.IsError || res.Text != "错误: ask channel closed" {
			t.Fatalf("结果 = %+v", res)
		}
		assertMethods(t, cli, []string{
			"plugin/v1/task.node.start",
			"plugin/v1/ask.user",
			"plugin/v1/task.node.done",
		})
		done := payloadOf(t, cli.snapshot(), 2)
		want := map[string]any{
			"task_id": "node-1",
			"state":   "error",
			"result":  "",
			"error":   "ask channel closed",
		}
		if !reflect.DeepEqual(done, want) {
			t.Fatalf("task.node.done 载荷 = %#v, want %#v", done, want)
		}
	})
}

// 未知域工具：不拨桥、精确文本（tool_stop 已删除，task_stop 为唯一停止入口）。
func TestRunUnknownTargets(t *testing.T) {
	t.Run("未知工具组", func(t *testing.T) {
		cli := &fakeConn{}
		withFakeDial(t, cli)
		res := Run(context.Background(), Group("bogus"), "x", nil, Context{}, Bridge{})
		if !res.IsError || res.Text != "错误: 未知工具组 bogus" {
			t.Fatalf("结果 = %+v", res)
		}
		if n := cli.callCount(); n != 0 {
			t.Fatalf("未知组不应拨桥/调用，got %d 次", n)
		}
	})
	t.Run("未知域工具", func(t *testing.T) {
		cli := &fakeConn{}
		withFakeDial(t, cli)
		res := Run(context.Background(), GroupDomain, "tool_stop", nil, Context{}, Bridge{})
		if !res.IsError || res.Text != "错误: 未知 server 工具 tool_stop" {
			t.Fatalf("结果 = %+v", res)
		}
		if n := cli.callCount(); n != 0 {
			t.Fatalf("未知域工具不应拨桥，got %d 次", n)
		}
	})
	t.Run("未知元工具", func(t *testing.T) {
		cli := &fakeConn{}
		withFakeDial(t, cli)
		res := Run(context.Background(), GroupMeta, "mcp_nope", nil, Context{}, Bridge{})
		if !res.IsError || res.Text != "错误: 未知元工具 mcp_nope" {
			t.Fatalf("结果 = %+v", res)
		}
	})
}

// 拨桥失败：错误文本带前缀，isError（不 panic）。
func TestRunDialFailure(t *testing.T) {
	old := dialConn
	dialConn = func(context.Context, Bridge) (transport.Conn, error) {
		return nil, errors.New("bridge down")
	}
	t.Cleanup(func() { dialConn = old })

	res := Run(context.Background(), GroupMeta, "mcp_find", map[string]any{}, Context{}, Bridge{Addr: "bad"})
	if !res.IsError || res.Text != "错误: bridge down" {
		t.Fatalf("结果 = %+v", res)
	}
}

// handler panic → IsError=true 且不冒泡（25 §0② 故障域补偿）。
func TestRunRecoversHandlerPanic(t *testing.T) {
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		if method == "plugin/v1/tools.list" {
			panic("boom")
		}
		return transport.Result{OK: true, Value: map[string]any{}}
	}}
	withFakeDial(t, cli)

	res := Run(context.Background(), GroupMeta, "mcp_find", map[string]any{}, Context{InstanceID: "ins-1"}, Bridge{})
	if res == nil || !res.IsError {
		t.Fatalf("panic 应转 isError 结果：%+v", res)
	}
	if res.Text != "tool mcp_find panic: boom" {
		t.Fatalf("panic 结果文本 = %q", res.Text)
	}
}

// ── meta：mcp_find ─────────────────────────────────────────

// metaFixture 是 meta 三工具共用的能力面（工具 2 + skill 2（1 条他实例不可见）+ resource 1）。
func metaFixture() *fakeConn {
	return &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		switch method {
		case "plugin/v1/tools.list":
			return transport.Result{OK: true, Value: map[string]any{"tools": []any{
				map[string]any{
					"name": "file_read", "description": "读取文件",
					"inputSchema": map[string]any{"type": "object"},
					"_meta":       map[string]any{"category": "file", "hot": true, "server": "file"},
				},
				map[string]any{
					"name": "task_run", "description": "运行任务",
					"_meta": map[string]any{"category": "task", "hot": false},
				},
			}}}
		case "plugin/v1/prompts.list":
			return transport.Result{OK: true, Value: map[string]any{"prompts": []any{
				map[string]any{"name": "review", "type": "skill", "description": "代码审查", "node": "n1", "scope": "ins-1"},
				map[string]any{"name": "hidden", "type": "skill", "description": "别人的技能", "node": "n2", "scope": "other"},
			}}}
		case "plugin/v1/resources.list":
			return transport.Result{OK: true, Value: map[string]any{"resources": []any{
				map[string]any{"name": "guide", "description": "指南", "uri": "res://guide", "node": "n3", "scope": "global"},
			}}}
		}
		return transport.Result{OK: true, Value: map[string]any{}}
	}}
}

// mcp_find 精确结构：query/type/count/tools 全字段（含 _meta 投影与 inputSchema/server）。
func TestRunMetaFindExactStructure(t *testing.T) {
	cli := metaFixture()
	withFakeDial(t, cli)

	res := Run(context.Background(), GroupMeta, "mcp_find", map[string]any{
		"query": "file", "type": "tool", "limit": float64(5),
	}, Context{InstanceID: "ins-1"}, Bridge{})
	if res.IsError {
		t.Fatalf("mcp_find 不应 isError: %q", res.Text)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
		t.Fatalf("结果不是合法 JSON：%v（%q）", err, res.Text)
	}
	want := map[string]any{
		"query": "file",
		"type":  "tool",
		"count": float64(1),
		"tools": []any{map[string]any{
			"kind": "tool", "name": "file_read", "description": "读取文件",
			"category": "file", "hot": true,
			"inputSchema": map[string]any{"type": "object"},
			"server":      "file",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mcp_find 结果 = %#v\nwant = %#v", got, want)
	}
	// 能力面查询按实例边界：tools.list 必须带 instance_id。
	first := payloadOf(t, cli.snapshot(), 0)
	if first["instance_id"] != "ins-1" {
		t.Fatalf("tools.list 未带 instance_id：%#v", first)
	}
}

// type 作用域：tool / skill / resource / all（含他实例资产不可见、资产按 kind+name 排序）。
func TestRunMetaFindTypeScope(t *testing.T) {
	cases := []struct {
		typ       string
		wantNames []string
	}{
		{"", []string{"file_read", "task_run", "guide", "review"}},
		{"all", []string{"file_read", "task_run", "guide", "review"}},
		{"tool", []string{"file_read", "task_run"}},
		{"skill", []string{"review"}},
		{"resource", []string{"guide"}},
	}
	for _, tc := range cases {
		name := tc.typ
		if name == "" {
			name = "(缺省)"
		}
		t.Run(name, func(t *testing.T) {
			cli := metaFixture()
			withFakeDial(t, cli)

			args := map[string]any{"query": ""}
			if tc.typ != "" {
				args["type"] = tc.typ
			}
			res := Run(context.Background(), GroupMeta, "mcp_find", args, Context{InstanceID: "ins-1"}, Bridge{})
			if res.IsError {
				t.Fatalf("mcp_find 不应 isError: %q", res.Text)
			}
			var got struct {
				Type  string           `json:"type"`
				Count int              `json:"count"`
				Tools []map[string]any `json:"tools"`
			}
			if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
				t.Fatalf("结果解析失败：%v", err)
			}
			names := make([]string, 0, len(got.Tools))
			for _, m := range got.Tools {
				names = append(names, m["name"].(string))
			}
			if !reflect.DeepEqual(names, tc.wantNames) {
				t.Fatalf("命中条目 = %v, want %v", names, tc.wantNames)
			}
			if got.Count != len(tc.wantNames) {
				t.Fatalf("count = %d, want %d", got.Count, len(tc.wantNames))
			}
			wantType := tc.typ
			if wantType == "" {
				wantType = "all"
			}
			if got.Type != wantType {
				t.Fatalf("type = %q, want %q", got.Type, wantType)
			}
		})
	}
}

// limit 截断命中数。
func TestRunMetaFindLimit(t *testing.T) {
	cli := metaFixture()
	withFakeDial(t, cli)

	res := Run(context.Background(), GroupMeta, "mcp_find", map[string]any{
		"query": "", "type": "tool", "limit": float64(1),
	}, Context{InstanceID: "ins-1"}, Bridge{})
	var got struct {
		Count int              `json:"count"`
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
		t.Fatalf("结果解析失败：%v", err)
	}
	if got.Count != 1 || len(got.Tools) != 1 || got.Tools[0]["name"] != "file_read" {
		t.Fatalf("limit=1 结果 = %+v", got)
	}
}

// 未知 type：精确错误文本，且不查能力面（findTypeScope 先于 listTools）。
func TestRunMetaFindInvalidType(t *testing.T) {
	cli := metaFixture()
	withFakeDial(t, cli)

	res := Run(context.Background(), GroupMeta, "mcp_find", map[string]any{"type": "agent"},
		Context{InstanceID: "ins-1"}, Bridge{})
	if !res.IsError {
		t.Fatalf("未知 type 应 isError：%+v", res)
	}
	if res.Text != `unknown type "agent"（支持：tool / skill / resource / all）` {
		t.Fatalf("错误文本 = %q", res.Text)
	}
	if n := cli.callCount(); n != 0 {
		t.Fatalf("未知 type 不应查询能力面，got %d 次（%v）", n, cli.methods())
	}
}

// ── meta：mcp_load / mcp_invoke ───────────────────────────

// mcp_load(kind=skill)：findAsset 选本实例资产 → prompts.get 取正文，结果结构精确。
func TestRunMetaLoadSkill(t *testing.T) {
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		switch method {
		case "plugin/v1/prompts.list":
			return transport.Result{OK: true, Value: map[string]any{"prompts": []any{
				map[string]any{"name": "review", "type": "skill", "description": "代码审查", "node": "n1", "scope": "ins-1"},
			}}}
		case "plugin/v1/prompts.get":
			return transport.Result{OK: true, Value: map[string]any{"content": "# 审查步骤"}}
		}
		return transport.Result{OK: true, Value: map[string]any{}}
	}}
	withFakeDial(t, cli)

	res := Run(context.Background(), GroupMeta, "mcp_load", map[string]any{
		"name": "review", "kind": "skill",
	}, Context{InstanceID: "ins-1"}, Bridge{})
	if res.IsError {
		t.Fatalf("mcp_load 不应 isError: %q", res.Text)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
		t.Fatalf("结果解析失败：%v（%q）", err, res.Text)
	}
	want := map[string]any{
		"kind": "skill", "name": "review", "content": "# 审查步骤",
		"node": "n1", "scope": "ins-1", "description": "代码审查",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mcp_load 结果 = %#v\nwant = %#v", got, want)
	}
	getPayload := payloadOf(t, cli.snapshot(), 1)
	if getPayload["name"] != "review" || getPayload["instance_id"] != "ins-1" {
		t.Fatalf("prompts.get 载荷 = %#v", getPayload)
	}
}

// 资产 / 工具未找到：精确错误文本。
func TestRunMetaLoadNotFound(t *testing.T) {
	t.Run("skill", func(t *testing.T) {
		cli := &fakeConn{}
		withFakeDial(t, cli)
		res := Run(context.Background(), GroupMeta, "mcp_load", map[string]any{"name": "nope", "kind": "skill"},
			Context{InstanceID: "ins-1"}, Bridge{})
		if !res.IsError || res.Text != `asset "nope" (kind=skill) not found` {
			t.Fatalf("结果 = %+v", res)
		}
	})
	t.Run("tool", func(t *testing.T) {
		cli := &fakeConn{}
		withFakeDial(t, cli)
		res := Run(context.Background(), GroupMeta, "mcp_load", map[string]any{"name": "nope"},
			Context{InstanceID: "ins-1"}, Bridge{})
		if !res.IsError || res.Text != `tool "nope" not found` {
			t.Fatalf("结果 = %+v", res)
		}
	})
	t.Run("name 必填", func(t *testing.T) {
		cli := &fakeConn{}
		withFakeDial(t, cli)
		res := Run(context.Background(), GroupMeta, "mcp_load", map[string]any{}, Context{}, Bridge{})
		if !res.IsError || res.Text != "name is required" {
			t.Fatalf("结果 = %+v", res)
		}
	})
}

// mcp_invoke：强制 async=never、带 work_dir 与 request_id（宿主准入 49 §2.19），单文本项直出。
func TestRunMetaInvoke(t *testing.T) {
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		if method == "plugin/v1/tools.call" {
			return transport.Result{OK: true, Value: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
				"isError": false,
			}}
		}
		return transport.Result{OK: true, Value: map[string]any{}}
	}}
	withFakeDial(t, cli)

	cx := Context{InstanceID: "ins-1", Session: "s-1", Turn: "turn-1", ToolCallID: "call-1", WorkDir: `C:\w`}
	res := Run(context.Background(), GroupMeta, "mcp_invoke", map[string]any{
		"name": "file_read", "arguments": map[string]any{"path": "a.txt"},
	}, cx, Bridge{})
	if res.IsError || res.Text != "ok" {
		t.Fatalf("mcp_invoke 结果 = %+v", res)
	}
	if n := cli.callCountOf("plugin/v1/tools.call"); n != 1 {
		t.Fatalf("tools.call 调用 %d 次, want 1", n)
	}
	call := payloadOf(t, cli.snapshot(), 0)
	want := map[string]any{
		"name":         "file_read",
		"arguments":    map[string]any{"path": "a.txt", "async": "never"},
		"work_dir":     `C:\w`,
		"instance_id":  "ins-1",
		"session":      "s-1",
		"turn":         "turn-1",
		"tool_call_id": "call-1",
		"display_name":      "",
		"request_id":   "meta-invoke-call-1",
	}
	if !reflect.DeepEqual(call, want) {
		t.Fatalf("tools.call 载荷 = %#v\nwant = %#v", call, want)
	}
}

// 元工具失败路径：桥调用失败 → 「错误: 」前缀 + isError（不吞错）。
func TestRunMetaInvokeCallFailure(t *testing.T) {
	cli := &fakeConn{fn: func(_ context.Context, method string) transport.Result {
		if method == "plugin/v1/tools.call" {
			return transport.Result{OK: false, Errors: []string{"tool exploded"}}
		}
		return transport.Result{OK: true, Value: map[string]any{}}
	}}
	withFakeDial(t, cli)

	res := Run(context.Background(), GroupMeta, "mcp_invoke", map[string]any{"name": "file_read"},
		Context{InstanceID: "ins-1", ToolCallID: "call-9", WorkDir: `C:\w`}, Bridge{})
	if !res.IsError || res.Text != "错误: tool exploded" {
		t.Fatalf("结果 = %+v", res)
	}
	call := payloadOf(t, cli.snapshot(), 0)
	if !strings.HasPrefix(call["request_id"].(string), "meta-invoke-") {
		t.Fatalf("request_id 前缀不符：%#v", call["request_id"])
	}
}
