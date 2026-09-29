// data-<domain>-* 消息面测试（A1 自 chonkpilot-server/server/config_test.go 迁移）：
// 直接驱动 persist.Service（独立总线 + 临时 usr 库 + 实例登记），不依赖 server 宿主。
//
// 外部模块黑盒（package persist_test）：经 persist.New/Start/Stop + 总线 data-* 主题驱动；
// usr 落库直连经 data.OpenSharedLayer(usrPath, data.LayerUsr)（usrPath 由 helper 三元组注入）。
package persist_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// 对齐 persist.go 私有常量（黑盒不可见，本地复制）：usr config 场景列表固定 key + 默认场景 key。
const scenarioListKey = "scenario_list"
const scenarioDefaultKey = "default"

// newTestPersistOpts 建独立测试环境：内存总线（chonk. 前缀）+ persist 服务（临时 usr 库；
// opts.AppDir 可注入 app 级知识库根）。第三返回值 = usr 库路径（黑盒直连落库断言用，
// 等价 persist 私有 usrDB 打开的库；svc.usrDB 语义 = data.OpenSharedLayer(p, data.LayerUsr)）。
func newTestPersistOpts(t *testing.T, opts persist.Options) (mq.Bus, *persist.Service, string) {
	t.Helper()
	data.Reset()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	if opts.UsrPath == "" {
		opts.UsrPath = t.TempDir() + "/usr.db"
	}
	svc := persist.New(bus, opts)
	if err := svc.Start(); err != nil {
		t.Fatalf("persist.Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() })
	t.Cleanup(data.Reset) // LIFO：先于 bus.Close 关闭缓存连接
	return bus, svc, opts.UsrPath
}

// newTestPersist 默认选项建环境。
func newTestPersist(t *testing.T) (mq.Bus, *persist.Service, string) {
	t.Helper()
	return newTestPersistOpts(t, persist.Options{})
}

// regInstance 发布 instance-register（persist 自持实例视图 → prjDB 可用），
// 返回 work_dir（测试种子数据写入 prj 库用）。
func regInstance(t *testing.T, bus mq.Bus) string {
	t.Helper()
	wd := t.TempDir()
	pubFire(bus, "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": wd,
	}))
	time.Sleep(30 * time.Millisecond)
	data.Register("ins-test", wd, "") // 测试种子写入直连 prj 库
	return wd
}

func jb(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// subRaw 以 v2 On 订阅主题，把回调适配为载荷风格 func(subject, payload)——测试收集器
// 保持简洁形态（仅测试用；总线收发 API = v2 On/Emit）。
func subRaw(bus mq.Bus, subject string, h func(subject string, payload []byte)) (mq.Sub, error) {
	return bus.On(subject, 0, func(_ context.Context, subject string, v *mq.Value) error {
		h(subject, v.Payload)
		return nil
	})
}

// pubFire fire-and-forget 发布（仅测试用；= v2 Emit 不 await，忽略派发结果）。
func pubFire(bus mq.Bus, subject string, payload any) {
	_ = bus.Emit(context.Background(), subject, payload)
}

// dataCall 发 data-* 请求并等结果（persist.reply 发布到同一 subject，订阅匹配 ok 载荷）。
// subject 形如 data-prj-config-save（相对主题，总线自动补 chonk. 前缀）。
func dataCall(t *testing.T, bus mq.Bus, subject string, payload map[string]any) map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 1)
	sub, err := subRaw(bus, subject, func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) != nil {
			return
		}
		if _, hasOK := m["ok"]; !hasOK {
			return // 请求无 ok 字段，响应有
		}
		select {
		case ch <- m:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	pubFire(bus, subject, jb(payload))
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting reply for %s", subject)
		return nil
	}
}

