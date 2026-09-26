// tasktree / knowledge / filelist / scenario / memory 五域门面（inline 绑定）与既有 MQ 面
// （`data-<domain>-*`）的**行为等价**单测（阶段 4 第四批验收 / 41 G-36：一份定义 → 两种绑定，
// 同一落点、同一语义、同一应答形状）。
//
// 两条路径（同一实例、同一数据根）：
//   - 门面 = inline 直调（`chonkpilot-data/facade/inline`；同进程函数调用，不经 MQ，见 23 §7）
//   - MQ   = persist 的 `data-tasktree-*` / `data-knowledge-*` / `data-filelist-*` /
//     `data-scenario-*` / `data-memory-*`（消息面，61 §3；既有驱动方式）
//
// 覆盖：门面写→MQ 读 / MQ 写→门面读 / 应答形状**逐字一致**（同域两路径经同一份 facade/wire
// 翻译 → 经 JSON 归一后深比较）/ 订阅面（task-deleted、data-scenario-refresh、data-memory-refresh）/
// **该域在总线上零请求**（单列用例）/ **G-26 移动作用域校验不被门面绕过**。
//
// 注：inline 绑定与同进程 MQ 面在测试里是**两个 Service 实例**，故用 `inline.NewWithOptions`
// 注入同一 UsrPath / AppDir（生产装配为同一实例，无此差异）。
package persist_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// domainFourSubjects 是第四批五域的全部请求主题（零请求断言用）。
var domainFourSubjects = []string{
	"data-tasktree-list", "data-tasktree-tasks", "data-tasktree-upsert", "data-tasktree-delete",
	"data-knowledge-root", "data-knowledge-list", "data-knowledge-read", "data-knowledge-save",
	"data-knowledge-create", "data-knowledge-delete", "data-knowledge-rename",
	"data-knowledge-mkdir", "data-knowledge-rmdir", "data-knowledge-rename-dir",
	"data-filelist-list", "data-filelist-put", "data-filelist-del",
	"data-scenario-list", "data-scenario-load", "data-scenario-save",
	"data-scenario-delete",
	"data-memory-list", "data-memory-read", "data-memory-save", "data-memory-delete",
}

// countReqs 统计若干主题上的**请求**条数（应答带 ok 字段 → 排除）。
func countReqs(t *testing.T, bus mq.Bus, subjects ...string) *int64 {
	t.Helper()
	var n int64
	for _, subj := range subjects {
		if _, err := bus.On(subj, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var m map[string]any
			if json.Unmarshal(v.Payload, &m) == nil {
				if _, isReply := m["ok"]; isReply {
					return nil
				}
			}
			atomic.AddInt64(&n, 1)
			return nil
		}); err != nil {
			t.Fatalf("subscribe %s: %v", subj, err)
		}
	}
	return &n
}

// collectTopic 订阅单主题并收集载荷（fire-and-forget 事件断言用）。
func collectTopic(t *testing.T, bus mq.Bus, subject string) chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 8)
	if _, err := subRaw(bus, subject, func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) == nil {
			select {
			case ch <- m:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	return ch
}

// waitEvent 等一条事件（超时 = 失败）。
func waitEvent(t *testing.T, ch chan map[string]any, what string) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatalf("未收到 %s 广播（订阅面缺失）", what)
		return nil
	}
}

// mqResult 经 MQ 面取应答 result（payload 自动补 instance_id，与 61 §0 一致）。
func mqResult(t *testing.T, bus mq.Bus, subject string, payload map[string]any) map[string]any {
	t.Helper()
	payload["instance_id"] = facadeInstance
	return dataResult(t, dataCall(t, bus, subject, payload))
}

// eqWire 断言门面侧组装结果与 MQ 回应答**逐字一致**（经 JSON 归一：门面侧是 Go 值、
// MQ 侧是序列化往返后的形状）。
func eqWire(t *testing.T, got map[string]any, want map[string]any, what string) {
	t.Helper()
	if !reflect.DeepEqual(normJSON(t, got), normJSON(t, want)) {
		t.Fatalf("%s 应答不一致：\n门面=%+v\nMQ  =%+v", what, normJSON(t, got), normJSON(t, want))
	}
}

// ── tasktree ─────────────────────────────────────────────────────

