// summary_prompt 系统文档化（capability/system/summary.md）存取测试（OP-01/OP-02，2026-10-06）：
// 读序 = 项目级文件 → 用户级文件 → 系统级文件 → embed 内置（data.SystemDoc）；
// save 写项目级纯文本文件；delete 回落继承。
package persist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// writeSystemDoc 写一份 system 文档（纯文本；父目录自动创建）。
func writeSystemDoc(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDataPromptSummaryFileStorage(t *testing.T) {
	appDir := t.TempDir() // 系统级 capability 根（Options.AppDir 注入）
	bus, _, usrPath := newTestPersistOpts(t, persist.Options{AppDir: appDir})
	wd := regInstance(t, bus)

	load := func() string {
		r := dataCall(t, bus, "data-prompt-load", map[string]any{
			"req_id": "p-load", "instance_id": "ins-test",
			"data": map[string]any{"id": "summary_prompt"},
		})
		v, _ := dataResult(t, r)["data"].(string)
		return v
	}
	save := func(text string) {
		r := dataCall(t, bus, "data-prompt-save", map[string]any{
			"req_id": "p-save", "instance_id": "ins-test",
			"data": map[string]any{"key": "summary_prompt", "value": text},
		})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("save failed: %+v", r)
		}
	}

	// 初始：项目/用户/系统均无文件 → 回落 embed 内置（= 出厂文件，与压缩插件实际生效值一致，I-65 ⑧）
	if got := load(); got != data.SystemDoc("summary") {
		t.Fatalf("initial summary prompt = %q, want embed builtin %q", got, data.SystemDoc("summary"))
	}

	// 系统级文件（出厂资源）→ 读命中
	sysFile := filepath.Join(appDir, "system", "summary.md")
	writeSystemDoc(t, sysFile, "系统级摘要提示词")
	if got := load(); got != "系统级摘要提示词" {
		t.Fatalf("system-level summary prompt = %q", got)
	}

	// 用户级文件 → 覆盖系统级
	usrFile := filepath.Join(filepath.Dir(usrPath), "capability", "system", "summary.md")
	writeSystemDoc(t, usrFile, "用户级摘要提示词")
	if got := load(); got != "用户级摘要提示词" {
		t.Fatalf("user-level summary prompt = %q", got)
	}

	// 项目级保存 → 文件落项目 capability/system/，读取覆盖用户级/系统级
	save("项目级摘要提示词")
	prjFile := filepath.Join(wd, ".chonkpilot", "capability", "system", "summary.md")
	if _, err := os.Stat(prjFile); err != nil {
		t.Fatalf("project summary file missing: %v", err)
	}
	if got := load(); got != "项目级摘要提示词" {
		t.Fatalf("project-level summary prompt = %q", got)
	}

	// delete → 项目文件删除，回落用户级
	r := dataCall(t, bus, "data-prompt-delete", map[string]any{
		"req_id": "p-del", "instance_id": "ins-test",
		"data": map[string]any{"id": "summary_prompt"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	if _, err := os.Stat(prjFile); !os.IsNotExist(err) {
		t.Fatalf("project summary file should be removed, err=%v", err)
	}
	if got := load(); got != "用户级摘要提示词" {
		t.Fatalf("after delete summary prompt = %q", got)
	}
}

// TestDataPromptSummaryNoWriteWhenSameAsInherited（P0-C）：内容与**继承值**完全相同 → 不写项目级
// 文件（保持继承，消除"回填有效值 + 点保存"造成的写放大与系统性遮蔽）；内容不同 → 写项目级覆盖；
// 已有项目级覆盖时保存回继承值 → 项目级文件被移除（回到继承）。
func TestDataPromptSummaryNoWriteWhenSameAsInherited(t *testing.T) {
	appDir := t.TempDir() // 系统级 capability 根（Options.AppDir 注入）
	bus, _, _ := newTestPersistOpts(t, persist.Options{AppDir: appDir})
	wd := regInstance(t, bus)
	prjFile := filepath.Join(wd, ".chonkpilot", "capability", "system", "summary.md")

	load := func() string {
		r := dataCall(t, bus, "data-prompt-load", map[string]any{
			"req_id": "p-load", "instance_id": "ins-test",
			"data": map[string]any{"id": "summary_prompt"},
		})
		v, _ := dataResult(t, r)["data"].(string)
		return v
	}
	save := func(text string) {
		r := dataCall(t, bus, "data-prompt-save", map[string]any{
			"req_id": "p-save", "instance_id": "ins-test",
			"data": map[string]any{"key": "summary_prompt", "value": text},
		})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("save failed: %+v", r)
		}
	}

	// ① 继承 = embed 内置（项目/用户/系统均无文件）→ 保存与之相同的内容 → 不产生项目级文件
	if got := load(); got != data.SystemDoc("summary") {
		t.Fatalf("initial = %q, want embed builtin", got)
	}
	save(data.SystemDoc("summary"))
	if _, err := os.Stat(prjFile); !os.IsNotExist(err) {
		t.Fatalf("内容与继承值（embed 内置）相同 → 不应写项目级文件: err=%v", err)
	}
	if got := load(); got != data.SystemDoc("summary") {
		t.Fatalf("load after same-as-inherited save = %q", got)
	}

	// 系统级文件存在 → 继承值 = 系统级内容；保存与之相同 → 仍不写项目级文件
	writeSystemDoc(t, filepath.Join(appDir, "system", "summary.md"), "系统级摘要提示词")
	save("系统级摘要提示词")
	if _, err := os.Stat(prjFile); !os.IsNotExist(err) {
		t.Fatalf("内容与继承值（系统级）相同 → 不应写项目级文件: err=%v", err)
	}
	if got := load(); got != "系统级摘要提示词" {
		t.Fatalf("load = %q", got)
	}

	// ② 不同 → 写项目级覆盖，回读为覆盖值
	save("项目级覆盖提示词")
	if _, err := os.Stat(prjFile); err != nil {
		t.Fatalf("不同内容应写项目级文件: %v", err)
	}
	if got := load(); got != "项目级覆盖提示词" {
		t.Fatalf("load = %q, want 覆盖值", got)
	}

	// ③ 已有覆盖时再保存为继承值 → 项目级文件移除（回到继承）
	save("系统级摘要提示词")
	if _, err := os.Stat(prjFile); !os.IsNotExist(err) {
		t.Fatalf("保存回继承值应移除项目级文件: err=%v", err)
	}
	if got := load(); got != "系统级摘要提示词" {
		t.Fatalf("恢复继承后 load = %q", got)
	}
}

// TestDataPromptMemoryPromptFileStorage（OP-04，2026-10-06）：记忆类别沉淀提示词文件化
// （capability/system/memory/<类别名>.md）：
//   - 读序 = 项目级文件 → 用户级文件 → 系统级文件 → embed 内置；
//   - 保存级别：项目级 8 类 → 项目级文件；唯一用户级类别「用户偏好」→ 用户级文件；
//   - 无任何提示词文件（新增自定义类别未建文件）→ 空串（该类不提取）；
//   - delete → 覆盖文件移除，回落继承。
func TestDataPromptMemoryPromptFileStorage(t *testing.T) {
	appDir := t.TempDir() // 系统级 capability 根（Options.AppDir 注入）
	bus, _, usrPath := newTestPersistOpts(t, persist.Options{AppDir: appDir})
	wd := regInstance(t, bus)

	key := func(cat string) map[string]any { return map[string]any{"id": "memory_prompt." + cat} }
	load := func(cat string) string {
		r := dataCall(t, bus, "data-prompt-load", map[string]any{
			"req_id": "mp-load", "instance_id": "ins-test", "data": key(cat),
		})
		v, _ := dataResult(t, r)["data"].(string)
		return v
	}
	save := func(cat, text string) {
		r := dataCall(t, bus, "data-prompt-save", map[string]any{
			"req_id": "mp-save", "instance_id": "ins-test",
			"data": map[string]any{"key": "memory_prompt." + cat, "value": text},
		})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("save %s failed: %+v", cat, r)
		}
	}
	del := func(cat string) {
		r := dataCall(t, bus, "data-prompt-delete", map[string]any{
			"req_id": "mp-del", "instance_id": "ins-test", "data": key(cat),
		})
		if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
			t.Fatalf("delete %s failed: %+v", cat, r)
		}
	}

	// ① 出厂默认 = embed 内置（出厂文件 src/initdata/capability/system/memory/项目概要.md）
	builtin := data.SystemDoc("memory/项目概要")
	if builtin == "" {
		t.Fatal("embed 内置记忆提示词缺失（memory/项目概要）")
	}
	if got := load("项目概要"); got != builtin {
		t.Fatalf("初始 memory prompt = %q, want embed builtin %q", got, builtin)
	}

	// ② 系统级磁盘文件 → 命中
	sysFile := filepath.Join(appDir, "system", "memory", "项目概要.md")
	writeSystemDoc(t, sysFile, "系统级记忆提示词")
	if got := load("项目概要"); got != "系统级记忆提示词" {
		t.Fatalf("system-level memory prompt = %q", got)
	}

	// ③ 用户级文件 → 覆盖系统级
	usrFile := filepath.Join(filepath.Dir(usrPath), "capability", "system", "memory", "项目概要.md")
	writeSystemDoc(t, usrFile, "用户级记忆提示词")
	if got := load("项目概要"); got != "用户级记忆提示词" {
		t.Fatalf("user-level memory prompt = %q", got)
	}

	// ④ 项目级保存 → 项目级文件（覆盖用户级/系统级）
	save("项目概要", "项目级记忆提示词")
	prjFile := filepath.Join(wd, ".chonkpilot", "capability", "system", "memory", "项目概要.md")
	if _, err := os.Stat(prjFile); err != nil {
		t.Fatalf("project memory prompt file missing: %v", err)
	}
	if got := load("项目概要"); got != "项目级记忆提示词" {
		t.Fatalf("project-level memory prompt = %q", got)
	}

	// ⑤ 唯一用户级类别「用户偏好」→ 写**用户级**文件（跨项目）
	save("用户偏好", "跨项目偏好提示词")
	upFile := filepath.Join(filepath.Dir(usrPath), "capability", "system", "memory", "用户偏好.md")
	if _, err := os.Stat(upFile); err != nil {
		t.Fatalf("用户偏好应收用户级文件: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wd, ".chonkpilot", "capability", "system", "memory", "用户偏好.md")); !os.IsNotExist(err) {
		t.Fatalf("用户偏好不应落项目级文件: err=%v", err)
	}
	if got := load("用户偏好"); got != "跨项目偏好提示词" {
		t.Fatalf("user-pref memory prompt = %q", got)
	}

	// ⑥ delete（恢复默认）= 移除该类别的**项目级 + 用户级**覆盖文件 → 回落继承（系统级/内置）
	del("项目概要")
	if _, err := os.Stat(prjFile); !os.IsNotExist(err) {
		t.Fatalf("delete 应移除项目级覆盖文件: err=%v", err)
	}
	if _, err := os.Stat(usrFile); !os.IsNotExist(err) {
		t.Fatalf("delete 应移除用户级覆盖文件: err=%v", err)
	}
	if got := load("项目概要"); got != "系统级记忆提示词" {
		t.Fatalf("delete 后回落系统级 = %q", got)
	}
	del("用户偏好")
	if _, err := os.Stat(upFile); !os.IsNotExist(err) {
		t.Fatalf("delete 应移除用户级覆盖文件: err=%v", err)
	}

	// ⑦ 新增自定义类别未建任何提示词文件 → 空串（该类不提取）
	if got := load("全新自定义类别"); got != "" {
		t.Fatalf("无任何提示词文件的类别应为空串，got=%q", got)
	}
}
