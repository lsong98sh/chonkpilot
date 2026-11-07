package server

// Ask 中心作答路由（2026-11-04）：独立待办窗口不是归属/投递实例，按 ask_id 全局兜底命中。
// 与既有精确语义的关系：instKey 精确 → deliverInstance → ask_id 全局（ask_id 全局唯一、取走即销）。

import (
	"testing"
	"time"
)

func TestTakeAskByAskIDFromAskCenter(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	s.mu.Lock()
	s.asks[instKey("ins-a", "ask-9")] = &askWaiter{
		askID: "ask-9", instanceID: "ins-a", deliverInstance: "ins-a",
		turnID: "t-9", toolID: "tc-9", expiresAt: time.Now().Add(time.Hour),
	}
	s.mu.Unlock()

	s.mu.Lock()
	w, ok := s.takeAskLocked("ins-center", "ask-9") // Ask 中心实例（非归属/投递实例）
	s.mu.Unlock()
	if !ok || w == nil || w.askID != "ask-9" {
		t.Fatalf("Ask 中心应按 ask_id 全局命中：ok=%v w=%+v", ok, w)
	}
	s.mu.Lock()
	_, left := s.asks[instKey("ins-a", "ask-9")]
	s.mu.Unlock()
	if left {
		t.Fatal("取走后登记应删除")
	}

	s.mu.Lock()
	_, ok = s.takeAskLocked("ins-center", "ask-missing")
	s.mu.Unlock()
	if ok {
		t.Fatal("未知 ask_id 不应命中")
	}
}

func TestTakeAskPrecedenceExactInstance(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	s.mu.Lock()
	s.asks[instKey("ins-b", "ask-same")] = &askWaiter{
		askID: "ask-same", instanceID: "ins-b", turnID: "t-b", expiresAt: time.Now().Add(time.Hour),
	}
	s.asks[instKey("ins-a", "ask-same")] = &askWaiter{
		askID: "ask-same", instanceID: "ins-a", turnID: "t-a", expiresAt: time.Now().Add(time.Hour),
	}
	s.mu.Unlock()

	s.mu.Lock()
	w, ok := s.takeAskLocked("ins-a", "ask-same")
	s.mu.Unlock()
	if !ok || w == nil || w.turnID != "t-a" {
		t.Fatalf("精确 instance 桶应优先：ok=%v w=%+v", ok, w)
	}
	// 另一桶仍保留（未被全局兜底误删）。
	s.mu.Lock()
	_, left := s.asks[instKey("ins-b", "ask-same")]
	s.mu.Unlock()
	if !left {
		t.Fatal("不应误删其他实例的同名 ask 登记")
	}
}
