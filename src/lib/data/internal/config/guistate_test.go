// guistate_test.go — 个人运行态 key 分层路由白盒：哪些 prj-config 键落 prjusr（本机本项目）。
//
// 覆盖：window/layout/filetree-*/opened-*/codegraph.status/vfts.status + 会话级前缀
// history.status.* / history.timeline.* 归入 prjusr；其余团队共享键仍落 prj。
// （注：记忆提取进度自 2026-10-06 起改**专用表** `memory_extract`，不再走 config 键路由。）
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
		{"codegraph.status", true},
		{"vfts.status", true},
		{"history.status.s-root", true},
		{"history.timeline.s-root", true},
		// 团队共享配置仍落 prj（不得被前缀误伤）。
		{"memory.enabled", false},
		{"memory.min-turn-tokens", false},
		{"history.enabled", false},
		{"keep_full_max_turns", false},
	}
	for _, c := range cases {
		if got := isLocalRuntimeKey(c.key); got != c.want {
			t.Fatalf("isLocalRuntimeKey(%q)=%v want %v", c.key, got, c.want)
		}
	}
}
