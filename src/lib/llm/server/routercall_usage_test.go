package server

import (
	"context"
	"testing"

	"github.com/lsong98sh/chonkpilot-agent/router"
)

// TestForwardRouterEventsCarriesUsageOnDone：router 的 usage 事件（LR-9 归一）必须随 done 投出，
// 供宿主 UsageObserver → `llm-usage` 事件使用；此前该事件被显式丢弃。
func TestForwardRouterEventsCarriesUsageOnDone(t *testing.T) {
	in := make(chan router.Event, 4)
	out := make(chan StreamEvent, 8)
	go forwardRouterEvents(context.Background(), in, out)

	in <- router.Event{Type: router.EvTextDelta, Text: "hi"}
	in <- router.Event{Type: router.EvUsage, Usage: &router.Usage{
		PromptTokens: 11, CompletionTokens: 2, TotalTokens: 13, ReasoningTokens: 1, CachedTokens: 5,
	}}
	in <- router.Event{Type: router.EvDone, FinishReason: "stop"}
	close(in)

	var last StreamEvent
	for ev := range out {
		if ev.Done {
			last = ev
		}
	}
	if last.Usage == nil {
		t.Fatal("done 事件应携带 usage")
	}
	if last.Usage.PromptTokens != 11 || last.Usage.CachedTokens != 5 || last.Usage.ReasoningTokens != 1 {
		t.Fatalf("usage 字段不符: %+v", *last.Usage)
	}
}

// TestForwardRouterEventsNoUsage：上游未提供 usage → done.Usage 为 nil（前端保持估算值）。
func TestForwardRouterEventsNoUsage(t *testing.T) {
	in := make(chan router.Event, 2)
	out := make(chan StreamEvent, 4)
	go forwardRouterEvents(context.Background(), in, out)

	in <- router.Event{Type: router.EvDone, FinishReason: "stop"}
	close(in)

	var last StreamEvent
	for ev := range out {
		if ev.Done {
			last = ev
		}
	}
	if last.Usage != nil {
		t.Fatalf("无 usage 事件时 done.Usage 应为 nil，实际 %+v", *last.Usage)
	}
}