// collectRefresh 订阅 data-<domain>-refresh 广播（save/delete 后 persist 主动发）。
func collectRefresh(t *testing.T, bus mq.Bus, domain string) chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 8)
	_, err := subRaw(bus, "data-"+domain+"-refresh", func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) == nil {
			select {
			case ch <- m:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func dataResult(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in reply: %+v", m)
	}
	return res
}

func dataList(res map[string]any) []any {
	list, _ := res["list"].([]any)
	return list
}

// TestDataPrjConfigSaveListLoadDelete：prj config 表 CRUD（记录 {"v": 值字符串}）+ 落库断言。
func TestDataPrjConfigSaveListLoadDelete(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	// save 载荷 {key, value}（value 为字符串）→ Upsert(key, {v: value})
	r := dataCall(t, bus, "data-prj-config-save", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"key": "history.enabled", "value": "false"},
	})
	res := dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("save failed: %+v", r)
	}
	if res["id"] != "history.enabled" {
		t.Fatalf("save id=%v", res["id"])
	}

	// 落库断言（prj 主库 config 表 {v: ...} 记录）
	db, err := data.Prj("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	var rec data.Record
	ok, _ := db.Table("config").Get("history.enabled", &rec)
	if !ok || rec["v"] != "false" {
		t.Fatalf("config not persisted: ok=%v rec=%+v", ok, rec)
	}

	// list → {list: {key: value} map}
	r = dataCall(t, bus, "data-prj-config-list", map[string]any{"req_id": "r2", "instance_id": "ins-test"})
	res = dataResult(t, r)
	kv, _ := res["list"].(map[string]any)
	if kv["history.enabled"] != "false" {
		t.Fatalf("list=%+v", kv)
	}

	// load{id} → {data: value 字符串}
	r = dataCall(t, bus, "data-prj-config-load", map[string]any{
		"req_id": "r3", "instance_id": "ins-test", "id": "history.enabled",
	})
	res = dataResult(t, r)
	if v, _ := res["data"].(string); v != "false" {
		t.Fatalf("load data=%+v", res["data"])
	}

	// delete → 库中移除
	r = dataCall(t, bus, "data-prj-config-delete", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "id": "history.enabled",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	ok, _ = db.Table("config").Get("history.enabled", &rec)
	if ok {
		t.Fatal("entry not deleted")
	}
}

// TestDataUserConfigSaveLoadListDelete：usr config 表单 key 整体对象——load 无记录回落默认
// （不 fail）；save 先读现有再增量 merge（不覆写未提交字段）。
func TestDataUserConfigSaveLoadListDelete(t *testing.T) {
	bus, _, _ := newTestPersist(t)

	// 初始 list 空
	r := dataCall(t, bus, "data-user-config-list", map[string]any{"req_id": "r1"})
	if n := len(dataList(dataResult(t, r))); n != 0 {
		t.Fatalf("initial list=%d", n)
	}

	// load 无记录 → 默认 {llms:[], defaultLLM:-1（无可用 LLM）, theme:"light"}（不 fail）
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r2"})
	res := dataResult(t, r)
	d, _ := res["data"].(map[string]any)
	if d["theme"] != "light" {
		t.Fatalf("default theme=%v", d["theme"])
	}
	if ll, _ := d["llms"].([]any); len(ll) != 0 {
		t.Fatalf("default llms=%+v", d["llms"])
	}
	if v, _ := d["defaultLLM"].(float64); v != -1 {
		t.Fatalf("default defaultLLM=%v (want -1 when no LLM)", d["defaultLLM"])
	}

	// save 增量 merge（只提交 theme/defaultScenario）
	r = dataCall(t, bus, "data-user-config-save", map[string]any{
		"req_id": "r3",
		"data":   map[string]any{"theme": "dark", "defaultScenario": "s1"},
	})
	res = dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok || res["id"] != "user_config" {
		t.Fatalf("save failed: %+v", r)
	}

	// list 单条整体
	r = dataCall(t, bus, "data-user-config-list", map[string]any{"req_id": "r4"})
	res = dataResult(t, r)
	list := dataList(res)
	if len(list) != 1 {
		t.Fatalf("list=%+v", list)
	}
	item := list[0].(map[string]any)
	if item["theme"] != "dark" {
		t.Fatalf("item=%+v", item)
	}

	// save 补 llms → theme 不被覆写（增量 merge）
	r = dataCall(t, bus, "data-user-config-save", map[string]any{
		"req_id": "r5",
		"data": map[string]any{
			"llms": []any{map[string]any{
				"name": "openai", "protocol": "openai", "apiKey": "sk-x",
				"model": "gpt-4", "baseUrl": "http://127.0.0.1:8901/v1",
			}},
			"defaultLLM": 0,
		},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("save llms failed: %+v", r)
	}

	// load：theme 保留 + llms 新增
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r6"})
	res = dataResult(t, r)
	d, _ = res["data"].(map[string]any)
	if d["theme"] != "dark" {
		t.Fatalf("theme overwritten: %+v", d)
	}
	if ll, _ := d["llms"].([]any); len(ll) != 1 {
		t.Fatalf("llms=%+v", d["llms"])
	}
	if v, _ := d["defaultScenario"].(string); v != "s1" {
		t.Fatalf("defaultScenario=%v", d["defaultScenario"])
	}

	// delete → list 空、load 回默认（不 fail）
	r = dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "r7", "id": "user_config"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	r = dataCall(t, bus, "data-user-config-list", map[string]any{"req_id": "r8"})
	if n := len(dataList(dataResult(t, r))); n != 0 {
		t.Fatalf("list after delete=%d", n)
	}
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r9"})
	res = dataResult(t, r)
	if d, _ := res["data"].(map[string]any); d["theme"] != "light" {
		t.Fatalf("load after delete should default: %+v", d)
	}
}

// TestDataUserConfigFreeKeyRoundTrip：usr config 表**自由键**通道（G-05 / P2-7）——
// data-user-config-save 写 recent_dirs（JSON 数组字符串）→ 落库 + load 原样回读；
// 单 key delete 只删该键、不误清整份用户配置；清空整份用户配置一并清掉自由键。
func TestDataUserConfigFreeKeyRoundTrip(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	value := `["C:/ws/a","C:/ws/b"]`

	// 写：自由键 recent_dirs（值 = JSON 字符串）
	r := dataCall(t, bus, "data-user-config-save", map[string]any{
		"req_id": "r1",
		"data":   map[string]any{"recent_dirs": value},
	})
	res := dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("save free key failed: %+v", r)
	}

	// 落库断言：usr config 表 recent_dirs = {v: value}
	db, release, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := data.GetConfig(db, "recent_dirs"); !ok || v != value {
		t.Fatalf("recent_dirs not persisted: ok=%v v=%q", ok, v)
	}
	release()

	// 读：load 回读自由键（与 typed 标量并存，theme 仍回落系统默认）
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r2"})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if got, _ := d["recent_dirs"].(string); got != value {
		t.Fatalf("load recent_dirs=%v", d["recent_dirs"])
	}
	if d["theme"] != "light" {
		t.Fatalf("theme default missing: %+v", d)
	}

	// 单 key delete：只删自由键，typed 默认仍在
	r = dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "r3", "id": "recent_dirs"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete free key failed: %+v", r)
	}
	db2, release2, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data.GetConfig(db2, "recent_dirs"); ok {
		t.Fatal("recent_dirs should be deleted")
	}
	release2()
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r4"})
	d, _ = dataResult(t, r)["data"].(map[string]any)
	if _, ok := d["recent_dirs"]; ok {
		t.Fatalf("recent_dirs should be gone after delete: %+v", d)
	}
}

// TestDataUserConfigDefaultLLMRef（2026-09-15）：defaultLLM 值类型改为 llmref（LLM 选择引用，
// 见 64-配置项一览 §3）——① provider name 字符串（usr 记录名 / 内置项 echo）原样往返（不再受
// llms 增删导致的索引移位影响）；② 空串 = 显式「系统默认（启动参数）」（键存在，不回落 0/-1）；
// ③ 旧记录 int 索引（数字）原样回读为 int（兼容）；④ 键缺失 → 首个可用 LLM：有 llms = 0、
// 无 llms = -1（既有语义不变）。
func TestDataUserConfigDefaultLLMRef(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	load := func(reqID string) map[string]any {
		t.Helper()
		r := dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": reqID})
		d, _ := dataResult(t, r)["data"].(map[string]any)
		return d
	}
	save := func(reqID string, data map[string]any) {
		t.Helper()
		r := dataCall(t, bus, "data-user-config-save", map[string]any{"req_id": reqID, "data": data})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("save %v failed: %+v", data, r)
		}
	}

	// ① name 字符串（内置 provider echo）：原样往返
	save("d1", map[string]any{"defaultLLM": "echo"})
	if v, ok := load("d2")["defaultLLM"].(string); !ok || v != "echo" {
		t.Fatalf("① defaultLLM=%v want \"echo\"（name 字符串）", load("d2")["defaultLLM"])
	}

	// ② 空串 = 显式「系统默认（启动参数）」：键存在且值保留（不会回落成 0 / -1）
	save("d3", map[string]any{"defaultLLM": ""})
	if v, ok := load("d4")["defaultLLM"].(string); !ok || v != "" {
		t.Fatalf("② defaultLLM=%v want \"\"（显式系统默认）", load("d4")["defaultLLM"])
	}

	// ③ 旧记录 int 索引（数字）→ 原样回读为 int（消费点按 llms[v] 解析，兼容）
	save("d5", map[string]any{"defaultLLM": 1})
	if v, ok := load("d6")["defaultLLM"].(float64); !ok || v != 1 {
		t.Fatalf("③ defaultLLM=%v want 1（旧 int 索引兼容）", load("d6")["defaultLLM"])
	}

	// ④ 键缺失 → 首个可用 LLM：无 llms = -1；有 llms = 0
	r := dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "d7", "id": "defaultLLM"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete defaultLLM failed: %+v", r)
	}
	if v, _ := load("d8")["defaultLLM"].(float64); v != -1 {
		t.Fatalf("④ 无 llms 且键缺失 defaultLLM=%v want -1", load("d8")["defaultLLM"])
	}
	save("d9", map[string]any{"llms": []any{map[string]any{"name": "openai", "model": "gpt-4"}}})
	if v, _ := load("d10")["defaultLLM"].(float64); v != 0 {
		t.Fatalf("④ 有 llms 且键缺失 defaultLLM=%v want 0", load("d10")["defaultLLM"])
	}
}

