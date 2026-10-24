package tasktree

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestTasktreeDialogRouting：带 dialog_id → 节点读写走对话库，不落 prjusr；
// 关闭 = 逻辑删除同样落在对话库。
func TestTasktreeDialogRouting(t *testing.T) {
	s, scope := newTasktreeEnv(t)
	inst := "ins-tt-dialog"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("PrjUsrFor: %v", err)
	}
	if _, release, err := data.OpenDialog(prj.Path(), "dlg-1"); err != nil {
		t.Fatalf("OpenDialog: %v", err)
	} else {
		release()
	}

	if _, err := s.TasktreeUpsert(facade.TasktreeUpsertRequest{
		InstanceID: inst, Scope: scope, DialogID: "dlg-1",
		Node: facade.TaskNode{ID: "n1", Kind: "llm", TopSession: "top1", Status: "running"},
	}); err != nil {
		t.Fatalf("TasktreeUpsert: %v", err)
	}
	list, err := s.TasktreeList(facade.TasktreeListRequest{
		InstanceID: inst, Scope: scope, DialogID: "dlg-1", TopSession: "top1",
	})
	if err != nil {
		t.Fatalf("TasktreeList: %v", err)
	}
	if len(list.Nodes) != 1 || list.Nodes[0].ID != "n1" {
		t.Fatalf("对话库任务树不符: %+v", list.Nodes)
	}
	if _, err := s.TasktreeDelete(facade.TasktreeDeleteRequest{
		InstanceID: inst, Scope: scope, DialogID: "dlg-1", NodeID: "n1",
	}); err != nil {
		t.Fatalf("TasktreeDelete: %v", err)
	}

	sdb, release, err := data.OpenExistingDialog(prj.Path(), "dlg-1")
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	defer release()
	var rec data.Record
	if ok, _ := sdb.Table("tasktree").Get("n1", &rec); !ok {
		t.Fatal("对话库缺 tasktree 行")
	}
	if rec["closed"] != true {
		t.Fatalf("关闭应标 closed=true: %+v", rec)
	}
	// 不污染 prjusr。
	var old data.Record
	if ok, _ := prj.Table("tasktree").Get("n1", &old); ok {
		t.Fatal("对话模式不应写 prjusr.tasktree")
	}
}

// TestTasktreeTasksAcrossDialogs（P3-4 残留回填）：无 dialog_id 的全实例快照 →
// 跨对话库聚合（每个对话各 1 个节点 → 2 条）。
func TestTasktreeTasksAcrossDialogs(t *testing.T) {
	s, scope := newTasktreeEnv(t)
	inst := "ins-tt-all"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("PrjUsrFor: %v", err)
	}
	for _, dlg := range []string{"dlg-a", "dlg-b"} {
		if _, release, err := data.OpenDialog(prj.Path(), dlg); err != nil {
			t.Fatalf("OpenDialog(%s): %v", dlg, err)
		} else {
			release()
		}
	}
	for _, dlg := range []string{"dlg-a", "dlg-b"} {
		if _, err := s.TasktreeUpsert(facade.TasktreeUpsertRequest{
			InstanceID: inst, Scope: scope, DialogID: dlg,
			Node: facade.TaskNode{ID: "n-" + dlg, Kind: "tool", TopSession: "top-" + dlg, Status: "running"},
		}); err != nil {
			t.Fatalf("TasktreeUpsert(%s): %v", dlg, err)
		}
	}
	all, err := s.TasktreeTasks(facade.TasktreeTasksRequest{InstanceID: inst, Scope: scope})
	if err != nil {
		t.Fatalf("TasktreeTasks: %v", err)
	}
	if len(all.List) != 2 {
		t.Fatalf("全实例快照应跨对话聚合 2 条，实际 %d", len(all.List))
	}
}

// TestTasktreeMissingDialogErrors：带未知 dialog_id → 明确报错（不回退 prjusr）。
func TestTasktreeMissingDialogErrors(t *testing.T) {
	s, scope := newTasktreeEnv(t)
	inst := "ins-tt-miss"
	if _, err := s.TasktreeUpsert(facade.TasktreeUpsertRequest{
		InstanceID: inst, Scope: scope, DialogID: "no-such-dialog",
		Node: facade.TaskNode{ID: "n1", Status: "running"},
	}); err == nil {
		t.Fatal("upsert 未知对话库应报错")
	}
	if _, err := s.TasktreeList(facade.TasktreeListRequest{
		InstanceID: inst, Scope: scope, DialogID: "no-such-dialog", TopSession: "top1",
	}); err == nil {
		t.Fatal("list 未知对话库应报错")
	}
	if _, err := s.TasktreeDelete(facade.TasktreeDeleteRequest{
		InstanceID: inst, Scope: scope, DialogID: "no-such-dialog", NodeID: "n1",
	}); err == nil {
		t.Fatal("delete 未知对话库应报错")
	}
}
