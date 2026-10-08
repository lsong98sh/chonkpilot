// dir 暴露名歧义（下划线）防护（RB-3 低危项）：
//
//	addDirRef 唯一性判重（同 scope 暴露名集合相交 → 显式报错）；
//	findDirLocked 确定性（shared 为 map 迭代随机 → 按 node key 排序后遍历，结果可复现）。
package mcpgateway

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildShared 构造一个最小共享节点（含 tools/byName/refs），返回节点与首条引用。
func buildShared(key, name, scope string, toolNames ...string) (*sharedNode, *dirRef) {
	tools := make([]*mcp.Tool, 0, len(toolNames))
	byName := make(map[string]*mcp.Tool, len(toolNames))
	for _, n := range toolNames {
		tool := &mcp.Tool{Name: n}
		tools = append(tools, tool)
		byName[n] = tool
	}
	ref := &dirRef{nodeKey: key, name: name, scope: scope, pk: provKey("dir", scope, name)}
	return &sharedNode{key: key, tools: tools, byName: byName, refs: []*dirRef{ref}}, ref
}

// TestAddDirRefRejectsExposedNameAmbiguity：两条同 scope 引用的暴露名集合相交
// （name=a+tool=b_c 与 name=a_b+tool=c 均得 a_b_c）→ addDirRef 显式报错。
func TestAddDirRefRejectsExposedNameAmbiguity(t *testing.T) {
	r := newRegistry()
	sn1, ref1 := buildShared("root-a", "a", scopeGlobal, "b_c")
	if got := r.swapSharedNode(sn1); got != sn1 {
		t.Fatal("swapSharedNode 应登记 sn1")
	}
	if err := r.addDirRef(sn1, ref1, &providerState{key: ref1.pk, ref: ref1}); err != nil {
		t.Fatalf("首条引用应注册成功：%v", err)
	}
	sn2, ref2 := buildShared("root-b", "a_b", scopeGlobal, "c")
	r.swapSharedNode(sn2)
	if err := r.addDirRef(sn2, ref2, &providerState{key: ref2.pk, ref: ref2}); err == nil {
		t.Fatal("暴露名歧义应报错，实得 nil")
	}
}

// TestFindDirLockedDeterministic：绕过 addDirRef 直接构造歧义状态，findDirLocked 必须
// 按 node key 排序确定性命中（root-a < root-b → 恒选 name=a 的引用），不随 map 迭代变化。
func TestFindDirLockedDeterministic(t *testing.T) {
	r := newRegistry()
	sn1, ref1 := buildShared("root-a", "a", scopeGlobal, "b_c")
	r.swapSharedNode(sn1)
	sn2, ref2 := buildShared("root-b", "a_b", scopeGlobal, "c")
	r.swapSharedNode(sn2)
	r.refs[ref1.pk] = ref1
	r.refs[ref2.pk] = ref2

	for i := 0; i < 200; i++ {
		rt, ok := r.findDirLocked("a_b_c", scopeGlobal)
		if !ok {
			t.Fatalf("第 %d 次应命中（暴露名 a_b_c）", i)
		}
		if rt.Provider != ref1.pk {
			t.Fatalf("第 %d 次应确定性命中 root-a（%s），实得 %s", i, ref1.pk, rt.Provider)
		}
		if rt.Original != "b_c" {
			t.Fatalf("第 %d 次原名应为 b_c，实得 %s", i, rt.Original)
		}
	}
}
