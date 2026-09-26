// RB-3 白盒（能力面共享化，2026-09-22）：
//
//	① 注册表**按 node key 唯一**、instance 只持**引用集**（不再按 instance 物理复制）；
//	② **路由不物化 N×K** —— 暴露名解析「前缀 → instance+node+tool → 查 refs 校验 → 命中共享 node」；
//	③ `tools/list` **按 instance 构建路径**（UI 全量路径保留）。
//
// 覆盖（40-演进计划 §RB-3 验收列）：
//   - 「同根两 instance 只建一份」：同根（规范化路径）跨 instance 只扫描一次 / 只建一份节点；
//   - 「跨 instance 不可见」：A 的暴露名在 B 的归属域**不可解析**（scoped 隔离不因共享而泄漏）；
//   - 「注销回收引用」：注销一条引用 → 共享节点保留（其它引用仍可解析）；引用归零 → 节点释放。
//
// 对外零变更的另一半断言：暴露名仍为 `<节点名>_<原名>`（与既有 applyPrefix 规则逐字一致）。
package mcpgateway

import (
	"context"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// countingScanner 是 ContractScanner 测试替身：按原语名建官方 server（复用 fakeScanner 形态），
// 并统计扫描次数（RB-3 ①「同根只建一份」的直接证据）。
type countingScanner struct {
	toolNames []string
	mu        sync.Mutex
	calls     int
}

func (c *countingScanner) Scan(root string) (*mcp.Server, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	ms := mcp.NewServer(&mcp.Implementation{Name: "test-dir-rb3", Version: "1.0.0"}, nil)
	for _, n := range c.toolNames {
		ms.AddTool(&mcp.Tool{
			Name:        n,
			Description: "测试工具",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
	return ms, nil
}

func (c *countingScanner) scanCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// rb3Gateway 建一个带注入扫描器的 gateway（不经总线 Start，直接调用内部方法白盒断言）。
func rb3Gateway(t *testing.T, sc ContractScanner) *Gateway {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })
	g, err := New(Params{Bus: bus, ContractScanner: sc, Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })
	return g
}

// TestRB3SharedNodeBuiltOnce：同根两 instance **只建一份**（①）；dir 工具**不物化进 routes 表**
// （②：N×K → K + N 条引用）；暴露名保持前缀（对外零变更）；tools/list 按 instance 构建（③）。
func TestRB3SharedNodeBuiltOnce(t *testing.T) {
	root := t.TempDir()
	sc := &countingScanner{toolNames: []string{"t1", "t2"}}
	g := rb3Gateway(t, sc)

	if err := g.registerDirNode("ins-a-user", root, "ins-a"); err != nil {
		t.Fatalf("register ins-a: %v", err)
	}
	if err := g.registerDirNode("ins-b-user", root, "ins-b"); err != nil {
		t.Fatalf("register ins-b: %v", err)
	}

	// ① 同根只扫描一次（不再按 instance 物理复制）
	if n := sc.scanCount(); n != 1 {
		t.Fatalf("同根两 instance 应只扫描一次，实得 %d", n)
	}
	// ① 共享节点唯一 + 引用集 2 条
	sn, ok := g.reg.sharedNodeOf(dirNodeKey(root))
	if !ok {
		t.Fatalf("共享节点未登记：%s", dirNodeKey(root))
	}
	if len(sn.refs) != 2 {
		t.Fatalf("引用集应 2 条，实得 %d", len(sn.refs))
	}
	// ② 路由不物化：routes 表 0 条（dir 路由按引用集即时展开）
	if n := len(g.reg.routes); n != 0 {
		t.Fatalf("dir 工具不应物化进 routes 表，实得 %d 条", n)
	}
	// ② 展开后逐项等价：全量 2 refs × 2 tools
	if n := len(g.reg.routesAll()); n != 4 {
		t.Fatalf("全量展开应 4 条，实得 %d", n)
	}
	// ③ 按 instance 构建路径：各自 global ∪ 归属 = 2 条，且名带节点前缀（对外零变更）
	a := g.reg.routesFor("ins-a")
	if len(a) != 2 {
		t.Fatalf("ins-a 可见路由应 2 条，实得 %d", len(a))
	}
	for _, rt := range a {
		if rt.Name != "ins-a-user_"+rt.Original || rt.Scope != "ins-a" {
			t.Fatalf("暴露名/归属不符：%+v", rt)
		}
	}
}

// TestRB3CrossInstanceInvisible：跨 instance **不可见** —— 共享节点不泄漏归属域隔离
// （前缀 → instance+node+tool 解析带 refs 校验）。
func TestRB3CrossInstanceInvisible(t *testing.T) {
	root := t.TempDir()
	sc := &countingScanner{toolNames: []string{"t1"}}
	g := rb3Gateway(t, sc)
	if err := g.registerDirNode("ins-a-user", root, "ins-a"); err != nil {
		t.Fatalf("register ins-a: %v", err)
	}
	if err := g.registerDirNode("ins-b-user", root, "ins-b"); err != nil {
		t.Fatalf("register ins-b: %v", err)
	}

	// 归属域内可解析
	if _, ok := g.reg.findFor("ins-a-user_t1", "ins-a"); !ok {
		t.Fatal("ins-a 应可解析自身 dir 工具")
	}
	// 跨 instance 不可解析（scoped 不遮蔽 → 不命中）
	if rt, ok := g.reg.findFor("ins-a-user_t1", "ins-b"); ok {
		t.Fatalf("跨 instance 不应命中：%+v", rt)
	}
	// 全量路径仍含两者（UI 全量保留）；按 instance 路径互不可见
	if n := len(g.reg.routesAll()); n != 2 {
		t.Fatalf("全量应 2 条，实得 %d", n)
	}
	if n := len(g.reg.routesFor("ins-b")); n != 1 {
		t.Fatalf("ins-b 可见应 1 条，实得 %d", n)
	}
}

// TestRB3UnregisterRecyclesRef：注销**回收引用** —— 一条注销后共享节点保留（其它引用仍可解析）；
// 引用归零 → 节点释放（不可再解析）。
func TestRB3UnregisterRecyclesRef(t *testing.T) {
	root := t.TempDir()
	sc := &countingScanner{toolNames: []string{"t1", "t2"}}
	g := rb3Gateway(t, sc)
	if err := g.registerDirNode("ins-a-user", root, "ins-a"); err != nil {
		t.Fatalf("register ins-a: %v", err)
	}
	if err := g.registerDirNode("ins-b-user", root, "ins-b"); err != nil {
		t.Fatalf("register ins-b: %v", err)
	}
	key := dirNodeKey(root)

	existed, removed := g.unregisterDirNode("ins-a-user", "ins-a")
	if !existed || removed != 2 {
		t.Fatalf("注销 ins-a：existed=%v removed=%d（want true/2）", existed, removed)
	}
	// 引用归零前：共享节点保留，其它引用不受影响
	sn, ok := g.reg.sharedNodeOf(key)
	if !ok || len(sn.refs) != 1 {
		t.Fatalf("注销一条后共享节点应保留且引用为 1：ok=%v refs=%d", ok, len(sn.refs))
	}
	if _, ok := g.reg.findFor("ins-a-user_t1", "ins-a"); ok {
		t.Fatal("已注销引用不应再解析")
	}
	if _, ok := g.reg.findFor("ins-b-user_t1", "ins-b"); !ok {
		t.Fatal("其它引用应仍可解析（共享节点未被提前关闭）")
	}

	// 最后一条引用注销 → 节点释放
	g.unregisterDirNode("ins-b-user", "ins-b") //nolint:errcheck
	if _, ok := g.reg.sharedNodeOf(key); ok {
		t.Fatal("引用归零后共享节点应被释放")
	}
	if _, ok := g.reg.findFor("ins-b-user_t1", "ins-b"); ok {
		t.Fatal("节点释放后不应再解析")
	}
	// 释放后可重新注册同根（重建一份）
	if err := g.registerDirNode("ins-b-user", root, "ins-b"); err != nil {
		t.Fatalf("重新注册同根失败：%v", err)
	}
	if n := sc.scanCount(); n != 2 {
		t.Fatalf("释放后重注册应重新扫描一次（累计 2），实得 %d", n)
	}
}
