// 总线命名空间前缀行为测试：相对主题自动补前缀、遗留全主题兼容透传、
// handler 收到的 subject 一律为去前缀后的相对形式。
package mq

import (
	"context"
	"testing"
)

const testPrefix = "chonk."

func newPrefixedBus(t *testing.T) *internalBus {
	t.Helper()
	b, err := New(Options{Prefix: testPrefix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b.(*internalBus)
}

// TestPrefixAccessor Prefix() 返回注入的前缀。
func TestPrefixAccessor(t *testing.T) {
	b := newPrefixedBus(t)
	if b.Prefix() != testPrefix {
		t.Fatalf("Prefix() = %q, want %q", b.Prefix(), testPrefix)
	}
	pb, _ := New(Options{})
	if pb.Prefix() != "" {
		t.Fatalf("默认 Prefix() = %q, want 空串", pb.Prefix())
	}
	_ = pb.Close()
}

// TestPrefixRelativeRoundTrip 相对主题订阅 + 相对主题发布 → 命中；handler subject 为相对形式。
func TestPrefixRelativeRoundTrip(t *testing.T) {
	b := newPrefixedBus(t)
	got := make(chan string, 1)
	if _, err := b.Subscribe("session-start", func(subject string, _ []byte) { got <- subject }); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish("session-start", nil); err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != "session-start" {
		t.Fatalf("handler subject = %q, want %q（去前缀相对形式）", s, "session-start")
	}
}

// TestPrefixValueSubject Emit 的 Value.Subject 同样为相对形式。
func TestPrefixValueSubject(t *testing.T) {
	b := newPrefixedBus(t)
	if _, err := b.On("req.one", 0, func(_ context.Context, subject string, v *Value) error {
		v.Result = subject
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v := b.Emit(context.Background(), "req.one", nil).Wait()
	if v.Subject != "req.one" {
		t.Fatalf("Value.Subject = %q, want %q", v.Subject, "req.one")
	}
	if r, _ := v.Result.(string); r != "req.one" {
		t.Fatalf("handler 收到 subject = %q", r)
	}
}

// TestPrefixLegacyFullPassthrough 兼容保护：发布遗留全主题（已带前缀）原样透传、不重复加，
// 相对订阅与全主题订阅都能命中，handler 统一收去前缀相对形式。
func TestPrefixLegacyFullPassthrough(t *testing.T) {
	b := newPrefixedBus(t)
	var rel, full []string
	if _, err := b.Subscribe("srv.notify.>", func(subject string, _ []byte) { rel = append(rel, subject) }); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Subscribe("chonk.srv.notify.>", func(subject string, _ []byte) { full = append(full, subject) }); err != nil {
		t.Fatal(err)
	}
	// 遗留全主题发布（data 面直传形态）→ 两个订阅都命中
	if err := b.Publish("chonk.srv.notify.data-user-config-save", nil); err != nil {
		t.Fatal(err)
	}
	// 相对形式发布（同构）→ 同样命中（内部同全主题）
	if err := b.Publish("srv.notify.data-scenario-refresh", nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"srv.notify.data-user-config-save", "srv.notify.data-scenario-refresh",
	}
	if len(rel) != 2 || len(full) != 2 {
		t.Fatalf("rel=%v full=%v", rel, full)
	}
	for i, w := range want {
		if rel[i] != w || full[i] != w {
			t.Fatalf("subject[%d] = rel:%q full:%q, want %q", i, rel[i], full[i], w)
		}
	}
}

// TestPrefixWildcardGt 相对 ">" 通配只匹配本总线前缀域内的主题。
func TestPrefixWildcardGt(t *testing.T) {
	b := newPrefixedBus(t)
	got := make(chan string, 3)
	if _, err := b.Subscribe(">", func(subject string, _ []byte) { got <- subject }); err != nil {
		t.Fatal(err)
	}
	_ = b.Publish("session-send", nil)
	_ = b.Publish("tool-call", nil)
	_ = b.Publish("chonk.legacy-full", nil) // 遗留全主题也在本前缀域内 → 命中
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		seen[<-got] = true
	}
	if !seen["session-send"] || !seen["tool-call"] || !seen["legacy-full"] {
		t.Fatalf("wildcard > 收到 = %v", seen)
	}
}

// TestPrefixIsolation 不同前缀/无前缀主题互不干扰（相对主题注册在总线前缀域内）。
func TestPrefixIsolation(t *testing.T) {
	b := newPrefixedBus(t)
	n := 0
	if _, err := b.On("a.b", 0, func(context.Context, string, *Value) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	// 无前缀总线上的同相对主题与带前缀总线隔离：这里以另一条带不同前缀的总线验证互不命中。
	b2, _ := New(Options{Prefix: "other."})
	defer b2.Close()
	n2 := 0
	_, _ = b2.On("a.b", 0, func(context.Context, string, *Value) error { n2++; return nil })
	// b 发布 a.b → 命中 b（n=1）；b2 未收到（前缀 other. 域内无 a.b 全主题发布）
	if err := b.Publish("a.b", nil); err != nil {
		t.Fatal(err)
	}
	if n != 1 || n2 != 0 {
		t.Fatalf("b=%d b2=%d（应 1/0）", n, n2)
	}
}