// TestFacadeTasktreeInlineEqualsMQPath：任务树域两路径**写读交叉等价** + 应答逐字一致 +
// 关闭（逻辑删除）广播 task-deleted + include_closed 语义一致。
func TestFacadeTasktreeInlineEqualsMQPath(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)
	now := time.Now().UTC().Format(time.RFC3339)

	// ① 门面 upsert（llm 节点）→ MQ list 读到（node_type 由实现侧按 kind 推）
	if _, err := api.TasktreeUpsert(facade.TasktreeUpsertRequest{
		InstanceID: facadeInstance,
		Node: facade.TaskNode{
			ID: "n1", TopSession: "top1", SessionID: "s1", Kind: "llm",
			Title: "根任务", Status: "running", CreatedAt: now, WorkDir: "C:/ws", State: "running",
		},
	}); err != nil {
		t.Fatalf("facade TasktreeUpsert: %v", err)
	}
	lr, err := api.TasktreeList(facade.TasktreeListRequest{InstanceID: facadeInstance, TopSession: "top1"})
	if err != nil || len(lr.Nodes) != 1 || lr.Nodes[0].NodeType != "session" {
		t.Fatalf("facade TasktreeList=%+v err=%v", lr, err)
	}
	eqWire(t, wire.TasktreeListResult(lr.Nodes),
		mqResult(t, bus, "data-tasktree-list", map[string]any{"top_session": "top1"}), "list")

	// ② MQ upsert（tool 子节点，含 tool_call_id + exec_json 待裁决明细 + closed=false）→ 门面读
	closedFalse := false
	mqResult(t, bus, "data-tasktree-upsert", map[string]any{"data": map[string]any{
		"task_id": "n2", "top_session": "top1", "session_id": "s1", "parent_node_id": "n1",
		"kind": "tool", "title": "子工具", "status": "running", "created_at": now,
		"tool_call_id": "tc-n2", "state": "awaiting", "closed": closedFalse,
		"exec_json": `{"phase":"awaiting","detail":{"timeout_s":30,"options":["detach","cancel"]}}`,
	}})
	tr, err := api.TasktreeTasks(facade.TasktreeTasksRequest{InstanceID: facadeInstance, SessionID: "s1"})
	if err != nil || len(tr.List) != 2 {
		t.Fatalf("facade TasktreeTasks=%+v err=%v", tr, err)
	}
	if tr.List[1].ID != "n2" || tr.List[1].ToolCallID != "tc-n2" || tr.List[1].Awaiting == nil {
		t.Fatalf("门面 tasks 未读到 MQ 写入：%+v", tr.List[1])
	}
	eqWire(t, wire.TasktreeTasksResult(tr.List),
		mqResult(t, bus, "data-tasktree-tasks", map[string]any{"session_id": "s1"}), "tasks")
	// init 视图（仅 llm / 运行中存活节点——两个节点都满足）两路径一致
	ir, err := api.TasktreeList(facade.TasktreeListRequest{
		InstanceID: facadeInstance, TopSession: "top1", Mode: "init",
	})
	if err != nil || len(ir.Nodes) != 2 {
		t.Fatalf("facade init list=%+v err=%v", ir, err)
	}
	eqWire(t, wire.TasktreeListResult(ir.Nodes),
		mqResult(t, bus, "data-tasktree-list", map[string]any{"top_session": "top1", "mode": "init"}), "init list")

	// ③ 关闭 = 逻辑删除：门面 delete → MQ 视图过滤 + task-deleted 广播（61 §3.4 载荷）
	ev := collectTopic(t, bus, "task-deleted")
	if _, err := api.TasktreeDelete(facade.TasktreeDeleteRequest{
		InstanceID: facadeInstance, NodeID: "n1",
	}); err != nil {
		t.Fatalf("facade TasktreeDelete: %v", err)
	}
	del := waitEvent(t, ev, "task-deleted")
	if del["instance_id"] != facadeInstance || del["node_id"] != "n1" {
		t.Fatalf("task-deleted 载荷不符：%+v", del)
	}
	if list := dataList(mqResult(t, bus, "data-tasktree-list", map[string]any{"top_session": "top1"})); len(list) != 0 {
		t.Fatalf("关闭后 MQ list 应过滤 closed：%+v", list)
	}
	// include_closed=true → 两路径都返回历史行（级联子树同样标记）
	ar, err := api.TasktreeList(facade.TasktreeListRequest{
		InstanceID: facadeInstance, TopSession: "top1", IncludeClosed: true,
	})
	if err != nil || len(ar.Nodes) != 2 {
		t.Fatalf("门面 include_closed list=%+v err=%v", ar, err)
	}
	eqWire(t, wire.TasktreeListResult(ar.Nodes),
		mqResult(t, bus, "data-tasktree-list", map[string]any{"top_session": "top1", "include_closed": true}),
		"include_closed list")

	// ④ MQ 关闭（幂等）→ 应答形状不变；门面关闭不存在的节点亦幂等成功
	if ok, _ := mqResult(t, bus, "data-tasktree-delete", map[string]any{"node_id": "n1"})["ok"].(bool); !ok {
		t.Fatal("MQ 幂等关闭应答形状变化")
	}
	if _, err := api.TasktreeDelete(facade.TasktreeDeleteRequest{
		InstanceID: facadeInstance, NodeID: "n-ghost",
	}); err != nil {
		t.Fatalf("门面关闭不存在节点应幂等成功：%v", err)
	}
}

// ── knowledge ────────────────────────────────────────────────────

