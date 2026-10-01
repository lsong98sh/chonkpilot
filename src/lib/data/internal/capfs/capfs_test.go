// 智能体「工具级别矩阵」+ capability 节点名→级别映射单测（25-MCP与场景分层模型 §4；
// P4 2026-10-01）。矩阵 = 编辑期（前端 AGENT_LEVEL_MATRIX）与运行期（llm-server 白名单
// 静默剔除）**共用单源**；前端守卫测试 scenarioToolMatrix.test.js 直接比对本文件字面量。
package capfs

import (
	"reflect"
	"testing"
)

// TestAgentToolLevels 四级矩阵逐项（prjusr→四级全可用 / project→{project,app} /
// user→{user,app} / app→{app}；未知 kind → 用户级集合）。
func TestAgentToolLevels(t *testing.T) {
	cases := []struct {
		kind string
		want []string
	}{
		{KindPrjUsr, []string{"prjusr", "user", "project", "app"}},
		{KindProject, []string{"project", "app"}},
		{KindUser, []string{"user", "app"}},
		{KindApp, []string{"app"}},
		{"", []string{"user", "app"}},      // 未知 → 用户级默认（与前端一致）
		{"ghost", []string{"user", "app"}}, // 未知 → 用户级默认
	}
	for _, c := range cases {
		if got := AgentToolLevels(c.kind); !reflect.DeepEqual(got, c.want) {
			t.Errorf("AgentToolLevels(%q) = %v, want %v", c.kind, got, c.want)
		}
	}
}

// TestLevelAllowed 矩阵判定：同级或更高级放行；越权拒绝；level 空 → 放行（保守）。
func TestLevelAllowed(t *testing.T) {
	// 同级 / 更高级 → true
	allow := []struct{ kind, level string }{
		{KindPrjUsr, KindPrjUsr}, {KindPrjUsr, KindUser}, {KindPrjUsr, KindProject}, {KindPrjUsr, KindApp},
		{KindProject, KindProject}, {KindProject, KindApp},
		{KindUser, KindUser}, {KindUser, KindApp},
		{KindApp, KindApp},
	}
	for _, c := range allow {
		if !LevelAllowed(c.kind, c.level) {
			t.Errorf("LevelAllowed(%q,%q) 应放行", c.kind, c.level)
		}
	}
	// 越权 → false
	deny := []struct{ kind, level string }{
		{KindProject, KindUser}, {KindProject, KindPrjUsr},
		{KindUser, KindProject}, {KindUser, KindPrjUsr},
		{KindApp, KindUser}, {KindApp, KindProject}, {KindApp, KindPrjUsr},
	}
	for _, c := range deny {
		if LevelAllowed(c.kind, c.level) {
			t.Errorf("LevelAllowed(%q,%q) 应拒绝（越权）", c.kind, c.level)
		}
	}
	// 工具级别无法判定（空）→ 放行（不误剔除）
	for _, k := range []string{KindApp, KindUser, KindProject, KindPrjUsr} {
		if !LevelAllowed(k, "") {
			t.Errorf("LevelAllowed(%q,\"\") 未知级别应放行", k)
		}
	}
}

// TestLevelOfNode capability dir 节点名 → 级别映射；无法判定 → ""（放行）。
func TestLevelOfNode(t *testing.T) {
	cases := []struct{ node, want string }{
		{"self", KindApp},
		{"ins-1-user", KindUser},
		{"ins-1-project", KindProject},
		{"ins-1-prjusr", KindPrjUsr},
		{"ins-with-dash-user", KindUser}, // instanceID 含连字符仍按后缀判定（末段）
		{"", ""},                         // 空 → 无法判定
		{"some-third-party", ""},         // 第三方节点 → 无法判定（放行）
		{"project", ""},                  // 裸后缀（非 dir 节点名）→ 无法判定
	}
	for _, c := range cases {
		if got := LevelOfNode(c.node); got != c.want {
			t.Errorf("LevelOfNode(%q) = %q, want %q", c.node, got, c.want)
		}
	}
}
