// 桥第四批域（tasktree / knowledge / filelist / scenario / memory）data-* 走 **data 门面** 的
// 接线与行为等价单测（阶段 4 第四批 / 41 G-36）：
//
//   - **不再"再发一条 MQ 给 persist"**：注入门面后，这些域的 `data-<domain>-*` 在**总线上不出现**
//     请求（计数器断言 = 0），而数据操作**确实生效**（经同进程 persist 服务回读同一落点）；
//   - **前端消息面一字不变**：应答载荷与「同一份 DTO 经 facade/wire 生成的结果」深比较相等；
//   - **未注入门面**（-no-server 薄客户端/分离形态）→ 回落总线转发（行为同改前）。
package bridge

import (
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// deepNorm 经 JSON 归一后深比较（门面侧是 Go 结构体、桥侧是序列化往返后的形状）。
func deepNorm(t *testing.T, got, want any) bool {
	t.Helper()
	return reflect.DeepEqual(normJSON(t, got), normJSON(t, want))
}

// TestDomainsDataGoesThroughFacade：五域 data-* 由桥走门面（总线上零请求），数据操作与
// 同进程数据面**同一落点**；应答载荷与门面 DTO → wire 结果逐字一致。
func TestDomainsDataGoesThroughFacade(t *testing.T) {
	br, svc, bus, instanceID := newFacadeBridgeEnv(t)
	reqs := countSubjects(t, bus,
		"data-tasktree-list", "data-tasktree-upsert",
		"data-knowledge-list", "data-knowledge-create",
		"data-filelist-list", "data-filelist-put",
		"data-scenario-list", "data-scenario-save", "data-scenario-load",
		"data-memory-list", "data-memory-save", "data-memory-read",
	)

	// ① tasktree：桥 upsert（链路 {ok:true}）→ 门面读到；list 应答与门面 DTO 逐字一致
	res, errs := br.PublishEvent("data-tasktree-upsert",
		`{"data":{"task_id":"n-br","top_session":"top-br","kind":"tool","status":"running","created_at":"2026-09-21T10:00:00Z"}}`)
	if len(errs) != 0 {
		t.Fatalf("tasktree upsert errs=%v", errs)
	}
	if !deepNorm(t, res, wire.OKResult()) {
		t.Fatalf("tasktree upsert 应答形状变化：%+v", res)
	}
	nl, err := svc.TasktreeList(facade.TasktreeListRequest{InstanceID: instanceID, TopSession: "top-br"})
	if err != nil || len(nl.Nodes) != 1 {
		t.Fatalf("门面未读到桥写入：%+v err=%v", nl, err)
	}
	res, _ = br.PublishEvent("data-tasktree-list", `{"top_session":"top-br"}`)
	if !deepNorm(t, res, wire.TasktreeListResult(nl.Nodes)) {
		t.Fatalf("tasktree list 应答与门面 DTO 不一致：%+v", res)
	}

	// ② knowledge：桥 create（项目级）→ 门面读到；list 应答与门面 DTO 逐字一致
	res, errs = br.PublishEvent("data-knowledge-create",
		`{"data":{"dir":"","type":"tool","name":"br_tool"}}`)
	if len(errs) != 0 {
		t.Fatalf("knowledge create errs=%v", errs)
	}
	cr, ok := res.(map[string]any)
	if !ok || cr["ok"] != true || cr["path"] != "br_tool.tool.md" {
		t.Fatalf("knowledge create 应答形状变化：%+v", res)
	}
	kl, err := svc.KnowledgeList(facade.KnowledgeListRequest{InstanceID: instanceID})
	if err != nil || len(kl.Files) != 1 || kl.Files[0].Name != "br_tool.tool.md" {
		t.Fatalf("门面未读到桥新建文档：%+v err=%v", kl, err)
	}
	res, _ = br.PublishEvent("data-knowledge-list", `{"data":{"dir":""}}`)
	if !deepNorm(t, res, wire.KnowledgeListResult(kl)) {
		t.Fatalf("knowledge list 应答与门面 DTO 不一致：%+v", res)
	}

	// ③ filelist：桥 put → 门面读到；list 应答与门面 DTO 逐字一致
	res, errs = br.PublishEvent("data-filelist-put", `{"data":{"key":"bk1","path":"C:/ws/x.txt","size":7,`+
		`"mtime":"2026-09-21T10:00:00Z","md5":"md5-br","doc_ids":["d1"],"chunks":1,"indexed_at":"2026-09-21T10:00:01Z"}}`)
	if len(errs) != 0 {
		t.Fatalf("filelist put errs=%v", errs)
	}
	if m, _ := res.(map[string]any); m["ok"] != true || m["id"] != "bk1" {
		t.Fatalf("filelist put 应答形状变化：%+v", res)
	}
	fl, err := svc.FileListList(facade.FileListListRequest{InstanceID: instanceID})
	if err != nil || len(fl.List) != 1 || fl.List[0].MD5 != "md5-br" {
		t.Fatalf("门面未读到桥写入：%+v err=%v", fl, err)
	}
	res, _ = br.PublishEvent("data-filelist-list", `{}`)
	if !deepNorm(t, res, wire.FileListListResult(fl)) {
		t.Fatalf("filelist list 应答与门面 DTO 不一致：%+v", res)
	}

	// ④ scenario：桥 save/load → 门面读到；load 应答与门面 DTO 逐字一致
	res, errs = br.PublishEvent("data-scenario-save",
		`{"data":{"id":"bs1","name":"桥场景","agents":[{"name":"主","isMain":true,"prompt":"p"}]}}`)
	if len(errs) != 0 {
		t.Fatalf("scenario save errs=%v", errs)
	}
	if m, _ := res.(map[string]any); m["ok"] != true || m["id"] != "bs1" {
		t.Fatalf("scenario save 应答形状变化：%+v", res)
	}
	sg, err := svc.ScenarioGet(facade.ScenarioGetRequest{InstanceID: instanceID, ScenarioID: "bs1"})
	if err != nil || sg.Scenario.Name != "桥场景" {
		t.Fatalf("门面未读到桥写入：%+v err=%v", sg, err)
	}
	res, _ = br.PublishEvent("data-scenario-load", `{"id":"bs1"}`)
	if !deepNorm(t, res, wire.ScenarioGetResult(sg.Scenario)) {
		t.Fatalf("scenario load 应答与门面 DTO 不一致：%+v", res)
	}
	res, _ = br.PublishEvent("data-scenario-list", `{}`)
	sl, err := svc.ScenarioList(facade.ScenarioListRequest{InstanceID: instanceID})
	if err != nil || !deepNorm(t, res, wire.ScenarioListResult(sl.List)) {
		t.Fatalf("scenario list 应答与门面 DTO 不一致：%+v err=%v", res, err)
	}

	// ⑤ memory：桥 save/read → 门面读到；read 应答与门面 DTO 逐字一致
	res, errs = br.PublishEvent("data-memory-save", `{"data":{"category":"桥记忆","content":"桥内容"}}`)
	if len(errs) != 0 {
		t.Fatalf("memory save errs=%v", errs)
	}
	if m, _ := res.(map[string]any); m["ok"] != true || m["id"] != "桥记忆" {
		t.Fatalf("memory save 应答形状变化：%+v", res)
	}
	mg, err := svc.MemoryGet(facade.MemoryGetRequest{InstanceID: instanceID, Category: "桥记忆"})
	if err != nil || mg.Doc.Content != "桥内容" {
		t.Fatalf("门面未读到桥写入：%+v err=%v", mg.Doc, err)
	}
	res, _ = br.PublishEvent("data-memory-read", `{"data":{"category":"桥记忆"}}`)
	if !deepNorm(t, res, wire.MemoryGetResult(mg.Doc)) {
		t.Fatalf("memory read 应答与门面 DTO 不一致：%+v", res)
	}
	res, _ = br.PublishEvent("data-memory-list", `{}`)
	ml, err := svc.MemoryList(facade.MemoryListRequest{InstanceID: instanceID})
	if err != nil || !deepNorm(t, res, wire.MemoryListResult(ml.List)) {
		t.Fatalf("memory list 应答与门面 DTO 不一致：%+v err=%v", res, err)
	}

	// ⑥ 关键断言：五域**一次 MQ 请求都没发**（全部经 data 门面）
	if n := atomic.LoadInt64(reqs); n != 0 {
		t.Fatalf("第四批域仍走了总线（请求数=%d）：应全部经 data 门面", n)
	}
}

// TestNoFacadeDomainsFallBackToMQ：未注入门面（-no-server 薄客户端/分离形态）→ 五域仍走总线。
func TestNoFacadeDomainsFallBackToMQ(t *testing.T) {
	br, _, bus, instanceID := newFacadeBridgeEnv(t)
	br.SetFacade(nil) // 模拟未接线
	reqs := countSubjects(t, bus, "data-tasktree-list", "data-scenario-list", "data-memory-list")

	for _, req := range []struct{ typ, payload string }{
		{"data-tasktree-list", `{"top_session":"top-fb"}`},
		{"data-scenario-list", `{}`},
		{"data-memory-list", `{}`},
	} {
		res, errs := br.PublishEvent(req.typ, req.payload)
		if len(errs) > 0 {
			t.Fatalf("%s 未接线时应回落总线（persist 在线）：errs=%v", req.typ, errs)
		}
		if res == nil {
			t.Fatalf("%s 回落路径应答为空", req.typ)
		}
	}
	if n := atomic.LoadInt64(reqs); n == 0 {
		t.Fatal("未注入门面时未走总线：应保留 MQ 转发路径")
	}
	_ = instanceID
}
