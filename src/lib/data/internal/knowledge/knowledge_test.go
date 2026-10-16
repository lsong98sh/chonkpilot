// knowledge 域门面实现白盒（req4「其他类型文件」/ req5「导入」，61 §3.3）：
//   - KnowledgeList 列出目录下**所有非隐藏文件**（不再只 `.md`），Type 由 capfs.TypeOfFile 给出
//     （原语 token 或 `file`）；
//   - KnowledgeRename **raw 文件语义**分流：源为 raw 文件（TypeOfFile == "file"）→ new_name 原样
//     （不追加 `.md`）；原语文件维持 NormalizeFileName；
//   - KnowledgeImport 四分支落盘（①空内容 ②原语契约原样 ③.md/.txt 转 resource 契约 ④其余原样）
//     + 目标已存在拒绝 + name 净化（防路径穿越）。
package knowledge

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// testService 造隔离的 knowledge 服务（app 级 capability 根 = <root>/capability）+ 自登记实例 Scope。
func testService(t *testing.T) (*Service, string, facade.Scope) {
	t.Helper()
	root := t.TempDir()
	appRoot := filepath.Join(root, "capability")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(root, "ws")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	usrPath := filepath.Join(root, "usr", "usr.db")
	svc := New(kernel.NewBase(nil, kernel.Options{UsrPath: usrPath, AppDir: appRoot}))
	scope := facade.Scope{WorkDir: workDir, DataDir: filepath.Join(workDir, ".chonkpilot")}
	return svc, appRoot, scope
}

