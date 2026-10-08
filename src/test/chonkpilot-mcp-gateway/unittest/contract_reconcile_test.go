// contract_reconcile_test.go — B2 契约面 · gateway 生产端对账（2026-10-05）。
//
// 目标（延续 B0/B1 机制）：以 `docs/spec/60-reference/61-messages.schema.json`（61 §5 gateway
// 方法面的机器可读抽取）为**唯一基准**，用**最小合法输入**经内存总线驱动真实 Gateway
// （`mcp-*` 相对主题），断言**实际写回的 v.Result / 发出的事件载荷键 ⊆ 契约声明**（多出 = 红）。
//
// 覆盖：
//   - §5.1/§5.2 方法面 result：tools/prompts/resources/servers 的 list/get/register/unregister ·
//     gateway/check · gateway/reload · tools/call
//   - §5.3 通知：mcp-tasks-report（异步完成回报）· mcp-gateway-changed（注册变化）
//
// 局限（覆盖边界，见 docs/spec/50-testing/50-测试体系.md §8.6）：
//   - 只驱动**可在内存总线 + 自备能力源**下跑通的方法（其余列于 TestGatewayNotDrivenRegistry）；
//   - 只对账**顶层键**（契约字段模型为逐主题平铺键）；
//   - 断言强度 = 存在级（键子集）；语义级留待后续批次。
package mcpgatewaytest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── 契约结构（与 61-messages.schema.json 对齐）─────────────────────────────

type crField struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type crTopic struct {
	Topic   string             `json:"topic"`
	Payload map[string]crField `json:"payload"`
	Result  map[string]crField `json:"result"`
	Event   map[string]crField `json:"event"`
}

type crDoc struct {
	Version string    `json:"version"`
	Topics  []crTopic `json:"topics"`
}

func (c *crDoc) byTopic() map[string]crTopic {
	m := make(map[string]crTopic, len(c.Topics))
	for _, ts := range c.Topics {
		m[ts.Topic] = ts
	}
	return m
}

// crFindFile 从本测试源文件目录向上查找契约文件（支持任意运行 cwd）。
func crFindFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	for d := filepath.Dir(file); ; {
		cand := filepath.Join(d, "docs", "spec", "60-reference", "61-messages.schema.json")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("未找到 61-messages.schema.json（自 %s 向上）", filepath.Dir(file))
		}
		d = parent
	}
}

func crLoad(t *testing.T) *crDoc {
	t.Helper()
	raw, err := os.ReadFile(crFindFile(t))
	if err != nil {
		t.Fatalf("读取契约失败: %v", err)
	}
	var c crDoc
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("契约 JSON 解析失败: %v", err)
	}
	if len(c.Topics) == 0 {
		t.Fatal("契约未收录任何主题")
	}
	return &c
}

// crAssertKeys 断言 actual 键集 ⊆ 声明（通用 error 恒放行）。
// 多出的键 = 红（无豁免机制：契约=61，须改契约或改代码，见 50 §8.4）。
func crAssertKeys(t *testing.T, topic, which string, declared map[string]crField, actual map[string]any) {
	t.Helper()
	var extra []string
	for k := range actual {
		if k == "error" {
			continue
		}
		if _, ok := declared[k]; ok {
			continue
		}
		extra = append(extra, k)
	}
	if len(extra) > 0 {
		t.Errorf("%s.%s 出现契约未声明的键（多键 = 红）: %v（契约声明=%v）",
			topic, which, extra, crKeys(declared))
	}
}

func crKeys(m map[string]crField) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── 对账主体 ──────────────────────────────────────────────────────────────

