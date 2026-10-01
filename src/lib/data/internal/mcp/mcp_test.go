// mcp 域门面实现白盒（12-数据层 · 25-MCP与场景分层模型 §4）：
//   - 文件落点：保存到某级 → `<级别根>/capability/mcps/<名>.json`（app/user/project 三级各测）；
//   - **同名跨级 = 最具体级优先、整条覆盖**：同名在 app/user/project 三级并存 → list 只余一条
//     （命中 project），且 URL/描述等字段**整条**取自最具体级（非字段级合并）；
//   - get / delete 按级别定位；删除后文件消失；
//   - 改名：save 携带 OldName → 旧文件被移除，不残留；
//   - 名非法（含路径分隔符 / 数字开头）→ 拒绝且不落盘。
package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// testService 造 app 级 capability 根 + 隔离的 usr 库路径；返回 (服务, app 级 capability 根, usr 路径)。
func testService(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	appRoot := filepath.Join(root, "capability")
	usrPath := filepath.Join(root, "usr", "usr.db")
	s := New(kernel.NewBase(nil, kernel.Options{UsrPath: usrPath, AppDir: appRoot}))
	return s, appRoot, usrPath
}

// mcpFilePath 某级 MCP 根下 <名>.json 的绝对路径。
func mcpFilePath(root, name string) string {
	return filepath.Join(root, capfs.McpFileName(name))
}

// TestMcpSaveFileLanding 保存到 app/user/project 三级 → 文件落在各级 `capability/mcps/<名>.json`。
func TestMcpSaveFileLanding(t *testing.T) {
	s, appRoot, usrPath := testService(t)
	workDir := filepath.Join(t.TempDir(), "proj")
	s.View.Register("ins-1", workDir, "")

	appMcpRoot, err := capfs.McpSystemRoot(appRoot)
	if err != nil {
		t.Fatalf("McpSystemRoot: %v", err)
	}
	cases := []struct {
		level   string
		root    string
		wantURL string
	}{
		{capfs.KindApp, appMcpRoot, "http://app"},
		{capfs.KindUser, capfs.McpUserRoot(usrPath), "http://user"},
		{capfs.KindProject, capfs.McpProjectRoot(workDir), "http://proj"},
	}
	for _, c := range cases {
		if _, err := s.McpSave(facade.McpSaveRequest{Server: facade.McpServer{
			Name: "srv_" + c.level, URL: c.wantURL, Enabled: true, Level: c.level,
			Transport: "http",
		}}); err != nil {
			t.Fatalf("McpSave(%s): %v", c.level, err)
		}
		p := mcpFilePath(c.root, "srv_"+c.level)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 级文件未落在预期路径 %s：%v", c.level, p, err)
		}
		obj, ok := capfs.ReadMcpFile(c.root, "srv_"+c.level)
		if !ok || obj["url"] != c.wantURL || obj["transport"] != "http" {
			t.Fatalf("%s 级文件内容异常：ok=%v obj=%+v", c.level, ok, obj)
		}
		// name/level 不入文件正文（由文件名与所在级承载）
		if _, has := obj["name"]; has {
			t.Fatalf("%s 级文件不应含 name 键：%+v", c.level, obj)
		}
	}
}

