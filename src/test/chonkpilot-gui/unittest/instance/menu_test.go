// menu_test.go — 托盘「列举实例」菜单项过滤的**纯逻辑**验证（🆕 2026-10-10；61 §4.1 ⑥ · 62 §3A）。
//
// 收录规则（instance.MenuInstances）：
//   - 仅顶层实例（子窗口实例不进菜单）；
//   - 排除 browser 形态（判据 = 不持 UDS 前门连接；flash 经 UDS 前门接入）；
//   - 仅收录 user == 当前用户的实例。
//
// 连接判据 = 注入的只读谓词（生产 = udsapi.Gateway.Connected）；本测用内存桩替代。
// 托盘 Win32 本体不做自动化（按既有约定），由 go build / go vet 保证。
package instance_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// hasID 判定结果是否含指定 id（保持顺序无关的断言辅助）。
func hasID(list []instance.Info, id string) bool {
	for _, in := range list {
		if in.ID == id {
			return true
		}
	}
	return false
}

// TestMenuInstancesFiltersBrowserOtherUserAndChildren：只保留 顶层 + 本用户 + 持连接 的实例。
func TestMenuInstancesFiltersBrowserOtherUserAndChildren(t *testing.T) {
	list := []instance.Info{
		{ID: "flash-mine", User: "alice"},                               // ✅ 顶层 + 本用户 + flash（有连接）
		{ID: "browser-mine", User: "alice"},                             // ✖ browser（无 UDS 连接）
		{ID: "flash-other", User: "bob"},                                // ✖ 跨用户
		{ID: "child-mine", User: "alice", ParentInstance: "flash-mine"}, // ✖ 子窗口实例
		{ID: "flash-mine-2", User: "alice"},                             // ✅
	}
	connected := map[string]bool{"flash-mine": true, "flash-mine-2": true, "child-mine": true}
	got := instance.MenuInstances(list, "alice", func(id string) bool { return connected[id] })

	if len(got) != 2 || !hasID(got, "flash-mine") || !hasID(got, "flash-mine-2") {
		t.Fatalf("应仅保留本用户 flash 顶层实例，实际 %+v", got)
	}
	if hasID(got, "browser-mine") {
		t.Fatal("browser 形态（无 UDS 连接）不应入菜单")
	}
	if hasID(got, "flash-other") {
		t.Fatal("跨用户实例不应入菜单")
	}
	if hasID(got, "child-mine") {
		t.Fatal("子窗口实例不应入菜单")
	}
}

// TestMenuInstancesPreservesOrder：结果保持入参顺序（调用方可自行排序）。
func TestMenuInstancesPreservesOrder(t *testing.T) {
	list := []instance.Info{
		{ID: "a", User: "alice"},
		{ID: "b", User: "alice"},
		{ID: "c", User: "alice"},
	}
	got := instance.MenuInstances(list, "alice", func(string) bool { return true })
	if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("结果顺序应与入参一致，实际 %+v", got)
	}
}

// TestMenuInstancesNilConnected：连接判据为 nil → 视为无任何连接（返回空）。
func TestMenuInstancesNilConnected(t *testing.T) {
	list := []instance.Info{{ID: "a", User: "alice"}}
	if got := instance.MenuInstances(list, "alice", nil); len(got) != 0 {
		t.Fatalf("connected=nil 应返回空，实际 %+v", got)
	}
}

// TestMenuInstancesUserMatchTrimFold：用户比对去空白 + 大小写不敏感（Windows 用户名语义），
// 并对齐 udsapi 的跨用户判定口径。
func TestMenuInstancesUserMatchTrimFold(t *testing.T) {
	list := []instance.Info{{ID: "a", User: " Alice "}, {ID: "b", User: "ALICE"}}
	got := instance.MenuInstances(list, "alice", func(string) bool { return true })
	if len(got) != 2 {
		t.Fatalf("去空白 + 大小写不敏感后应全部命中，实际 %+v", got)
	}
}

// TestMenuInstancesEmptyResultNotNil：无命中 → 返回空切片（非 nil），供「（无实例）」置灰项判定。
func TestMenuInstancesEmptyResultNotNil(t *testing.T) {
	got := instance.MenuInstances([]instance.Info{{ID: "b", User: "bob"}}, "alice", func(string) bool { return true })
	if got == nil {
		t.Fatal("无命中应返回空切片，而非 nil")
	}
	if len(got) != 0 {
		t.Fatalf("无命中应为空，实际 %+v", got)
	}
}