// TestGatewayResultKeysSubsetOfContract 驱动各 gateway 方法，断言写回 result 键 ⊆ 契约（§5.1/§5.2）。
func TestGatewayResultKeysSubsetOfContract(t *testing.T) {
	c := crLoad(t)
	byTopic := c.byTopic()

	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_cr", desc: "contract reconcile mock",
		handle: func(_ context.Context) (*mcp.CallToolResult, error) { return mockResult("cr-ok"), nil },
	}})

	cases := []struct {
		topic   string
		method  string
		payload map[string]any
	}{
		{"mcp-tools-list", "tools/list", map[string]any{}},
		{"mcp-tools-call", "tools/call", map[string]any{"name": "self_tool_cr"}},
		{"mcp-tools-register", "tools/register", map[string]any{"name": "cr_reg_tool", "description": "d"}},
		{"mcp-tools-unregister", "tools/unregister", map[string]any{"name": "cr_reg_tool"}},
		{"mcp-prompts-list", "prompts/list", map[string]any{}},
		{"mcp-prompts-register", "prompts/register", map[string]any{"name": "cr_prompt", "description": "d", "content": "x"}},
		{"mcp-prompts-unregister", "prompts/unregister", map[string]any{"name": "cr_prompt"}},
		{"mcp-resources-list", "resources/list", map[string]any{}},
		{"mcp-resources-register", "resources/register", map[string]any{"name": "cr_res", "uri": "cr://res"}},
		{"mcp-resources-unregister", "resources/unregister", map[string]any{"name": "cr_res"}},
		{"mcp-servers-list", "servers/list", map[string]any{}},
		{"mcp-gateway-check", "gateway/check", map[string]any{}},
		{"mcp-gateway-reload", "gateway/reload", map[string]any{}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.topic, func(t *testing.T) {
			ts, ok := byTopic[tc.topic]
			if !ok {
				t.Fatalf("契约未收录主题 %s", tc.topic)
			}
			res := callMethod(t, bus, tc.method, tc.payload)
			crAssertKeys(t, tc.topic, "result", ts.Result, res)
		})
	}
}

// TestGatewayTaskReportEventSubsetOfContract 异步完成回报（mcp-tasks-report）事件键 ⊆ 契约（§5.3）。
func TestGatewayTaskReportEventSubsetOfContract(t *testing.T) {
	c := crLoad(t)
	ts, ok := c.byTopic()["mcp-tasks-report"]
	if !ok {
		t.Fatal("契约未收录 mcp-tasks-report")
	}
	bus, _ := startTestGW(t, []mockSpec{{
		name: "tool_cr_async", desc: "async cr", meta: map[string]any{"async": "always"},
		handle: func(_ context.Context) (*mcp.CallToolResult, error) { return mockResult("cr-async-ok"), nil },
	}})
	ch := subReport(t, bus)
	res := callMethod(t, bus, "tools/call", map[string]any{"name": "self_tool_cr_async"})
	taskID := pendingTaskID(t, res)
	rep := awaitReport(t, ch, taskID)
	crAssertKeys(t, "mcp-tasks-report", "event", ts.Event, rep)
}

