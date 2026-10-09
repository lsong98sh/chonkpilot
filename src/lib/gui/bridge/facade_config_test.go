// 桥 config 类 data-* 走 **data 门面** 的接线与行为等价单测（阶段 4 第二批 / 41 G-34）：
//
//   - **不再"再发一条 MQ 给 persist"**：注入门面后，config 类 data-* 在**总线上不出现**请求
//     （计数器断言 = 0），而数据操作**确实生效**（经 MQ 面/persist 直读同一份落点回读）；
//   - **前端消息面一字不变**：`PublishEvent` 的入参（主题名 + payload）与返回的应答载荷键名
//     与 MQ 路径逐字一致（list / data / ok / id）；
//   - **未注入门面**（-no-server 薄客户端/分离形态）→ 回落总线转发（非 config 类域同样走总线）。
package bridge

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// facadeGetPrjReq 构造 prj-config 单键读请求（经 MQ 面/persist 直读同一落点用）。
func facadeGetPrjReq(instanceID, key string) facade.ConfigKVGetRequest {
	return facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Keys: []string{key},
	}
}

// newFacadeBridgeEnv 建桥测试环境：临时 usr 库的 persist（MQ 面）+ 同总线的 inline 门面。
// 返回值 = 桥（已注入门面）、persist 服务（MQ 面回读断言用）、总线。
func newFacadeBridgeEnv(t *testing.T) (*Bridge, *persist.Service, mq.Bus, string) {
	t.Helper()
	data.Reset()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	svc := persist.New(bus, persist.Options{UsrPath: t.TempDir() + "/usr.db"})
	if err := svc.Start(); err != nil {
		t.Fatalf("persist.Start: %v", err)
	}
	t.Cleanup(svc.Stop)
	t.Cleanup(data.Reset)

	const instanceID = "ins-br"
	wd := t.TempDir()
	body, _ := json.Marshal(map[string]any{
		"instance_id": instanceID, "work_dir": wd, "data_dir": "",
	})
	if err := bus.Emit(context.Background(), "instance-register", body).Wait().Err(); err != nil {
		t.Fatalf("instance-register: %v", err)
	}
	data.Register(instanceID, wd, "")
	time.Sleep(20 * time.Millisecond) // 等 persist 自持实例视图登记

	br := New(instanceID, wd, "", nil, bus)
	br.SetFacade(inline.New(bus))
	return br, svc, bus, instanceID
}

// countSubjects 统计若干主题上的**请求**条数（应答带 ok 字段 → 排除）。
func countSubjects(t *testing.T, bus mq.Bus, subjects ...string) *int64 {
	t.Helper()
	var n int64
	for _, s := range subjects {
		if _, err := bus.On(s, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var m map[string]any
			if json.Unmarshal(v.Payload, &m) == nil {
				if _, isReply := m["ok"]; isReply {
					return nil
				}
			}
			atomic.AddInt64(&n, 1)
			return nil
		}); err != nil {
			t.Fatalf("subscribe %s: %v", s, err)
		}
	}
	return &n
}