// TestMcpListMostSpecificWins 同名跨级 → 最具体级优先、**整条覆盖**（非字段级合并）。
func TestMcpListMostSpecificWins(t *testing.T) {
	s, _, _ := testService(t)
	workDir := filepath.Join(t.TempDir(), "proj")
	s.View.Register("ins-1", workDir, "")

	// app：完整定义；user：只覆盖 url；project：再覆盖 url + description。
	seed := []struct {
		level string
		srv   facade.McpServer
	}{
		{capfs.KindApp, facade.McpServer{Name: "shared", URL: "http://app", Enabled: true, Transport: "http", Description: "app-desc", Namespace: "ns-app"}},
		{capfs.KindUser, facade.McpServer{Name: "shared", URL: "http://user", Enabled: true, Transport: "http", Description: "user-desc", Namespace: "ns-user"}},
		{capfs.KindProject, facade.McpServer{Name: "shared", URL: "http://proj", Enabled: true, Transport: "sse", Description: "proj-desc"}},
	}
	for _, sd := range seed {
		srv := sd.srv
		srv.Level = sd.level
		if _, err := s.McpSave(facade.McpSaveRequest{Server: srv}); err != nil {
			t.Fatalf("McpSave(%s): %v", sd.level, err)
		}
	}

	list, err := s.McpList(facade.McpListRequest{InstanceID: "ins-1"})
	if err != nil {
		t.Fatalf("McpList: %v", err)
	}
	var got *facade.McpServer
	n := 0
	for i := range list.List {
		if list.List[i].Name == "shared" {
			got = &list.List[i]
			n++
		}
	}
	if n != 1 {
		t.Fatalf("同名跨级应只余一条生效（同名只一份）：n=%d list=%+v", n, list.List)
	}
	if got == nil || got.Level != capfs.KindProject {
		t.Fatalf("生效定义应命中 project（最具体级）：%+v", got)
	}
	if got.URL != "http://proj" || got.Transport != "sse" || got.Description != "proj-desc" {
		t.Fatalf("生效定义字段应整条取自 project：%+v", got)
	}
	// 整条覆盖：project 未设 Namespace → 空（**不**回落 user 的 ns-user）
	if got.Namespace != "" {
		t.Fatalf("整条覆盖：project 未设的字段不得回落低级（namespace=%q）", got.Namespace)
	}
}

// TestMcpGetAndDeleteByLevel 按级别定位 / 删除；删除后文件消失。
func TestMcpGetAndDeleteByLevel(t *testing.T) {
	s, _, usrPath := testService(t)
	if _, err := s.McpSave(facade.McpSaveRequest{Server: facade.McpServer{
		Name: "one", URL: "http://u", Enabled: true, Level: capfs.KindUser,
	}}); err != nil {
		t.Fatalf("McpSave: %v", err)
	}
	// level 空 = 具体级优先 → 命中 user
	g, err := s.McpGet(facade.McpGetRequest{Name: "one"})
	if err != nil || g.Server.Level != capfs.KindUser || g.Server.URL != "http://u" {
		t.Fatalf("McpGet 异常：%v %+v", err, g.Server)
	}
	if _, err := s.McpDelete(facade.McpDeleteRequest{Name: "one"}); err != nil {
		t.Fatalf("McpDelete: %v", err)
	}
	if capfs.McpFileExists(capfs.McpUserRoot(usrPath), "one") {
		t.Fatal("删除后 user 级文件应消失")
	}
	if _, err := s.McpGet(facade.McpGetRequest{Name: "one"}); err == nil {
		t.Fatal("删除后 McpGet 应报未找到")
	}
}

// TestMcpSaveRenameRemovesOldFile 改名（OldName 非空且不同）→ 旧文件被移除，新文件就位。
func TestMcpSaveRenameRemovesOldFile(t *testing.T) {
	s, _, usrPath := testService(t)
	if _, err := s.McpSave(facade.McpSaveRequest{Server: facade.McpServer{
		Name: "oldname", URL: "http://x", Enabled: true, Level: capfs.KindUser,
	}}); err != nil {
		t.Fatalf("McpSave(old): %v", err)
	}
	if _, err := s.McpSave(facade.McpSaveRequest{
		Server:  facade.McpServer{Name: "newname", URL: "http://x", Enabled: true, Level: capfs.KindUser},
		OldName: "oldname", OldLevel: capfs.KindUser,
	}); err != nil {
		t.Fatalf("McpSave(rename): %v", err)
	}
	root := capfs.McpUserRoot(usrPath)
	if capfs.McpFileExists(root, "oldname") {
		t.Fatal("改名后旧文件应被移除")
	}
	if !capfs.McpFileExists(root, "newname") {
		t.Fatal("改名后新文件应存在")
	}
}

// TestMcpSaveRejectsInvalidName 非法名（数字开头 / 含路径分隔符 / 空）→ 拒绝且不落盘。
func TestMcpSaveRejectsInvalidName(t *testing.T) {
	s, _, usrPath := testService(t)
	for _, bad := range []string{"1abc", "a/b", "a b", "", "名字"} {
		if _, err := s.McpSave(facade.McpSaveRequest{Server: facade.McpServer{
			Name: bad, URL: "http://x", Enabled: true, Level: capfs.KindUser,
		}}); err == nil {
			t.Fatalf("非法名 %q 应被拒", bad)
		}
	}
	if capfs.ListMcpNames(capfs.McpUserRoot(usrPath)) != nil {
		t.Fatal("非法名不应落盘")
	}
}
