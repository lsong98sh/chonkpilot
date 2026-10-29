// guistate_test.go — 个人运行态 key 分层路由白盒：哪些 prj-config 键落 prjusr（本机本项目）。
//
// 覆盖：window/layout/filetree-*/opened-* + 会话级前缀 history.status.* / history.timeline.* 归入 prjusr；
// 其余团队共享键仍落 prj。**插件业务运行态不在白名单**（PLUGIN-UI-2：`codegraph.status` / `vfts.status`
// 已随插件自持 UI 退场，见 49 §2.9）。
package config

import "testing"

// TestIsLocalRuntimeKey 逐类断言路由判据：精确名 / window.*·layout.* / 会话级前缀。
func TestIsLocalRuntimeKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"layout", true},
		{"window", true},
		{"window.0", true},
		{"opened-files", true},
		{"history.status.s-root", true},
		{"history.timeline.s-root", true},
		// 团队共享配置仍落 prj（不得被前缀误伤）。
		{"memory.enabled", false},
		{"memory.min-turn-tokens", false},
		{"history.enabled", false},
		{"keep_full_max_turns", false},
		// 插件业务运行态：不再由宿主路由（PLUGIN-UI-2）。
		{"codegraph.status", false},
		{"vfts.status", false},
	}
	for _, c := range cases {
		if got := isLocalRuntimeKey(c.key); got != c.want {
			t.Fatalf("isLocalRuntimeKey(%q)=%v want %v", c.key, got, c.want)
		}
	}
}