// TestDataScenarioSaveLoadListDelete（v6 文件化 · 25 §6 · 42 §2 (171)）：场景 = 独立根
// `<scenarios>/<场景目录>/`（与 capability/ 平级）——**出厂场景 = app 级**（出厂内容 = 磁盘目录，
// 不再由 list 物化到 user 级）；save 按 id（= 目录名，用户指定）写目录；
// load 按 id（可带 level 限定）；delete 删目录。
func TestDataScenarioSaveLoadListDelete(t *testing.T) {
	appDir := appCapabilityRoot(t)
	bus, _, usrPath := newTestPersistOpts(t, persist.Options{AppDir: appDir})
	// app 级场景根 = <AppDir 的父>/scenarios；user 级场景根 = usr 主库所在目录/scenarios（25 §6）
	appScen := filepath.Join(filepath.Dir(appDir), "scenarios")
	usrScen := filepath.Join(filepath.Dir(usrPath), "scenarios")

	// list → 命中 app 级出厂默认场景（**不**物化到 user 级）
	r := dataCall(t, bus, "data-scenario-list", map[string]any{"req_id": "r1"})
	res := dataResult(t, r)
	list := dataList(res)
	if len(list) != 1 {
		t.Fatalf("seeded list=%+v", list)
	}
	def := list[0].(map[string]any)
	if persist.Sval(def["id"]) != "default" || persist.Sval(def["key"]) != "default" ||
		def["name"] != "开发场景" || def["level"] != "app" {
		t.Fatalf("seeded default=%+v", def)
	}
	if ags, _ := def["agents"].([]any); len(ags) == 0 {
		t.Fatalf("seeded default missing agents: %+v", def)
	}
	// 落盘断言：app 级随发布资源 scenarios/default（scenario.json + main.agent.md）
	dir := filepath.Join(appScen, "default")
	if _, err := os.Stat(filepath.Join(dir, "scenario.json")); err != nil {
		t.Fatalf("scenario.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.agent.md")); err != nil {
		t.Fatalf("main.agent.md missing: %v", err)
	}
	// 反向证据：user 级场景根**不**被物化（原 materializeDefaultScenario 已随 T6 撤除）
	if _, err := os.Stat(filepath.Join(usrScen, "default")); !os.IsNotExist(err) {
		t.Fatalf("user 级不应物化出厂默认场景：%v", err)
	}

	// 新建场景（id = 用户指定的目录名）
	r = dataCall(t, bus, "data-scenario-save", map[string]any{
		"req_id": "r2",
		"data": map[string]any{
			"id": "s2", "name": "场景2", "description": "描述2",
			"agents": []any{
				map[string]any{"name": "主", "isMain": true, "prompt": "主提示"},
				map[string]any{"name": "子A", "roleTag": "A", "description": "子A描述", "prompt": "A提示"},
			},
		},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("save new failed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(usrScen, "s2", "main.agent.md")); err != nil {
		t.Fatalf("s2 main.agent.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(usrScen, "s2", "子A.agent.md")); err != nil {
		t.Fatalf("s2 子A.agent.md missing: %v", err)
	}

	// load 按 id：name/agents 回读，主 agent 在首位
	r = dataCall(t, bus, "data-scenario-load", map[string]any{"req_id": "r3", "id": "s2"})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d["name"] != "场景2" || d["level"] != "user" {
		t.Fatalf("load s2=%+v", d)
	}
	ags, _ := d["agents"].([]any)
	if len(ags) != 2 {
		t.Fatalf("agents=%+v", ags)
	}
	if first := ags[0].(map[string]any); first["isMain"] != true || first["name"] != "主" {
		t.Fatalf("main agent should be first: %+v", first)
	}

	// list 两条
	r = dataCall(t, bus, "data-scenario-list", map[string]any{"req_id": "r4"})
	if n := len(dataList(dataResult(t, r))); n != 2 {
		t.Fatalf("list n=%d", n)
	}

	// delete s2 → 剩默认
	r = dataCall(t, bus, "data-scenario-delete", map[string]any{"req_id": "r5", "id": "s2"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(usrScen, "s2")); !os.IsNotExist(err) {
		t.Fatal("s2 dir should be removed")
	}
}

// TestDataSecuritySaveListLoadDelete：prj-security 走 prj config 表 security- 前缀（同 prompt，
// save 载荷 {key, value}；list/load 返回 {key: value} 平铺 / 值字符串）。
func TestDataSecuritySaveListLoadDelete(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	// save {key, value}（value 为 JSON 字符串）
	r := dataCall(t, bus, "data-prj-security-save", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"key": "ws", "value": `{"dir":"C:/ws","writable":true}`},
	})
	res := dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok || res["id"] != "ws" {
		t.Fatalf("save failed: %+v", r)
	}

	// 落库断言：config 表 security-ws = {v: ...}
	db, err := data.Prj("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	var rec data.Record
	ok, _ := db.Table("config").Get("security-ws", &rec)
	if !ok || rec["v"] != `{"dir":"C:/ws","writable":true}` {
		t.Fatalf("security not persisted: ok=%v rec=%+v", ok, rec)
	}

	// list → {list: {key: value} map}
	r = dataCall(t, bus, "data-prj-security-list", map[string]any{"req_id": "r2", "instance_id": "ins-test"})
	res = dataResult(t, r)
	kv, _ := res["list"].(map[string]any)
	if kv["ws"] != `{"dir":"C:/ws","writable":true}` {
		t.Fatalf("security list=%+v", kv)
	}

	// load{id=key} → {data: value}
	r = dataCall(t, bus, "data-prj-security-load", map[string]any{
		"req_id": "r3", "instance_id": "ins-test", "id": "ws",
	})
	res = dataResult(t, r)
	if v, _ := res["data"].(string); v != `{"dir":"C:/ws","writable":true}` {
		t.Fatalf("load data=%+v", res["data"])
	}

	// delete → 库中移除
	r = dataCall(t, bus, "data-prj-security-delete", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "id": "ws",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	if ok, _ := db.Table("config").Get("security-ws", &rec); ok {
		t.Fatal("security entry not deleted")
	}
}

// TestDataSecurityEntryKeyRoundTrip：**前端契约往返**（一条信任目录 = 一个 persist key，key = 前端生成的
// 不透明 id，value = `{"dir","writable"}` JSON 字符串）：增 → 改（同 key 换 value）→ 删，并断言
// security- 前缀与其它 prj 域键（enable-codegraph / history.enabled）互不串域。
func TestDataSecurityEntryKeyRoundTrip(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	secSave := func(reqID, key, value string) map[string]any {
		return dataResult(t, dataCall(t, bus, "data-prj-security-save", map[string]any{
			"req_id": reqID, "instance_id": "ins-test",
			"data": map[string]any{"key": key, "value": value},
		}))
	}
	secList := func(reqID string) map[string]any {
		res := dataResult(t, dataCall(t, bus, "data-prj-security-list", map[string]any{
			"req_id": reqID, "instance_id": "ins-test",
		}))
		kv, _ := res["list"].(map[string]any)
		if kv == nil {
			t.Fatalf("security list 非 map: %+v", res)
		}
		return kv
	}

	// 同库其它域键（无前缀，全程不应被 security 增删改影响）
	for i, k := range []string{"enable-codegraph", "history.enabled"} {
		res := dataResult(t, dataCall(t, bus, "data-prj-config-save", map[string]any{
			"req_id": "c0", "instance_id": "ins-test",
			"data": map[string]any{"key": k, "value": "true"},
		}))
		if ok, _ := res["ok"].(bool); !ok {
			t.Fatalf("prj-config save %d failed: %+v", i, res)
		}
	}

	// 1) 增两条
	v1 := `{"dir":"E:/trust-a","writable":false}`
	v2 := `{"dir":"E:/trust-b","writable":true}`
	if ok, _ := secSave("s1", "dir-k1", v1)["ok"].(bool); !ok {
		t.Fatal("save entry1 failed")
	}
	if ok, _ := secSave("s2", "dir-k2", v2)["ok"].(bool); !ok {
		t.Fatal("save entry2 failed")
	}
	kv := secList("s3")
	if len(kv) != 2 || kv["dir-k1"] != v1 || kv["dir-k2"] != v2 {
		t.Fatalf("security list after add = %+v", kv)
	}

	// 2) 改：同 key 换 value（= 勾选 writable）
	v1b := `{"dir":"E:/trust-a","writable":true}`
	if ok, _ := secSave("s4", "dir-k1", v1b)["ok"].(bool); !ok {
		t.Fatal("update entry1 failed")
	}
	kv = secList("s5")
	if kv["dir-k1"] != v1b || kv["dir-k2"] != v2 {
		t.Fatalf("security list after update = %+v", kv)
	}

	// 3) 删一条 → 仅剩一条
	if ok, _ := dataResult(t, dataCall(t, bus, "data-prj-security-delete", map[string]any{
		"req_id": "s6", "instance_id": "ins-test", "id": "dir-k2",
	}))["ok"].(bool); !ok {
		t.Fatal("delete entry2 failed")
	}
	kv = secList("s7")
	if len(kv) != 1 || kv["dir-k1"] != v1b {
		t.Fatalf("security list after delete = %+v", kv)
	}

	// 4) 其它域键不受影响（security 增删改只写 security-*）
	// 注：prj-config list 无前缀 = 全表平铺，本就含 prompt-*/security-* 条目（既有行为，非本次改动），
	// 故此处只断言值未被改动。
	prjRes := dataResult(t, dataCall(t, bus, "data-prj-config-list", map[string]any{
		"req_id": "c2", "instance_id": "ins-test",
	}))
	prjKV, _ := prjRes["list"].(map[string]any)
	if prjKV["enable-codegraph"] != "true" || prjKV["history.enabled"] != "true" {
		t.Fatalf("prj-config keys affected by security ops: %+v", prjKV)
	}
	// security 域 list 只含本域条目（不带 security- 前缀回显）
	if _, leaked := kv["security-"]; leaked {
		t.Fatalf("security list 回显未剥前缀: %+v", kv)
	}
}

// TestDataPromptSaveListLoad：prompt 域走 prj config 表 prompt- 前缀（{key, value}；key 剥前缀）。
func TestDataPromptSaveListLoad(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	r := dataCall(t, bus, "data-prompt-save", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"key": "tool_usage_prompt", "value": "工具使用说明"},
	})
	res := dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok || res["id"] != "tool_usage_prompt" {
		t.Fatalf("save failed: %+v", r)
	}

	// 落库断言：config 表 prompt-tool_usage_prompt = {v: ...}
	db, err := data.Prj("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	var rec data.Record
	ok, _ := db.Table("config").Get("prompt-tool_usage_prompt", &rec)
	if !ok || rec["v"] != "工具使用说明" {
		t.Fatalf("prompt not persisted: ok=%v rec=%+v", ok, rec)
	}

	// list 剥前缀 → {tool_usage_prompt: value}
	r = dataCall(t, bus, "data-prompt-list", map[string]any{"req_id": "r2", "instance_id": "ins-test"})
	res = dataResult(t, r)
	kv, _ := res["list"].(map[string]any)
	if kv["tool_usage_prompt"] != "工具使用说明" {
		t.Fatalf("prompt list=%+v", kv)
	}

	// load{id=key} → {data: value}
	r = dataCall(t, bus, "data-prompt-load", map[string]any{
		"req_id": "r3", "instance_id": "ins-test", "id": "tool_usage_prompt",
	})
	res = dataResult(t, r)
	if v, _ := res["data"].(string); v != "工具使用说明" {
		t.Fatalf("load data=%+v", res["data"])
	}
}

// TestDataRefreshBroadcast：save/delete 后广播 data-<domain>-refresh（{id, op, list}）。
func TestDataRefreshBroadcast(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	ch := collectRefresh(t, bus, "prj-config")

	dataCall(t, bus, "data-prj-config-save", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"key": "keep_full_turns", "value": "true"},
	})
	select {
	case m := <-ch:
		if m["op"] != "save" || m["id"] != "keep_full_turns" {
			t.Fatalf("refresh=%+v", m)
		}
		if m["instance_id"] != "ins-test" { // 业务 payload 一律必带 instance_id（61 §0.1）
			t.Fatalf("refresh 缺 instance_id: %+v", m)
		}
		if _, has := m["list"]; !has {
			t.Fatalf("refresh missing list: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no refresh broadcast on save")
	}

	dataCall(t, bus, "data-prj-config-delete", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "id": "keep_full_turns",
	})
	select {
	case m := <-ch:
		if m["op"] != "delete" || m["id"] != "keep_full_turns" {
			t.Fatalf("refresh=%+v", m)
		}
		if m["instance_id"] != "ins-test" {
			t.Fatalf("refresh(delete) 缺 instance_id: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no refresh broadcast on delete")
	}
}

// TestDataConfigUnknownInstance：未登记实例 → 错误回复（ok=false + error），不 panic
// （scenario/user-config 为 usr 全局无需实例；用 prj-config-list 覆盖 prj 域）。
func TestDataConfigUnknownInstance(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	r := dataCall(t, bus, "data-prj-config-list", map[string]any{
		"req_id": "r1", "instance_id": "ins-ghost",
	})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("should fail for unknown instance: %+v", r)
	}
	if r["error"] == nil || r["error"] == "" {
		t.Fatalf("missing error: %+v", r)
	}
}

// appScenarioResourceRoot 定位仓库内 **app 级场景资源根** `src/initdata/scenarios`
// （25 §8.2：出厂场景 = app 级；出厂内容 = 磁盘目录（源 = 本目录，由构建脚本投放，不再 embed））。
// 由本测试文件位置反推仓库根（黑盒测试不复制数据层私有路径规则）。
func appScenarioResourceRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// <repo>/src/test/chonkpilot-data/unittest/persist/persist_test.go → <repo>/src/initdata/scenarios
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..",
		"src", "initdata", "scenarios")
	if _, err := os.Stat(filepath.Join(root, scenarioDefaultKey, "scenario.json")); err != nil {
		t.Fatalf("app 级出厂场景资源缺失（%s）：%v", root, err)
	}
	return root
}

