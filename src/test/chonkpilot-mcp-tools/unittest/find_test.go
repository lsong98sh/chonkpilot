// Package chonkpilotmcptools — chonkpilot-mcp-tools 黑盒回归测试
// （工程目录 chonkpilot-test/chonkpilot-mcp-tools/unittest）。
//
// mcp-tools 的工具实现全部位于 internal/（外部模块无法 import），且 executor 与上层
// 的契约是进程级（<exe> <tool> --input=<json> → stdout JSON/文本），因此本测试为
// **进程级黑盒**：exec chonkpilot-core-executor.exe，断言 stdout/退出码（对齐
// chonkpilot-test/chonkpilot-mcp-tools/systest/run_executor_tests.py 的调用形态）。
//
// 被测 exe：`dist/other/capability/tools/core/chonkpilot-core-executor.exe`（须经
// build-mcp-server.ps1 构建；D-28 产物分区后不在场时测试 Skip）。
package chonkpilotmcptools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// coreExe 定位 chonkpilot-core-executor.exe（相对本测试源文件路径，不依赖 cwd）。
// 落点：dist/other/capability/tools/core/（build-mcp-server.ps1 现行产物布局，D-28 产物分区）。
func coreExe(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	p := filepath.Join(root, "dist", "other", "capability", "tools", "core", "chonkpilot-core-executor.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	t.Skipf("executor 未构建（先跑 build-mcp-server.ps1）")
	return ""
}

// runFind 以 <exe> file_find --input=<tmp.json> 执行，返回退出码与 **stdout**
// （executor 的 stderr 是运行日志，不混入结果；结果 JSON/文本只走 stdout）。
func runFind(t *testing.T, exe string, args map[string]any) (int, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if err := os.WriteFile(in, raw, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	cmd := exec.Command(exe, "file_find", "--input="+in)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec: %v (stderr=%q)", err, se.String())
	}
	return code, so.String()
}

// normPath 归一化输出行路径（反斜杠 → 斜杠、清理）。输出行 = 相对搜索根的 rel 路径。
func normPath(s string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(s)))
}

// assertFileSet 断言 file 模式输出 = 期望路径集合（顺序无关、数量一致、无重复/多余行）。
// 空期望时断言 "(file: no matches)"。
func assertFileSet(t *testing.T, exit int, out string, want []string) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("exit=%d 应成功；out=%q", exit, out)
	}
	if len(want) == 0 {
		if !strings.Contains(out, "(file: no matches)") {
			t.Fatalf("期望无匹配文案，got out=%q", out)
		}
		return
	}
	got := map[string]bool{}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "(file:") {
			continue
		}
		got[normPath(ln)] = true
	}
	wantMap := map[string]bool{}
	for _, w := range want {
		wantMap[normPath(w)] = true
	}
	if len(got) != len(wantMap) {
		t.Fatalf("条目数量不符：got=%d %v want=%v (out=%q)", len(got), sortedKeys(got), want, out)
	}
	for w := range wantMap {
		if !got[w] {
			t.Fatalf("缺期望条目 %q；got=%v (out=%q)", w, sortedKeys(got), out)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// makeTree 建一个固定时间布局的目录树并返回 root：
//
//	root/a.txt      mtime tA（最早，-72h）
//	root/b.txt      mtime tB（-48h）
//	root/c.txt      mtime tC（-24h）
//	root/sub/d.txt  mtime tD（-2h，最新）
//	root/sub/e.txt  mtime tE（-120h，最老）
//
// mtime 一律截断到秒（规避 FS 纳秒精度差异），返回各 rel → time.Time。
func makeTree(t *testing.T) (string, map[string]time.Time) {
	t.Helper()
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)
	mts := map[string]time.Time{
		"a.txt": now.Add(-72 * time.Hour),
		"b.txt": now.Add(-48 * time.Hour),
		"c.txt": now.Add(-24 * time.Hour),
		"d.txt": now.Add(-2 * time.Hour),
		"e.txt": now.Add(-120 * time.Hour),
	}
	files := map[string]string{ // rel → 内容（sub/ 内文件）
		"a.txt":     "alpha",
		"b.txt":     "beta",
		"c.txt":     "TODO in c",
		"sub/d.txt": "delta",
		"sub/e.txt": "epsilon",
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		m := mts[filepath.Base(rel)]
		if err := os.Chtimes(abs, m, m); err != nil {
			t.Fatalf("chtimes %s: %v", rel, err)
		}
	}
	return root, mts
}

// —— 回归基线：无过滤 / glob / depth / grep / summary / tree / 无匹配 ——

func TestFindBaseline(t *testing.T) {
	exe := coreExe(t)
	root, _ := makeTree(t)

	t.Run("全部递归列出", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{"path": root})
		assertFileSet(t, code, out, []string{"a.txt", "b.txt", "c.txt", "sub/d.txt", "sub/e.txt"})
	})
	t.Run("glob 过滤", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{"path": root, "glob": "*.txt"})
		assertFileSet(t, code, out, []string{"a.txt", "b.txt", "c.txt", "sub/d.txt", "sub/e.txt"})
	})
	t.Run("glob 仅根", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{"path": root, "glob": "a.*"})
		assertFileSet(t, code, out, []string{"a.txt"})
	})
	t.Run("depth=1 仅当前目录", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{"path": root, "depth": 1})
		assertFileSet(t, code, out, []string{"a.txt", "b.txt", "c.txt"})
	})
	t.Run("glob 无匹配", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{"path": root, "glob": "*.nomatch"})
		assertFileSet(t, code, out, nil)
	})
	t.Run("grep 命中（summary）", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{"path": root, "grep": "TODO", "output": "summary"})
		if code != 0 {
			t.Fatalf("exit=%d out=%q", code, out)
		}
		if !strings.Contains(out, "c.txt") {
			t.Fatalf("grep 应命中 c.txt，out=%q", out)
		}
		if strings.Contains(out, "a.txt") || strings.Contains(out, "sub") {
			t.Fatalf("grep 不应命中其他文件，out=%q", out)
		}
		if !strings.Contains(out, "TODO in c") {
			t.Fatalf("summary 应含命中行内容，out=%q", out)
		}
	})
	t.Run("tree 输出", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{"path": root, "output": "tree"})
		if code != 0 {
			t.Fatalf("exit=%d out=%q", code, out)
		}
		for _, want := range []string{".", "sub/", "a.txt", "e.txt"} {
			if !strings.Contains(out, want) {
				t.Fatalf("tree 缺 %q，out=%q", want, out)
			}
		}
	})
	t.Run("tree 空目录", func(t *testing.T) {
		empty := t.TempDir()
		code, out := runFind(t, exe, map[string]any{"path": empty, "output": "tree"})
		if code != 0 || !strings.Contains(out, "(tree: empty)") {
			t.Fatalf("tree 空目录应输出 (tree: empty)；code=%d out=%q", code, out)
		}
	})
}

