// gen_test.go —— 生成物一致性（防漂移）测试。
//
// 运行生成逻辑，与仓库中**已提交**的生成文件逐字比对；不一致 = 红，提示重新生成。
// 这是"一致性由生成器保证"的关键：改 schema 后忘了重新生成 → 本测试即报红。
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRootFromTest 自本测试源文件向上查找仓库根。
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	for d := filepath.Dir(file); ; {
		cand := filepath.Join(d, "docs", "spec", "60-reference", "61-messages.schema.json")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("未找到仓库根（自 %s 向上）", filepath.Dir(file))
		}
		d = parent
	}
}

// TestGeneratedFilesUpToDate 断言已提交生成物 == 由 schema 重新生成的内容。
func TestGeneratedFilesUpToDate(t *testing.T) {
	root := repoRootFromTest(t)

	raw, err := os.ReadFile(filepath.Join(root, "docs", "spec", "60-reference", "61-messages.schema.json"))
	if err != nil {
		t.Fatalf("读取 schema: %v", err)
	}
	out, err := Generate(raw)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{"Go 键常量", filepath.Join(root, "src", "lib", "core", "msgkeys", "msgkeys_gen.go"), out.Go},
		{"JS 键常量", filepath.Join(root, "src", "frontend", "src", "events", "msgkeys.js"), out.JS},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("%s 不可读（请重新生成：go run src/tools/genmsg）: %v", tc.path, err)
			}
			if string(got) != tc.want {
				t.Fatalf("%s 与 schema 不一致 —— 请重新生成：go run src/tools/genmsg", tc.path)
			}
		})
	}
}
