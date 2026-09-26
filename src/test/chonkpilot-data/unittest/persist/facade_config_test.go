// config 域门面（inline 绑定）与既有 MQ 面（`data-<domain>-*`）的**行为等价**单测
// （阶段 4 第二批验收 / 41 G-34：一份定义 → 两种绑定，同一落点、同一语义、同一应答形状）。
//
// 两条路径（同一实例、同一数据根）：
//   - 门面 = inline 直调（`chonkpilot-data/facade/inline`；同进程函数调用，不经 MQ，见 23 §7）
//   - MQ   = persist 的 `data-prj-config-*` / `data-prompt-*` / `data-prj-security-*` /
//     `data-user-config-*`（消息面，61 §3.1；既有驱动方式）
//
// 覆盖：写读交叉等价（门面写→MQ 读，MQ 写→门面读）/ 分层读序（prjusr 覆盖 prj）/
// 个人运行态键落 prjusr / 文件化键（prompt.summary_prompt）回落链一致 /
// 变更广播由实现侧发出（订阅方两路径同源）/ **MQ 应答 payload 键名与形态未变**（61 零变更）。
//
// 文件放在 persist 测试目录内，以便复用同一套宿主 helper
// （newTestPersist / regInstance / dataCall / dataResult / collectRefresh）。
package persist_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// mqSaveKV 经 MQ 写一个 kv 键（data-<domain>-save）；断言应答形状 {ok:true, id:key}。
func mqSaveKV(t *testing.T, bus mq.Bus, domain, key, value string) {
	t.Helper()
	res := dataResult(t, dataCall(t, bus, "data-"+domain+"-save", map[string]any{
		"instance_id": facadeInstance, "data": map[string]any{"key": key, "value": value},
	}))
	if ok, _ := res["ok"].(bool); !ok || res["id"] != key {
		t.Fatalf("MQ save 应答形状变化：%+v（应 {ok:true,id:%q}）", res, key)
	}
}

// mqLoadKV 经 MQ 读一个 kv 键（data-<domain>-load）；断言应答形状 {data: <字符串>}。
func mqLoadKV(t *testing.T, bus mq.Bus, domain, key string) string {
	t.Helper()
	res := dataResult(t, dataCall(t, bus, "data-"+domain+"-load", map[string]any{
		"instance_id": facadeInstance, "id": key,
	}))
	v, ok := res["data"].(string)
	if !ok {
		t.Fatalf("MQ load 应答缺 data 字符串：%+v", res)
	}
	return v
}

// mqListKV 经 MQ 读 kv 域全表（data-<domain>-list）；断言应答形状 {list: {key: value}}。
func mqListKV(t *testing.T, bus mq.Bus, domain string) map[string]any {
	t.Helper()
	res := dataResult(t, dataCall(t, bus, "data-"+domain+"-list", map[string]any{
		"instance_id": facadeInstance,
	}))
	list, ok := res["list"].(map[string]any)
	if !ok {
		t.Fatalf("MQ list 应答缺 list 对象：%+v", res)
	}
	return list
}