// TestFacadeKnowledgeInlineEqualsMQPath：知识库域两路径等价（root / list / create / read /
// save / mkdir / rename（移动）/ rename-dir / rmdir / delete），且 **G-26 移动作用域校验
// 由实现侧执行**（门面路径不得绕过）。
func TestFacadeKnowledgeInlineEqualsMQPath(t *testing.T) {
	appRoot := t.TempDir()
	bus, _, _ := newTestPersistOpts(t, persist.Options{AppDir: appRoot})
	wd := regInstance(t, bus)
	api := inline.NewWithOptions(bus, persist.Options{AppDir: appRoot})
	projectRoot := filepath.ToSlash(filepath.Join(wd, ".chonkpilot", "capability"))

	// ① root（app / user / project）两路径逐字一致
	for _, kind := range []string{"app", "user", "project"} {
		fr, err := api.KnowledgeRoot(facade.KnowledgeRootRequest{InstanceID: facadeInstance, Kind: kind})
		if err != nil {
			t.Fatalf("facade KnowledgeRoot(%s): %v", kind, err)
		}
		eqWire(t, wire.KnowledgeRootResult(fr),
			mqResult(t, bus, "data-knowledge-root", map[string]any{"data": map[string]any{"kind": kind}}),
			"root("+kind+")")
	}
	if root, _ := api.KnowledgeRoot(facade.KnowledgeRootRequest{InstanceID: facadeInstance, Kind: "project"}); filepath.ToSlash(root.Root) != projectRoot {
		t.Fatalf("project 根 = %q want %q", root.Root, projectRoot)
	}

	// ② 门面 create（app 级 tools 目录）→ MQ read 读到契约模板；create 应答形状 {ok,path}
	toolsDir := filepath.ToSlash(filepath.Join(appRoot, "tools"))
	cr, err := api.KnowledgeCreate(facade.KnowledgeCreateRequest{
		InstanceID: facadeInstance, Dir: toolsDir, Type: "tool", Name: "my_tool",
	})
	if err != nil || !cr.OK {
		t.Fatalf("facade KnowledgeCreate=%+v err=%v", cr, err)
	}
	if cr.Path != filepath.ToSlash(filepath.Join(toolsDir, "my_tool.tool.md")) {
		t.Fatalf("create path=%q", cr.Path)
	}
	read := mqResult(t, bus, "data-knowledge-read", map[string]any{"data": map[string]any{"path": cr.Path}})
	if doc, _ := read["doc"].(map[string]any); doc["title"] != "my_tool" {
		t.Fatalf("MQ read 未读到门面新建文档：%+v", read)
	}

	// ③ MQ save（改描述 + 参数段）→ 门面 read 读到同一份（doc 领域形态 + 原文）；两路径逐字一致
	mqResult(t, bus, "data-knowledge-save", map[string]any{"data": map[string]any{
		"path": cr.Path,
		"doc": map[string]any{
			"title": "my_tool", "description": "改过的描述",
			"parameters": "properties:\n    a:\n        type: string",
			"content":    "正文",
		},
	}})
	rr, err := api.KnowledgeRead(facade.KnowledgeReadRequest{InstanceID: facadeInstance, Path: cr.Path})
	if err != nil || rr.Doc.Description != "改过的描述" || rr.Doc.Content != "正文" {
		t.Fatalf("门面未读到 MQ 写入：%+v err=%v", rr.Doc, err)
	}
	eqWire(t, wire.KnowledgeReadResult(rr),
		mqResult(t, bus, "data-knowledge-read", map[string]any{"data": map[string]any{"path": cr.Path}}), "read")

	// ④ mkdir + list：门面建目录 → MQ list 反映；两路径 list 逐字一致
	mr, err := api.KnowledgeMkdir(facade.KnowledgeMkdirRequest{
		InstanceID: facadeInstance, Parent: appRoot, Name: "sub",
	})
	if err != nil || mr.Path == "" {
		t.Fatalf("facade KnowledgeMkdir=%+v err=%v", mr, err)
	}
	lr, err := api.KnowledgeList(facade.KnowledgeListRequest{InstanceID: facadeInstance, Dir: appRoot})
	if err != nil {
		t.Fatalf("facade KnowledgeList: %v", err)
	}
	eqWire(t, wire.KnowledgeListResult(lr),
		mqResult(t, bus, "data-knowledge-list", map[string]any{"data": map[string]any{"dir": appRoot}}), "list")

	// ⑤ 跨目录移动（G-26）：门面 rename → MQ list 该子目录可见；MQ rename 回原目录 → 门面 list 反映
	if _, err := api.KnowledgeRename(facade.KnowledgeRenameRequest{
		InstanceID: facadeInstance, Path: cr.Path, NewName: "sub/my_tool.tool.md",
	}); err != nil {
		t.Fatalf("facade KnowledgeRename（移动）: %v", err)
	}
	subDir := filepath.ToSlash(filepath.Join(appRoot, "sub"))
	if files, _ := mqResult(t, bus, "data-knowledge-list",
		map[string]any{"data": map[string]any{"dir": subDir}})["files"].([]any); len(files) != 1 {
		t.Fatalf("MQ list 未反映门面移动：%+v", files)
	}
	mqResult(t, bus, "data-knowledge-rename", map[string]any{"data": map[string]any{
		"path": filepath.ToSlash(filepath.Join(subDir, "my_tool.tool.md")), "new_name": "tools/my_tool.tool.md",
	}})
	after, err := api.KnowledgeList(facade.KnowledgeListRequest{InstanceID: facadeInstance, Dir: toolsDir})
	if err != nil || len(after.Files) != 1 || after.Files[0].Name != "my_tool.tool.md" {
		t.Fatalf("MQ 移动后门面 list 未反映：%+v err=%v", after, err)
	}

	// ⑥ G-26 校验不被绕过：越界移动（`..` 逃逸）两路径同报错
	failFacade := errString(t, func() error {
		_, err := api.KnowledgeRename(facade.KnowledgeRenameRequest{
			InstanceID: facadeInstance, Path: cr.Path, NewName: "../../evil.md",
		})
		return err
	})
	failMQ := dataFail(t, dataCall(t, bus, "data-knowledge-rename", map[string]any{
		"instance_id": facadeInstance,
		"data":        map[string]any{"path": cr.Path, "new_name": "../../evil.md"},
	}))
	if failFacade == "" || !strings.Contains(failMQ, "invalid path segment") {
		t.Fatalf("越界移动错误文案不符：门面=%q MQ=%q", failFacade, failMQ)
	}

	// ⑦ rename-dir / rmdir / delete 两路径一致
	if _, err := api.KnowledgeRenameDir(facade.KnowledgeRenameDirRequest{
		InstanceID: facadeInstance, Path: subDir, NewName: "sub2",
	}); err != nil {
		t.Fatalf("facade KnowledgeRenameDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "sub2")); err != nil {
		t.Fatalf("目录改名未生效：%v", err)
	}
	if _, err := api.KnowledgeRmdir(facade.KnowledgeRmdirRequest{
		InstanceID: facadeInstance, Path: filepath.ToSlash(filepath.Join(appRoot, "sub2")),
	}); err != nil {
		t.Fatalf("facade KnowledgeRmdir: %v", err)
	}
	// 门面 save（app 级）→ MQ read 读到
	savedPath := filepath.ToSlash(filepath.Join(toolsDir, "saved.tool.md"))
	if _, err := api.KnowledgeSave(facade.KnowledgeSaveRequest{
		InstanceID: facadeInstance, Path: savedPath,
		Doc: facade.KnowledgeDoc{Title: "saved", Description: "门面写", Content: "x"},
	}); err != nil {
		t.Fatalf("facade KnowledgeSave: %v", err)
	}
	if got := mqResult(t, bus, "data-knowledge-read", map[string]any{
		"data": map[string]any{"path": savedPath},
	}); got["doc"].(map[string]any)["description"] != "门面写" {
		t.Fatalf("MQ 未读到门面 save：%+v", got)
	}
	// MQ delete → 门面 list 不再列出
	if ok, _ := mqResult(t, bus, "data-knowledge-delete",
		map[string]any{"data": map[string]any{"path": savedPath}})["ok"].(bool); !ok {
		t.Fatal("MQ delete 应答形状变化（应 {ok:true}）")
	}
	final, err := api.KnowledgeList(facade.KnowledgeListRequest{InstanceID: facadeInstance, Dir: toolsDir})
	if err != nil || len(final.Files) != 1 {
		t.Fatalf("MQ delete 后门面 list=%+v err=%v", final, err)
	}
}

// errString 取一次调用的错误文案（"" = 无错）。
func errString(t *testing.T, fn func() error) string {
	t.Helper()
	if err := fn(); err != nil {
		return err.Error()
	}
	return ""
}

// ── filelist ─────────────────────────────────────────────────────

// TestFacadeFileListInlineEqualsMQPath：文件清单域两路径等价（put / list（前缀 + 分页）/ del）
// + 应答逐字一致 + 未登记实例同报错。
func TestFacadeFileListInlineEqualsMQPath(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	// ① 门面 put（两条）→ MQ list 读到；两路径 list 逐字一致
	for _, e := range []facade.FileListEntry{
		{Key: "k1", Path: "C:/ws/a.txt", Size: 12, MTime: "2026-09-12T10:00:00Z",
			MD5: "md5-a", DocIDs: []string{"1", "2"}, Chunks: 2, IndexedAt: "2026-09-12T10:00:01Z"},
		{Key: "k2", Path: "C:/ws/sub/b.go", Size: 34, MTime: "2026-09-12T11:00:00Z",
			MD5: "md5-b", DocIDs: []string{"3"}, Chunks: 1, IndexedAt: "2026-09-12T11:00:01Z"},
	} {
		if _, err := api.FileListPut(facade.FileListPutRequest{
			InstanceID: facadeInstance, Entry: e,
		}); err != nil {
			t.Fatalf("facade FileListPut(%s): %v", e.Key, err)
		}
	}
	lr, err := api.FileListList(facade.FileListListRequest{InstanceID: facadeInstance})
	if err != nil || len(lr.List) != 2 || lr.Total != 2 {
		t.Fatalf("facade FileListList=%+v err=%v", lr, err)
	}
	eqWire(t, wire.FileListListResult(lr),
		mqResult(t, bus, "data-filelist-list", map[string]any{}), "list")
	// 前缀过滤 + 分页两路径一致
	pr, err := api.FileListList(facade.FileListListRequest{InstanceID: facadeInstance, Prefix: "C:/ws/sub/"})
	if err != nil || len(pr.List) != 1 || pr.Total != 1 {
		t.Fatalf("facade 前缀过滤 list=%+v err=%v", pr, err)
	}
	eqWire(t, wire.FileListListResult(pr),
		mqResult(t, bus, "data-filelist-list", map[string]any{"data": map[string]any{"prefix": "C:/ws/sub/"}}),
		"前缀过滤 list")
	pg, err := api.FileListList(facade.FileListListRequest{InstanceID: facadeInstance, Offset: 1, Limit: 1})
	if err != nil || len(pg.List) != 1 || pg.List[0].Key != "k2" || pg.Total != 2 {
		t.Fatalf("facade 分页 list=%+v err=%v", pg, err)
	}
	eqWire(t, wire.FileListListResult(pg),
		mqResult(t, bus, "data-filelist-list", map[string]any{"data": map[string]any{"offset": 1, "limit": 1}}),
		"分页 list")

	// ② MQ put（同 key 覆盖）→ 门面 list 读到新 md5；put 应答 {ok,id} 形状不变
	if got := mqResult(t, bus, "data-filelist-put", map[string]any{"data": map[string]any{
		"key": "k1", "path": "C:/ws/a.txt", "size": 20, "mtime": "2026-09-12T12:00:00Z",
		"md5": "md5-a2", "doc_ids": []any{"7"}, "chunks": 1, "indexed_at": "2026-09-12T12:00:01Z",
	}}); got["ok"] != true || got["id"] != "k1" {
		t.Fatalf("MQ put 应答形状变化：%+v", got)
	}
	after, err := api.FileListList(facade.FileListListRequest{InstanceID: facadeInstance, Prefix: "C:/ws/a"})
	if err != nil || len(after.List) != 1 || after.List[0].MD5 != "md5-a2" {
		t.Fatalf("门面未读到 MQ 覆盖：%+v err=%v", after, err)
	}

	// ③ 门面 del（含不存在的键 → 不计入）→ MQ list 读空；MQ del 应答形状 {ok,deleted}
	dr, err := api.FileListDelete(facade.FileListDeleteRequest{
		InstanceID: facadeInstance, Keys: []string{"k1", "k2", "k-ghost"},
	})
	if err != nil || dr.Deleted != 2 {
		t.Fatalf("facade FileListDelete=%+v err=%v", dr, err)
	}
	if got := mqResult(t, bus, "data-filelist-list", map[string]any{}); len(dataList(got)) != 0 {
		t.Fatalf("门面删除后 MQ 仍读到：%+v", got)
	}
	if got := mqResult(t, bus, "data-filelist-del", map[string]any{
		"data": map[string]any{"keys": []any{"k-ghost"}},
	}); got["ok"] != true || got["deleted"].(float64) != 0 {
		t.Fatalf("MQ del 应答形状变化：%+v", got)
	}

	// ④ 未登记实例：两路径同报错（不静默）
	if _, err := api.FileListList(facade.FileListListRequest{InstanceID: "ins-ghost"}); err == nil {
		t.Fatal("未登记实例应报错")
	}
	if ok, _ := dataCall(t, bus, "data-filelist-list", map[string]any{"instance_id": "ins-ghost"})["ok"].(bool); ok {
		t.Fatal("MQ 未登记实例应失败")
	}
}

// ── scenario ─────────────────────────────────────────────────────

// TestFacadeScenarioInlineEqualsMQPath：场景域两路径等价（list（命中 app 级出厂默认）/ load / save /
// delete）+ 应答逐字一致 + 写入广播 data-scenario-refresh。
func TestFacadeScenarioInlineEqualsMQPath(t *testing.T) {
	appDir := appCapabilityRoot(t)
	bus, _, usrPath := newTestPersistOpts(t, persist.Options{AppDir: appDir})
	regInstance(t, bus)
	api := inline.NewWithOptions(bus, persist.Options{UsrPath: usrPath, AppDir: appDir})

	// ① list：门面列举命中 **app 级**出厂默认场景（25 §8.2 T6；不再物化到 user 级）；两路径逐字一致
	lr, err := api.ScenarioList(facade.ScenarioListRequest{InstanceID: facadeInstance})
	if err != nil || len(lr.List) != 1 || lr.List[0].ID != "default" ||
		lr.List[0].Name != "开发场景" || lr.List[0].Level != "app" {
		t.Fatalf("facade ScenarioList=%+v err=%v", lr, err)
	}
	if lr.List[0].SystemPrompt == "" {
		t.Fatalf("默认场景 systemPrompt 应派生自主 agent（保留供兼容字段）：%+v", lr.List[0])
	}
	eqWire(t, wire.ScenarioListResult(lr.List),
		mqResult(t, bus, "data-scenario-list", map[string]any{}), "list")

	// ② 门面 save（主 + 子 agent）→ MQ load 读到；save 广播 data-scenario-refresh
	refresh := collectTopic(t, bus, "data-scenario-refresh")
	sr, err := api.ScenarioSave(facade.ScenarioSaveRequest{InstanceID: facadeInstance, Scenario: facade.Scenario{
		ID: "s2", Name: "场景2", Description: "描述2",
		Agents: []facade.ScenarioAgent{
			{Name: "主", IsMain: true, Prompt: "主提示"},
			{Name: "子A", RoleTag: "A", Description: "子A描述", Prompt: "A提示"},
		},
	}})
	if err != nil || sr.ID != "s2" {
		t.Fatalf("facade ScenarioSave=%+v err=%v", sr, err)
	}
	if ev := waitEvent(t, refresh, "data-scenario-refresh"); ev["id"] != "s2" || ev["op"] != "save" {
		t.Fatalf("scenario 刷新广播字段不符：%+v", ev)
	}
	gr, err := api.ScenarioGet(facade.ScenarioGetRequest{InstanceID: facadeInstance, ScenarioID: "s2"})
	if err != nil || gr.Scenario.Name != "场景2" || len(gr.Scenario.Agents) != 2 || !gr.Scenario.Agents[0].IsMain {
		t.Fatalf("facade ScenarioGet=%+v err=%v", gr, err)
	}
	eqWire(t, wire.ScenarioGetResult(gr.Scenario),
		mqResult(t, bus, "data-scenario-load", map[string]any{"id": "s2"}), "load")

	// ③ MQ save（旧形态：只给 systemPrompt）→ 门面 load 读到派生主 agent；两路径一致
	mqResult(t, bus, "data-scenario-save", map[string]any{"data": map[string]any{
		"id": "legacy", "name": "旧形态", "systemPrompt": "旧系统提示",
	}})
	lg, err := api.ScenarioGet(facade.ScenarioGetRequest{InstanceID: facadeInstance, ScenarioID: "legacy"})
	if err != nil || lg.Scenario.SystemPrompt != "旧系统提示" || len(lg.Scenario.Agents) == 0 {
		t.Fatalf("门面未读到 MQ 写入（旧形态归一）：%+v err=%v", lg.Scenario, err)
	}
	eqWire(t, wire.ScenarioGetResult(lg.Scenario),
		mqResult(t, bus, "data-scenario-load", map[string]any{"id": "legacy"}), "load(legacy)")

	// ④ 门面 delete → MQ list 少一条；MQ delete → 门面 list 少一条
	if _, err := api.ScenarioDelete(facade.ScenarioDeleteRequest{
		InstanceID: facadeInstance, ScenarioID: "legacy",
	}); err != nil {
		t.Fatalf("facade ScenarioDelete: %v", err)
	}
	if got := mqResult(t, bus, "data-scenario-list", map[string]any{}); len(dataList(got)) != 2 {
		t.Fatalf("门面删除后 MQ list=%+v", got)
	}
	if ok, _ := mqResult(t, bus, "data-scenario-delete", map[string]any{"id": "s2"})["ok"].(bool); !ok {
		t.Fatal("MQ delete 应答形状变化")
	}
	if after, err := api.ScenarioList(facade.ScenarioListRequest{InstanceID: facadeInstance}); err != nil ||
		len(after.List) != 1 {
		t.Fatalf("MQ 删除后门面 list=%+v err=%v", after, err)
	}
}

// ── memory ───────────────────────────────────────────────────────

// TestFacadeMemoryInlineEqualsMQPath：记忆库域两路径等价（list（预置）/ read / save / delete）
// + 应答逐字一致 + save 广播 data-memory-refresh + 预置类别不可删（两路径同文案）。
func TestFacadeMemoryInlineEqualsMQPath(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	regInstance(t, bus)
	api := inline.NewWithOptions(bus, persist.Options{UsrPath: usrPath})
	enableMemory(t, bus) // 预置类别文件仅启用时落盘（P0-B）

	// ① list：门面首次列举（预置 8 项目级 + 用户偏好）→ 两路径逐字一致
	lr, err := api.MemoryList(facade.MemoryListRequest{InstanceID: facadeInstance})
	if err != nil || len(lr.List) != len(memoryProjectCategories)+1 {
		t.Fatalf("facade MemoryList=%+v err=%v", lr, err)
	}
	eqWire(t, wire.MemoryListResult(lr.List),
		mqResult(t, bus, "data-memory-list", map[string]any{}), "list")

	// ② 门面 read（预置类别）→ 两路径一致（含 tokens 口径）
	gr, err := api.MemoryGet(facade.MemoryGetRequest{InstanceID: facadeInstance, Category: "项目概要"})
	if err != nil || gr.Doc.Level != "project" || gr.Doc.Tokens == 0 {
		t.Fatalf("facade MemoryGet=%+v err=%v", gr.Doc, err)
	}
	eqWire(t, wire.MemoryGetResult(gr.Doc),
		mqResult(t, bus, "data-memory-read", map[string]any{"data": map[string]any{"category": "项目概要"}}),
		"read")

	// ③ 门面 save（新建自定义类别）→ MQ 读到（清单 + 全文）；save 广播 data-memory-refresh
	refresh := collectTopic(t, bus, "data-memory-refresh")
	sr, err := api.MemorySave(facade.MemorySaveRequest{
		InstanceID: facadeInstance, Category: "自定义", Content: "自定义内容",
	})
	if err != nil || sr.ID != "自定义" {
		t.Fatalf("facade MemorySave=%+v err=%v", sr, err)
	}
	if ev := waitEvent(t, refresh, "data-memory-refresh"); ev["id"] != "自定义" || ev["op"] != "save" {
		t.Fatalf("memory 刷新广播字段不符：%+v", ev)
	}
	if got := mqResult(t, bus, "data-memory-read", map[string]any{
		"data": map[string]any{"category": "自定义"},
	}); got["data"].(map[string]any)["content"] != "自定义内容" {
		t.Fatalf("MQ 未读到门面写入：%+v", got)
	}
	// 用户偏好（唯一用户级）落在 usr 主库同级目录
	if _, err := os.Stat(filepath.Join(filepath.Dir(usrPath), "用户偏好.md")); err != nil {
		t.Fatalf("用户级偏好文件缺失：%v", err)
	}

	// ④ MQ save（改预置类别全文）→ 门面 read 读到同一份；两路径一致
	mqResult(t, bus, "data-memory-save", map[string]any{"data": map[string]any{
		"category": "项目概要", "content": "由 MQ 改写",
	}})
	back, err := api.MemoryGet(facade.MemoryGetRequest{InstanceID: facadeInstance, Category: "项目概要"})
	if err != nil || back.Doc.Content != "由 MQ 改写" {
		t.Fatalf("门面未读到 MQ 写入：%+v err=%v", back.Doc, err)
	}
	eqWire(t, wire.MemoryGetResult(back.Doc),
		mqResult(t, bus, "data-memory-read", map[string]any{"data": map[string]any{"category": "项目概要"}}),
		"read(MQ 写)")

	// ⑤ 删自定义类别（门面）→ MQ 读报 unknown；预置类别不可删（两路径同文案）；非法类别名两路径同拒
	if dr, err := api.MemoryDelete(facade.MemoryDeleteRequest{
		InstanceID: facadeInstance, Category: "自定义",
	}); err != nil || dr.ID != "自定义" {
		t.Fatalf("facade MemoryDelete=%+v err=%v", dr, err)
	}
	if ok, _ := dataCall(t, bus, "data-memory-read", map[string]any{
		"instance_id": facadeInstance, "data": map[string]any{"category": "自定义"},
	})["ok"].(bool); ok {
		t.Fatal("删除后 MQ 仍读到自定义类别")
	}
	if _, err := api.MemoryDelete(facade.MemoryDeleteRequest{
		InstanceID: facadeInstance, Category: "项目概要",
	}); err == nil {
		t.Fatal("预置类别应不可删（领域规则）")
	}
	presetErr := dataFail(t, dataCall(t, bus, "data-memory-delete", map[string]any{
		"instance_id": facadeInstance, "data": map[string]any{"category": "项目概要"},
	}))
	if !strings.Contains(presetErr, "preset memory category cannot be deleted") {
		t.Fatalf("预置类别删除错误文案不符：%q", presetErr)
	}
	if _, err := api.MemorySave(facade.MemorySaveRequest{
		InstanceID: facadeInstance, Category: "bad/name", Content: "x",
	}); err == nil {
		t.Fatal("非法类别名应被拒")
	}
	if ok, _ := dataCall(t, bus, "data-memory-save", map[string]any{
		"instance_id": facadeInstance, "data": map[string]any{"category": "bad/name", "content": "x"},
	})["ok"].(bool); ok {
		t.Fatal("MQ 非法类别名应失败")
	}
}

// TestFacadeDomainsFourZeroBusRequests：**五域在总线上零请求**——门面（inline 绑定）全量动作
// 直调实现，不在 `data-<domain>-*` 上发任何请求（订阅面广播是 fire-and-forget 事件，不计数）。
func TestFacadeDomainsFourZeroBusRequests(t *testing.T) {
	// app 级根须含出厂默认场景（`<AppDir 的父>/scenarios/default`）—— 场景域 restore 依赖它
	// （app 级随发布只读资源，25 §8.2 T6；无「代码内嵌默认场景」可回落）。
	appRoot := appCapabilityRoot(t)
	bus, _, usrPath := newTestPersistOpts(t, persist.Options{AppDir: appRoot})
	regInstance(t, bus)
	api := inline.NewWithOptions(bus, persist.Options{AppDir: appRoot, UsrPath: usrPath})
	reqs := countReqs(t, bus, domainFourSubjects...)
	now := time.Now().UTC().Format(time.RFC3339)

	// tasktree
	if _, err := api.TasktreeUpsert(facade.TasktreeUpsertRequest{InstanceID: facadeInstance,
		Node: facade.TaskNode{ID: "n1", TopSession: "top1", Kind: "tool", Status: "running", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.TasktreeList(facade.TasktreeListRequest{InstanceID: facadeInstance, TopSession: "top1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.TasktreeTasks(facade.TasktreeTasksRequest{InstanceID: facadeInstance, TopSession: "top1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.TasktreeDelete(facade.TasktreeDeleteRequest{InstanceID: facadeInstance, NodeID: "n1"}); err != nil {
		t.Fatal(err)
	}
	// knowledge
	if _, err := api.KnowledgeRoot(facade.KnowledgeRootRequest{InstanceID: facadeInstance, Kind: "app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.KnowledgeList(facade.KnowledgeListRequest{InstanceID: facadeInstance, Dir: appRoot}); err != nil {
		t.Fatal(err)
	}
	toolsDir := filepath.ToSlash(filepath.Join(appRoot, "tools"))
	if _, err := api.KnowledgeCreate(facade.KnowledgeCreateRequest{
		InstanceID: facadeInstance, Dir: toolsDir, Type: "tool", Name: "zero",
	}); err != nil {
		t.Fatal(err)
	}
	zeroPath := filepath.ToSlash(filepath.Join(toolsDir, "zero.tool.md"))
	if _, err := api.KnowledgeRead(facade.KnowledgeReadRequest{InstanceID: facadeInstance, Path: zeroPath}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.KnowledgeSave(facade.KnowledgeSaveRequest{InstanceID: facadeInstance, Path: zeroPath,
		Doc: facade.KnowledgeDoc{Title: "zero", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.KnowledgeMkdir(facade.KnowledgeMkdirRequest{InstanceID: facadeInstance, Parent: appRoot, Name: "zd"}); err != nil {
		t.Fatal(err)
	}
	zd := filepath.ToSlash(filepath.Join(appRoot, "zd"))
	if _, err := api.KnowledgeRenameDir(facade.KnowledgeRenameDirRequest{
		InstanceID: facadeInstance, Path: zd, NewName: "zd2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.KnowledgeRename(facade.KnowledgeRenameRequest{
		InstanceID: facadeInstance, Path: zeroPath, NewName: "zd2/zero.tool.md",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.KnowledgeRmdir(facade.KnowledgeRmdirRequest{
		InstanceID: facadeInstance, Path: filepath.ToSlash(filepath.Join(appRoot, "zd2")),
	}); err != nil {
		t.Fatal(err)
	}
	// filelist
	if _, err := api.FileListPut(facade.FileListPutRequest{InstanceID: facadeInstance,
		Entry: facade.FileListEntry{Key: "kz", Path: "C:/ws/z.txt"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.FileListList(facade.FileListListRequest{InstanceID: facadeInstance}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.FileListDelete(facade.FileListDeleteRequest{
		InstanceID: facadeInstance, Keys: []string{"kz"},
	}); err != nil {
		t.Fatal(err)
	}
	// scenario
	if _, err := api.ScenarioList(facade.ScenarioListRequest{InstanceID: facadeInstance}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ScenarioSave(facade.ScenarioSaveRequest{InstanceID: facadeInstance,
		Scenario: facade.Scenario{ID: "sz", Name: "零请求"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ScenarioGet(facade.ScenarioGetRequest{InstanceID: facadeInstance, ScenarioID: "sz"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ScenarioDelete(facade.ScenarioDeleteRequest{InstanceID: facadeInstance, ScenarioID: "sz"}); err != nil {
		t.Fatal(err)
	}
	// memory（总开关打开：经 MQ 面，非本域请求）
	enableMemory(t, bus)
	if _, err := api.MemoryList(facade.MemoryListRequest{InstanceID: facadeInstance}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.MemoryGet(facade.MemoryGetRequest{InstanceID: facadeInstance, Category: "项目概要"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.MemorySave(facade.MemorySaveRequest{
		InstanceID: facadeInstance, Category: "零请求", Content: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.MemoryDelete(facade.MemoryDeleteRequest{
		InstanceID: facadeInstance, Category: "零请求",
	}); err != nil {
		t.Fatal(err)
	}
	// knowledge delete（收尾：先建再删）
	delPath := filepath.ToSlash(filepath.Join(toolsDir, "del.tool.md"))
	if _, err := api.KnowledgeCreate(facade.KnowledgeCreateRequest{
		InstanceID: facadeInstance, Dir: toolsDir, Type: "tool", Name: "del",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.KnowledgeDelete(facade.KnowledgeDeleteRequest{
		InstanceID: facadeInstance, Path: delPath,
	}); err != nil {
		t.Fatalf("facade KnowledgeDelete: %v", err)
	}

	// 关键断言：五域**一次 MQ 请求都没发**（enableMemory 走的 prj-config 属 config 域，不计入）
	if n := atomic.LoadInt64(reqs); n != 0 {
		t.Fatalf("第四批域仍走了总线（请求数=%d）：应全部经 data 门面", n)
	}
}
