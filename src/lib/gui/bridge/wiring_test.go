// wiring_test.go — 桥接线守卫（统一异步模型 2026-09-13，[42 §2 (59)]）。
//
// 目的：`PublishEvent` 对**未登记的单字方法 type 静默丢弃**（bridge.go:261-264），
// 漏登记会让前端按钮"点了没反应"且无报错。这里锁住超时裁决所需的接线：
//   - 上行：前端 type `mcp-tools-wait` → 相对主题 `mcp-tools-wait`（gateway 消费）；
//   - 上行：前端 type `task-stop` → 相对主题 `task-stop`（server 消费；超时裁决「取消」与
//     任务树 ▍ 停止统一走此入口，2026-09-18 取代已移除的 `mcp-tasks-cancel`）；
//   - 下行：相对主题 `mcp-tools-timeout` → 前端 type 同名（原名直通，无映射项）。
package bridge

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

// TestFrontMethodWaitWiring 上行「等待完成」白名单接线：漏了则前端 emit 被静默丢弃。
func TestFrontMethodWaitWiring(t *testing.T) {
	subj, ok := frontMethodSubjects["mcp-tools-wait"]
	if !ok {
		t.Fatal("frontMethodSubjects 缺少 mcp-tools-wait：前端「等待完成」将被静默丢弃")
	}
	if subj != "mcp-tools-wait" {
		t.Fatalf("mcp-tools-wait 应映射到 gateway 方法面相对主题 mcp-tools-wait（methodSubject(\"tools/wait\")），实际 %q", subj)
	}
}

// TestFrontMethodTaskStopWiring 上行「停止」白名单接线（2026-09-18）：工具行「停止」与任务树 ▍
// 统一走前端 type `task-stop`（→ 相对主题 `task-stop`，server 消费）。须同名映射；漏登记会被
// 总线静默丢弃（无订阅方）→「点了没反应」。同时**不得**保留已移除的 `mcp-tasks-cancel`
// （gateway 方法面已删，保留只会在总线上无订阅方、静默丢弃）。
func TestFrontMethodTaskStopWiring(t *testing.T) {
	subj, ok := frontMethodSubjects["task-stop"]
	if !ok {
		t.Fatal("frontMethodSubjects 缺少 task-stop：工具行「停止」将被静默丢弃")
	}
	if subj != "task-stop" {
		t.Fatalf("task-stop 应同名映射到相对主题 task-stop，实际 %q", subj)
	}
	if _, ok := frontMethodSubjects["mcp-tasks-cancel"]; ok {
		t.Fatal("frontMethodSubjects 不应保留已移除的 mcp-tasks-cancel（gateway 方法面已删）")
	}
}

// TestToolsTimeoutPassthrough 下行「超时待裁决」事件：未登记映射时按原名直通到前端。
func TestToolsTimeoutPassthrough(t *testing.T) {
	b := &Bridge{}
	if got := b.eventType("mcp-tools-timeout"); got != "mcp-tools-timeout" {
		t.Fatalf("mcp-tools-timeout 应原名直通前端，实际 %q", got)
	}
}

// TestFrontMethodSubchatWiring 上行单聊主题白名单接线（2026-10-16，61 §4.2/§4.5）：
// `subchat-list` / `subchat-finish` 是**无点分的单段主题** → 不走「点分直通」分支，
// 必须列 `frontMethodSubjects`（同名映射），否则 `PublishEvent` 静默丢弃（前端按钮无反应）。
// 下行事件（`subchat-opened` / `subchat-state`）无需登记：`eventType` 原名直通。
func TestFrontMethodSubchatWiring(t *testing.T) {
	for _, typ := range []string{msgkeys.TopicSubchatList, msgkeys.TopicSubchatFinish, msgkeys.TopicSubchatContinue} {
		subj, ok := frontMethodSubjects[typ]
		if !ok {
			t.Fatalf("frontMethodSubjects 缺少 %s：前端上行将被静默丢弃", typ)
		}
		if subj != typ {
			t.Fatalf("%s 应同名映射到相对主题 %s，实际 %q", typ, typ, subj)
		}
	}
	b := &Bridge{}
	for _, typ := range []string{msgkeys.TopicSubchatOpened, msgkeys.TopicSubchatState} {
		if got := b.eventType(typ); got != typ {
			t.Fatalf("下行事件 %s 应原名直通前端，实际 %q", typ, got)
		}
	}
}

