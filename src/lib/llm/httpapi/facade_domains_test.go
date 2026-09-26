// browser 入口（`POST /publish`）的 config 类与第四批五域**门面直调**接线单测
// （阶段 4 第二/四批 / 41 G-34 / G-36）：
//
//   - config 类（prj-config / prompt / prj-security / user-config）与 tasktree / knowledge /
//     filelist / scenario / memory 上行**优先走 data 门面**（服务端进程内直调）→ 总线上**零请求**
//     （消除"config 类仍走总线"这条残留）；
//   - 应答与门面 DTO → `facade/wire` 结果**逐字一致**；
//   - 未注入门面 → 回落总线 persist 路径（行为同改前）。
package httpapi

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// countDomainReqs 统计 config 类与第四批域请求主题上的**请求**条数（应答带 ok 字段 → 排除）。
func countDomainReqs(t *testing.T, bus mq.Bus) *int64 {
	t.Helper()
	var n int64
	for _, subj := range []string{
		"data-prj-config-save", "data-prj-config-list", "data-prj-config-load",
		"data-user-config-save", "data-user-config-load",
		"data-tasktree-list", "data-tasktree-upsert",
		"data-knowledge-list", "data-knowledge-create",
		"data-filelist-list", "data-filelist-put",
		"data-scenario-list", "data-scenario-save",
		"data-memory-list", "data-memory-save",
	} {
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

// eqWire 断言 /publish 应答 result 与门面 DTO → wire 结果逐字一致（JSON 归一后深比较）。
func eqWire(t *testing.T, got map[string]any, want map[string]any, what string) {
	t.Helper()
	if !reflect.DeepEqual(normJSON(t, got), normJSON(t, want)) {
		t.Fatalf("%s 应答与门面 DTO 不一致：\n入口=%+v\n门面=%+v", what, normJSON(t, got), normJSON(t, want))
	}
}

// TestConfigAndDomainsPublishGoesThroughFacade：browser 入口 config 类 + 五域经门面
// （总线上零请求），应答与门面 DTO 逐字一致，数据落点与同进程数据面同源。
func TestConfigAndDomainsPublishGoesThroughFacade(t *testing.T) {
	data.Reset()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	// 同进程数据面（MQ 面 / 落点回读的同一实现）
	svc := persist.New(bus, persist.Options{UsrPath: t.TempDir() + "/usr.db"})
	if err := svc.Start(); err != nil {
		t.Fatalf("persist.Start: %v", err)
	}
	t.Cleanup(svc.Stop)
	t.Cleanup(data.Reset)

	// 入口与数据面**共用同一条总线**（装配期同进程形态），门面 = inline 绑定
	srv, _, base := newTestServerOptsShared(t, bus, Options{
		WorkDir: t.TempDir(),
		DataDir: t.TempDir(),
		Facade:  inline.New(bus),
	})
	inst := srv.InstanceID()
	reqs := countDomainReqs(t, bus)
	res := func(env map[string]any) map[string]any {
		t.Helper()
		r, _ := env["result"].(map[string]any)
		return r
	}

	// ① config 类（kv）：save → 应答 {ok,id}；load 读到；list 平铺
	if m := res(publish(t, base, "data-prj-config-save", `{"data":{"key":"layout.br","value":"300"}}`)); m["ok"] != true || m["id"] != "layout.br" {
		t.Fatalf("prj-config save 应答形状变化：%+v", m)
	}
	if m := res(publish(t, base, "data-prj-config-load", `{"id":"layout.br"}`)); m["data"] != "300" {
		t.Fatalf("prj-config load 应答形状变化：%+v", m)
	}
	kv, err := svc.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: inst, Keys: []string{"layout.br"},
	})
	if err != nil || kv.Values["layout.br"] != "300" {
		t.Fatalf("门面写入未被同进程数据面读到：%+v err=%v", kv.Values, err)
	}

	// ② user-config：save → {ok,id:user_config}；load → {data:{…}}
	if m := res(publish(t, base, "data-user-config-save", `{"data":{"theme":"nord"}}`)); m["ok"] != true || m["id"] != "user_config" {
		t.Fatalf("user-config save 应答形状变化：%+v", m)
	}
	if d, _ := res(publish(t, base, "data-user-config-load", `{}`))["data"].(map[string]any); d["theme"] != "nord" {
		t.Fatalf("user-config load 应答形状变化：%+v", d)
	}

	// ③ tasktree：upsert → {ok:true}；list 与门面 DTO 逐字一致
	if m := res(publish(t, base, "data-tasktree-upsert", `{"data":{"task_id":"n-hx","top_session":"top-hx",`+
		`"kind":"tool","status":"running","created_at":"2026-09-21T10:00:00Z"}}`)); m["ok"] != true {
		t.Fatalf("tasktree upsert 应答形状变化：%+v", m)
	}
	nl, err := svc.TasktreeList(facade.TasktreeListRequest{InstanceID: inst, TopSession: "top-hx"})
	if err != nil || len(nl.Nodes) != 1 {
		t.Fatalf("门面写入未被读到：%+v err=%v", nl, err)
	}
	eqWire(t, res(publish(t, base, "data-tasktree-list", `{"top_session":"top-hx"}`)),
		wire.TasktreeListResult(nl.Nodes), "tasktree list")

	// ④ knowledge：create → {ok,path}；list 与门面 DTO 逐字一致
	if m := res(publish(t, base, "data-knowledge-create", `{"data":{"dir":"","type":"tool","name":"hx_tool"}}`)); m["ok"] != true || m["path"] != "hx_tool.tool.md" {
		t.Fatalf("knowledge create 应答形状变化：%+v", m)
	}
	kl, err := svc.KnowledgeList(facade.KnowledgeListRequest{InstanceID: inst})
	if err != nil || len(kl.Files) != 1 {
		t.Fatalf("门面未读到入口新建文档：%+v err=%v", kl, err)
	}
	eqWire(t, res(publish(t, base, "data-knowledge-list", `{"data":{"dir":""}}`)),
		wire.KnowledgeListResult(kl), "knowledge list")

	// ⑤ filelist：put → {ok,id}；list 与门面 DTO 逐字一致
	if m := res(publish(t, base, "data-filelist-put", `{"data":{"key":"hk1","path":"C:/ws/h.txt","size":3,`+
		`"mtime":"2026-09-21T10:00:00Z","md5":"md5-hx","doc_ids":["d1"],"chunks":1,"indexed_at":"2026-09-21T10:00:01Z"}}`)); m["ok"] != true || m["id"] != "hk1" {
		t.Fatalf("filelist put 应答形状变化：%+v", m)
	}
	fl, err := svc.FileListList(facade.FileListListRequest{InstanceID: inst})
	if err != nil || len(fl.List) != 1 {
		t.Fatalf("门面未读到入口写入：%+v err=%v", fl, err)
	}
	eqWire(t, res(publish(t, base, "data-filelist-list", `{}`)), wire.FileListListResult(fl), "filelist list")

	// ⑥ scenario：save → {ok,id}；list/load 与门面 DTO 逐字一致
	if m := res(publish(t, base, "data-scenario-save", `{"data":{"id":"hs1","name":"入口场景",`+
		`"agents":[{"name":"主","isMain":true,"prompt":"p"}]}}`)); m["ok"] != true || m["id"] != "hs1" {
		t.Fatalf("scenario save 应答形状变化：%+v", m)
	}
	sg, err := svc.ScenarioGet(facade.ScenarioGetRequest{InstanceID: inst, ScenarioID: "hs1"})
	if err != nil || sg.Scenario.Name != "入口场景" {
		t.Fatalf("门面未读到入口写入：%+v err=%v", sg, err)
	}
	eqWire(t, res(publish(t, base, "data-scenario-load", `{"id":"hs1"}`)),
		wire.ScenarioGetResult(sg.Scenario), "scenario load")

	// ⑦ memory：save → {ok,id}；read 与门面 DTO 逐字一致
	if m := res(publish(t, base, "data-memory-save", `{"data":{"category":"入口记忆","content":"hc"}}`)); m["ok"] != true || m["id"] != "入口记忆" {
		t.Fatalf("memory save 应答形状变化：%+v", m)
	}
	mg, err := svc.MemoryGet(facade.MemoryGetRequest{InstanceID: inst, Category: "入口记忆"})
	if err != nil || mg.Doc.Content != "hc" {
		t.Fatalf("门面未读到入口写入：%+v err=%v", mg.Doc, err)
	}
	eqWire(t, res(publish(t, base, "data-memory-read", `{"data":{"category":"入口记忆"}}`)),
		wire.MemoryGetResult(mg.Doc), "memory read")

	// ⑧ 关键断言：config 类 + 五域**一次 MQ 请求都没发**（全部经 data 门面）
	if n := atomic.LoadInt64(reqs); n != 0 {
		t.Fatalf("config/第四批域仍走了总线（请求数=%d）：应全部经 data 门面", n)
	}

	// ⑨ 未注入门面（薄切片）→ 回落总线 persist 路径（与数据面共用同一条总线）
	srv2 := New(bus, Options{WorkDir: t.TempDir(), DataDir: t.TempDir(), Addr: "127.0.0.1:0"})
	if err := srv2.Start(); err != nil {
		t.Fatalf("srv2.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv2.Shutdown(ctx)
	})
	env := publish(t, "http://"+srv2.Addr(), "data-scenario-list", `{"instance_id":"`+inst+`"}`)
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("未接线时应回落总线（persist 在线）：%+v", env)
	}
	if atomic.LoadInt64(reqs) == 0 {
		t.Fatal("未注入门面时未走总线：应保留 MQ 转发路径")
	}
}

// newTestServerOptsShared 起一个真实监听的入口，**复用调用方总线**（与同进程数据面同一条总线）。
func newTestServerOptsShared(t *testing.T, bus mq.Bus, opts Options) (*Server, mq.Bus, string) {
	t.Helper()
	opts.Bus = bus
	opts.Addr = "127.0.0.1:0"
	s := New(bus, opts)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, bus, "http://" + s.Addr()
}
