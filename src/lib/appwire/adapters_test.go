package appwire

import (
	"sort"
	"strings"
	"testing"
)

// TestDefaultPluginsSingleSource：内嵌插件清单**只剩 codegraph / vfts**。
//
// 2026-10-24：memory（记忆沉淀）与 history（文件检查点链）改为独立 exe 插件
// （src/plugins/memory · src/plugins/history，manifest 见
// src/initdata/capability/plugins/<id>/plugin.json），宿主**不再编译期持有**它们
// —— 避免「内嵌 + 外挂」双跑（双打点 / 双沉淀）。本断言防回归。
func TestDefaultPluginsSingleSource(t *testing.T) {
	var names []string
	for _, h := range DefaultPlugins(nil) {
		names = append(names, h.Name())
	}
	sort.Strings(names)
	got := strings.Join(names, ",")
	if got != "codegraph,vfts" {
		t.Fatalf("内嵌插件清单应只剩 codegraph,vfts（memory/history 已外挂），got=%s", got)
	}
}
