// prj-config 域的分层路由测试（12-数据层）：
// 个人运行态 key（window/layout/filetree-*/opened-*）落 prjusr；团队配置 key 仍落 prj；
// load 时 prjusr 优先、回落 prj；list 合并两层（prjusr 覆盖）。
package persist_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func TestPrjConfigGuiStateRouting(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	save := func(t *testing.T, key, value string) {
		t.Helper()
		r := dataCall(t, bus, "data-prj-config-save", map[string]any{
			"req_id":      "s-" + key,
			"instance_id": "ins-test",
			"data":        map[string]any{"key": key, "value": value},
		})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("save %s failed: %+v", key, r)
		}
	}

	// 个人运行态键 → prjusr；团队配置键 → prj
	save(t, "layout", `{"filetreeWidth":300}`)
	save(t, "opened-files", `["a.txt"]`)
	save(t, "codegraph.status", `{"state":"ready"}`)
	save(t, "history.enabled", "true")

	prj, releasePrj, err := data.Prj("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	defer releasePrj() // 短开（D-45）：用完即释
	pudb, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := data.GetConfig(pudb, "layout"); !ok {
		t.Fatal("layout should be in prjusr")
	}
	if _, ok := data.GetConfig(prj, "layout"); ok {
		t.Fatal("layout must NOT leak into team-shared prj db")
	}
	if _, ok := data.GetConfig(pudb, "codegraph.status"); !ok {
		t.Fatal("codegraph.status should be in prjusr")
	}
	if _, ok := data.GetConfig(prj, "codegraph.status"); ok {
		t.Fatal("codegraph.status must NOT leak into team-shared prj db")
	}
	if v, ok := data.GetConfig(prj, "history.enabled"); !ok || v != "true" {
		t.Fatalf("history.enabled should stay in prj, got %q ok=%v", v, ok)
	}
	if _, ok := data.GetConfig(pudb, "history.enabled"); ok {
		t.Fatal("team key must NOT go to prjusr")
	}

	// load：prjusr 优先
	r := dataCall(t, bus, "data-prj-config-load", map[string]any{
		"req_id": "l1", "instance_id": "ins-test", "id": "layout",
	})
	if v, _ := dataResult(t, r)["data"].(string); v != `{"filetreeWidth":300}` {
		t.Fatalf("load layout = %q", v)
	}

	// prj 侧放一份团队预设 window（细 key 形态）→ prjusr 未设时回落 prj
	if err := data.SetConfig(prj, "window.width", "1280"); err != nil {
		t.Fatal(err)
	}
	r = dataCall(t, bus, "data-prj-config-load", map[string]any{
		"req_id": "l2", "instance_id": "ins-test", "id": "window.width",
	})
	if v, _ := dataResult(t, r)["data"].(string); v != "1280" {
		t.Fatalf("fallback prj window.width = %q", v)
	}
	// prjusr 覆盖后以 prjusr 为准
	save(t, "window.width", "1920")
	r = dataCall(t, bus, "data-prj-config-load", map[string]any{
		"req_id": "l3", "instance_id": "ins-test", "id": "window.width",
	})
	if v, _ := dataResult(t, r)["data"].(string); v != "1920" {
		t.Fatalf("prjusr override window.width = %q", v)
	}

	// list 合并两层
	r = dataCall(t, bus, "data-prj-config-list", map[string]any{
		"req_id": "l4", "instance_id": "ins-test",
	})
	list, _ := dataResult(t, r)["list"].(map[string]any)
	for _, k := range []string{"layout", "opened-files", "history.enabled", "window.width"} {
		if _, ok := list[k]; !ok {
			t.Fatalf("list missing %s: %+v", k, list)
		}
	}
	if list["window.width"] != "1920" {
		t.Fatalf("list window.width = %v", list["window.width"])
	}

	// delete 两层同删
	r = dataCall(t, bus, "data-prj-config-delete", map[string]any{
		"req_id": "d1", "instance_id": "ins-test", "id": "window.width",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	if _, ok := data.GetConfig(pudb, "window.width"); ok {
		t.Fatal("prjusr window.width should be deleted")
	}
	if _, ok := data.GetConfig(prj, "window.width"); ok {
		t.Fatal("prj window.width should be deleted")
	}
}

// TestConfigLevelPrjOverUsr：项目层优先（02-配置层级 §3 读序 prjusr → prj → usr → 系统默认）：
// ① prj 设 defaultScenario（usr 有不同值）→ 取 prj；
// ② prj 未设 → 回落 usr；
// ③ prjusr/prj/usr 均未设 → 系统默认（""）；
// ④ 局部运行态 key（isLocalRuntimeKey）不被同名 usr 值污染，非局部 key 走 usr 兜底。
func TestConfigLevelPrjOverUsr(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	regInstance(t, bus)

	pudb, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	usr, release, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	savePrj := func(key, value string) {
		t.Helper()
		r := dataCall(t, bus, "data-prj-config-save", map[string]any{
			"req_id": "s-" + key, "instance_id": "ins-test",
			"data": map[string]any{"key": key, "value": value},
		})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("prj save %s failed: %+v", key, r)
		}
	}
	delPrj := func(key string) {
		t.Helper()
		r := dataCall(t, bus, "data-prj-config-delete", map[string]any{
			"req_id": "d-" + key, "instance_id": "ins-test", "id": key,
		})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("prj delete %s failed: %+v", key, r)
		}
	}
	loadUserConfig := func() map[string]any {
		t.Helper()
		r := dataCall(t, bus, "data-user-config-load", map[string]any{
			"req_id": "l-uc", "instance_id": "ins-test",
		})
		d, _ := dataResult(t, r)["data"].(map[string]any)
		return d
	}
	loadPrjConfig := func(key string) string {
		t.Helper()
		r := dataCall(t, bus, "data-prj-config-load", map[string]any{
			"req_id": "l-" + key, "instance_id": "ins-test", "id": key,
		})
		v, _ := dataResult(t, r)["data"].(string)
		return v
	}

	// usr 基线：defaultScenario=usr-sc、defaultLLM=1
	r := dataCall(t, bus, "data-user-config-save", map[string]any{
		"req_id": "u1", "data": map[string]any{"defaultScenario": "usr-sc", "defaultLLM": 1},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("usr save failed: %+v", r)
	}

	// ① prj 覆盖 usr
	savePrj("defaultScenario", "prj-sc")
	savePrj("defaultLLM", "0")
	d := loadUserConfig()
	if d["defaultScenario"] != "prj-sc" {
		t.Fatalf("① defaultScenario=%v want prj-sc", d["defaultScenario"])
	}
	if v, _ := d["defaultLLM"].(float64); v != 0 {
		t.Fatalf("① defaultLLM=%v want 0", d["defaultLLM"])
	}

	// prjusr 比 prj 更优先（本机本项目）
	if err := data.SetConfig(pudb, "defaultScenario", "pudb-sc"); err != nil {
		t.Fatal(err)
	}
	if d := loadUserConfig(); d["defaultScenario"] != "pudb-sc" {
		t.Fatalf("prjusr 优先 defaultScenario=%v want pudb-sc", d["defaultScenario"])
	}
	if err := data.DeleteConfig(pudb, "defaultScenario"); err != nil {
		t.Fatal(err)
	}

	// ② prj 未设 → 回落 usr
	delPrj("defaultScenario")
	if d := loadUserConfig(); d["defaultScenario"] != "usr-sc" {
		t.Fatalf("② defaultScenario=%v want usr-sc", d["defaultScenario"])
	}
	delPrj("defaultLLM")
	if v, _ := loadUserConfig()["defaultLLM"].(float64); v != 1 {
		t.Fatalf("② defaultLLM=%v want 1", v)
	}

	// ③ 两级均未设 → 系统默认（""）
	r = dataCall(t, bus, "data-user-config-delete", map[string]any{"req_id": "u2", "id": "defaultScenario"})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("usr delete defaultScenario failed: %+v", r)
	}
	if d := loadUserConfig(); d["defaultScenario"] != "" {
		t.Fatalf("③ defaultScenario=%v want empty", d["defaultScenario"])
	}

	// ④ 局部运行态 key 不被 usr 值污染；非局部 key 走 usr 兜底
	if err := data.SetConfig(usr, "window.width", "9999"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(usr, "logLevel", "debug"); err != nil {
		t.Fatal(err)
	}
	if v := loadPrjConfig("window.width"); v != "" {
		t.Fatalf("④ 局部运行态 key 被 usr 污染: %q", v)
	}
	if v := loadPrjConfig("logLevel"); v != "debug" {
		t.Fatalf("④ 非局部 key usr 兜底=%q want debug", v)
	}
	savePrj("window.width", "1280") // prj 预设仍优先于 usr
	if v := loadPrjConfig("window.width"); v != "1280" {
		t.Fatalf("④ 局部运行态 key prj 预设=%q want 1280", v)
	}
}