// TestConfigDataGoesThroughFacade：config 类 data-* 由桥走门面（总线上零请求），
// 且数据操作与 MQ 面**同一落点**（persist 回读一致）；应答 payload 键名与 MQ 路径一致。
func TestConfigDataGoesThroughFacade(t *testing.T) {
	br, svc, bus, instanceID := newFacadeBridgeEnv(t)
	reqs := countSubjects(t, bus,
		"data-prj-config-save", "data-prj-config-load", "data-prj-config-list", "data-prj-config-delete",
		"data-user-config-save", "data-user-config-load",
	)

	// ① save：应答 = {ok:true, id:<key>}（与 MQ 路径逐字一致）
	res, errs := br.PublishEvent("data-prj-config-save",
		`{"data":{"key":"layout.sidebar","value":"280"}}`)
	if len(errs) != 0 {
		t.Fatalf("save errs=%v", errs)
	}
	m, _ := res.(map[string]any)
	if m["ok"] != true || m["id"] != "layout.sidebar" {
		t.Fatalf("save 应答形状变化：%+v", res)
	}
	// 落点断言：同一实例经 MQ 面（persist）读回同一份
	got, err := svc.ConfigKVGet(facadeGetPrjReq(instanceID, "layout.sidebar"))
	if err != nil || got.Values["layout.sidebar"] != "280" {
		t.Fatalf("门面写入未被 MQ 面读到：%+v err=%v", got.Values, err)
	}

	// ①b 批量写（2026-09-28，61 §3.1）：报文 {data:{entries}} → {ok:true,id:首键}；两键都经门面落库
	res, errs = br.PublishEvent("data-prj-config-save", `{"data":{"entries":{"e.b":"2","e.a":"1"}}}`)
	if len(errs) != 0 {
		t.Fatalf("batch save errs=%v", errs)
	}
	m, _ = res.(map[string]any)
	if m["ok"] != true || m["id"] != "e.a" {
		t.Fatalf("batch save 应答形状变化（应 {ok:true,id:e.a}）：%+v", res)
	}
	got2, err := svc.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Keys: []string{"e.a", "e.b"},
	})
	if err != nil || got2.Values["e.a"] != "1" || got2.Values["e.b"] != "2" {
		t.Fatalf("batch save 未落库：%+v err=%v", got2.Values, err)
	}

	// ② load：应答 = {data:<字符串>}
	res, _ = br.PublishEvent("data-prj-config-load", `{"id":"layout.sidebar"}`)
	m, _ = res.(map[string]any)
	if m["data"] != "280" {
		t.Fatalf("load 应答形状变化（应 {data:\"280\"}）：%+v", res)
	}

	// ③ list：应答 = {list:{...}}（平铺键值）
	res, _ = br.PublishEvent("data-prj-config-list", `{}`)
	m, _ = res.(map[string]any)
	list := asAnyMap(m["list"])
	if list == nil || list["layout.sidebar"] != "280" {
		t.Fatalf("list 应答形状变化（应 {list:{...}}）：%+v", res)
	}

	// ④ user-config：save/load 应答形状不变（{ok:true,id:user_config} / {data:{...}}）
	res, _ = br.PublishEvent("data-user-config-save", `{"data":{"theme":"nord"}}`)
	m, _ = res.(map[string]any)
	if m["ok"] != true || m["id"] != "user_config" {
		t.Fatalf("user-config save 应答形状变化：%+v", res)
	}
	res, _ = br.PublishEvent("data-user-config-load", `{}`)
	m, _ = res.(map[string]any)
	cfg, _ := m["data"].(map[string]any)
	if cfg["theme"] != "nord" {
		t.Fatalf("user-config load 应答形状变化/取值不符：%+v", res)
	}

	// ⑤ 关键断言：config 类**一次 MQ 请求都没发**（不再"再发一条 MQ 给 persist"）
	if n := atomic.LoadInt64(reqs); n != 0 {
		t.Fatalf("config 类仍走了总线（请求数=%d）：应全部经 data 门面", n)
	}

	// ⑥ delete：应答 = {ok:true}；删除后 MQ 面读为空
	res, _ = br.PublishEvent("data-prj-config-delete", `{"id":"layout.sidebar"}`)
	m, _ = res.(map[string]any)
	if m["ok"] != true {
		t.Fatalf("delete 应答形状变化（应 {ok:true}）：%+v", res)
	}
	got, _ = svc.ConfigKVGet(facadeGetPrjReq(instanceID, "layout.sidebar"))
	if got.Values["layout.sidebar"] != "" {
		t.Fatalf("删除后 MQ 面仍读到：%+v", got.Values)
	}
}

