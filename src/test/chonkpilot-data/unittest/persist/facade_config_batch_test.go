// 项目配置**批量写**（2026-09-28，[61-消息一览 §3.1]）契约单测：
//   - 一次批量写 N 键 → **恰好 1 条** data-prj-config-refresh（载荷带 `ids` 全组键 + `id` = 首键）；
//   - N 键全部落库；MQ 路径（`data-prj-config-save` 报文 `{data:{entries}}`）与门面路径等价；
//   - 单键路径**行为不变**（仍 1 条、`id` 正确、**不写 `ids`**）；
//   - `delete` 路径不变（逐键广播、载荷不写 `ids`）；同值重复写**仍各发 1 条**（信号语义不丢）；
//   - 实例隔离：不同实例的批量写各带各自 `instance_id`，值互不可见。
//
// 复用本包既有宿主 helper（newTestPersist / regInstance / dataCall / dataResult /
// collectRefresh / waitRefresh / mqSaveKV / mqLoadKV）。
package persist_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// regInstanceAs 发布 instance-register（id + work_dir 指定；B 用例需同/异 work_dir 多实例）。
func regInstanceAs(t *testing.T, bus mq.Bus, id, wd string) {
	t.Helper()
	pubFire(bus, "instance-register", jb(map[string]any{
		"instance_id": id, "client_type": "unittest", "work_dir": wd,
	}))
	time.Sleep(30 * time.Millisecond)
}

// expectNoMoreRefresh 断言窗口内不再收到刷新广播（"恰好 1 条"断言用）。
func expectNoMoreRefresh(t *testing.T, ch chan map[string]any, wait time.Duration) {
	t.Helper()
	select {
	case m := <-ch:
		t.Fatalf("收到多余刷新广播（应恰好 1 条）：%+v", m)
	case <-time.After(wait):
	}
}

// idsOf 取刷新载荷的 `ids`（未带 → nil）。
func idsOf(ev map[string]any) []string {
	raw, _ := ev["ids"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

// TestConfigKVSetBatchSingleRefresh：批量写 N 键 = 恰好 1 条 refresh（ids 全组键、id = 首键）；
// 门面路径与 MQ 路径等价；单键写 = 1 条且不写 ids（载荷与改前一致）。
func TestConfigKVSetBatchSingleRefresh(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	// ① 门面批量写 3 键 → 1 条 refresh（ids 字典序 = a/b/c，id = 首键 a）
	ch := collectRefresh(t, bus, "prj-config")
	if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance,
		Entries: map[string]string{"b.key": "2", "a.key": "1", "c.key": "3"},
	}); err != nil {
		t.Fatalf("facade ConfigKVSet(batch): %v", err)
	}
	ev := waitRefresh(t, ch, "prj-config")
	if ev["op"] != "save" || ev["instance_id"] != facadeInstance {
		t.Fatalf("批量 refresh 字段不符：%+v", ev)
	}
	if ids := idsOf(ev); len(ids) != 3 || ids[0] != "a.key" || ids[1] != "b.key" || ids[2] != "c.key" {
		t.Fatalf("批量 refresh 应带字典序全组 ids：%+v", ev["ids"])
	}
	if ev["id"] != "a.key" {
		t.Fatalf("批量 refresh id 应为首键：%+v", ev["id"])
	}
	expectNoMoreRefresh(t, ch, 200*time.Millisecond)

	// N 键全部落库
	got, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance,
		Keys: []string{"a.key", "b.key", "c.key"},
	})
	if err != nil || got.Values["a.key"] != "1" || got.Values["b.key"] != "2" || got.Values["c.key"] != "3" {
		t.Fatalf("批量写落库不正确：%+v err=%v", got.Values, err)
	}

	// ② MQ 路径批量写：报文 {data:{entries}} → 应答 {ok:true,id:首键} + 1 条 refresh（ids 2 键）
	ch2 := collectRefresh(t, bus, "prj-config")
	res := dataResult(t, dataCall(t, bus, "data-prj-config-save", map[string]any{
		"instance_id": facadeInstance,
		"data":        map[string]any{"entries": map[string]any{"y": "2", "x": "1"}},
	}))
	if res["ok"] != true || res["id"] != "x" {
		t.Fatalf("MQ 批量 save 应答形状不符（应 {ok:true,id:x}）：%+v", res)
	}
	ev2 := waitRefresh(t, ch2, "prj-config")
	if ids := idsOf(ev2); len(ids) != 2 || ids[0] != "x" || ids[1] != "y" {
		t.Fatalf("MQ 批量 refresh 应带 2 键 ids：%+v", ev2["ids"])
	}
	expectNoMoreRefresh(t, ch2, 200*time.Millisecond)
	if v := mqLoadKV(t, bus, facade.DomainPrjConfig, "y"); v != "2" {
		t.Fatalf("MQ 批量写未落库：%q", v)
	}

	// ③ 单键写：仍 1 条、id 正确、**不写 ids**（载荷与改前逐字节等价）
	ch3 := collectRefresh(t, bus, "prj-config")
	mqSaveKV(t, bus, facade.DomainPrjConfig, "solo", "v")
	ev3 := waitRefresh(t, ch3, "prj-config")
	if ev3["id"] != "solo" || ev3["op"] != "save" {
		t.Fatalf("单键 refresh 字段不符：%+v", ev3)
	}
	if _, has := ev3["ids"]; has {
		t.Fatalf("单键 refresh 不应写 ids（载荷须与改前等价）：%+v", ev3)
	}
	expectNoMoreRefresh(t, ch3, 200*time.Millisecond)
}

