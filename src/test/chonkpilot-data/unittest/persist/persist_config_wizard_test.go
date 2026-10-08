// 「场景向导」辅助选项配置的 L2 黑盒测试：向导生成时经门面 ConfigKVSet 写 prj-config，
//   - 写入成功后广播 data-prj-config-refresh（订阅面；一次批量 = 1 条，载荷带 ids 全组键）；
//   - 写入的配置可经 ConfigKVGet / ConfigKVList 原样读回（= 确证「写入的配置被使用」）。
//
// 复用本包既有宿主 helper（newTestPersist / regInstance / collectRefresh / waitRefresh /
// idsOf）+ inline 门面绑定。
package persist_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
)

// TestConfigKVSetPrjConfigRefreshAndReadback：向导写入的 prj-config 辅助选项
//（memory.enabled / memory.category.<类> / enable-codegraph / enable-vfts / history.enabled）
// 批量落库 + 广播 1 条 data-prj-config-refresh + 键值可读回。
func TestConfigKVSetPrjConfigRefreshAndReadback(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	keys := []string{
		"memory.enabled",
		"memory.category.用户偏好",
		"enable-codegraph",
		"enable-vfts",
		"history.enabled",
	}
	entries := map[string]string{}
	for _, k := range keys {
		entries[k] = "true"
	}

	ch := collectRefresh(t, bus, "prj-config")
	resp, err := api.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance, Entries: entries,
	})
	if err != nil {
		t.Fatalf("ConfigKVSet(prj-config): %v", err)
	}
	if !resp.OK {
		t.Fatalf("ConfigKVSet 应 OK=true：%+v", resp)
	}

	// 刷新广播：1 条，op=save，带写入实例 id，批量带 ids 全组键（id = 字典序首键）。
	ev := waitRefresh(t, ch, "prj-config")
	if ev["op"] != "save" || ev["instance_id"] != facadeInstance {
		t.Fatalf("刷新广播字段不符：%+v", ev)
	}
	ids := idsOf(ev)
	if len(ids) != len(keys) {
		t.Fatalf("批量刷新应带全部 %d 键 ids：%+v", len(keys), ev["ids"])
	}
	if ids[0] != "enable-codegraph" || ev["id"] != "enable-codegraph" {
		t.Fatalf("批量刷新 id/ids[0] 应为字典序首键 enable-codegraph：id=%v ids=%v", ev["id"], ids)
	}

	// 读回（按最差绑定设计的批量按键读）：写入的键全部可读回且值为 "true"。
	got, err := api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance, Keys: keys,
	})
	if err != nil {
		t.Fatalf("ConfigKVGet(prj-config): %v", err)
	}
	for _, k := range keys {
		if got.Values[k] != "true" {
			t.Fatalf("键 %q 读回=%q want true（写入的配置未被使用）", k, got.Values[k])
		}
	}

	// 全表读：同源可见（prj + prjusr 合并）。
	list, err := api.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjConfig, InstanceID: facadeInstance,
	})
	if err != nil {
		t.Fatalf("ConfigKVList(prj-config): %v", err)
	}
	for _, k := range keys {
		if list.List[k] != "true" {
			t.Fatalf("list 中键 %q=%q want true", k, list.List[k])
		}
	}
}
