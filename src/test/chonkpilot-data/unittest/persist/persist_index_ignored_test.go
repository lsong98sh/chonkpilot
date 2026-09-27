// 索引排除判定域（data-index-ignored，2026-09-27 新增只读面）黑盒往返：
// 经 persist 独立总线驱动 data-index-ignored —— 构造 prj 配置（两引擎开关 + skip-dirs +
// stack-gitignore）+ 临时 workdir（含 .gitignore：*.gen.go / !special.gen.go / sub/）+
// 入参若干相对路径 → 断言 enabled 与配置一致、ignored 精确集合（含**祖先剪枝**与 '!' 反选）、
// truncated 行为、异常兜底不报错，且**零副作用**（不写配置/状态）。
package persist_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// writeTree 在 root 下按相对路径写文件（自动建目录）。
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// prjSave 写 prj-config 单键（值 = 字符串）。
func prjSave(t *testing.T, bus mq.Bus, key, value string) {
	t.Helper()
	r := dataCall(t, bus, "data-prj-config-save", map[string]any{
		"req_id": "s-" + key, "instance_id": "ins-test",
		"data": map[string]any{"key": key, "value": value},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("save %s failed: %+v", key, r)
	}
}

// indexIgnored 发 data-index-ignored 请求并返回 result。
func indexIgnored(t *testing.T, bus mq.Bus, instanceID string, paths []any) map[string]any {
	t.Helper()
	r := dataCall(t, bus, "data-index-ignored", map[string]any{
		"req_id": "i1", "instance_id": instanceID, "paths": paths,
	})
	return dataResult(t, r)
}

// engineOf 取单引擎结果（enabled, ignored）。
func engineOf(t *testing.T, res map[string]any, name string) (bool, []string) {
	t.Helper()
	m, ok := res[name].(map[string]any)
	if !ok {
		t.Fatalf("result 缺 %s 引擎载荷：%+v", name, res)
	}
	enabled, _ := m["enabled"].(bool)
	raw, _ := m["ignored"].([]any)
	ig := make([]string, 0, len(raw))
	for _, e := range raw {
		s, _ := e.(string)
		ig = append(ig, s)
	}
	return enabled, ig
}

func TestDataIndexIgnored(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := regInstance(t, bus)

	// 临时 workdir 树：根 .gitignore = *.gen.go / !special.gen.go / sub/；sub/ 内 '!' 不可救回。
	writeTree(t, wd, map[string]string{
		".gitignore":     "*.gen.go\n!special.gen.go\nsub/\n",
		"main.go":        "p",
		"a.gen.go":       "p",
		"special.gen.go": "p",
		"userdir/u.go":   "p",
		"vdir/v.go":      "p",
		"sub/.gitignore": "!x.go\n",
		"sub/x.go":       "p",
	})

	// prj 配置：两引擎均启用 + 各自用户规则 + 叠加 gitignore。
	prjSave(t, bus, "enable-codegraph", "true")
	prjSave(t, bus, "codegraph.skip-dirs", "userdir/")
	prjSave(t, bus, "codegraph.stack-gitignore", "true")
	prjSave(t, bus, "enable-vfts", "true")
	prjSave(t, bus, "vfts.skip-dirs", "vdir/")
	prjSave(t, bus, "vfts.stack-gitignore", "true")

	paths := []any{
		"main.go",                   // 不排除
		"a.gen.go",                  // 非锚定文件规则
		"special.gen.go",            // 同级 '!' 反选 → 不排除
		"sub/x.go",                  // 祖先剪枝（sub/ 命中，子级 '!' 不可救回）
		"userdir/u.go",              // codegraph 用户规则
		"vdir/v.go",                 // vfts 用户规则
		".chonkpilot/chonkpilot.db", // 内置强制排除（prj 库自身）
		"ghost.gen.go",              // 不存在但规则命中（判定纯按路径）
		"a.gen.go",                  // 重复入参 → 原样回显、不去重
	}
	res := indexIgnored(t, bus, "ins-test", paths)

	cgEnabled, cgIgnored := engineOf(t, res, "codegraph")
	if !cgEnabled {
		t.Fatal("codegraph.enabled 应为 true")
	}
	wantCg := []string{"a.gen.go", "sub/x.go", "userdir/u.go", ".chonkpilot/chonkpilot.db", "ghost.gen.go", "a.gen.go"}
	if strings.Join(cgIgnored, "|") != strings.Join(wantCg, "|") {
		t.Fatalf("codegraph.ignored = %v，期望 %v", cgIgnored, wantCg)
	}

	vtEnabled, vtIgnored := engineOf(t, res, "vfts")
	if !vtEnabled {
		t.Fatal("vfts.enabled 应为 true")
	}
	wantVt := []string{"a.gen.go", "sub/x.go", "vdir/v.go", ".chonkpilot/chonkpilot.db", "ghost.gen.go", "a.gen.go"}
	if strings.Join(vtIgnored, "|") != strings.Join(wantVt, "|") {
		t.Fatalf("vfts.ignored = %v，期望 %v", vtIgnored, wantVt)
	}
	if _, has := res["truncated"]; has {
		t.Fatalf("未超限不应带 truncated：%+v", res)
	}

	// 关掉 vfts → enabled=false 且 ignored 为空（codegraph 不受影响）。
	prjSave(t, bus, "enable-vfts", "false")
	res = indexIgnored(t, bus, "ins-test", paths)
	if cgEnabled2, _ := engineOf(t, res, "codegraph"); !cgEnabled2 {
		t.Fatal("vfts 关闭不应影响 codegraph.enabled")
	}
	vtEnabled2, vtIgnored2 := engineOf(t, res, "vfts")
	if vtEnabled2 || len(vtIgnored2) != 0 {
		t.Fatalf("vfts 关闭应 enabled=false 且 ignored=[]：enabled=%v ignored=%v", vtEnabled2, vtIgnored2)
	}

	// 零副作用：本调用不写任何配置/状态 —— prj-config 键集与调用前一致。
	prjKeys := func() string {
		lr := dataResult(t, dataCall(t, bus, "data-prj-config-list", map[string]any{
			"req_id": "l1", "instance_id": "ins-test",
		}))
		kv, _ := lr["list"].(map[string]any)
		keys := make([]string, 0, len(kv))
		for k := range kv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return strings.Join(keys, "|")
	}
	before := prjKeys()
	indexIgnored(t, bus, "ins-test", paths)
	if after := prjKeys(); after != before {
		t.Fatalf("data-index-ignored 不应改动配置键集：before=%s after=%s", before, after)
	}
}

// TestDataIndexIgnoredTruncated：paths 超过上限 → 只判定前 20000 条并置 truncated=true。
func TestDataIndexIgnoredTruncated(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := regInstance(t, bus)
	writeTree(t, wd, map[string]string{".gitignore": "*.gen.go\n", "main.go": "p", "a.gen.go": "p"})
	prjSave(t, bus, "enable-codegraph", "true")
	prjSave(t, bus, "codegraph.stack-gitignore", "true")
	prjSave(t, bus, "enable-vfts", "true")
	prjSave(t, bus, "vfts.stack-gitignore", "true")

	// 第 0 条命中（先证明判定生效）；第 20001 条（索引 20000）在窗口外 → 不得被判定。
	paths := make([]any, 20001)
	for i := range paths {
		paths[i] = "main.go"
	}
	paths[0] = "a.gen.go"
	paths[20000] = "a.gen.go"

	res := indexIgnored(t, bus, "ins-test", paths)
	if v, _ := res["truncated"].(bool); !v {
		t.Fatalf("超限应置 truncated=true：%+v", res)
	}
	_, cgIgnored := engineOf(t, res, "codegraph")
	if strings.Join(cgIgnored, "|") != "a.gen.go" {
		t.Fatalf("只应判定前 20000 条（第 20001 条窗口外）：%v", cgIgnored)
	}
}

// TestDataIndexIgnoredGraceful：实例未登记（workdir 不可解析）→ 不报错（ok:true），
// 两引擎 enabled 按配置（读不到配置 → false）、ignored 为空。
func TestDataIndexIgnoredGraceful(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	r := dataCall(t, bus, "data-index-ignored", map[string]any{
		"req_id": "g1", "instance_id": "ins-ghost", "paths": []any{"a.gen.go", "sub/x.go"},
	})
	if ok, _ := r["ok"].(bool); !ok {
		t.Fatalf("未登记实例应不报错（降级应答）：%+v", r)
	}
	res := dataResult(t, r)
	for _, name := range []string{"codegraph", "vfts"} {
		enabled, ignored := engineOf(t, res, name)
		if enabled || len(ignored) != 0 {
			t.Fatalf("%s 应降级为 enabled=false + ignored=[]：enabled=%v ignored=%v", name, enabled, ignored)
		}
	}
	// paths 非数组（非法载荷）→ 不 panic、ignored 空。
	r = dataCall(t, bus, "data-index-ignored", map[string]any{
		"req_id": "g2", "instance_id": "ins-ghost", "paths": "not-an-array",
	})
	if ok, _ := r["ok"].(bool); !ok {
		t.Fatalf("非法 paths 应不报错：%+v", r)
	}
}
