// 记忆库带出指引白盒：类别清单动态获取（data-memory-list）——新增自定义类别后，
// 指引文本应包含该类别（而非仅静态预置清单）；取数失败才回落静态预置。
// 另覆盖 I-68 ② 短时缓存：TTL 内第二次不再发 data-memory-list（计数断言）、
// data-memory-refresh / TTL 过期后重新取数、并发无 race。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestMemoryGuideDynamicCategories：建自定义类别（data-memory-save）→ memoryGuide
// 的指引文本包含该类别；静态预置类别（项目概要）仍在。
func TestMemoryGuideDynamicCategories(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	enableTestMemory(t, s)

	res := dataCall(t, s, "data-memory-save", map[string]any{
		"req_id": "mg1", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策", "content": "记录架构决策"},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("建自定义类别失败: %+v", res)
	}

	guide := s.memoryGuide("ins-test", testWorkDir)
	if !strings.Contains(guide, "架构决策") {
		t.Fatalf("指引应包含自定义类别 %q: %q", "架构决策", guide)
	}
	if !strings.Contains(guide, "项目概要") {
		t.Fatalf("指引应保留预置类别: %q", guide)
	}
}

// enableTestMemory 打开测试实例的记忆库总开关（data-prj-config-save，既有面）：
// 指引门控与 plugin-memory 同口径（默认关闭）→ 断言"启用态指引文本"的用例须先启用。
func enableTestMemory(t *testing.T, s *Server) {
	t.Helper()
	res := dataCall(t, s, "data-prj-config-save", map[string]any{
		"req_id": "mg-enable", "instance_id": "ins-test",
		"data": map[string]any{"key": "memory.enabled", "value": "true"},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("启用记忆库失败: %+v", res)
	}
}

// saveTestPrjConfig 写一条项目配置（data-prj-config-save，既有面）。
func saveTestPrjConfig(t *testing.T, s *Server, key, value string) {
	t.Helper()
	res := dataCall(t, s, "data-prj-config-save", map[string]any{
		"req_id": "mg-cfg-" + key, "instance_id": "ins-test",
		"data": map[string]any{"key": key, "value": value},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("写项目配置 %s=%s 失败: %+v", key, value, res)
	}
}

// TestMemoryGuideDisabled（P0-A ①）：memory.enabled 关闭（缺失）→ 指引为空（不注入），
// 且**不发** data-memory-list（关闭态不取类别清单）。
func TestMemoryGuideDisabled(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	count := countDataMemoryList(t, s)

	if g := s.memoryGuide("ins-test", testWorkDir); g != "" {
		t.Fatalf("记忆库关闭（配置缺失）时指引应为空: %q", g)
	}
	if got := count(); got != 0 {
		t.Fatalf("关闭态不应请求 data-memory-list，实得 %d 次", got)
	}

	// 显式 false 同样不注入
	saveTestPrjConfig(t, s, "memory.enabled", "false")
	if g := s.memoryGuide("ins-test", testWorkDir); g != "" {
		t.Fatalf("memory.enabled=false 时指引应为空: %q", g)
	}
	if got := count(); got != 0 {
		t.Fatalf("关闭态不应请求 data-memory-list，实得 %d 次", got)
	}
}

// TestMemoryGuideCategoryDisabled（P0-A ②）：启用记忆库 + 某类别 memory.category.X=false
// → 指引不含 X，仍含其它启用类别；"用户偏好"同键口径可单独关闭。
func TestMemoryGuideCategoryDisabled(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	enableTestMemory(t, s)
	saveTestPrjConfig(t, s, "memory.category.开发规范", "false")
	saveTestPrjConfig(t, s, "memory.category."+persist.MemoryUserCategory, "false")

	guide := s.memoryGuide("ins-test", testWorkDir)
	if strings.Contains(guide, "开发规范") {
		t.Fatalf("被禁用的类别不应列入指引: %q", guide)
	}
	if strings.Contains(guide, "用户偏好") {
		t.Fatalf("被禁用的用户偏好不应列入指引: %q", guide)
	}
	if !strings.Contains(guide, "项目概要") {
		t.Fatalf("其它启用类别应仍在指引中: %q", guide)
	}
	if !strings.Contains(guide, "用户记忆文件") {
		t.Fatalf("启用态指引文本不应改变: %q", guide)
	}

	// 全类别禁用 + 用户偏好禁用 → 无可指引项 → 空串
	for _, c := range persist.MemoryCategoryNames() {
		saveTestPrjConfig(t, s, "memory.category."+c, "false")
	}
	if g := s.memoryGuide("ins-test", testWorkDir); g != "" {
		t.Fatalf("全部类别禁用时指引应为空: %q", g)
	}
}

// TestMemoryCategoryNamesFallback：取数失败（未知实例 → persist 报错）→ 回落静态预置清单。
func TestMemoryCategoryNamesFallback(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	got := s.memoryCategoryNames("ins-不存在")
	want := persist.MemoryCategoryNames()
	if len(got) != len(want) {
		t.Fatalf("回落预置清单 = %v，want %v", got, want)
	}
}

// countDataMemoryList 订阅 data-memory-list，对**请求**（无 ok 字段）计数；返回读取函数。
// 应答（persist.reply，带 ok）与请求同主题，故须排除应答、只数请求。
func countDataMemoryList(t *testing.T, s *Server) func() int {
	t.Helper()
	var mu sync.Mutex
	n := 0
	sub, err := s.bus.On("data-memory-list", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if _, isReply := m["ok"]; isReply {
			return nil
		}
		mu.Lock()
		n++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// TestMemoryGuideCategoryCache：TTL 内首次请求后第二次不再发 data-memory-list（计数断言）。
func TestMemoryGuideCategoryCache(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	enableTestMemory(t, s)
	count := countDataMemoryList(t, s)

	if g := s.memoryGuide("ins-test", testWorkDir); !strings.Contains(g, "项目概要") {
		t.Fatalf("首次指引应含预置类别: %q", g)
	}
	if g := s.memoryGuide("ins-test", testWorkDir); !strings.Contains(g, "项目概要") {
		t.Fatalf("二次指引应含预置类别: %q", g)
	}
	if got := count(); got != 1 {
		t.Fatalf("data-memory-list 请求次数 = %d，want 1（第二次应命中缓存）", got)
	}
}

// TestMemoryGuideCacheInvalidation：收到 data-memory-refresh（既有主题）→ 缓存失效重新取数；
// 超 TTL → 重新取数。
func TestMemoryGuideCacheInvalidation(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	enableTestMemory(t, s)
	count := countDataMemoryList(t, s)

	s.memoryGuide("ins-test", testWorkDir) // 1：取数并缓存
	s.memoryGuide("ins-test", testWorkDir) // 命中缓存
	if got := count(); got != 1 {
		t.Fatalf("缓存命中前请求次数 = %d，want 1", got)
	}

	// refresh：记忆域 save/delete 后 persist 发出的既有广播（同步分发 → 返回即已失效）
	_ = s.bus.Emit(context.Background(), "data-memory-refresh", jb(map[string]any{
		"instance_id": "ins-test", "id": "架构决策", "op": "save",
	}))
	s.memoryGuide("ins-test", testWorkDir)
	if got := count(); got != 2 {
		t.Fatalf("refresh 后请求次数 = %d，want 2（缓存应被失效重取）", got)
	}

	// TTL 过期：把缓存项过期时刻拨到过去（等价 TTL 到期）→ 重新取数
	s.memCache.mu.Lock()
	e := s.memCache.entries["ins-test"]
	e.expires = time.Now().Add(-time.Second)
	s.memCache.entries["ins-test"] = e
	s.memCache.mu.Unlock()
	s.memoryGuide("ins-test", testWorkDir)
	if got := count(); got != 3 {
		t.Fatalf("TTL 过期后请求次数 = %d，want 3（过期应重取）", got)
	}
}

// TestMemoryGuideCacheConcurrent：多 turn 并发取指引 + 并发 refresh 失效，无 race（-race 断言）。
func TestMemoryGuideCacheConcurrent(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	enableTestMemory(t, s)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if g := s.memoryGuide("ins-test", testWorkDir); !strings.Contains(g, "项目概要") {
					t.Errorf("指引缺预置类别: %q", g)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			_ = s.bus.Emit(context.Background(), "data-memory-refresh", jb(map[string]any{"instance_id": "ins-test"}))
		}
	}()
	wg.Wait()
}