// TestFacadeConfigKVInlineEqualsMQPath：kv 域（prj-config / prompt / prj-security）两路径
// **写读交叉等价** + MQ 应答 payload 键名/形态未变（61 零变更）。
func TestFacadeConfigKVInlineEqualsMQPath(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	for _, domain := range []string{facade.DomainPrjConfig, facade.DomainPrompt, facade.DomainPrjSecurity} {
		viaFacadeKey, viaMQKey := "k-facade", "k-mq"
		if domain != facade.DomainPrjConfig {
			// 前缀域：list 会与其它域名并存，用同一实例同域两键交叉验证
			viaFacadeKey, viaMQKey = "p-facade", "p-mq"
		}
		// ① 门面写 k-facade；MQ 写 k-mq（同一域、同一实例）
		if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain: domain, InstanceID: facadeInstance,
			Entries: map[string]string{viaFacadeKey: "v-facade"},
		}); err != nil {
			t.Fatalf("[%s] facade ConfigKVSet: %v", domain, err)
		}
		mqSaveKV(t, bus, domain, viaMQKey, "v-mq")

		// ② 交叉读：门面读 MQ 写的键 + MQ 读门面写的键（同一落点）
		resp, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
			Domain: domain, InstanceID: facadeInstance, Keys: []string{viaMQKey, viaFacadeKey},
		})
		if err != nil {
			t.Fatalf("[%s] facade ConfigKVGet: %v", domain, err)
		}
		if resp.Values[viaMQKey] != "v-mq" || resp.Values[viaFacadeKey] != "v-facade" {
			t.Fatalf("[%s] 门面读回与写入不一致：%+v", domain, resp.Values)
		}
		if got := mqLoadKV(t, bus, domain, viaFacadeKey); got != "v-facade" {
			t.Fatalf("[%s] MQ 读门面写入不一致：%q", domain, got)
		}
		if got := mqLoadKV(t, bus, domain, viaMQKey); got != "v-mq" {
			t.Fatalf("[%s] MQ 读 MQ 写入不一致：%q", domain, got)
		}

		// ③ 全表读：两路径返回同一份（门面 list == MQ list == 平铺 键→值字符串）
		list, err := api.ConfigKVList(facade.ConfigKVListRequest{Domain: domain, InstanceID: facadeInstance})
		if err != nil {
			t.Fatalf("[%s] facade ConfigKVList: %v", domain, err)
		}
		mqList := mqListKV(t, bus, domain)
		if !reflect.DeepEqual(toAnyMap(list.List), mqList) {
			t.Fatalf("[%s] 两路径 list 不一致：\n门面=%+v\nMQ  =%+v", domain, list.List, mqList)
		}
		// ④ 未配置键 → 空串（与 load 应答同口径）
		empty, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
			Domain: domain, InstanceID: facadeInstance, Keys: []string{"k-absent"},
		})
		if err != nil || empty.Values["k-absent"] != "" {
			t.Fatalf("[%s] 未配置键应读回空串：%+v err=%v", domain, empty.Values, err)
		}
		if got := mqLoadKV(t, bus, domain, "k-absent"); got != "" {
			t.Fatalf("[%s] MQ 未配置键应读回空串：%q", domain, got)
		}

		// ⑤ 门面删 → MQ 读为空；MQ 删 → 门面读为空（删除语义等价）
		if _, err := api.ConfigKVDelete(facade.ConfigKVDeleteRequest{
			Domain: domain, InstanceID: facadeInstance, Keys: []string{viaMQKey},
		}); err != nil {
			t.Fatalf("[%s] facade ConfigKVDelete: %v", domain, err)
		}
		if got := mqLoadKV(t, bus, domain, viaMQKey); got != "" {
			t.Fatalf("[%s] 门面删除后 MQ 仍读到：%q", domain, got)
		}
		if res := dataResult(t, dataCall(t, bus, "data-"+domain+"-delete", map[string]any{
			"instance_id": facadeInstance, "id": viaFacadeKey,
		})); res["ok"] != true {
			t.Fatalf("[%s] MQ delete 应答形状变化：%+v（应 {ok:true}）", domain, res)
		}
		after, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
			Domain: domain, InstanceID: facadeInstance, Keys: []string{viaFacadeKey},
		})
		if err != nil || after.Values[viaFacadeKey] != "" {
			t.Fatalf("[%s] MQ 删除后门面仍读到：%+v", domain, after.Values)
		}
	}
}

