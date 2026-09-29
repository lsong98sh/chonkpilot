// data-knowledge-* 消息面测试（B 类随迁）：capability 原语文件域 CRUD。
// app 根经 persist.Options.AppDir 注入临时目录；project 根 = 实例 work_dir/.chonkpilot/capability。
// 外部模块黑盒（package persist_test）：经 persist.New/Start/Stop + 总线 data-* 主题驱动；
// project 根路径本地等价 filepath.Join(work_dir, ".chonkpilot", "capability")（原白盒私有 kbProjectRoot）。
package persist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newKnowledgeEnv 建带 app 知识库根（临时 capability 目录）的环境 + 实例。
func newKnowledgeEnv(t *testing.T) (mq.Bus, string) {
	t.Helper()
	appRoot := t.TempDir()
	bus, _, _ := newTestPersistOpts(t, persist.Options{AppDir: appRoot})
	regInstance(t, bus)
	return bus, appRoot
}

// TestDataKnowledgeRootListCreateReadSaveDelete：app 根 root/list（预置四类目录）/
// create(read 契约解析) / save（description 回写）/ delete。
func TestDataKnowledgeRootListCreateReadSaveDelete(t *testing.T) {
	bus, appRoot := newKnowledgeEnv(t)

	// root（app）
	r := dataCall(t, bus, "data-knowledge-root", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"kind": "app"},
	})
	res := dataResult(t, r)
	if res["kind"] != "app" {
		t.Fatalf("root=%+v", res)
	}

	// list app 根（绝对路径）→ 预置分层目录 `tools/` + `knowledge/`（后者内含 skills/prompts/resources）
	r = dataCall(t, bus, "data-knowledge-list", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"dir": appRoot},
	})
	res = dataResult(t, r)
	dirs, _ := res["dirs"].([]any)
	if len(dirs) != 2 {
		t.Fatalf("dirs=%+v", dirs)
	}

	// create tool 于 appRoot/tools
	toolsDir := filepath.Join(appRoot, "tools")
	r = dataCall(t, bus, "data-knowledge-create", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"dir": toolsDir, "type": "tool", "name": "my_tool"},
	})
	res = dataResult(t, r)
	if res["path"] != filepath.ToSlash(filepath.Join(toolsDir, "my_tool.tool.md")) {
		t.Fatalf("create path=%+v", res)
	}

	// list tools → 1 个 type=tool 文件
	r = dataCall(t, bus, "data-knowledge-list", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "data": map[string]any{"dir": toolsDir},
	})
	res = dataResult(t, r)
	files, _ := res["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files=%+v", files)
	}
	f0, _ := files[0].(map[string]any)
	if f0["type"] != "tool" || f0["name"] != "my_tool.tool.md" {
		t.Fatalf("file=%+v", f0)
	}

	// read → 契约解析（Title/meta 分区）
	p := filepath.Join(toolsDir, "my_tool.tool.md")
	r = dataCall(t, bus, "data-knowledge-read", map[string]any{
		"req_id": "r5", "instance_id": "ins-test", "data": map[string]any{"path": p},
	})
	res = dataResult(t, r)
	doc, _ := res["doc"].(map[string]any)
	if doc["title"] != "my_tool" {
		t.Fatalf("doc=%+v", res["doc"])
	}
	if _, ok := doc["meta"].(map[string]any); !ok {
		t.Fatalf("meta missing: %+v", res["doc"])
	}
	if doc["description"] != "my_tool 描述" { // 模板默认描述
		t.Fatalf("description=%+v", doc["description"])
	}

	// save：doc 更新 description 回写
	doc["description"] = "更新后的描述"
	r = dataCall(t, bus, "data-knowledge-save", map[string]any{
		"req_id": "r6", "instance_id": "ins-test",
		"data": map[string]any{"path": p, "doc": doc},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("save failed: %+v", r)
	}
	r = dataCall(t, bus, "data-knowledge-read", map[string]any{
		"req_id": "r7", "instance_id": "ins-test", "data": map[string]any{"path": p},
	})
	if d2, _ := dataResult(t, r)["doc"].(map[string]any); d2["description"] != "更新后的描述" {
		t.Fatalf("description not saved: %+v", d2)
	}

	// delete
	r = dataCall(t, bus, "data-knowledge-delete", map[string]any{
		"req_id": "r8", "instance_id": "ins-test", "data": map[string]any{"path": p},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
}

// TestDataKnowledgeProjectRootMkdirCreate：project 根（v6：<work_dir>/.chonkpilot/capability）
// list 预置 + mkdir/create。
func TestDataKnowledgeProjectRootMkdirCreate(t *testing.T) {
	bus, _ := newKnowledgeEnv(t)
	// v6 项目级根：原 work_dir/@mcp 迁移为 work_dir/.chonkpilot/capability（12-数据层）
	prjRoot := filepath.Join(regInstance(t, bus), ".chonkpilot", "capability")

	// list 相对 dir（默认归属 project 根）→ 预置分层目录 tools/ + knowledge/
	r := dataCall(t, bus, "data-knowledge-list", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"dir": ""},
	})
	res := dataResult(t, r)
	if dirs, _ := res["dirs"].([]any); len(dirs) != 2 {
		t.Fatalf("project dirs=%+v", dirs)
	}
	_ = prjRoot // root 路径由 persist 实例绑定解析（work_dir/.chonkpilot/capability 自动创建）

	// mkdir 自定义目录
	r = dataCall(t, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r2", "instance_id": "ins-test",
		"data": map[string]any{"parent": "tools", "name": "custom"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("mkdir failed: %+v", r)
	}

	// 该目录下 create prompt
	r = dataCall(t, bus, "data-knowledge-create", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"dir": "tools/custom", "type": "prompt", "name": "explain"},
	})
	if path, _ := dataResult(t, r)["path"].(string); path != "tools/custom/explain.prompt.md" {
		t.Fatalf("create path=%+v", dataResult(t, r))
	}
	if _, err := os.Stat(filepath.Join(prjRoot, "tools/custom/explain.prompt.md")); err != nil {
		t.Fatalf("file not created under project root: %v", err)
	}

	// rmdir 删除目录（文件保留时 RemoveAll 也会删——语义同桥，验证目录消失）
	r = dataCall(t, bus, "data-knowledge-rmdir", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "data": map[string]any{"path": "tools/custom"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("rmdir failed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(prjRoot, "tools/custom")); !os.IsNotExist(err) {
		t.Fatal("dir not removed")
	}
}

// TestDataKnowledgeRenameDirKeepsName：目录语义 vs 文件语义的边界（I-73② 回归依据）——
// data-knowledge-rename-dir 目录改名**名称原样**（不追加 .md，前端目录行必须走它）；
// data-knowledge-rename 文件改名则按 normalizePrimitiveFileName 补 .md（缺后缀时）。
// 反例（旧前端目录行走 rename）→ 目标被追加 .md，实测 "rename … smoke_dir1.md: Access is denied"。
func TestDataKnowledgeRenameDirKeepsName(t *testing.T) {
	bus, appRoot := newKnowledgeEnv(t)
	toolsDir := filepath.Join(appRoot, "tools")

	// mkdir（绝对 parent → app 根内）→ 目录落盘
	r := dataCall(t, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"parent": filepath.ToSlash(toolsDir), "name": "smoke_dir"},
	})
	dirSlash, _ := dataResult(t, r)["path"].(string)
	if want := filepath.ToSlash(filepath.Join(toolsDir, "smoke_dir")); dirSlash != want {
		t.Fatalf("mkdir path=%q want=%q", dirSlash, want)
	}

	// rename-dir → 新名原样（目录语义）
	r = dataCall(t, bus, "data-knowledge-rename-dir", map[string]any{
		"req_id": "r2", "instance_id": "ins-test",
		"data": map[string]any{"path": dirSlash, "new_name": "smoke_dir2"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("rename-dir failed: %+v", r)
	}
	fi, err := os.Stat(filepath.Join(toolsDir, "smoke_dir2"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("rename-dir 未生成目录 smoke_dir2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(toolsDir, "smoke_dir2.md")); !os.IsNotExist(err) {
		t.Fatal("rename-dir 追加了 .md（目录语义被文件语义覆盖）")
	}
	if _, err := os.Stat(filepath.Join(toolsDir, "smoke_dir")); !os.IsNotExist(err) {
		t.Fatal("rename-dir 后旧目录仍在")
	}

	// 对照：文件改名走 rename → 缺 .md 后缀时补 .md（文件语义，与目录语义必须分流）
	filePath := filepath.Join(toolsDir, "smoke_file.tool.md")
	r = dataCall(t, bus, "data-knowledge-create", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"dir": filepath.ToSlash(toolsDir), "type": "tool", "name": "smoke_file"},
	})
	if got, _ := dataResult(t, r)["path"].(string); got != filepath.ToSlash(filePath) {
		t.Fatalf("create path=%+v", dataResult(t, r))
	}
	r = dataCall(t, bus, "data-knowledge-rename", map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
		"data": map[string]any{"path": filepath.ToSlash(filePath), "new_name": "renamed"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("rename failed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(toolsDir, "renamed.md")); err != nil {
		t.Fatalf("文件改名未按文件语义补 .md: %v", err)
	}
}

// dataFail 断言回复为失败（ok=false），返回 error 文本。
func dataFail(t *testing.T, m map[string]any) string {
	t.Helper()
	if ok, _ := m["ok"].(bool); ok {
		t.Fatalf("expected failure reply, got %+v", m)
	}
	msg, _ := m["error"].(string)
	if msg == "" {
		t.Fatalf("failure reply without error: %+v", m)
	}
	return msg
}

// TestDataKnowledgeMoveAcrossDirs：**G-26 ①** —— data-knowledge-rename / rename-dir 支持
// **跨目录移动**（new_name 含路径分隔符 = 目标路径；前端拖拽移动改走知识库域的依据）：
// ① 文件跨目录移动（root 相对路径写法）；② 目录跨目录移动（子树随迁；根内绝对路径写法）；
// ③ 同名冲突 → 拒绝（不覆盖，两个方向都验）。
func TestDataKnowledgeMoveAcrossDirs(t *testing.T) {
	bus, appRoot := newKnowledgeEnv(t)
	toolsDir := filepath.ToSlash(filepath.Join(appRoot, "tools"))
	skillsDir := filepath.ToSlash(filepath.Join(appRoot, "knowledge", "skills"))

	// list 根 → 预置四类分类目录（真实流程同口径）；再建 tools/sub（文件移动目标）
	r := dataCall(t, bus, "data-knowledge-list", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"dir": appRoot},
	})
	dataResult(t, r)
	r = dataCall(t, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"parent": toolsDir, "name": "sub"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("mkdir tools/sub failed: %+v", r)
	}

	// 源文件 appRoot/tools/my_tool.tool.md
	r = dataCall(t, bus, "data-knowledge-create", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"dir": toolsDir, "type": "tool", "name": "my_tool"},
	})
	dataResult(t, r)
	oldFile := filepath.Join(appRoot, "tools", "my_tool.tool.md")

	// ① 文件跨目录移动：new_name = 知识库根相对路径
	r = dataCall(t, bus, "data-knowledge-rename", map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
		"data": map[string]any{"path": filepath.ToSlash(oldFile), "new_name": "tools/sub/my_tool.tool.md"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("文件跨目录移动失败: %+v", r)
	}
	moved := filepath.Join(appRoot, "tools", "sub", "my_tool.tool.md")
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("文件未落到目标目录: %v", err)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatal("移动后旧文件仍在")
	}

	// ② 目录跨目录移动（子树随迁）：new_name = 根内**绝对路径**写法（覆盖该分支）
	r = dataCall(t, bus, "data-knowledge-rename-dir", map[string]any{
		"req_id": "r5", "instance_id": "ins-test",
		"data": map[string]any{"path": filepath.ToSlash(filepath.Join(appRoot, "tools", "sub")), "new_name": skillsDir + "/sub"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("目录跨目录移动失败: %+v", r)
	}
	if fi, err := os.Stat(filepath.Join(appRoot, "knowledge", "skills", "sub")); err != nil || !fi.IsDir() {
		t.Fatalf("目录未落到目标父目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "knowledge", "skills", "sub", "my_tool.tool.md")); err != nil {
		t.Fatalf("目录子树未随迁: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "tools", "sub")); !os.IsNotExist(err) {
		t.Fatal("移动后旧目录仍在")
	}

	// ③ 同名冲突 → 拒绝（文件）：先在 tools 下重建同名文件（内容不同），再尝试移动覆盖
	r = dataCall(t, bus, "data-knowledge-create", map[string]any{
		"req_id": "r6", "instance_id": "ins-test",
		"data": map[string]any{"dir": toolsDir, "type": "tool", "name": "my_tool"},
	})
	dataResult(t, r)
	r = dataCall(t, bus, "data-knowledge-rename", map[string]any{
		"req_id": "r7", "instance_id": "ins-test",
		"data": map[string]any{
			"path":     filepath.ToSlash(filepath.Join(appRoot, "knowledge", "skills", "sub", "my_tool.tool.md")),
			"new_name": "tools/my_tool.tool.md",
		},
	})
	if msg := dataFail(t, r); !strings.Contains(msg, "exists") {
		t.Fatalf("冲突错误应说明已存在: %q", msg)
	}
	for _, p := range []string{
		filepath.Join(appRoot, "knowledge", "skills", "sub", "my_tool.tool.md"),
		filepath.Join(appRoot, "tools", "my_tool.tool.md"),
	} { // 两端都未被动过（拒绝覆盖 = 不丢数据）
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("拒绝覆盖后 %s 丢失: %v", p, err)
		}
	}

	// ③b 同名冲突 → 拒绝（目录）
	r = dataCall(t, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r8", "instance_id": "ins-test", "data": map[string]any{"parent": toolsDir, "name": "dup"},
	})
	dataResult(t, r)
	r = dataCall(t, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r9", "instance_id": "ins-test", "data": map[string]any{"parent": skillsDir, "name": "dup"},
	})
	dataResult(t, r)
	r = dataCall(t, bus, "data-knowledge-rename-dir", map[string]any{
		"req_id": "r10", "instance_id": "ins-test",
		"data": map[string]any{"path": filepath.ToSlash(filepath.Join(appRoot, "tools", "dup")), "new_name": "skills/dup"},
	})
	dataFail(t, r)
	for _, p := range []string{
		filepath.Join(appRoot, "tools", "dup"),
		filepath.Join(appRoot, "knowledge", "skills", "dup"),
	} {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Fatalf("拒绝覆盖后目录 %s 丢失: %v", p, err)
		}
	}
}