// TestGatewayChangedEventSubsetOfContract 注册变化通知（mcp-gateway-changed）事件键 ⊆ 契约（§5.3）。
func TestGatewayChangedEventSubsetOfContract(t *testing.T) {
	c := crLoad(t)
	ts, ok := c.byTopic()["mcp-gateway-changed"]
	if !ok {
		t.Fatal("契约未收录 mcp-gateway-changed")
	}
	bus, _ := startTestGW(t, nil)
	ch := make(chan map[string]any, 8)
	sub, err := bus.On("mcp-gateway-changed", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			select {
			case ch <- m:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	callMethod(t, bus, "tools/register", map[string]any{"name": "cr_changed_tool", "description": "d"})
	select {
	case m := <-ch:
		crAssertKeys(t, "mcp-gateway-changed", "event", ts.Event, m)
	case <-time.After(3 * time.Second):
		t.Fatal("注册后未收到 mcp-gateway-changed")
	}
}

// TestRegistrantCallbackPayloadSubsetOfContract 驱动 gateway → 注册方远程回调（§5.4），断言 gateway
// **实际发出的回调载荷**键 ⊆ 契约（B3 检索/引擎面：codegraph-tool-call / vfts-tool-call）。
// 结果侧（插件 onToolCall 回执形状）不在本面驱动，见 TestGatewayNotDrivenRegistry 登记。
func TestRegistrantCallbackPayloadSubsetOfContract(t *testing.T) {
	c := crLoad(t)
	byTopic := c.byTopic()

	for _, topic := range []string{"codegraph-tool-call", "vfts-tool-call"} {
		topic := topic
		t.Run(topic, func(t *testing.T) {
			ts, ok := byTopic[topic]
			if !ok {
				t.Fatalf("契约未收录主题 %s", topic)
			}
			bus, _ := startTestGW(t, nil)

			got := make(chan map[string]any, 1)
			sub, err := bus.On(topic, 0, func(_ context.Context, _ string, v *mq.Value) error {
				var m map[string]any
				if json.Unmarshal(v.Payload, &m) == nil {
					select {
					case got <- m:
					default:
					}
				}
				// 回执：镜像插件 toolReply 形态（gateway regProv 仅读 content/isError）。
				v.Result = map[string]any{
					"resultType": "complete",
					"content":    []any{map[string]any{"type": "text", "text": "ok"}},
					"isError":    false,
				}
				return nil
			})
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			defer func() { _ = sub.Unsubscribe() }()

			callMethod(t, bus, "tools/register", map[string]any{
				"name": "cr_cb_tool", "description": "d", "handler_subject": topic,
			})
			callMethod(t, bus, "tools/call", map[string]any{
				"name": "self_cr_cb_tool", "arguments": map[string]any{},
				// turn 上下文（tools/call 顶层嵌入 Context）→ 回调载荷应带 context。
				"session": "s1", "turn": "t1", "instance_id": "ins1", "tool_call_id": "tc1",
			})

			select {
			case m := <-got:
				crAssertKeys(t, topic, "payload", ts.Payload, m)
				if _, ok := m["context"]; !ok {
					t.Errorf("%s 回调载荷缺 context（tools/call 已下发 turn 上下文）: %v", topic, m)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("%s 未收到 gateway 回调载荷", topic)
			}
		})
	}
}

// gatewayNotDriven 记录本批「不可在内存总线低成本驱动」的 gateway 方法面及原因（不硬凑）。
func TestGatewayNotDrivenRegistry(t *testing.T) {
	entries := []struct {
		topic  string
		reason string
	}{
		{"mcp-servers-get", "需先接入一个真实下游 server（servers/register 触发 spawn/proxy 连接），单测级最小驱动成本高"},
		{"mcp-servers-register", "proxied 需真实 url 就绪、spawned 需拉起子进程 → 依赖外部进程"},
		{"mcp-servers-unregister", "同上（须先 register 成功）"},
		{"mcp-prompts-get", "须先注册带内容的 prompt 资产（register 后按 name 取）；结果 {name,description,content?,type?,arguments?} 可由 register/list 路径间接覆盖"},
		{"mcp-resources-read", "须先注册 resource 资产（uri/content）"},
		{"mcp-tools-timeout", "须控件进入 manual/never 超时裁决态（依赖超时与执行池时序），留待专测"},
		{"history-pre-tool-hook", "前置钩子主题由注册方（plugin-history）声明 handler_subject/pre_hook_subject 后触发；gateway 侧仅转发，留待插件侧对账"},
		{"codegraph-tool-call", "**结果侧**未驱动：codegraph 插件 onToolCall 需引擎子进程/实例装配；回执形状 {resultType,content,isError} 由插件单测 TestToolReply 断言。**载荷侧**已由 TestRegistrantCallbackPayloadSubsetOfContract 驱动"},
		{"vfts-tool-call", "**结果侧**未驱动：vfts 插件 onToolCall 需引擎子进程/实例装配；回执形状由插件单测断言。**载荷侧**已由 TestRegistrantCallbackPayloadSubsetOfContract 驱动"},
	}
	for _, e := range entries {
		if e.topic == "" || e.reason == "" {
			t.Errorf("未驱动登记项不完整: %+v", e)
		}
		t.Logf("[未驱动] topic=%s | 理由=%s", e.topic, e.reason)
	}
}
