package appwire

import "testing"

// TestDefaultPluginsEmpty：内嵌插件（宿主编译期 Hook）**全部退场**，DefaultPlugins 必须为空。
//
// 演进口径：
//   - 2026-10-24：memory / history 改为独立 exe 插件（外挂，manifest 见
//     src/initdata/capability/plugins/<id>/plugin.json）→ 宿主不再编译期持有；
//   - 2026-10-25 D-71：codegraph / vfts 改为**进程外插件**（chonkpilot-plugins 仓，插件进程内
//     自管索引）→ 内嵌 hook 与插件双写同一索引库（覆盖配置 / .tmp rename 冲突 / zvec 锁），
//     `appwire.DefaultPlugins` 返回 nil；
//   - compress：压缩已内建到会话侧（`llm/server/compress.go` + `chonkpilot-session`），不再有插件形态。
//
// 本断言防「内嵌 + 外挂」双跑回归。
func TestDefaultPluginsEmpty(t *testing.T) {
	if hs := DefaultPlugins(nil); len(hs) != 0 {
		names := make([]string, 0, len(hs))
		for _, h := range hs {
			names = append(names, h.Name())
		}
		t.Fatalf("内嵌插件应全部退场（DefaultPlugins=nil），got=%v", names)
	}
}
