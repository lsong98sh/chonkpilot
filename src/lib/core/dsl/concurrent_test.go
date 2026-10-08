package dsl

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentLoopNoSharedWrite 并发 LOOP 内对外层变量/对象字段/列表下标的写为「分支内副本」，
// 不回写共享作用域（原实现 setVar/deepSetFields/evalSubscriptWrite 会并发改写共享 map/slice → 竞态）。
func TestConcurrentLoopNoSharedWrite(t *testing.T) {
	e := newEnv(nil, nil)
	e.actions = append(e.actions, capAction())
	res := e.run(t, `SET 0 => outer
SET {"x":0} => cfg
SET ["a","b"] => lst
SET [] => acc
LOOP n=["a","b","c","d"] concurrency=4
   SET 1 => outer
   SET 1 => cfg.x
   SET "z" => lst[0]
   PUSH n => acc
   LLM "work" "{{n}}"
END
CAP "outer={{outer}};cfg={{cfg.x}};lst={{lst}}"
`)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	e.mu.Lock()
	calls := len(e.calls)
	e.mu.Unlock()
	if calls != 4 {
		t.Fatalf("4 次迭代各执行一次: %v", e.calls)
	}
	// 共享作用域值未被并发分支改写（分支捕获互不可见，§6.1）。
	var got string
	for _, s := range res.Summary {
		if strings.HasPrefix(s, "outer=") {
			got = s
			break
		}
	}
	if want := `outer=0;cfg=0;lst=["a","b"]`; got != want {
		t.Fatalf("共享作用域被并发分支改写: got %q, want %q", got, want)
	}
}

// TestConcurrentParallelNoSharedWrite PARALLEL 各分支对外层变量/对象字段/列表下标的写不回写共享作用域。
func TestConcurrentParallelNoSharedWrite(t *testing.T) {
	e := newEnv(nil, nil)
	e.actions = append(e.actions, capAction())
	res := e.run(t, `SET 0 => outer
SET {"x":0} => cfg
SET ["a","b"] => lst
PARALLEL
   SET 1 => outer
   SET 1 => cfg.x
   SET "z" => lst[0]
   LLM "b" "branch"
END
CAP "outer={{outer}};cfg={{cfg.x}};lst={{lst}}"
`)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	e.mu.Lock()
	calls := len(e.calls)
	e.mu.Unlock()
	if calls != 1 {
		t.Fatalf("LLM 分支应执行一次: %v", e.calls)
	}
	var got string
	for _, s := range res.Summary {
		if strings.HasPrefix(s, "outer=") {
			got = s
			break
		}
	}
	if want := `outer=0;cfg=0;lst=["a","b"]`; got != want {
		t.Fatalf("共享作用域被并发分支改写: got %q, want %q", got, want)
	}
}

// TestConcurrentLoopContinueSkipsIteration 并发 LOOP 的 CONTINUE 只跳过本次迭代，
// 不影响其余迭代（原实现落入 err != nil 分支 → markStop 停掉整层调度）。
func TestConcurrentLoopContinueSkipsIteration(t *testing.T) {
	const n = 12
	items := make([]string, n)
	for i := range items {
		if i == 0 {
			items[i] = `{"name":"i0","skip":true}`
			continue
		}
		items[i] = fmt.Sprintf(`{"name":"i%d"}`, i)
	}
	e := newEnv(map[string]string{"items.json": "[" + strings.Join(items, ",") + "]"}, nil)
	res := e.run(t, `LOOP item=#"items.json".array concurrency=2
   IF item.skip == true
      CONTINUE
   END
   SLP 5
   LLM "work" "处理 {{item.name}}"
END
`)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != n-1 {
		t.Fatalf("CONTINUE 只应跳过 skip 项，其余 %d 项执行（实际 %d）: %v", n-1, len(e.calls), e.calls)
	}
}

// TestConcurrentLoopCancelStopsScheduling 并发 LOOP 中取消上下文：中途停止调度，
// 未执行全部条目且不记录错误（ctx.Done 分支用带标签 break 真正跳出调度循环）。
func TestConcurrentLoopCancelStopsScheduling(t *testing.T) {
	const n = 12
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"name":"i%d"}`, i)
	}
	fs := &memFS{files: map[string]string{"items.json": "[" + strings.Join(items, ",") + "]"}}

	var eng *Engine
	var once sync.Once
	var calls int32
	act := Action{Name: "WORK", Run: func(_ *Scope, _ string) (string, error) {
		atomic.AddInt32(&calls, 1)
		once.Do(func() { eng.Cancel() }) // 首个迭代取消
		time.Sleep(2 * time.Millisecond)
		return "", nil
	}}
	eng = NewEngine(Options{Files: fs, Actions: []Action{act}})
	script, err := Parse(`LOOP item=#"items.json".array concurrency=2
   WORK "{{item.name}}"
END
`, []Action{act})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := eng.Execute(script); err != nil {
		t.Fatalf("取消应正常返回: %v", err)
	}
	if res := eng.Result(); len(res.Errors) != 0 {
		t.Fatalf("取消不应记录错误: %v", res.Errors)
	}
	if got := atomic.LoadInt32(&calls); got <= 0 || got >= n {
		t.Fatalf("取消后应停止调度（执行 %d / %d）", got, n)
	}
}
