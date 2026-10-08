// G-18 取消传播验收（主仓复现 TECH-POC/cancelprop A 组）：父轮次取消 → 在飞子轮次
// （llm_run 的 LLM 子步）**立即**收到取消并终止，而不是跑满。
//
// 观测点（黑盒可达）：子轮次的 mock LLM 请求被客户端中止 —— 服务端 `r.Context().Done()`。
//
//	修前（gapA 子 ctx 非父 ctx 派生 + gapB 只等 done）：取消不影响子 ctx → 子 LLM 请求
//	  跑满（1500ms）→ 父侧阻塞到在飞子步结束；
//	修后（gapA ctx 继承 + gapB select ctx）：取消即中止 → 远早于子步全程。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestChildTurnCancelPropagation：期望「父轮次取消 → 在飞子轮次 LLM 请求被中止」。
func TestChildTurnCancelPropagation(t *testing.T) {
	const childStep = 1500 * time.Millisecond // 子步 LLM 慢响应时长（"不可打断"窗口）

	var (
		mu         sync.Mutex
		childCalls int
	)
	childAborted := make(chan time.Duration, 4)

	// 脚本 = 5 个串行 LLM 子步（每个子步 = 一个子 turn）——对齐 PoC「子步骤 5/5 vs 1/5」。
	script := `LLM "worker" "step 1" "取消传播步 1"
LLM "worker" "step 2" "取消传播步 2"
LLM "worker" "step 3" "取消传播步 3"
LLM "worker" "step 4" "取消传播步 4"
LLM "worker" "step 5" "取消传播步 5"
`
	const trigger = "cancel please"
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		switch {
		case strings.Contains(text, trigger):
			// 父轮次：llm_run（脚本 = 5 子步）
			args := jb(map[string]any{"script": script, "tool_call_display_name": "取消传播作业"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-cancel", "type": "function",
					"function": map[string]any{"name": "llm_run", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(text, "step "):
			// 子轮次：慢响应；请求 ctx 被取消（父取消下传）即中止
			mu.Lock()
			childCalls++
			mu.Unlock()
			start := time.Now()
			select {
			case <-time.After(childStep):
				t.Logf("子轮次 LLM 请求跑满 %v（未被取消中止 → 修前行为）", childStep)
			case <-r.Context().Done():
				select {
				case childAborted <- time.Since(start):
				default:
				}
			}
			llmSSE(w, []string{sseChunk(map[string]any{"content": "child-ok"}, "stop")})
		default:
			llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
		}
	}))
	defer llm.Close()

	s := newTestServer(t, llm)
	startTurn(t, s, "s-cancel", "t-cancel")

	// 先订父轮次事件流（complete 可能在取消瞬间即发，事后订阅会漏）
	evsCh := make(chan []map[string]any, 1)
	go func() { evsCh <- collectTurn(t, s.bus, "t-cancel", 10*time.Second) }()

	// 触发父轮次（异步：llm_run 经 `go runSubJob` 派生第 1 个子轮次）
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cancel", "turn": "t-cancel",
		"type": "text-user", "content": trigger,
	}))

	// 等子轮次的第一个 LLM 请求到达（= 子步在飞）
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := childCalls
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("子轮次未开始（llm_run 未派生子会话）")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 取消父轮次
	s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cancel", "turn": "t-cancel",
	})).Wait()

	// 断言①（核心）：在飞子步的 LLM 请求被中止，且远早于子步全程（gapA+gapB 生效）
	select {
	case d := <-childAborted:
		t.Logf("子轮次在飞 LLM 请求在请求发出后 %v 被中止（子步全程 %v）", d.Round(time.Millisecond), childStep)
		if d >= childStep-200*time.Millisecond {
			t.Fatalf("子轮次请求直到跑满才结束（%v）：取消未沿 ctx 下传（gapA/gapB 未生效）", d)
		}
	case <-time.After(childStep - 400*time.Millisecond):
		t.Fatal("父轮次取消后子轮次请求未中止：子 ctx 未继承父 ctx 或等待不可打断（gapA/gapB 未生效）")
	}

	// 断言②：父轮次落 interrupted 终态（既有语义不变）
	var evs []map[string]any
	select {
	case evs = <-evsCh:
	case <-time.After(10 * time.Second):
		t.Fatal("未收到父轮次 llm-complete")
	}
	c := lastComplete(evs)
	if c == nil || c["status"] != "interrupted" {
		t.Fatalf("父轮次终态应为 interrupted：%+v（events=%d）", c, len(evs))
	}

	// 断言③：取消后不再启动新的子步（后置步未启动）
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if childCalls != 1 {
		t.Fatalf("取消后仍启动了新子步：childCalls=%d want 1", childCalls)
	}
}
