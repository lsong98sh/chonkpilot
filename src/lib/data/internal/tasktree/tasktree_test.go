// tasktree 域单测（A-18）：节点落库的状态列合并收进 UpdateIn 单事务 RMW 后——同 id 重放
// 不复活已逻辑删除节点（closed/deleted_at 保留）、显式携带 closed 以请求为准；逻辑删除
// （级联子树标记）与「节点不存在 → 幂等 OK」行为不变。
package tasktree

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// newTasktreeEnv 构造测试服务与实例 Scope。目录先于 data.Reset 的 Cleanup 注册创建
// （清理顺序 LIFO → data.Reset 关共享层连接先于 TempDir 删除执行——Windows 句柄占用）；
// dataDir 非空 = 临时形态，prj 与 prjusr 同库落临时目录，不污染用户数据根。
func newTasktreeEnv(t *testing.T) (*Service, facade.Scope) {
	t.Helper()
	usrDir := t.TempDir()
	wd := t.TempDir()
	dd := t.TempDir()
	data.Reset() // 清前一用例经共享层缓存持有的连接
	t.Cleanup(data.Reset)
	s := New(kernel.NewBase(nil, kernel.Options{UsrPath: filepath.Join(usrDir, "usr.db"), AppDir: usrDir}))
	return s, facade.Scope{WorkDir: wd, DataDir: dd}
}

func upsertNode(t *testing.T, s *Service, inst string, scope facade.Scope, n facade.TaskNode) {
	t.Helper()
	if _, err := s.TasktreeUpsert(facade.TasktreeUpsertRequest{InstanceID: inst, Scope: scope, Node: n}); err != nil {
		t.Fatalf("upsert %s: %v", n.ID, err)
	}
}

// TestTasktreeUpsertKeepsClosedOnReplay：删除后同 id 重放（不带 closed）不得复活；
// 显式携带 closed=false 时以请求为准。
func TestTasktreeUpsertKeepsClosedOnReplay(t *testing.T) {
	s, scope := newTasktreeEnv(t)
	inst := "ins-tt"

	upsertNode(t, s, inst, scope, facade.TaskNode{ID: "n1", Kind: "llm", TopSession: "top1", Status: "running"})
	if _, err := s.TasktreeDelete(facade.TasktreeDeleteRequest{InstanceID: inst, Scope: scope, NodeID: "n1"}); err != nil {
		t.Fatalf("delete n1: %v", err)
	}

	// 同 id 重放 running（Closed=nil = 请求未携带）
	upsertNode(t, s, inst, scope, facade.TaskNode{ID: "n1", Kind: "llm", TopSession: "top1", Status: "running"})

	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	var rec data.Record
	if ok, err := prj.Table("tasktree").Get("n1", &rec); err != nil || !ok {
		t.Fatalf("get n1: ok=%v err=%v", ok, err)
	}
	if v, _ := rec["closed"].(bool); !v {
		t.Fatalf("重放复活了已逻辑删除节点：closed=%v", rec["closed"])
	}
	if kernel.Sval(rec["deleted_at"]) == "" {
		t.Fatal("deleted_at 未保留")
	}
	if got := kernel.Sval(rec["status"]); got != "running" {
		t.Fatalf("重放的新状态应落库：got=%q", got)
	}

	// 显式携带 closed=false → 以请求为准（解闭场景）
	f := false
	upsertNode(t, s, inst, scope, facade.TaskNode{ID: "n1", Kind: "llm", TopSession: "top1", Status: "running", Closed: &f})
	if ok, err := prj.Table("tasktree").Get("n1", &rec); err != nil || !ok {
		t.Fatalf("get n1: ok=%v err=%v", ok, err)
	}
	if v, _ := rec["closed"].(bool); v {
		t.Fatal("显式 closed=false 应以请求为准")
	}
}

// TestTasktreeDeleteCascadesAndIdempotent：级联子树逐行标记 closed；重复删 / 删不存在节点
// 幂等（OK=true，不建空行）。
func TestTasktreeDeleteCascadesAndIdempotent(t *testing.T) {
	s, scope := newTasktreeEnv(t)
	inst := "ins-tt2"

	upsertNode(t, s, inst, scope, facade.TaskNode{ID: "p1", Kind: "tool", TopSession: "top1"})
	upsertNode(t, s, inst, scope, facade.TaskNode{ID: "c1", Kind: "tool", TopSession: "top1", ParentID: "p1"})
	if _, err := s.TasktreeDelete(facade.TasktreeDeleteRequest{InstanceID: inst, Scope: scope, NodeID: "p1"}); err != nil {
		t.Fatalf("delete p1: %v", err)
	}

	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	for _, id := range []string{"p1", "c1"} {
		var rec data.Record
		if ok, err := prj.Table("tasktree").Get(id, &rec); err != nil || !ok {
			t.Fatalf("get %s: ok=%v err=%v（逻辑删除应保留行）", id, ok, err)
		}
		if v, _ := rec["closed"].(bool); !v {
			t.Fatalf("%s 应已标记 closed", id)
		}
	}

	// 重复删 + 删不存在节点 → 幂等 OK；不存在的节点不得建空行
	for _, id := range []string{"p1", "ghost"} {
		resp, err := s.TasktreeDelete(facade.TasktreeDeleteRequest{InstanceID: inst, Scope: scope, NodeID: id})
		if err != nil || !resp.OK {
			t.Fatalf("delete %s 应幂等成功：err=%v resp=%+v", id, err, resp)
		}
	}
	var rec data.Record
	if ok, _ := prj.Table("tasktree").Get("ghost", &rec); ok {
		t.Fatal("不存在节点不应被建行")
	}
}
