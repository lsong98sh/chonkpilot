// 智能体「工具级别矩阵」+ capability 节点名→级别映射单测（25-MCP与场景分层模型 §4；
// P4 2026-10-01）。矩阵 = 编辑期（前端 AGENT_LEVEL_MATRIX）与运行期（llm-server 白名单
// 静默剔除）**共用单源**；前端守卫测试 scenarioToolMatrix.test.js 直接比对本文件字面量。
package capfs

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestMcpRoots 四级 MCP 配置根（app/user/project/prjusr；与场景根同构 = <级根>/capability/mcps）：
// 四级齐备时全含；workDir/prjUsrCap 缺失时对应级被剔除。
func TestMcpRoots(t *testing.T) {
	appRoot := filepath.Join("a", "capability") // = 系统级 capability 根（AppDir）
	usrPath := filepath.Join("u", "usr.db")
	workDir := filepath.Join("w", "proj")
	prjUsrCap := filepath.Join("d", "cap")
	levels := McpRoots(appRoot, usrPath, workDir, prjUsrCap)
	want := []Level{
		{Kind: KindApp, Root: filepath.Join(appRoot, DirMcps)},
		{Kind: KindUser, Root: filepath.Join("u", "capability", DirMcps)},
		{Kind: KindProject, Root: filepath.Join(workDir, ".chonkpilot", "capability", DirMcps)},
		{Kind: KindPrjUsr, Root: filepath.Join(prjUsrCap, DirMcps)},
	}
	if !reflect.DeepEqual(levels, want) {
		t.Fatalf("McpRoots 四级根不符：\n got=%+v\nwant=%+v", levels, want)
	}
	// workDir / prjUsrCap 缺失 → 只余 app + user
	got := McpRoots(appRoot, usrPath, "", "")
	if len(got) != 2 || got[0].Kind != KindApp || got[1].Kind != KindUser {
		t.Fatalf("缺 workDir/prjUsrCap 时应只余 app+user：%+v", got)
	}
}

// TestValidMcpName server 名合法性（字母开头，仅字母/数字/下划线）。
func TestValidMcpName(t *testing.T) {
	ok := []string{"a", "abc", "a_b", "My_Server1"}
	for _, n := range ok {
		if !ValidMcpName(n) {
			t.Errorf("ValidMcpName(%q) 应为真", n)
		}
	}
	bad := []string{"", "1abc", "a-b", "a b", "a/b", "名字", "a.b"}
	for _, n := range bad {
		if ValidMcpName(n) {
			t.Errorf("ValidMcpName(%q) 应为假", n)
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

// TestPreviewDescriptionTolerant 列表描述预览（req4/req5）：`.md` 取契约摘要首行；非 md 只读头部
// 前缀取文本行——含 NUL / 非合法 UTF-8 → 空串（二进制），且**不整读大文件**（超 MaxDocReadBytes
// 的非 md 文件仍能出描述，不得因整读拒读）。
func TestPreviewDescriptionTolerant(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// 契约 md → 摘要首行
	contractDoc := Template("resource", "note")
	contractDoc.Description = "摘要首行\n第二行"
	if got, _ := PreviewDescription(write("note.resource.md", []byte(BuildDoc(contractDoc)))); got != "摘要首行" {
		t.Fatalf("md 契约摘要首行 = %q，want 摘要首行", got)
	}
	// 非 md 文本 → 首个文本行
	if got, _ := PreviewDescription(write("config.json", []byte("{\"a\":1}\nsecond\n"))); got != `{"a":1}` {
		t.Fatalf("json 文本行 = %q，want {\"a\":1}", got)
	}
	// 非 md 二进制（含 NUL）→ 空串（不炸）
	if got, _ := PreviewDescription(write("logo.png", []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a, 0xff})); got != "" {
		t.Fatalf("二进制描述应为空串，实得 %q", got)
	}
	// 非 md 非法 UTF-8 → 空串
	if got, _ := PreviewDescription(write("bad.bin", []byte{0xff, 0xfe, 'a', 'b'})); got != "" {
		t.Fatalf("非法 UTF-8 描述应为空串，实得 %q", got)
	}
	// 超上限大文件（非 md）→ 不整读，前缀首行仍可出
	big := append([]byte("hello world\n"), bytes.Repeat([]byte("y"), MaxDocReadBytes+1024)...)
	if got, err := PreviewDescription(write("huge.log", big)); err != nil || got != "hello world" {
		t.Fatalf("大非 md 文件应按前缀出描述（不整读），实得 (%q, %v)", got, err)
	}
	// 长行截断（> 60 字节 + …）
	long := strings.Repeat("x", 80)
	if got, _ := PreviewDescription(write("long.txt", []byte(long+"\n"))); got != long[:60]+"…" {
		t.Fatalf("长行应截断为 60 字符 + …，实得 %q", got)
	}
}
