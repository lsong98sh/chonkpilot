// promise 内核单测（61-消息一览 §9.1：order/Result/Errors 收集/恒 accept/v1 兼容）。
package mq

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestBus(t *testing.T) *internalBus {
	t.Helper()
	b, err := newBus("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b.(*internalBus)
}

// TestOrderExecution order 升序、同 order 按注册序。
func TestOrderExecution(t *testing.T) {
	b := newTestBus(t)
	var got []string
	subs := []struct {
		order int
		name  string
	}{
		{0, "A"}, {2, "C"}, {1, "B"}, {0, "D"},
	}
	for _, s := range subs {
		s := s
		if _, err := b.On("t.x", s.order, func(_ context.Context, _ string, _ *Value) error {
			got = append(got, s.name)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	b.Emit(context.Background(), "t.x", nil).Wait()
	want := []string{"A", "D", "B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order exec = %v, want %v", got, want)
	}
}

// TestResultWriteback handler 写回 Result，await 取回。
func TestResultWriteback(t *testing.T) {
	b := newTestBus(t)
	if _, err := b.On("req.one", 0, func(_ context.Context, subject string, v *Value) error {
		v.Result = map[string]any{"echo": subject, "payload": string(v.Payload)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := b.Emit(context.Background(), "req.one", []byte("ping"))
	select {
	case <-f.Done():
	case <-time.After(time.Second):
		t.Fatal("future not resolved")
	}
	v := f.Wait()
	if v.Err() != nil {
		t.Fatal(v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res["echo"] != "req.one" || res["payload"] != "ping" {
		t.Fatalf("result = %v", v.Result)
	}
}

// TestErrorsCollectedAccept error+panic 收集进 Errors，后续 handler 照跑、恒 accept。
func TestErrorsCollectedAccept(t *testing.T) {
	b := newTestBus(t)
	e1 := errors.New("boom")
	order := []string{}
	if _, err := b.On("e.t", 0, func(context.Context, string, *Value) error { order = append(order, "1"); return e1 }); err != nil {
		t.Fatal(err)
	}
	if _, err := b.On("e.t", 0, func(context.Context, string, *Value) error { order = append(order, "2"); panic("p") }); err != nil {
		t.Fatal(err)
	}
	if _, err := b.On("e.t", 0, func(_ context.Context, _ string, v *Value) error {
		order = append(order, "3")
		v.Result = "ok"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v := b.Emit(context.Background(), "e.t", nil).Wait() // 不 reject，正常返回
	if v.Err() == nil || v.Err() != e1 {
		t.Fatalf("first error = %v", v.Err())
	}
	if len(v.Errors) != 2 {
		t.Fatalf("errors = %v", v.Errors)
	}
	if v.Result != "ok" {
		t.Fatalf("result = %v（后续 handler 应照跑）", v.Result)
	}
	if !reflect.DeepEqual(order, []string{"1", "2", "3"}) {
		t.Fatalf("exec order = %v", order)
	}
}

// TestCanceledCtx ctx 取消 → 错误收集进 Errors（恒 accept）。
func TestCanceledCtx(t *testing.T) {
	b := newTestBus(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	b.On("c.t", 0, func(context.Context, string, *Value) error { ran = true; return nil })
	v := b.Emit(ctx, "c.t", nil).Wait()
	if ran || v.Err() == nil {
		t.Fatalf("ran=%v err=%v", ran, v.Err())
	}
}

// TestV1CompatMixed v1 Subscribe/Publish 与 v2 On/Emit 混用同一主题。
func TestV1CompatMixed(t *testing.T) {
	b := newTestBus(t)
	var v1got []string
	b.Subscribe("m.t", func(subject string, payload []byte) { v1got = append(v1got, subject+"="+string(payload)) })
	b.On("m.t", 0, func(_ context.Context, _ string, v *Value) error { v.Result = "v2-ok"; return nil })

	if err := b.Publish("m.t", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v1got, []string{"m.t=hi"}) {
		t.Fatalf("v1 got = %v", v1got)
	}
	v := b.Emit(context.Background(), "m.t", "hi2").Wait()
	if v.Result != "v2-ok" || !reflect.DeepEqual(v1got, []string{"m.t=hi", "m.t=hi2"}) {
		t.Fatalf("v=%+v v1got=%v", v, v1got)
	}
}

// TestWildcardOrder 通配与精确订阅统一按 order 排序。
func TestWildcardOrder(t *testing.T) {
	b := newTestBus(t)
	var got []string
	b.On("a.>", 1, func(context.Context, string, *Value) error { got = append(got, "wild1"); return nil })
	b.On("a.b", 0, func(context.Context, string, *Value) error { got = append(got, "exact0"); return nil })
	b.On("a.>", 2, func(context.Context, string, *Value) error { got = append(got, "wild2"); return nil })
	b.Emit(context.Background(), "a.b", nil).Wait()
	if !reflect.DeepEqual(got, []string{"exact0", "wild1", "wild2"}) {
		t.Fatalf("got = %v", got)
	}
}

// TestUnsubscribe 退订后不再收到。
func TestUnsubscribe(t *testing.T) {
	b := newTestBus(t)
	n := 0
	s1, _ := b.On("u.t", 0, func(context.Context, string, *Value) error { n++; return nil })
	s2, _ := b.On("u.>", 0, func(context.Context, string, *Value) error { n++; return nil })
	_ = s1.Unsubscribe()
	_ = s2.Unsubscribe()
	b.Emit(context.Background(), "u.t", nil).Wait()
	if n != 0 {
		t.Fatalf("after unsubscribe n = %d", n)
	}
}

// TestCloseSemantics 关闭后订阅/发布行为。
func TestCloseSemantics(t *testing.T) {
	b := newTestBus(t)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.On("x", 0, func(context.Context, string, *Value) error { return nil }); err != errClosed {
		t.Fatalf("On err = %v", err)
	}
	if _, err := b.Subscribe("x", func(string, []byte) {}); err != errClosed {
		t.Fatalf("Subscribe err = %v", err)
	}
	if err := b.Publish("x", nil); err != errClosed {
		t.Fatalf("Publish err = %v", err)
	}
	v := b.Emit(context.Background(), "x", nil).Wait()
	if v.Err() != errClosed {
		t.Fatalf("Emit err = %v", v.Err())
	}
}

// TestPayloadKinds 载荷形态（nil/[]byte/string/map/struct）归一为 JSON payload。
func TestPayloadKinds(t *testing.T) {
	b := newTestBus(t)
	var got map[string]string
	b.On("p.t", 0, func(_ context.Context, _ string, v *Value) error { return v.JSON(&got) })
	b.Emit(context.Background(), "p.t", map[string]string{"k": "v"}).Wait()
	if got["k"] != "v" {
		t.Fatalf("map payload got %v", got)
	}
	type P struct {
		A string `json:"a"`
	}
	var got2 P
	b.On("p.t2", 0, func(_ context.Context, _ string, v *Value) error { return v.JSON(&got2) })
	b.Emit(context.Background(), "p.t2", P{A: "x"}).Wait()
	if got2.A != "x" {
		t.Fatalf("struct payload got %+v", got2)
	}
}

// TestConcurrentEmit 并发发布线程安全（-race 下验证；handler 顺序各自保持）。
func TestConcurrentEmit(t *testing.T) {
	b := newTestBus(t)
	var total int64
	b.On("cc.t", 0, func(_ context.Context, _ string, v *Value) error {
		atomic.AddInt64(&total, 1)
		return nil
	})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				b.Emit(context.Background(), "cc.t", nil).Wait()
			}
		}()
	}
	wg.Wait()
	if total != 800 {
		t.Fatalf("total = %d", total)
	}
}

// TestValueJSONHelpers Value.JSON / Err。
func TestValueJSONHelpers(t *testing.T) {
	v := &Value{Payload: []byte(`{"a":1}`)}
	var out map[string]int
	if err := v.JSON(&out); err != nil || out["a"] != 1 {
		t.Fatalf("json=%v err=%v", out, err)
	}
	if v.Err() != nil {
		t.Fatal("no err expected")
	}
}