// TestNonConfigDataStaysOnMQ：非 config 类（如 snapshot）**不经门面** → 仍走总线转发
// （键值面之外的数据域本批未推广，见 41 G-34；行为同改前）。
func TestNonConfigDataStaysOnMQ(t *testing.T) {
	br, _, bus, instanceID := newFacadeBridgeEnv(t)
	reqs := countSubjects(t, bus, "data-snapshot-get")
	payload := `{"instance_id":"` + instanceID + `","session_id":"s-br","id":"s-br"}`
	if _, errs := br.PublishEvent("data-snapshot-get", payload); len(errs) > 0 {
		t.Fatalf("snapshot 请求应经 MQ 面应答（persist 在线）：errs=%v", errs)
	}
	if n := atomic.LoadInt64(reqs); n == 0 {
		t.Fatal("非 config 类 data-* 未走总线：应保留 MQ 转发路径")
	}
}

// TestNoFacadeFallsBackToMQ：未注入门面（-no-server 薄客户端/分离形态）→ config 类仍走总线。
func TestNoFacadeFallsBackToMQ(t *testing.T) {
	br, svc, bus, instanceID := newFacadeBridgeEnv(t)
	br.SetFacade(nil) // 模拟未接线
	reqs := countSubjects(t, bus, "data-prj-config-save")

	res, errs := br.PublishEvent("data-prj-config-save", `{"data":{"key":"k-fb","value":"1"}}`)
	if len(errs) != 0 {
		t.Fatalf("fallback save errs=%v", errs)
	}
	if m, _ := res.(map[string]any); m["ok"] != true {
		t.Fatalf("fallback save 应答形状变化：%+v", res)
	}
	if n := atomic.LoadInt64(reqs); n == 0 {
		t.Fatal("未注入门面时应走总线转发（MQ 路径）")
	}
	got, err := svc.ConfigKVGet(facadeGetPrjReq(instanceID, "k-fb"))
	if err != nil || got.Values["k-fb"] != "1" {
		t.Fatalf("MQ 路径写入未生效：%+v err=%v", got.Values, err)
	}
}

// TestUiSaveLayoutBatchSingleBroadcast：gui.ui.save 的多键 layout 聚合为**一次**批量
// data-prj-config-save（entries 载荷，D-33）——N 键 = 1 次持久化往返 + 1 条刷新广播，
// 且全键落库（不再逐键 N 次 / N 条刷新）。
func TestUiSaveLayoutBatchSingleBroadcast(t *testing.T) {
	br, svc, bus, instanceID := newFacadeBridgeEnv(t)
	saves := countSubjects(t, bus, "data-prj-config-save")
	refreshes := countSubjects(t, bus, "data-prj-config-refresh")

	// measureLayout 返回的多键形态（7 个键）一次 ui.save。
	res, errs := br.PublishEvent("gui.ui.save", `{"layout":{"sidebar":280,"filetree":220,`+
		`"code":600,"bottom":160,"right":300,"preview":200,"console":180}}`)
	if len(errs) != 0 {
		t.Fatalf("ui.save errs=%v", errs)
	}
	if m, _ := res.(map[string]any); m["ok"] != true {
		t.Fatalf("ui.save 应答形状变化：%+v", res)
	}

	// 全键落库（一次批量写生效）。
	keys := []string{"layout.sidebar", "layout.filetree", "layout.code", "layout.bottom",
		"layout.right", "layout.preview", "layout.console"}
	got, err := svc.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Keys: keys,
	})
	if err != nil {
		t.Fatalf("批量布局回读失败：%v", err)
	}
	for _, k := range keys {
		if got.Values[k] == "" {
			t.Fatalf("布局键未落库：%s（%+v）", k, got.Values)
		}
	}
	if got.Values["layout.sidebar"] != "280" {
		t.Fatalf("布局值不符：%+v", got.Values)
	}

	time.Sleep(50 * time.Millisecond) // 等刷新广播投递
	if n := atomic.LoadInt64(saves); n != 0 {
		t.Fatalf("门面注入下 config 类不应走总线：请求数=%d", n)
	}
	if n := atomic.LoadInt64(refreshes); n != 1 {
		t.Fatalf("多键布局应只发 1 条刷新广播（D-33）：实际=%d", n)
	}
}