// appCapabilityRoot 造一个**临时 app 级场景根**：把仓库内出厂场景
// `<repo>/src/initdata/scenarios/default` 复制到 `<tmp>/scenarios/default`，返回
// `persist.Options.AppDir`（= 系统级 capability 根；场景独立根 `scenarios/` 与其**平级**，25 §6）。
// 只投放出厂场景 `scenarios/default/` → list 计数确定（仓库当前无其它 app 级场景）。
func appCapabilityRoot(t *testing.T) string {
	t.Helper()
	src := filepath.Join(appScenarioResourceRoot(t), scenarioDefaultKey)
	base := t.TempDir()
	appDir := filepath.Join(base, "capability")
	dst := filepath.Join(base, "scenarios", scenarioDefaultKey)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return appDir
}

// TestScenarioDefaultAgentsSeeded（25 §8.2 · 42 §2 (171) 新语义）：出厂场景 = **app 级**
// `scenarios/default/`（出厂内容 = 磁盘目录，源 `src/initdata/scenarios`，由构建脚本投放；
// 原「list 首次 seed 物化到 user 级」已撤）——list 命中 app 级 default：1 条 isMain 主 agent
// （Loop Engineer/主，居首）+ 8 条子 agent，name/description/roleTag 齐备。
func TestScenarioDefaultAgentsSeeded(t *testing.T) {
	bus, _, _ := newTestPersistOpts(t, persist.Options{AppDir: appCapabilityRoot(t)})

	r := dataCall(t, bus, "data-scenario-list", map[string]any{"req_id": "r1"})
	list := dataList(dataResult(t, r))
	if len(list) != 1 {
		t.Fatalf("seed list=%+v（app 级仅投放出厂默认场景）", list)
	}
	def := list[0].(map[string]any)
	if lv := persist.Sval(def["level"]); lv != "app" {
		t.Fatalf("出厂场景应归属 app 级（出厂内容由 embed 物化），实际 level=%q：%+v", lv, def)
	}
	ags, _ := def["agents"].([]any)
	if len(ags) == 0 {
		t.Fatalf("default scenario missing agents: %+v", def)
	}
	if persist.Sval(def["key"]) != scenarioDefaultKey {
		t.Fatalf("seeded key=%v", def["key"])
	}
	first, _ := ags[0].(map[string]any)
	if isMain, _ := first["isMain"].(bool); !isMain {
		t.Fatalf("first agent should be main: %+v", first)
	}
	mainCount := 0
	subs := 0
	for _, a := range ags {
		m, _ := a.(map[string]any)
		if m == nil {
			t.Fatalf("bad agent entry: %+v", a)
		}
		for _, f := range []string{"name", "description", "roleTag"} {
			if s, _ := m[f].(string); s == "" {
				t.Fatalf("agent missing field %s: %+v", f, m)
			}
		}
		if isMain, _ := m["isMain"].(bool); isMain {
			mainCount++
		} else {
			subs++
		}
	}
	if mainCount != 1 {
		t.Fatalf("main agent count=%d want 1", mainCount)
	}
	if persist.Sval(first["name"]) != "Loop Engineer" || persist.Sval(first["roleTag"]) != "主" {
		t.Fatalf("main agent=%+v", first)
	}
	// RB-7（2026-09-22）：主 agent（main.agent.md 内容）含「知识库资产检索」细则 —— mcp_find + mcp_load + 与记忆的区别。
	mp := persist.Sval(first["prompt"])
	if !strings.Contains(mp, "mcp_find") || !strings.Contains(mp, "mcp_load") || !strings.Contains(mp, "记忆") {
		t.Fatalf("主 agent prompt 应含资产检索细则（mcp_find/mcp_load/记忆）: %q", mp)
	}
	if subs != 8 {
		t.Fatalf("sub agent count=%d want 8", subs)
	}
}