// TestRegisterCarriesParentInstance 写侧父子链（61 §4.1 ④ · 单聊 §12.4 B4）：
// SetParentInstance 后 Start 上报的 instance-register 带直接父实例；顶层实例**不带该键**。
func TestRegisterCarriesParentInstance(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer func() { _ = bus.Close() }()

	regs := make(chan map[string]any, 8)
	if _, err := bus.On(msgkeys.TopicInstanceRegister, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			select {
			case regs <- m:
			default:
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	child := New("child-1", "wd", "", func(string) {}, bus)
	child.SetParentInstance("parent-1")
	if err := child.Start(); err != nil {
		t.Fatalf("child.Start: %v", err)
	}
	select {
	case m := <-regs:
		if m[msgkeys.InstanceRegisterEventInstanceId] != "child-1" {
			t.Fatalf("instance-register 实例 id 不符: %+v", m)
		}
		if m[msgkeys.InstanceRegisterEventParentInstance] != "parent-1" {
			t.Fatalf("子窗口 instance-register 应带 parent_instance=parent-1: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("未捕获子窗口 instance-register")
	}

	top := New("top-1", "wd", "", func(string) {}, bus)
	if err := top.Start(); err != nil {
		t.Fatalf("top.Start: %v", err)
	}
	select {
	case m := <-regs:
		if _, ok := m[msgkeys.InstanceRegisterEventParentInstance]; ok {
			t.Fatalf("顶层实例不得带 parent_instance: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("未捕获顶层 instance-register")
	}
}

// TestRegisterCarriesSessionID 写侧会话绑定（61 §4.1 ④ · 群聊 §13 D42 / §15.4 N23；
// 🆕 2026-10-16 SUBCHAT-3 阶段 F-2）：SetSessionID 后 Start 上报的 instance-register 带
// `session_id`（= 该窗口承载的会话）；主窗口（未设置）**不带该键**。
func TestRegisterCarriesSessionID(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer func() { _ = bus.Close() }()

	regs := make(chan map[string]any, 8)
	if _, err := bus.On(msgkeys.TopicInstanceRegister, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			select {
			case regs <- m:
			default:
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	chat := New("chat-1", "wd", "", func(string) {}, bus)
	chat.SetSessionID("group-abc")
	if err := chat.Start(); err != nil {
		t.Fatalf("chat.Start: %v", err)
	}
	select {
	case m := <-regs:
		if m[msgkeys.InstanceRegisterEventSessionId] != "group-abc" {
			t.Fatalf("对话/群聊窗口 instance-register 应带 session_id=group-abc: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("未捕获对话窗口 instance-register")
	}

	main := New("main-1", "wd", "", func(string) {}, bus)
	if err := main.Start(); err != nil {
		t.Fatalf("main.Start: %v", err)
	}
	select {
	case m := <-regs:
		if _, ok := m[msgkeys.InstanceRegisterEventSessionId]; ok {
			t.Fatalf("主窗口不得带 session_id: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("未捕获主窗口 instance-register")
	}
}

// TestCompatLlmErrorRetryablePassthrough（S21）：兼容层旧 `llm-error` 的 retryable 必须
// **透传 llm-complete 的真实分类**（而非旧硬编码 false）——前端据此决定自动续写/错误气泡。
// 缺字段（旧发布方）→ false（旧兼容契约语义）。
func TestCompatLlmErrorRetryablePassthrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"retryable=true 透传", `{"session":"s1","turn":"t1","status":"error","code":"LLM_STREAM_ERROR","retryable":true}`, true},
		{"retryable=false 透传", `{"session":"s1","turn":"t1","status":"error","code":"LLM_REQUEST_FAILED","retryable":false}`, false},
		{"字段缺失→false", `{"session":"s1","turn":"t1","status":"error","code":"EMPTY_REPLY"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var scripts []string
			b := New("ins-1", "wd", "dd", func(script string) {
				mu.Lock()
				scripts = append(scripts, script)
				mu.Unlock()
			}, nil)
			b.compatEmit("llm-complete", []byte(tc.raw))

			mu.Lock()
			defer mu.Unlock()
			found := false
			for _, s := range scripts {
				if !strings.Contains(s, `"type":"llm-error"`) {
					continue
				}
				env := strings.TrimSuffix(strings.TrimPrefix(s, "window.mq.emitRemote("), ")")
				var e struct {
					Type    string `json:"type"`
					Payload string `json:"payload"`
				}
				if err := json.Unmarshal([]byte(env), &e); err != nil {
					t.Fatalf("信封解析失败: %v（script=%s）", err, s)
				}
				var p map[string]any
				if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
					t.Fatalf("payload 解析失败: %v（payload=%s）", err, e.Payload)
				}
				if got, _ := p["retryable"].(bool); got != tc.want {
					t.Fatalf("llm-error.retryable=%v want %v（payload=%s）", p["retryable"], tc.want, e.Payload)
				}
				found = true
			}
			if !found {
				t.Fatalf("未兼发 llm-error：scripts=%v", scripts)
			}
		})
	}
}
