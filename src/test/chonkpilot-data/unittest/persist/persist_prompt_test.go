// summary_prompt 文件化（capability/prompts/summary.prompt.md）存取测试：
// 项目级文件优先 → 系统级文件 → 旧 prj config key；save 写项目级；delete 回落系统级。
package persist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

func TestDataPromptSummaryFileStorage(t *testing.T) {
	appDir := t.TempDir() // 系统级 capability 根（Options.AppDir 注入）
	bus, _, _ := newTestPersistOpts(t, persist.Options{AppDir: appDir})
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

	// 初始：项目与系统均无文件 → 回落内置默认（与压缩插件实际生效值一致，I-65 ⑧）
	if got := load(); got != data.DefaultSummaryPrompt {
		t.Fatalf("initial summary prompt = %q, want builtin default %q", got, data.DefaultSummaryPrompt)
	}

	// 系统级文件（出厂资源）→ 读命中
	sysFile := filepath.Join(appDir, "prompts", "summary.prompt.md")
	if err := os.MkdirAll(filepath.Dir(sysFile), 0o755); err != nil {
		t.Fatalf("mkdir sys prompts: %v", err)
	}
	if err := os.WriteFile(sysFile, []byte("# Summary Prompt\n\n[content]\n系统级摘要提示词\n"), 0o644); err != nil {
		t.Fatalf("write sys prompt: %v", err)
	}
	if got := load(); got != "系统级摘要提示词" {
		t.Fatalf("system-level summary prompt = %q", got)
	}

	// 项目级保存 → 文件落项目 capability/prompts/，读取覆盖系统级
	save("项目级摘要提示词")
	prjFile := filepath.Join(wd, ".chonkpilot", "capability", "prompts", "summary.prompt.md")
	if _, err := os.Stat(prjFile); err != nil {
		t.Fatalf("project prompt file missing: %v", err)
	}
	if got := load(); got != "项目级摘要提示词" {
		t.Fatalf("project-level summary prompt = %q", got)
	}

	// delete → 项目文件删除，回落系统级
	r := dataCall(t, bus, "data-prompt-delete", map[string]any{
		"req_id": "p-del", "instance_id": "ins-test",
		"data": map[string]any{"id": "summary_prompt"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	if _, err := os.Stat(prjFile); !os.IsNotExist(err) {
		t.Fatalf("project prompt file should be removed, err=%v", err)
	}
	if got := load(); got != "系统级摘要提示词" {
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
	prjFile := filepath.Join(wd, ".chonkpilot", "capability", "prompts", "summary.prompt.md")

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

	// ① 继承 = 内置默认（项目/系统均无文件）→ 保存与之相同的内容 → 不产生项目级文件
	if got := load(); got != data.DefaultSummaryPrompt {
		t.Fatalf("initial = %q, want builtin default", got)
	}
	save(data.DefaultSummaryPrompt)
	if _, err := os.Stat(prjFile); !os.IsNotExist(err) {
		t.Fatalf("内容与继承值（内置默认）相同 → 不应写项目级文件: err=%v", err)
	}
	if got := load(); got != data.DefaultSummaryPrompt {
		t.Fatalf("load after same-as-inherited save = %q", got)
	}

	// 系统级文件存在 → 继承值 = 系统级内容；保存与之相同 → 仍不写项目级文件
	sysFile := filepath.Join(appDir, "prompts", "summary.prompt.md")
	if err := os.MkdirAll(filepath.Dir(sysFile), 0o755); err != nil {
		t.Fatalf("mkdir sys prompts: %v", err)
	}
	if err := os.WriteFile(sysFile, []byte("# Summary Prompt\n\n[content]\n系统级摘要提示词\n"), 0o644); err != nil {
		t.Fatalf("write sys prompt: %v", err)
	}
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