// TestDataKnowledgeMoveOutOfRootRejected：**G-26 ②** —— 作用域底线：目标越界一律拒绝
// （`..` 逃逸 / 根外绝对路径 / 跨级移动到另一 capability 根 / 目录移入自身），且源不被改动、
// 根外不产生任何文件。
func TestDataKnowledgeMoveOutOfRootRejected(t *testing.T) {
	bus, appRoot := newKnowledgeEnv(t)
	toolsDir := filepath.ToSlash(filepath.Join(appRoot, "tools"))

	r := dataCall(t, bus, "data-knowledge-list", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"dir": appRoot},
	})
	dataResult(t, r)
	r = dataCall(t, bus, "data-knowledge-create", map[string]any{
		"req_id": "r2", "instance_id": "ins-test",
		"data": map[string]any{"dir": toolsDir, "type": "tool", "name": "mv"},
	})
	dataResult(t, r)
	src := filepath.Join(appRoot, "tools", "mv.tool.md")

	// ① `..` 逃逸 → 拒绝
	r = dataCall(t, bus, "data-knowledge-rename", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"path": filepath.ToSlash(src), "new_name": "../escaped.tool.md"},
	})
	if msg := dataFail(t, r); !strings.Contains(msg, "..") {
		t.Fatalf("`..` 逃逸错误信息应点明非法段: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(appRoot), "escaped.tool.md")); !os.IsNotExist(err) {
		t.Fatal("`..` 逃逸在知识库根外产生了文件")
	}

	// ② 根外绝对路径 → 拒绝
	outside := filepath.Join(t.TempDir(), "x.tool.md")
	r = dataCall(t, bus, "data-knowledge-rename", map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
		"data": map[string]any{"path": filepath.ToSlash(src), "new_name": filepath.ToSlash(outside)},
	})
	if msg := dataFail(t, r); !strings.Contains(msg, "outside") {
		t.Fatalf("根外绝对路径错误信息应点明越界: %q", msg)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("根外绝对路径移动产生了文件")
	}

	// ③ 跨级移动（源 = app 根 → 目标 = project 根）→ 拒绝（与前端「只在当前知识库根内」一致）
	pr := dataCall(t, bus, "data-knowledge-root", map[string]any{
		"req_id": "r5", "instance_id": "ins-test", "data": map[string]any{"kind": "project"},
	})
	projRoot, _ := dataResult(t, pr)["root"].(string)
	if projRoot == "" {
		t.Fatalf("project root 解析失败: %+v", pr)
	}
	r = dataCall(t, bus, "data-knowledge-rename", map[string]any{
		"req_id": "r6", "instance_id": "ins-test",
		"data": map[string]any{"path": filepath.ToSlash(src), "new_name": projRoot + "/tools/mv.tool.md"},
	})
	dataFail(t, r)
	if _, err := os.Stat(filepath.Join(projRoot, "tools", "mv.tool.md")); !os.IsNotExist(err) {
		t.Fatal("跨级移动在 project 根产生了文件")
	}

	// ④ 目录移入自身子目录 → 拒绝
	r = dataCall(t, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r7", "instance_id": "ins-test", "data": map[string]any{"parent": toolsDir, "name": "selfdir"},
	})
	dataResult(t, r)
	r = dataCall(t, bus, "data-knowledge-rename-dir", map[string]any{
		"req_id": "r8", "instance_id": "ins-test",
		"data": map[string]any{
			"path":     filepath.ToSlash(filepath.Join(appRoot, "tools", "selfdir")),
			"new_name": "tools/selfdir/inner",
		},
	})
	if msg := dataFail(t, r); !strings.Contains(msg, "inside source") {
		t.Fatalf("移入自身子目录错误信息应点明: %q", msg)
	}

	// 被拒绝的各次尝试都不得改动源
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("拒绝路径下源文件被改动: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(appRoot, "tools", "selfdir")); err != nil || !fi.IsDir() {
		t.Fatalf("拒绝路径下源目录被改动: %v", err)
	}
}