// TestFacadeConfigKVPrjUsrLayering：prj-config 的**分层语义**两路径一致：
//   - 个人运行态键（layout.*）落 prjusr（不污染团队共享库），list 时 prjusr 覆盖 prj；
//   - 其余键落 prj；load 读序 prjusr → prj → usr（usr 兜底仅对无前缀非运行态键生效）。
func TestFacadeConfigKVPrjUsrLayering(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)
	domain := facade.DomainPrjConfig

	// ① 门面写：个人运行态键（layout.sidebar）→ prjusr；团队键（team.key）→ prj
	if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain: domain, InstanceID: facadeInstance,
		Entries: map[string]string{"layout.sidebar": "300", "team.key": "team-val"},
	}); err != nil {
		t.Fatalf("facade ConfigKVSet: %v", err)
	}
	prj, err := data.Prj(facadeInstance)
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	pudb, err := data.PrjUsr(facadeInstance)
	if err != nil {
		t.Fatalf("data.PrjUsr: %v", err)
	}
	if v, ok := data.GetConfig(pudb, "layout.sidebar"); !ok || v != "300" {
		t.Fatalf("个人运行态键应落 prjusr：ok=%v v=%q", ok, v)
	}
	if _, ok := data.GetConfig(prj, "layout.sidebar"); ok {
		t.Fatal("个人运行态键不应落 prj（团队共享库）")
	}
	if v, ok := data.GetConfig(prj, "team.key"); !ok || v != "team-val" {
		t.Fatalf("团队键应落 prj：ok=%v v=%q", ok, v)
	}

	// ② usr 兜底（02-配置层级 §3 读序）：usr 写同键 → 无 prj 值时读到 usr；运行态键不回落 usr
	udb, release, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(udb, "team.key", "usr-val"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(udb, "layout.sidebar", "999"); err != nil {
		t.Fatal(err)
	}
	release()
	resp, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: domain, InstanceID: facadeInstance, Keys: []string{"team.key", "layout.sidebar", "usr.only"},
	})
	if err != nil {
		t.Fatalf("facade ConfigKVGet: %v", err)
	}
	if resp.Values["team.key"] != "team-val" {
		t.Fatalf("prj 值应优先于 usr：%q", resp.Values["team.key"])
	}
	if resp.Values["layout.sidebar"] != "300" {
		t.Fatalf("运行态键应取 prjusr（不回落 usr）：%q", resp.Values["layout.sidebar"])
	}
	if got := mqLoadKV(t, bus, domain, "team.key"); got != "team-val" {
		t.Fatalf("MQ 读序与门面不一致：%q", got)
	}
	if got := mqLoadKV(t, bus, domain, "layout.sidebar"); got != "300" {
		t.Fatalf("MQ 运行态读序与门面不一致：%q", got)
	}

	// ③ prj 删（两层同删）→ usr 兜底生效（门面 MQ 同结果）
	if _, err := api.ConfigKVDelete(facade.ConfigKVDeleteRequest{
		Domain: domain, InstanceID: facadeInstance, Keys: []string{"team.key"},
	}); err != nil {
		t.Fatalf("facade ConfigKVDelete: %v", err)
	}
	resp, err = api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: domain, InstanceID: facadeInstance, Keys: []string{"team.key"},
	})
	if err != nil || resp.Values["team.key"] != "usr-val" {
		t.Fatalf("删 prj 后应回落 usr：%+v err=%v", resp.Values, err)
	}
	if got := mqLoadKV(t, bus, domain, "team.key"); got != "usr-val" {
		t.Fatalf("MQ 删 prj 后应回落 usr：%q", got)
	}
}

