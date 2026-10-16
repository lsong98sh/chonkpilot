package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadAction 覆盖 READ 动作：首行元数据 + 次行起内容，以及 info / start-limit 语义。
func TestReadAction(t *testing.T) {
	ec := newEditCtx("", nil)
	f := tmpFile(t, "read.txt", "line1\nline2\nline3\n")

	out, err := ec.runRead(f, readReq{path: f})
	if err != nil {
		t.Fatalf("runRead: %v", err)
	}
	parts := strings.SplitN(out, "\n", 2)
	if len(parts) != 2 {
		t.Fatalf("应有「元数据行 + 内容」，got %q", out)
	}
	for _, key := range []string{"path=", "md5=", "encoding=", "lines=", "size="} {
		if !strings.Contains(parts[0], key) {
			t.Fatalf("元数据行缺 %q：%q", key, parts[0])
		}
	}
	if parts[1] != "line1\nline2\nline3" {
		t.Fatalf("内容不符，got %q", parts[1])
	}

	t.Run("info 只返回元数据", func(t *testing.T) {
		out, err := ec.runRead(f, readReq{path: f, infoOnly: true})
		if err != nil {
			t.Fatalf("runRead(info): %v", err)
		}
		if strings.Contains(out, "\n") {
			t.Fatalf("info 模式不应含内容行，got %q", out)
		}
		if !strings.Contains(out, "lines=3") {
			t.Fatalf("info 元数据应含 lines=3，got %q", out)
		}
	})

	t.Run("start/limit 分段", func(t *testing.T) {
		out, err := ec.runRead(f, readReq{path: f, start: 2, limit: 1})
		if err != nil {
			t.Fatalf("runRead(start/limit): %v", err)
		}
		if !strings.HasSuffix(out, "\nline2") {
			t.Fatalf("start=2 limit=1 应取第 2 行，got %q", out)
		}
	})
}

// TestDiffAction 覆盖 DIFF 动作：两文件统一 diff。
func TestDiffAction(t *testing.T) {
	ec := newEditCtx("", nil)
	a := tmpFile(t, "a.txt", "hello\nworld\n")
	b := tmpFile(t, "b.txt", "hello\nthere\n")

	out, err := ec.runDiff([][2]string{{a, b}})
	if err != nil {
		t.Fatalf("runDiff: %v", err)
	}
	if !strings.Contains(out, "--- "+a) || !strings.Contains(out, "+++ "+b) {
		t.Fatalf("diff 头不符，got %q", out)
	}
	if !strings.Contains(out, "-world") || !strings.Contains(out, "+there") {
		t.Fatalf("diff 体应含删除/新增行，got %q", out)
	}
}

// TestFindAction 覆盖 FIND 动作：基本查找（file/glob）、grep summary、tree 空目录。
func TestFindAction(t *testing.T) {
	ec := newEditCtx("", nil)
	dir := t.TempDir()
	files := map[string]string{
		"a.go":     "package main\n",
		"b.txt":    "TODO fix me\n",
		"sub/c.go": "package sub\n",
	}
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("glob 仅列出匹配文件", func(t *testing.T) {
		out, err := ec.runFindQuery(dir, findOpts{glob: "*.go"})
		if err != nil {
			t.Fatalf("runFindQuery: %v", err)
		}
		if !strings.Contains(out, "a.go") || strings.Contains(out, "b.txt") {
			t.Fatalf("glob=*.go 应仅列出 a.go，got %q", out)
		}
	})

	t.Run("grep summary 命中行", func(t *testing.T) {
		out, err := ec.runFindQuery(dir, findOpts{grep: "TODO", output: "summary"})
		if err != nil {
			t.Fatalf("runFindQuery(grep): %v", err)
		}
		if !strings.Contains(out, "b.txt") || !strings.Contains(out, "TODO fix me") {
			t.Fatalf("summary 应含命中文件与命中行，got %q", out)
		}
	})

	t.Run("无匹配文案", func(t *testing.T) {
		out, err := ec.runFindQuery(dir, findOpts{glob: "*.nomatch"})
		if err != nil {
			t.Fatalf("runFindQuery: %v", err)
		}
		if !strings.Contains(out, "(file: no matches)") {
			t.Fatalf("无匹配应给文案，got %q", out)
		}
	})

	t.Run("tree 输出目录结构", func(t *testing.T) {
		out, err := ec.runFindQuery(dir, findOpts{output: "tree"})
		if err != nil {
			t.Fatalf("runFindQuery(tree): %v", err)
		}
		for _, want := range []string{".", "a.go", "sub/"} {
			if !strings.Contains(out, want) {
				t.Fatalf("tree 缺 %q，got %q", want, out)
			}
		}
	})
}

// TestQueryActionsEndToEnd 经 filesys_run 全链路（DSL 解析 → 路径预校验 → 动作分发 → `=> #"file"` 落盘）
// 验证 READ / DIFF / FIND 三个查询动作的接线（含 tokenizer 与 {{}} 插值）。
func TestQueryActionsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sl := filepath.ToSlash
	run := func(script string) {
		t.Helper()
		if res := HandleFileManager("", map[string]interface{}{"script": script}); !res.Success {
			t.Fatalf("脚本失败：%s（script=%q）", res.Error, script)
		}
	}
	readOut := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读落盘结果失败：%v", err)
		}
		return string(data)
	}

	t.Run("READ 落盘含元数据与内容", func(t *testing.T) {
		out := filepath.Join(dir, "read.out")
		run(`READ #"` + sl(a) + `" => #"` + sl(out) + `"`)
		s := readOut(out)
		if !strings.Contains(s, "md5=") || !strings.Contains(s, "lines=1") || !strings.Contains(s, "hello") {
			t.Fatalf("READ 落盘应含元数据与内容，got %q", s)
		}
	})

	t.Run("DIFF 落盘含 diff", func(t *testing.T) {
		out := filepath.Join(dir, "diff.out")
		run(`DIFF #"` + sl(a) + `" #"` + sl(b) + `" => #"` + sl(out) + `"`)
		s := readOut(out)
		if !strings.Contains(s, "-hello") || !strings.Contains(s, "+world") {
			t.Fatalf("DIFF 落盘不符，got %q", s)
		}
	})

	t.Run("FIND 落盘列出文件", func(t *testing.T) {
		out := filepath.Join(dir, "find.out")
		run(`FIND #"` + sl(dir) + `" glob="*.txt" => #"` + sl(out) + `"`)
		if s := readOut(out); !strings.Contains(s, "a.txt") || !strings.Contains(s, "b.txt") {
			t.Fatalf("FIND 落盘应含 a.txt/b.txt，got %q", s)
		}
	})

	t.Run("READ 支持 {{}} 插值路径", func(t *testing.T) {
		t.Setenv(EnvWorkDir, dir)
		out := filepath.Join(dir, "read2.out")
		run(`READ #"{{env.CHONKPILOT_WORKDIR}}/a.txt" => #"` + sl(out) + `"`)
		if s := readOut(out); !strings.Contains(s, "hello") {
			t.Fatalf("插值 READ 应读到 a.txt，got %q", s)
		}
	})
}
