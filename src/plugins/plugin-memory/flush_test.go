// 手动沉淀（memory.flush，2026-09-20 批 3 ⑱）白盒：用户显式触发一次沉淀。
//
// 覆盖：
//   - 触发一次 → 按启用类别**各沉淀一次**（可观测：data-memory-save 次数 + 回执 saved 清单）；
//   - **不受 memory.min-turn-tokens 门控**（同一配置下自动沉淀仍被阈值挡住 → 证明差异只属手动）；
//   - 作用域 = payload 指定的 instance/session（取该会话**最近一轮** data-session-history）；
//   - 前置不满足（缺 session / 记忆库未启用 / 会话无轮次）→ `{ok:false, reason}` 明确作答。
package memory

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// flushStub 记录一次手动沉淀链路里各数据面/LLM 的调用次数与载荷。
type flushStub struct {
	saves    int32
	llmCalls int32
	mu       sync.Mutex
	prompts  map[string]string // 类别 → 沉淀 prompt（含旧全文 + 本轮新信息）
	history  []map[string]any  // data-session-history 收到的 data 载荷
}

// wire 装配最小桩：配置 / 最近轮次 / 本轮消息 / 类别清单 / 读写 / LLM。
func (s *flushStub) wire(t *testing.T, bus mq.Bus, cfg map[string]any, turns []any) {
	t.Helper()
	reply(t, bus, prjConfigListSubject, func(map[string]any) map[string]any {
		return map[string]any{"list": cfg}
	})
	reply(t, bus, sessionHistorySubject, func(data map[string]any) map[string]any {
		s.mu.Lock()
		s.history = append(s.history, data)
		s.mu.Unlock()
		return map[string]any{"messages": map[string]any{"turns": turns, "messages": []any{}, "has_more": false}}
	})
	reply(t, bus, sessionLoadSubject, func(map[string]any) map[string]any {
		return map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": "甲甲甲"},
			map[string]any{"role": "assistant", "content": "乙乙乙"},
		}}
	})
	reply(t, bus, memoryListSubject, func(map[string]any) map[string]any {
		return map[string]any{"list": []any{
			map[string]any{"category": "项目概要", "level": "project"},
			map[string]any{"category": "开发规范", "level": "project"},
			map[string]any{"category": "用户偏好", "level": "user"},
		}}
	})
	reply(t, bus, memoryReadSubject, func(data map[string]any) map[string]any {
		return map[string]any{"data": map[string]any{"content": "旧全文-" + strval(data["category"])}}
	})
	reply(t, bus, memorySaveSubject, func(map[string]any) map[string]any {
		atomic.AddInt32(&s.saves, 1)
		return map[string]any{"ok": true}
	})
	_, _ = bus.On(llmSimpleSubject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		atomic.AddInt32(&s.llmCalls, 1)
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(v.Payload, &req)
		s.mu.Lock()
		for _, cat := range []string{"项目概要", "开发规范", "用户偏好"} {
			if strings.Contains(req.Prompt, "【类别】"+cat) {
				s.prompts[cat] = req.Prompt
			}
		}
		s.mu.Unlock()
		v.Result = map[string]any{"text": "重写后的全文"}
		return nil
	})
}