// TestFacadeConfigWritesBroadcastRefresh：**订阅面**（23 §7 请求面 + 订阅面成对）——
// 经门面写入同样广播 data-<domain>-refresh（{id, op} + 最新 list），user-config 另兼容
// config-refresh；payload 键名与 MQ 路径一致（订阅方：前端 / 插件 / llm server 热生效）。
func TestFacadeConfigWritesBroadcastRefresh(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	prjRefresh := collectRefresh(t, bus, "prj-config")
	if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance,
		Entries: map[string]string{"logLevel": "debug"},
	}); err != nil {
		t.Fatalf("facade ConfigKVSet: %v", err)
	}
	ev := waitRefresh(t, prjRefresh, "prj-config")
	if ev["id"] != "logLevel" || ev["op"] != "save" || ev["instance_id"] != facadeInstance {
		t.Fatalf("刷新广播字段不符：%+v", ev)
	}
	if list, _ := ev["list"].(map[string]any); list["logLevel"] != "debug" {
		t.Fatalf("刷新广播应带最新 list：%+v", ev["list"])
	}

	ucRefresh := collectRefresh(t, bus, "user-config")
	// 兼容广播 config-refresh（I-42）：主题名不是 data-<domain>-refresh，故单独订阅
	legacy := make(chan map[string]any, 4)
	if _, err := subRaw(bus, "config-refresh", func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) == nil {
			select {
			case legacy <- m:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.UserConfigSet(facade.UserConfigSetRequest{Entries: map[string]any{"theme": "dark"}}); err != nil {
		t.Fatalf("facade UserConfigSet: %v", err)
	}
	ucev := waitRefresh(t, ucRefresh, "user-config")
	if ucev["id"] != "user_config" || ucev["op"] != "save" {
		t.Fatalf("user-config 刷新广播字段不符：%+v", ucev)
	}
	legacyEv := waitRefresh(t, legacy, "config-refresh")
	if _, ok := legacyEv["instance_id"]; !ok {
		t.Fatalf("config-refresh 必带 instance_id 字段（61 §0）：%+v", legacyEv)
	}
}

// TestFacadeUserConfigInlineEqualsMQPath：用户配置域两路径等价（写读交叉 + 应答形状未变）。
func TestFacadeUserConfigInlineEqualsMQPath(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	// ① 门面写 theme + 集合 llms → 落 usr 库（与 MQ 面同源）
	if _, err := api.UserConfigSet(facade.UserConfigSetRequest{
		Entries: map[string]any{
			"theme": "nord",
			"llms":  []any{map[string]any{"name": "m1", "model": "gpt-x"}},
		},
	}); err != nil {
		t.Fatalf("facade UserConfigSet: %v", err)
	}
	// MQ 读：load 返回合并对象（{data:{...}}）
	d := mqUserConfigLoad(t, bus)
	if d["theme"] != "nord" {
		t.Fatalf("MQ load 应读到门面写入：%+v", d["theme"])
	}
	if llms, _ := d["llms"].([]any); len(llms) != 1 {
		t.Fatalf("MQ load 应读到 llms 集合：%+v", d["llms"])
	}
	// 门面读：UserConfigGet 合并值；UserConfigView = usr 主库视图（list 应答形态）
	resp, err := api.UserConfigGet(facade.UserConfigGetRequest{InstanceID: facadeInstance})
	if err != nil {
		t.Fatalf("facade UserConfigGet: %v", err)
	}
	if resp.Config["theme"] != "nord" {
		t.Fatalf("门面 UserConfigGet theme 不符：%+v", resp.Config["theme"])
	}
	view, err := api.UserConfigView(facade.UserConfigViewRequest{})
	if err != nil {
		t.Fatalf("facade UserConfigView: %v", err)
	}
	if len(view.List) != 1 || view.List[0]["theme"] != "nord" {
		t.Fatalf("门面 UserConfigView 应返回单条整体对象：%+v", view.List)
	}
	// MQ list 归一形态 {list: [...]}
	mqListRaw := dataResult(t, dataCall(t, bus, "data-user-config-list", map[string]any{"req_id": "l1"}))
	mqList, ok := mqListRaw["list"].([]any)
	if !ok {
		t.Fatalf("MQ list 应答形状变化（应 {list:[...]}）：%+v", mqListRaw)
	}
	if len(mqList) != len(view.List) {
		t.Fatalf("两路径 list 条数不一致：门面=%d MQ=%d", len(view.List), len(mqList))
	}

	// ② MQ 写（增量）→ 门面读同一份
	if res := dataResult(t, dataCall(t, bus, "data-user-config-save", map[string]any{
		"instance_id": facadeInstance, "data": map[string]any{"locale": "en-US"},
	})); res["ok"] != true || res["id"] != "user_config" {
		t.Fatalf("MQ save 应答形状变化：%+v（应 {ok:true,id:user_config}）", res)
	}
	resp, err = api.UserConfigGet(facade.UserConfigGetRequest{InstanceID: facadeInstance})
	if err != nil || resp.Config["locale"] != "en-US" {
		t.Fatalf("门面应读到 MQ 写入：%+v err=%v", resp.Config["locale"], err)
	}

	// ③ 逐键删（两路径等价）：门面删 theme → MQ 读回落系统默认 light
	if _, err := api.UserConfigDelete(facade.UserConfigDeleteRequest{Keys: []string{"theme"}}); err != nil {
		t.Fatalf("facade UserConfigDelete: %v", err)
	}
	d2 := mqUserConfigLoad(t, bus)
	if d2["theme"] != "light" {
		t.Fatalf("删除后应回落系统默认（light）：%+v", d2["theme"])
	}
	// ④ 未知键 → 明确报错（不兜底清空整份；两路径同口径）
	if _, err := api.UserConfigDelete(facade.UserConfigDeleteRequest{Keys: []string{"no-such-key"}}); err == nil {
		t.Fatal("未知键应报错（禁止兜底清空整份用户配置）")
	}
	failReply := dataCall(t, bus, "data-user-config-delete", map[string]any{
		"instance_id": facadeInstance, "id": "no-such-key",
	})
	if ok, _ := failReply["ok"].(bool); ok {
		t.Fatalf("MQ 未知键应失败：%+v", failReply)
	}
	if msg, _ := failReply["error"].(string); !strings.Contains(msg, "unknown config key") {
		t.Fatalf("MQ 未知键错误文案应保留：%+v", failReply)
	}
	// ⑤ 清空整份（无 key）→ MQ load 仍返回完整默认
	if _, err := api.UserConfigDelete(facade.UserConfigDeleteRequest{}); err != nil {
		t.Fatalf("facade 清空整份: %v", err)
	}
	d3 := mqUserConfigLoad(t, bus)
	if d3["theme"] != "light" || d3["defaultScenario"] != "" {
		t.Fatalf("清空后应回落默认：%+v", d3)
	}
}

// mqUserConfigLoad 经 MQ 读用户配置（data-user-config-load）；断言应答形状 {data:{...}}。
func mqUserConfigLoad(t *testing.T, bus mq.Bus) map[string]any {
	t.Helper()
	res := dataResult(t, dataCall(t, bus, "data-user-config-load", map[string]any{
		"instance_id": facadeInstance,
	}))
	d, ok := res["data"].(map[string]any)
	if !ok {
		t.Fatalf("MQ load 应答形状变化（应 {data:{...}}）：%+v", res)
	}
	return d
}

// TestFacadePromptSummaryPromptFile：prompt 域的**文件化键**（summary_prompt）两路径一致：
// 未配置 → 内置默认；门面写入 → MQ 读到同一份（文件为唯一落点）。
func TestFacadePromptSummaryPromptFile(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	// ① 未配置：门面读 = MQ 读 = 内置默认（回落链终点）
	resp, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrompt, InstanceID: facadeInstance, Keys: []string{"summary_prompt"},
	})
	if err != nil {
		t.Fatalf("facade ConfigKVGet(prompt): %v", err)
	}
	want := data.DefaultSummaryPrompt
	if resp.Values["summary_prompt"] != want {
		t.Fatalf("未配置应回落内置默认：%q", resp.Values["summary_prompt"])
	}
	if got := mqLoadKV(t, bus, facade.DomainPrompt, "summary_prompt"); got != want {
		t.Fatalf("MQ 未配置应回落内置默认：%q", got)
	}

	// ② 门面写（非继承值 → 落项目级文件）→ MQ 读到同一份
	const custom = "SENTINEL-SUMMARY-PROMPT"
	if _, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain: facade.DomainPrompt, InstanceID: facadeInstance,
		Entries: map[string]string{"summary_prompt": custom},
	}); err != nil {
		t.Fatalf("facade ConfigKVSet(prompt): %v", err)
	}
	if got := mqLoadKV(t, bus, facade.DomainPrompt, "summary_prompt"); got != custom {
		t.Fatalf("MQ 应读到门面写入的文件化值：%q", got)
	}

	// ③ 门面删（删项目级文件）→ 两路径同回落内置默认
	if _, err := api.ConfigKVDelete(facade.ConfigKVDeleteRequest{
		Domain: facade.DomainPrompt, InstanceID: facadeInstance, Keys: []string{"summary_prompt"},
	}); err != nil {
		t.Fatalf("facade ConfigKVDelete(prompt): %v", err)
	}
	resp, err = api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrompt, InstanceID: facadeInstance, Keys: []string{"summary_prompt"},
	})
	if err != nil || resp.Values["summary_prompt"] != want {
		t.Fatalf("删除后应回落内置默认：%q err=%v", resp.Values["summary_prompt"], err)
	}
	if got := mqLoadKV(t, bus, facade.DomainPrompt, "summary_prompt"); got != want {
		t.Fatalf("MQ 删除后应回落内置默认：%q", got)
	}
}

// waitRefresh 等一条域刷新广播（超时 = 失败）。
func waitRefresh(t *testing.T, ch chan map[string]any, domain string) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatalf("未收到 data-%s-refresh 广播", domain)
		return nil
	}
}

// toAnyMap 把门面 DTO 的平铺字符串表转成载荷形态（与 MQ list 应答比对用）。
func toAnyMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