// —— period：RFC3339 / date-only / 边界 / 单端 / 组合 ——

func TestFindPeriod(t *testing.T) {
	exe := coreExe(t)
	root, mts := makeTree(t)
	rfc := func(rel string) string { return mts[filepath.Base(rel)].Format(time.RFC3339) }

	t.Run("闭区间精确命中（含边界）", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": rfc("b.txt"), "to": rfc("d.txt")},
		})
		// 边界 b.txt(==from) 与 d.txt(==to) 都应含；a/e 排除
		assertFileSet(t, code, out, []string{"b.txt", "c.txt", "sub/d.txt"})
	})
	t.Run("仅 from（≥）", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": rfc("c.txt")},
		})
		assertFileSet(t, code, out, []string{"c.txt", "sub/d.txt"})
	})
	t.Run("仅 to（≤）", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"to": rfc("b.txt")},
		})
		assertFileSet(t, code, out, []string{"a.txt", "b.txt", "sub/e.txt"})
	})
	t.Run("区间无文件", func(t *testing.T) {
		mid := mts["b.txt"].Add(time.Minute) // 落在 a-b 之间无文件
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": mid.Add(-time.Second).Format(time.RFC3339), "to": mid.Format(time.RFC3339)},
		})
		assertFileSet(t, code, out, nil)
	})
	t.Run("与 glob 组合", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"glob":   "*.txt",
			"period": map[string]any{"to": rfc("b.txt")},
		})
		assertFileSet(t, code, out, []string{"a.txt", "b.txt", "sub/e.txt"})
	})
}

func TestFindPeriodDateOnly(t *testing.T) {
	exe := coreExe(t)
	root := t.TempDir()
	// 固定锚点日（避开“今天”漂移）；mtime 设于日内 10:00/23:00（本地时区）
	dayFrom := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	dayTo := time.Date(2026, 6, 16, 23, 0, 0, 0, time.Local)
	dayOutside := time.Date(2026, 6, 14, 23, 0, 0, 0, time.Local)
	files := map[string]time.Time{
		"in1.txt": dayFrom,
		"in2.txt": dayTo,
		"out.txt": dayOutside,
	}
	for name, mt := range files {
		abs := filepath.Join(root, name)
		if err := os.WriteFile(abs, []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chtimes(abs, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	t.Run("date-only from 含当日整天", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": "2026-06-15"},
		})
		// in1(6-15 10:00) 与 in2(6-16 23:00) ≥ 6-15 00:00 均含；out(6-14) 排除
		assertFileSet(t, code, out, []string{"in1.txt", "in2.txt"})
	})
	t.Run("date-only to 含当日整天", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"to": "2026-06-15"},
		})
		// in1 与 out ≤ 6-15 23:59:59 含；in2(6-16) 排除
		assertFileSet(t, code, out, []string{"in1.txt", "out.txt"})
	})
	t.Run("date-only 区间单日", func(t *testing.T) {
		code, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": "2026-06-16", "to": "2026-06-16"},
		})
		assertFileSet(t, code, out, []string{"in2.txt"})
	})
}

// —— period 非法输入：必须报错且不返回文件 ——

func TestFindPeriodInvalid(t *testing.T) {
	exe := coreExe(t)
	root, _ := makeTree(t)

	invalid := []struct {
		name string
		args map[string]any
		want string
	}{
		{"from 非字符串", map[string]any{"path": root, "period": map[string]any{"from": 123}}, "period.from 必须是字符串"},
		{"from 无法解析", map[string]any{"path": root, "period": map[string]any{"from": "not-a-date"}}, "无法解析"},
		{"to 无法解析", map[string]any{"path": root, "period": map[string]any{"to": "2026-13-01"}}, "无法解析"},
		{"from 晚于 to", map[string]any{"path": root, "period": map[string]any{"from": "2026-09-02", "to": "2026-09-01"}}, "period.from 晚于 period.to"},
		{"period 非对象", map[string]any{"path": root, "period": "2026-09-01"}, ""}, // 顶层非对象 → 不过滤（宽松容忍）
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runFind(t, exe, tc.args)
			if tc.want == "" {
				// 顶层 period 非对象被容忍 → 应成功且列出全部
				assertFileSet(t, code, out, []string{"a.txt", "b.txt", "c.txt", "sub/d.txt", "sub/e.txt"})
				return
			}
			if code == 0 {
				t.Fatalf("非法输入应失败退出，got code=0 out=%q", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("错误消息应含 %q，got out=%q", tc.want, out)
			}
		})
	}
}