// TestConfigKVDeletePathUnchanged：delete 仍逐键广播（1 键 1 条、载荷不写 ids、id = 键）。
func TestConfigKVDeletePathUnchanged(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance, Entries: map[string]string{"d.key": "v"},
	}); err != nil {
		t.Fatal(err)
	}
	ch := collectRefresh(t, bus, "prj-config")
	if _, err := api.ConfigKVDelete(facade.ConfigKVDeleteRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance, Keys: []string{"d.key"},
	}); err != nil {
		t.Fatal(err)
	}
	ev := waitRefresh(t, ch, "prj-config")
	if ev["id"] != "d.key" || ev["op"] != "delete" {
		t.Fatalf("delete refresh 字段不符：%+v", ev)
	}
	if _, has := ev["ids"]; has {
		t.Fatalf("delete refresh 不应写 ids（载荷不变）：%+v", ev)
	}
	expectNoMoreRefresh(t, ch, 200*time.Millisecond)
}

// TestConfigKVSetSameValueStillRefreshes：同值重复写仍**各发 1 条**刷新
// （codegraph.action / history.clear 等动作信号语义不被去重/合并吞掉）。
func TestConfigKVSetSameValueStillRefreshes(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	ch := collectRefresh(t, bus, "prj-config")
	for i := 0; i < 2; i++ {
		if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain: facade.DomainPrjConfig, InstanceID: facadeInstance,
			Entries: map[string]string{"codegraph.action": "rebuild"},
		}); err != nil {
			t.Fatal(err)
		}
		ev := waitRefresh(t, ch, "prj-config")
		if ev["id"] != "codegraph.action" || ev["op"] != "save" {
			t.Fatalf("第 %d 次同值写未产生刷新：%+v", i+1, ev)
		}
	}
}

// TestConfigKVSetInstanceIsolation：不同实例的批量写各带各自 instance_id（多窗口按 instance_id
// 过滤 → 隔离），且值互不可见。
func TestConfigKVSetInstanceIsolation(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus) // ins-test

	wd2 := t.TempDir()
	pubFire(bus, "instance-register", jb(map[string]any{
		"instance_id": "ins-2", "client_type": "unittest", "work_dir": wd2,
	}))
	time.Sleep(30 * time.Millisecond)
	data.Register("ins-2", wd2, "")
	api := inline.New(bus)

	ch := collectRefresh(t, bus, "prj-config")
	if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: "ins-2", Entries: map[string]string{"iso": "2"},
	}); err != nil {
		t.Fatal(err)
	}
	ev := waitRefresh(t, ch, "prj-config")
	if ev["instance_id"] != "ins-2" {
		t.Fatalf("刷新广播应带写入实例 id：%+v", ev)
	}
	got, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance, Keys: []string{"iso"},
	})
	if err != nil || got.Values["iso"] != "" {
		t.Fatalf("实例间不应串值：%q err=%v", got.Values["iso"], err)
	}
}

// TestConfigKVSetRefreshFansOutSameWorkDir（G-41-b）：prj 配置写后广播 `data-prj-config-refresh`
// 需投递给**同 work_dir 的全部在册实例**（各带自身 instance_id）；异 work_dir 实例**不收**。
func TestConfigKVSetRefreshFansOutSameWorkDir(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := t.TempDir()
	// ins-a / ins-b 同 work_dir；ins-x 异 work_dir。
	regInstanceAs(t, bus, "ins-a", wd)
	regInstanceAs(t, bus, "ins-b", wd)
	regInstanceAs(t, bus, "ins-x", t.TempDir())

	ch := collectRefresh(t, bus, "prj-config")
	res := dataResult(t, dataCall(t, bus, "data-prj-config-save", map[string]any{
		"instance_id": "ins-a", "data": map[string]any{"key": "fan.key", "value": "v"},
	}))
	if res["ok"] != true {
		t.Fatalf("save 应答不符：%+v", res)
	}

	received := map[string]bool{}
	for i := 0; i < 2; i++ { // 同 work_dir 两实例各一条
		ev := waitRefresh(t, ch, "prj-config")
		if ev["id"] != "fan.key" || ev["op"] != "save" {
			t.Fatalf("refresh 字段不符：%+v", ev)
		}
		id, _ := ev["instance_id"].(string)
		received[id] = true
	}
	if !received["ins-a"] || !received["ins-b"] {
		t.Fatalf("同 work_dir 两实例都应收到 refresh：%+v", received)
	}
	if received["ins-x"] {
		t.Fatalf("异 work_dir 实例不应收到 refresh：%+v", received)
	}
	expectNoMoreRefresh(t, ch, 200*time.Millisecond)
}

// TestDataRequestRequiresInstanceID（G-41-c）：白名单（全局级域 user-config/scenario/mcp/knowledge）
// 外的 data 请求缺 instance_id → **明确错误**（不静默走"唯一实例回退"）；白名单内正常处理。
func TestDataRequestRequiresInstanceID(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus) // 已登记唯一实例（旧行为会静默回退到它）

	// 严格域（如 session）缺 instance_id → {ok:false, error 指明 instance_id}
	r := dataCall(t, bus, "data-session-list", map[string]any{"req_id": "e1"})
	if okv, _ := r["ok"].(bool); okv {
		t.Fatalf("data-session-list 缺 instance_id 应失败：%+v", r)
	}
	if msg, _ := r["error"].(string); !strings.Contains(msg, "instance_id") {
		t.Fatalf("错误应指明缺 instance_id：%+v", r)
	}

	// 全局级域（user-config）缺 instance_id → 正常处理
	res := dataResult(t, dataCall(t, bus, "data-user-config-list", map[string]any{"req_id": "e2"}))
	if _, hasList := res["list"]; !hasList {
		t.Fatalf("user-config 缺 instance_id 应正常返回 list：%+v", res)
	}
}
