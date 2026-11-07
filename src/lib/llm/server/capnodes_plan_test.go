// capability 热重扫决策（P3）：只重建变化节点，未变化节点不注销 → 无工具面窗口。
package server

import (
	"testing"
)

func TestPlanCapabilityNodesUnchanged(t *testing.T) {
	insts := map[string]string{"i1": "/wd"}
	specs := map[string][]capNodeSpec{"i1": {{suffix: "user", root: "/u", sig: "s1"}}}
	existing := map[string][]string{"i1": {"i1-user"}}
	oldSigs := map[string]map[string]string{"i1": {"i1-user": "s1"}}

	p := planCapabilityNodes(insts, specs, existing, oldSigs)
	if n := len(p.ops()); n != 0 {
		t.Fatalf("根未变应零操作，got %d: %+v", n, p.ops())
	}
	if got := p.nodes["i1"]; len(got) != 1 || got[0] != "i1-user" {
		t.Fatalf("收敛名单不符: %+v", got)
	}
}

func TestPlanCapabilityNodesAddBeforeRemove(t *testing.T) {
	insts := map[string]string{"i1": "/wd"}
	// 新增 project（先注册），user 根消失（最后注销）。
	specs := map[string][]capNodeSpec{"i1": {{suffix: "project", root: "/p", sig: "s2"}}}
	existing := map[string][]string{"i1": {"i1-user"}}
	oldSigs := map[string]map[string]string{"i1": {"i1-user": "s1"}}

	p := planCapabilityNodes(insts, specs, existing, oldSigs)
	ops := p.ops()
	if len(ops) != 2 {
		t.Fatalf("应为「注册新 + 注销废弃」两操作，got %+v", ops)
	}
	if !ops[0].register || ops[0].nodeName != "i1-project" {
		t.Fatalf("应先注册新增节点: %+v", ops[0])
	}
	if ops[1].register || ops[1].nodeName != "i1-user" {
		t.Fatalf("后注销废弃节点: %+v", ops[1])
	}
}

func TestPlanCapabilityNodesChangedPair(t *testing.T) {
	insts := map[string]string{"i1": "/wd"}
	specs := map[string][]capNodeSpec{"i1": {{suffix: "user", root: "/u", sig: "s2"}}}
	existing := map[string][]string{"i1": {"i1-user"}}
	oldSigs := map[string]map[string]string{"i1": {"i1-user": "s1"}}

	p := planCapabilityNodes(insts, specs, existing, oldSigs)
	ops := p.ops()
	if len(ops) != 2 {
		t.Fatalf("变更节点应为注销+注册成对，got %+v", ops)
	}
	if ops[0].register || ops[0].nodeName != "i1-user" {
		t.Fatalf("先注销变更节点: %+v", ops[0])
	}
	if !ops[1].register || ops[1].nodeName != "i1-user" || ops[1].root != "/u" {
		t.Fatalf("再注册变更节点: %+v", ops[1])
	}
	if p.sigs["i1"]["i1-user"] != "s2" {
		t.Fatalf("收敛签名应更新: %+v", p.sigs["i1"])
	}
}

func TestPlanCapabilityNodesRootGone(t *testing.T) {
	insts := map[string]string{"i1": "/wd"}
	specs := map[string][]capNodeSpec{"i1": nil}
	existing := map[string][]string{"i1": {"i1-user", "i1-prjusr"}}
	oldSigs := map[string]map[string]string{"i1": {"i1-user": "a", "i1-prjusr": "b"}}

	p := planCapabilityNodes(insts, specs, existing, oldSigs)
	ops := p.ops()
	if len(ops) != 2 || ops[0].register || ops[1].register {
		t.Fatalf("根消失应只注销: %+v", ops)
	}
	if len(p.nodes["i1"]) != 0 {
		t.Fatalf("收敛名单应为空: %+v", p.nodes["i1"])
	}
}