// TestKnowledgeListAllNonHiddenFiles 目录下**所有非隐藏文件**均列出（json 等非 md 也可见）；
// 隐藏文件跳过；Type = 原语 token 或 `file`；非 md 描述从容错取文本行。
func TestKnowledgeListAllNonHiddenFiles(t *testing.T) {
	s, appRoot, scope := testService(t)
	write := func(rel, content string) {
		full := filepath.Join(appRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", "{\"a\":1}\nsecond\n")
	write("notes.md", "随手记\n")
	write(".hidden.md", "不应列出\n")
	write("emptybin.dat", "")

	resp, err := s.KnowledgeList(facade.KnowledgeListRequest{InstanceID: "testinst", Dir: appRoot, Scope: scope})
	if err != nil {
		t.Fatalf("KnowledgeList: %v", err)
	}
	byName := map[string]facade.KnowledgeFile{}
	for _, f := range resp.Files {
		byName[f.Name] = f
	}
	if _, ok := byName[".hidden.md"]; ok {
		t.Fatalf("隐藏文件不应列出：%+v", resp.Files)
	}
	for _, want := range []string{"config.json", "notes.md", "emptybin.dat"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("非隐藏文件 %q 应列出：%+v", want, resp.Files)
		}
	}
	// 根目录下（非类型目录）非原语文件 → Type = file
	if byName["config.json"].Type != "file" || byName["notes.md"].Type != "file" {
		t.Fatalf("非原语文件 Type 应为 file：%+v", byName)
	}
	// 非 md 描述 = 首个文本行（容错，不炸）
	if byName["config.json"].Description != "{\"a\":1}" {
		t.Fatalf("config.json 描述 = %q，want {\"a\":1}", byName["config.json"].Description)
	}
	// 类型目录下的原语文件 → Type = 原语 token
	write("tools/x.tool.md", capfs.BuildDoc(capfs.Template("tool", "x")))
	tresp, err := s.KnowledgeList(facade.KnowledgeListRequest{InstanceID: "testinst", Dir: filepath.Join(appRoot, "tools"), Scope: scope})
	if err != nil {
		t.Fatalf("KnowledgeList(tools): %v", err)
	}
	found := false
	for _, f := range tresp.Files {
		if f.Name == "x.tool.md" {
			found = true
			if f.Type != "tool" {
				t.Fatalf("x.tool.md Type = %q，want tool", f.Type)
			}
		}
	}
	if !found {
		t.Fatalf("x.tool.md 未列出：%+v", tresp.Files)
	}
}

// TestKnowledgeRenameRawVsPrimitive raw 文件改名保留原名（不追加 .md）；原语文件维持 NormalizeFileName。
func TestKnowledgeRenameRawVsPrimitive(t *testing.T) {
	s, appRoot, scope := testService(t)
	write := func(rel, content string) string {
		full := filepath.Join(appRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	// raw 文件（config.json，Type=file）→ 原样名，不追加 .md
	cfg := write("config.json", "{\"a\":1}\n")
	if _, err := s.KnowledgeRename(facade.KnowledgeRenameRequest{
		InstanceID: "testinst", Path: cfg, NewName: "settings.json", Scope: scope,
	}); err != nil {
		t.Fatalf("raw 改名: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "settings.json")); err != nil {
		t.Fatalf("raw 改名后 settings.json 应存在：%v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "settings.json.md")); err == nil {
		t.Fatal("raw 改名不得追加 .md")
	}
	// 原语文件（tools/foo.tool.md，Type=tool）→ 走 NormalizeFileName（补 .md）
	tool := write("tools/foo.tool.md", capfs.BuildDoc(capfs.Template("tool", "foo")))
	if _, err := s.KnowledgeRename(facade.KnowledgeRenameRequest{
		InstanceID: "testinst", Path: tool, NewName: "bar", Scope: scope,
	}); err != nil {
		t.Fatalf("原语改名: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "tools", "bar.md")); err != nil {
		t.Fatalf("原语文件改名应补 .md（bar.md）：%v", err)
	}
}

// TestKnowledgeImportFourBranches 四分支落盘 + 目标已存在拒绝 + name 净化（防路径穿越）。
func TestKnowledgeImportFourBranches(t *testing.T) {
	s, appRoot, scope := testService(t)
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

	importer := func(name, content string) facade.KnowledgeImportResponse {
		resp, err := s.KnowledgeImport(facade.KnowledgeImportRequest{
			InstanceID: "testinst", Dir: appRoot,
			Files: []facade.KnowledgeImportFile{{Name: name, Content: content}}, Scope: scope,
		})
		if err != nil {
			t.Fatalf("KnowledgeImport(%s): %v", name, err)
		}
		return resp
	}
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(appRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("读 %s: %v", rel, err)
		}
		return b
	}
	notExist := func(rel string) {
		if _, err := os.Stat(filepath.Join(appRoot, filepath.FromSlash(rel))); err == nil {
			t.Fatalf("%s 不应存在", rel)
		}
	}

	// ① content 空 → 原样落盘（新「其他类型」：保留 name，0 字节）
	if r := importer("config.json", ""); !r.OK || len(r.Errors) != 0 {
		t.Fatalf("① 空内容导入应 ok：%+v", r)
	}
	if b := read("config.json"); len(b) != 0 {
		t.Fatalf("① 空内容应落 0 字节，实得 %d", len(b))
	}
	// ① 的 .txt 变体不得被误转化为 .resource.md
	importer("empty.txt", "")
	if _, err := os.Stat(filepath.Join(appRoot, "empty.txt")); err != nil {
		t.Fatalf("① 空内容 .txt 应原样落盘：%v", err)
	}

	// ② 命中原语后缀且内容可解析为 v2 契约 → 原样落盘（字节不变）
	toolDoc := capfs.BuildDoc(capfs.Template("tool", "a"))
	importer("a.tool.md", b64([]byte(toolDoc)))
	if string(read("a.tool.md")) != toolDoc {
		t.Fatalf("② 原语契约应原样落盘")
	}

	// ③ .txt 合法 UTF-8 文本 → 转化为 <去后缀名>.resource.md 契约
	importer("readme.txt", b64([]byte("hello world")))
	res := read("readme.resource.md")
	doc, err := capfs.ParseDocErr(string(res))
	if err != nil {
		t.Fatalf("③ 转化结果应为合法 v2 契约：%v", err)
	}
	if doc.Name != "readme" || !strings.Contains(doc.Content, "hello world") {
		t.Fatalf("③ 转化契约 name/正文异常：name=%q content=%q", doc.Name, doc.Content)
	}
	notExist("readme.txt")

	// ④ 其余（二进制）→ 原样落盘（保留文件名，字节原样）
	png := []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a, 0xff}
	importer("logo.png", b64(png))
	if string(read("logo.png")) != string(png) {
		t.Fatalf("④ 二进制应原样落盘")
	}

	// name 净化（防穿越）：`../escape.json` → 落 Dir 下 escape.json，根外不留
	importer("../escape.json", "")
	if _, err := os.Stat(filepath.Join(appRoot, "escape.json")); err != nil {
		t.Fatalf("净化后应落 escape.json：%v", err)
	}
	notExist("../escape.json")

	// 目标已存在 → 拒绝（不覆盖），错误项含文件名
	r := importer("config.json", "")
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0].Error, "config.json") {
		t.Fatalf("已存在目标应记 errors：%+v", r)
	}
}