// TestManualFlushTriggersDistillOnce：一次 memory.flush → 每个启用类别**恰好沉淀一次**；
// 回执含 saved/enabled/turn；阈值极大也照沉淀（不受 memory.min-turn-tokens 门控）。
func TestManualFlushTriggersDistillOnce(t *testing.T) {
	bus := newTestBus(t)
	st := &flushStub{prompts: map[string]string{}}
	// 阈值极大：自动沉淀必被挡住（下面用 extract 反证差异只属手动触发）。
	st.wire(t, bus, map[string]any{
		memoryEnabledKey:              "true",
		memoryMinTokensKey:            "999999",
		memoryCategoryPrefix + "开发规范": "false", // 显式关闭 → 不应写入
	}, []any{
		map[string]any{"turn_id": "t-1"},
		map[string]any{"turn_id": "t-2"}, // 最近一轮
	})

	p := New(DefaultOptions())
	if err := p.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	v := bus.Emit(context.Background(), "memory.flush", map[string]any{
		"instance_id": "ins-1", "session": "s1",
	}).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("memory.flush 派发错误：%v", err)
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("memory.flush 应写回结果，实际 %#v", v.Result)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("手动沉淀应成功：%+v", res)
	}
	if res["turn"] != "t-2" {
		t.Fatalf("应取会话**最近一轮**：%+v", res)
	}
	if n, _ := res["enabled"].(int); n != 2 {
		t.Fatalf("启用类别数应为 2（开发规范已关闭）：%+v", res)
	}
	saved, _ := res["saved"].([]string)
	if len(saved) != 2 {
		t.Fatalf("成功类别应为 2：%+v", saved)
	}
	for _, c := range saved {
		if c == "开发规范" {
			t.Fatalf("已关闭类别不应被写入：%+v", saved)
		}
	}
	// 可观测效果：一次触发 = 每启用类别恰好一次 save / 一次 llm-simple（不重复触发）
	if n := atomic.LoadInt32(&st.saves); n != 2 {
		t.Fatalf("data-memory-save 次数应为 2（一次触发一次），实际 %d", n)
	}
	if n := atomic.LoadInt32(&st.llmCalls); n != 2 {
		t.Fatalf("llm-simple 次数应为 2，实际 %d", n)
	}
	if pr, ok := st.prompts["项目概要"]; !ok || !strings.Contains(pr, "旧全文-项目概要") || !strings.Contains(pr, "甲甲甲") {
		t.Fatalf("沉淀 prompt 应含旧全文与本轮新信息：%q", pr)
	}
	// 作用域：仅按 payload 的 session 取轮次（一次）
	if len(st.history) != 1 || st.history[0]["session_id"] != "s1" {
		t.Fatalf("data-session-history 请求应恰一次且限定该会话：%+v", st.history)
	}

	// 反证：同一配置下自动沉淀（session-compress 路径）被阈值挡住 →「不受阈值门控」只属手动触发
	before := atomic.LoadInt32(&st.llmCalls)
	p.extract(turnEvent{InstanceID: "ins-1", WorkDir: `C:\ws`, Session: "s1", LastTurn: "t-2"})
	if n := atomic.LoadInt32(&st.llmCalls); n != before {
		t.Fatalf("阈值 999999 下自动沉淀不应调 LLM（%d → %d）", before, n)
	}
}

// TestManualFlushGuards：前置不满足 → `{ok:false, reason}` 明确作答（不静默、不误报成功）。
func TestManualFlushGuards(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		cfg     map[string]any
		turns   []any
		want    string
	}{
		{"缺 session", map[string]any{"instance_id": "ins-1"}, map[string]any{memoryEnabledKey: "true"},
			[]any{map[string]any{"turn_id": "t-1"}}, "instance_id/session required"},
		{"记忆库未启用", map[string]any{"instance_id": "ins-1", "session": "s1"}, map[string]any{},
			[]any{map[string]any{"turn_id": "t-1"}}, memoryEnabledKey + " not enabled"},
		{"会话无轮次", map[string]any{"instance_id": "ins-1", "session": "s1"},
			map[string]any{memoryEnabledKey: "true"}, []any{}, "no turn in session s1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := newTestBus(t)
			st := &flushStub{prompts: map[string]string{}}
			st.wire(t, bus, c.cfg, c.turns)
			p := New(DefaultOptions())
			if err := p.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
				t.Fatalf("Start: %v", err)
			}
			v := bus.Emit(context.Background(), "memory.flush", c.payload).Wait()
			res, _ := v.Result.(map[string]any)
			if res == nil || res["ok"] != false {
				t.Fatalf("%s 应回 ok:false，实际 %#v", c.name, v.Result)
			}
			if !strings.Contains(strval(res["reason"]), c.want) {
				t.Fatalf("%s 原因应含 %q，实际 %q", c.name, c.want, res["reason"])
			}
			if n := atomic.LoadInt32(&st.saves); n != 0 {
				t.Fatalf("%s 不应写入任何类别，实际 save %d 次", c.name, n)
			}
		})
	}
}