// TestScenarioCrossLevelDupRejected（25-MCP与场景分层模型 §6）：场景 id **全局唯一（跨级亦然）**——
//
//	① user 级自建 → 再 save 同名到 project 级 **报错**（无覆盖语义；错误文案指明已存在于哪级）；
//	①b 与 **app 级出厂场景**同名 → save 到 user 级 **报错**（42 §2 (171)④：场景 id 全局唯一，
//	   用户须改用其它 id；app 级本身可编辑，同名编辑应显式 level=app 而非另建副本）；
//	② 同级别同名 → **正常更新**（不报错）；
//	③ save 落**新独立根 `scenarios/`**（与 capability/ 平级）→ list/load 命中新根路径；
//	④ list：id 全局唯一 → 无重名，且出厂场景归属 **app 级**。
func TestScenarioCrossLevelDupRejected(t *testing.T) {
	bus, _, usrPath := newTestPersistOpts(t, persist.Options{AppDir: appCapabilityRoot(t)})
	wd := regInstance(t, bus) // 登记实例 → project 级可见
	usrScen := filepath.Join(filepath.Dir(usrPath), "scenarios")
	prjScen := filepath.Join(wd, ".chonkpilot", "scenarios")

	save := func(reqID, id, level, name string) map[string]any {
		t.Helper()
		return dataCall(t, bus, "data-scenario-save", map[string]any{
			"req_id": reqID,
			"data": map[string]any{
				"id": id, "name": name, "level": level,
				"agents": []any{map[string]any{"name": "主", "isMain": true, "prompt": "p-" + name}},
			},
		})
	}

	// ① user 级先建 → 落新独立根
	if ok, _ := dataResult(t, save("r1", "shared", "user", "用户版"))["ok"].(bool); !ok {
		t.Fatal("save user failed")
	}
	if _, err := os.Stat(filepath.Join(usrScen, "shared", "scenario.json")); err != nil {
		t.Fatalf("user scenario not written under new root: %v", err)
	}

	// ①b 与 app 级出厂场景同名（default）→ save 到 user 级拒绝，且**未落盘**
	dupApp := save("r2", scenarioDefaultKey, "user", "改过的默认")
	if ok, _ := dupApp["ok"].(bool); ok {
		t.Fatalf("app 级同名 save 应失败: %+v", dupApp)
	}
	msgApp, _ := dupApp["error"].(string)
	if !strings.Contains(msgApp, scenarioDefaultKey) || !strings.Contains(msgApp, "app") {
		t.Fatalf("error message should name id + app level: %q", msgApp)
	}
	if _, err := os.Stat(filepath.Join(usrScen, scenarioDefaultKey)); !os.IsNotExist(err) {
		t.Fatal("rejected save must not write user dir")
	}

	// ① project 级同名 → 拒绝（错误文案含 id + 已存在级别），且**未落盘**
	dup := save("r3", "shared", "project", "项目版")
	if ok, _ := dup["ok"].(bool); ok {
		t.Fatalf("cross-level duplicate save should fail: %+v", dup)
	}
	msg, _ := dup["error"].(string)
	if !strings.Contains(msg, "shared") || !strings.Contains(msg, "user") {
		t.Fatalf("error message should name id + existing level: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(prjScen, "shared")); !os.IsNotExist(err) {
		t.Fatal("rejected save must not write project dir")
	}

	// ② 同级别同名 → 正常更新（放行）
	if ok, _ := dataResult(t, save("r4", "shared", "user", "用户版2"))["ok"].(bool); !ok {
		t.Fatal("same-level same-name save should update without error")
	}
	r := dataCall(t, bus, "data-scenario-load", map[string]any{"req_id": "r5", "id": "shared"})
	if d, _ := dataResult(t, r)["data"].(map[string]any); d["name"] != "用户版2" || d["level"] != "user" {
		t.Fatalf("same-level update not applied: %+v", d)
	}

	// ③ 另一 id 写 project 级 → 落 <workdir>/.chonkpilot/scenarios；load 命中
	if ok, _ := dataResult(t, save("r6", "prj-only", "project", "项目版"))["ok"].(bool); !ok {
		t.Fatal("save project failed")
	}
	if _, err := os.Stat(filepath.Join(prjScen, "prj-only", "scenario.json")); err != nil {
		t.Fatalf("project scenario not written under <workdir>/.chonkpilot/scenarios: %v", err)
	}
	r = dataCall(t, bus, "data-scenario-load", map[string]any{"req_id": "r7", "id": "prj-only"})
	if d, _ := dataResult(t, r)["data"].(map[string]any); d["level"] != "project" {
		t.Fatalf("load project scenario=%+v", d)
	}
	// 反向证据：旧位置 capability/prompts 下**不得**出现场景目录
	if _, err := os.Stat(filepath.Join(filepath.Dir(usrPath), "capability", "prompts", "shared")); !os.IsNotExist(err) {
		t.Fatal("scenario must not be written under capability/prompts（旧位置）")
	}

	// ④ list：id 全局唯一 → 无重名；各条级别正确（出厂场景 = app 级）
	r = dataCall(t, bus, "data-scenario-list", map[string]any{"req_id": "r8"})
	levels := map[string]string{}
	for _, e := range dataList(dataResult(t, r)) {
		m, _ := e.(map[string]any)
		k := persist.Sval(m["id"])
		if _, dup := levels[k]; dup {
			t.Fatalf("scenario id 应全局唯一，出现重复: %+v", m)
		}
		levels[k] = persist.Sval(m["level"])
	}
	if levels["shared"] != "user" || levels["prj-only"] != "project" || levels[scenarioDefaultKey] != "app" {
		t.Fatalf("list id→level=%+v", levels)
	}

	// delete project 场景 → ok
	if ok, _ := dataResult(t, dataCall(t, bus, "data-scenario-delete", map[string]any{
		"req_id": "r9", "id": "prj-only", "level": "project",
	}))["ok"].(bool); !ok {
		t.Fatal("delete project scenario failed")
	}
}

// TestScenarioLegacyLocationIgnored（25 §6 · 既有数据不迁移）：旧位置
// `capability/prompts/<场景>/` 不再被扫描 → **不崩**、旧场景 list/load 均不可见；
// 旧目录原样保留（不迁移 / 不删除）；且 list **不向 user 级物化**出厂场景
// （出厂场景 = app 级，出厂内容由 embed 提供，缺失时物化到 app 级，42 §2 (171)）。
func TestScenarioLegacyLocationIgnored(t *testing.T) {
	bus, _, usrPath := newTestPersistOpts(t, persist.Options{AppDir: appCapabilityRoot(t)})
	home := filepath.Dir(usrPath)

	// 旧位置放一个"场景"（scenario.json + main.agent.md）——模拟既有数据
	legacy := filepath.Join(home, "capability", "prompts", "legacy-sc")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "scenario.json"), []byte(`{"name":"旧场景"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "main.agent.md"), []byte("# 旧\n\n[meta]\nismain=true\n\n[content]\n旧提示\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// list：不崩；旧场景**不可见** → 只余 app 级出厂默认场景（新根来源 = 随发布只读资源）
	r := dataCall(t, bus, "data-scenario-list", map[string]any{"req_id": "r1"})
	list := dataList(dataResult(t, r))
	if len(list) != 1 || persist.Sval(list[0].(map[string]any)["id"]) != scenarioDefaultKey {
		t.Fatalf("legacy scenario should be ignored: %+v", list)
	}
	if lv := persist.Sval(list[0].(map[string]any)["level"]); lv != "app" {
		t.Fatalf("出厂默认场景应来自 app 级，实际 level=%q", lv)
	}
	// load 旧 id → 明确失败（按"无场景"降级，不崩）
	r = dataCall(t, bus, "data-scenario-load", map[string]any{"req_id": "r2", "id": "legacy-sc"})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("legacy scenario must not load: %+v", r)
	}
	// 旧位置**原样保留**（不迁移、不删除）
	if _, err := os.Stat(filepath.Join(legacy, "scenario.json")); err != nil {
		t.Fatalf("legacy data must be untouched: %v", err)
	}
	// 反向证据：user 级场景根**不**被物化（原 `materializeDefaultScenario` 已随 T6 撤除）
	if _, err := os.Stat(filepath.Join(home, "scenarios")); !os.IsNotExist(err) {
		t.Fatalf("user 级不应物化场景（list 物化已撤），实际存在: %v", err)
	}
}

// TestScenarioSystemPromptDerived：systemPrompt 是**保留供兼容的派生字段**（= 主 agent 的 prompt；
// 25 §3 起**场景层提示词**由 llm/server 侧自行拼接、不取用本字段）——旧形态（只传 systemPrompt
// 无 agents）save 后落到主 agent，load 回读 systemPrompt 一致（兼容写入/回读路径不变）。
func TestScenarioSystemPromptDerived(t *testing.T) {
	bus, _, _ := newTestPersist(t)

	// 旧形态：只给 systemPrompt
	r := dataCall(t, bus, "data-scenario-save", map[string]any{
		"req_id": "r1",
		"data":   map[string]any{"id": "legacy", "name": "旧形态", "systemPrompt": "旧系统提示"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("save legacy failed: %+v", r)
	}
	r = dataCall(t, bus, "data-scenario-load", map[string]any{"req_id": "r2", "id": "legacy"})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d["systemPrompt"] != "旧系统提示" {
		t.Fatalf("derived systemPrompt=%v", d["systemPrompt"])
	}
	ags, _ := d["agents"].([]any)
	if len(ags) == 0 {
		t.Fatalf("legacy save should materialize main agent: %+v", d)
	}
	if first := ags[0].(map[string]any); first["isMain"] != true || first["prompt"] != "旧系统提示" {
		t.Fatalf("main agent=%+v", first)
	}

	// 默认场景：systemPrompt = main.agent.md 内容（非空）
	r = dataCall(t, bus, "data-scenario-list", map[string]any{"req_id": "r3"})
	for _, e := range dataList(dataResult(t, r)) {
		m, _ := e.(map[string]any)
		if persist.Sval(m["id"]) != "default" {
			continue
		}
		if s, _ := m["systemPrompt"].(string); s == "" {
			t.Fatalf("default scenario systemPrompt should be derived from main agent: %+v", m)
		}
	}
}

// TestUserConfigLegacyMigration：v5 整块 user_config → 首次访问自动拆为逐 key + 专用表
// 并删除整块（12-数据层 迁移）。
func TestUserConfigLegacyMigration(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)

	// 预置 legacy 整块（模拟 v5 数据）
	db, release, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{
		"theme":           "dark",
		"locale":          "en-US",
		"responseTimeout": 120,
		"llms":            []any{map[string]any{"name": "openai", "apiKey": "sk-x"}},
		"mcpServers":      []any{map[string]any{"name": "local", "enabled": true}},
	}
	if err := db.Table("config").Upsert("user_config", data.Record{"v": string(jb(legacy))}); err != nil {
		t.Fatal(err)
	}
	release()

	// 触发 load → 自动迁移
	r := dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r1"})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d["theme"] != "dark" || d["locale"] != "en-US" {
		t.Fatalf("scalars not migrated: %+v", d)
	}
	if v, _ := d["responseTimeout"].(float64); v != 120 {
		t.Fatalf("responseTimeout=%v", d["responseTimeout"])
	}
	if ll, _ := d["llms"].([]any); len(ll) != 1 {
		t.Fatalf("llms=%+v", d["llms"])
	}
	if ms, _ := d["mcpServers"].([]any); len(ms) != 1 {
		t.Fatalf("mcpServers=%+v", d["mcpServers"])
	}

	// 整块已删除；逐 key 与专用表已就位
	db2, release2, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	if _, ok := data.GetConfig(db2, "user_config"); ok {
		t.Fatal("legacy blob should be removed")
	}
	if v, ok := data.GetConfig(db2, "theme"); !ok || v != "dark" {
		t.Fatalf("theme key=%q ok=%v", v, ok)
	}
	if keys, _ := db2.Table("llms").ListKeys(); len(keys) != 1 {
		t.Fatalf("llms table keys=%v", keys)
	}
	if keys, _ := db2.Table("mcps").ListKeys(); len(keys) != 1 {
		t.Fatalf("mcps table keys=%v", keys)
	}
}

// TestUserConfigRefreshBroadcastsConfigRefresh：I-42 —— data-user-config-save/delete 后，
// 除 data-user-config-refresh 外 persist 还广播 config-refresh（前端 ChatPanel 据此刷新 LLM
// 列表；此前全仓无发布方）；payload 必带 instance_id（无归属取空串，61 §0）。
func TestUserConfigRefreshBroadcastsConfigRefresh(t *testing.T) {
	bus, _, _ := newTestPersist(t)

	ch := make(chan map[string]any, 8)
	if _, err := subRaw(bus, "config-refresh", func(_ string, p []byte) {
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

	// save 带归属实例 → config-refresh 携带 instance_id
	r := dataCall(t, bus, "data-user-config-save", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"theme": "dark"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("save failed: %+v", r)
	}
	select {
	case m := <-ch:
		if m["instance_id"] != "ins-test" {
			t.Fatalf("config-refresh instance_id=%v want ins-test", m["instance_id"])
		}
		if m["op"] != "save" {
			t.Fatalf("config-refresh op=%v want save", m["op"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 config-refresh（I-42 发布方缺失）")
	}

	// delete 无归属实例 → config-refresh 仍必带 instance_id（空串）
	r = dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "r2", "id": "user_config"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	select {
	case m := <-ch:
		if v, ok := m["instance_id"]; !ok || v != "" {
			t.Fatalf("config-refresh instance_id 必带（空串）: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 delete 的 config-refresh")
	}
}

// TestDataUserConfigCCompilerPathRoundTrip（P0-1）：① 经消息写规范键 cCompilerPath → 落库并回读
// 一致；② 存量旧键 cPath（旧前端 / 旧数据）经消息入口一次性迁移为新键（旧键消失、新键有值）；
// ③ 旧键 delete 请求 → 迁移后按新键删该项（不直接拒）；合法键 delete 仍只删该项。
func TestDataUserConfigCCompilerPathRoundTrip(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	const cVal = `C:\msys64\ucrt64\bin\gcc.exe`

	// ① save cCompilerPath → 回读一致（前端 SettingsPathsPage C/C++ 行）
	r := dataCall(t, bus, "data-user-config-save", map[string]any{
		"req_id": "r1", "data": map[string]any{"cCompilerPath": cVal},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("save cCompilerPath failed: %+v", r)
	}
	db, release, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := data.GetConfig(db, "cCompilerPath"); !ok || got != cVal {
		t.Fatalf("cCompilerPath 未落库：ok=%v v=%q", ok, got)
	}
	release()
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r2"})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if v, _ := d["cCompilerPath"].(string); v != cVal {
		t.Fatalf("load cCompilerPath=%v want %q", d["cCompilerPath"], cVal)
	}

	// 先删新键（合法键 delete = 只删该项），再写存量旧键 cPath → 入口迁移后新键取旧值
	r = dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "r3", "id": "cCompilerPath"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete cCompilerPath failed: %+v", r)
	}
	const legacyVal = `C:\legacy\gcc.exe`
	db2, release2, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(db2, "cPath", legacyVal); err != nil {
		t.Fatal(err)
	}
	release2()

	// ② 旧键迁移：新键有值、旧键清除
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r4"})
	d, _ = dataResult(t, r)["data"].(map[string]any)
	if v, _ := d["cCompilerPath"].(string); v != legacyVal {
		t.Fatalf("旧键 cPath 未迁移：cCompilerPath=%v want %q", d["cCompilerPath"], legacyVal)
	}
	db3, release3, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data.GetConfig(db3, "cPath"); ok {
		t.Fatal("迁移后旧键 cPath 应已清除")
	}
	release3()

	// ③ 旧键 delete 请求（旧前端仍可能发）→ 迁移后按新键删该项（不直接拒）
	db4, release4, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(db4, "cPath", `C:\legacy2\gcc.exe`); err != nil {
		t.Fatal(err)
	}
	release4()
	r = dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "r5", "id": "cPath"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("旧键 delete 应迁移后按新键删除: %+v", r)
	}
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r6"})
	d, _ = dataResult(t, r)["data"].(map[string]any)
	if v, _ := d["cCompilerPath"].(string); v != "" {
		t.Fatalf("delete 后 cCompilerPath=%q want 空串", v)
	}
}

// TestDataUserConfigUnknownKeyDeleteErrors（P0-2）：未知键 delete → ok:false + 明确错误，且**不得**
// 回落「清空整份用户配置」——theme/locale/llms/mcpServers/超时重试四项/自由键等既有配置全部完好。
func TestDataUserConfigUnknownKeyDeleteErrors(t *testing.T) {
	bus, _, _ := newTestPersist(t)

	// 预置多类型既有配置：标量（字符串 + int）、集合 llms/mcpServers、自由键 recent_dirs
	r := dataCall(t, bus, "data-user-config-save", map[string]any{"req_id": "r1", "data": map[string]any{
		"theme": "dark", "locale": "en-US", "defaultScenario": "s1",
		"responseTimeout": 300, "streamTimeout": 30, "retryCount": 5, "retryDelay": 9,
		"llms": []any{map[string]any{
			"name": "openai", "protocol": "openai", "apiKey": "sk-x", "model": "gpt-4",
			"baseUrl": "http://127.0.0.1:8901/v1",
		}},
		"mcpServers":  []any{map[string]any{"name": "fs", "command": "node"}},
		"recent_dirs": `["C:/ws/a"]`,
	}})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("seed save failed: %+v", r)
	}

	// 未知键（旧前端错拼键名风格）delete → ok:false + 明确错误（fail 回复无 result 字段）
	r = dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "r2", "id": "cPathTypo"})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("未知键 delete 不应成功: %+v", r)
	}
	if msg, _ := r["error"].(string); !strings.Contains(msg, "unknown config key: cPathTypo") {
		t.Fatalf("错误信息=%q want 含 unknown config key: cPathTypo", msg)
	}

	// 回归断言：整份配置未被清空（list 仍单条）且各既有键全部完好
	r = dataCall(t, bus, "data-user-config-list", map[string]any{"req_id": "r3"})
	if n := len(dataList(dataResult(t, r))); n != 1 {
		t.Fatalf("未知键 delete 后 list=%d（疑似整份配置被清空）", n)
	}
	r = dataCall(t, bus, "data-user-config-load", map[string]any{"req_id": "r4"})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d["theme"] != "dark" || d["locale"] != "en-US" || d["defaultScenario"] != "s1" {
		t.Fatalf("既有标量键被清：theme=%v locale=%v defaultScenario=%v", d["theme"], d["locale"], d["defaultScenario"])
	}
	for k, want := range map[string]float64{"responseTimeout": 300, "streamTimeout": 30, "retryCount": 5, "retryDelay": 9} {
		if v, _ := d[k].(float64); v != want {
			t.Fatalf("既有超时/重试键被清：%s=%v want %v", k, d[k], want)
		}
	}
	if ll, _ := d["llms"].([]any); len(ll) != 1 {
		t.Fatalf("llms 被清空：%+v", d["llms"])
	}
	if ms, _ := d["mcpServers"].([]any); len(ms) != 1 {
		t.Fatalf("mcpServers 被清空：%+v", d["mcpServers"])
	}
	if v, _ := d["recent_dirs"].(string); v != `["C:/ws/a"]` {
		t.Fatalf("自由键 recent_dirs 被清：%v", d["recent_dirs"])
	}
}
