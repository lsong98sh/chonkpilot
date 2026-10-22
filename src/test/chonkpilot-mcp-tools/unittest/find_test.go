// Package chonkpilotmcptools — chonkpilot-mcp-tools 黑盒回归测试
// （工程目录 chonkpilot-test/chonkpilot-mcp-tools/unittest）。
//
// mcp-tools 的工具实现全部位于 internal/（外部模块无法 import），且 executor 与上层
// 的契约是进程级（<exe> <tool> --input=<json> → stdout JSON/文本），因此本测试为
// **进程级黑盒**：exec chonkpilot-tools-executor.exe（file 域 11 个原子工具），断言
// stdout（results[]/summary）与退出码。
//
// 被测 exe：`dist/other/capability/executors/chonkpilot-tools-executor.exe`（须经
// build-mcp-server.ps1 构建；D-28 产物分区后不在场时测试 Skip）。
package chonkpilotmcptools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// distExe 定位 dist/other/capability/executors 下的 executor（相对本测试源文件路径，不依赖 cwd）。
//
// 产物缺失默认 Skip；设环境变量 CHONK_REQUIRE_DIST=1 时改为**强制失败**（防"假绿"——产物没构建
// 却整包全绿）。默认 `go test ./...` 在无产物时仍不报红。
func distExe(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	p := filepath.Join(root, "dist", "other", "capability", "executors", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	requireDistOrSkip(t, "executor 未构建（先跑 build-mcp-server.ps1）", p)
	return ""
}

// toolsExe 定位 file 域统一执行器 chonkpilot-tools-executor.exe。
func toolsExe(t *testing.T) string {
	t.Helper()
	return distExe(t, "chonkpilot-tools-executor.exe")
}

// requireDistOrSkip：产物缺失时默认 Skip；设 CHONK_REQUIRE_DIST=1 则强制失败（门禁防假绿），
// 并在默认 Skip 路径显著标注（t.Logf + 汇总提示）。
func requireDistOrSkip(t *testing.T, reason, path string) {
	t.Helper()
	if os.Getenv("CHONK_REQUIRE_DIST") == "1" {
		t.Fatalf("[CHONK_REQUIRE_DIST=1] 产物缺失，强制失败（防假绿）：%s（%s）", reason, path)
	}
	t.Logf("⚠️ 跳过（产物缺失 → 假绿风险）：%s（%s）；设 CHONK_REQUIRE_DIST=1 可强制失败", reason, path)
	t.Skipf("%s", reason)
}

// findHit 是 file_find 的一处内容命中。
type findHit struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// findItem 是 file_find 的一个结果项（json 模式）。
type findItem struct {
	Rel  string    `json:"rel"`
	Hits []findHit `json:"hits"`
}

// findOut 是 file_find 单个 target 的结果（含状态与三种输出形态）。
type findOut struct {
	Status       string     `json:"status"`
	Error        string     `json:"error"`
	Items        []findItem `json:"items"`
	Lines        []string   `json:"lines"`
	Text         string     `json:"text"`
	MatchedTotal int        `json:"matched_total"`
	Truncated    bool       `json:"truncated"`
}

// runFind 以 file_find 执行一次查找。args 沿用旧 FIND 用例的参数名
// （path/glob/grep/depth/output/limit/period），在内部映射为新形状
// {purpose, targets:[{root, ...}]}；返回退出码、首个 target 结果与原始 stdout。
func runFind(t *testing.T, exe string, args map[string]any) (int, findOut, string) {
	t.Helper()
	target := map[string]any{"root": args["path"]}
	for _, k := range []string{
		"glob", "grep", "regex", "depth", "output", "limit", "meta",
		"maxHitsPerFile", "skipDirs", "ignoreFiles", "from", "to",
	} {
		if v, ok := args[k]; ok {
			target[k] = v
		}
	}
	if p, ok := args["period"].(map[string]any); ok {
		if v, ok := p["from"]; ok {
			target["from"] = v
		}
		if v, ok := p["to"]; ok {
			target["to"] = v
		}
	}
	payload := map[string]any{"purpose": "file_find 黑盒测试", "targets": []any{target}}
	code, out := runTool(t, exe, "file_find", payload, baseEnv())

	var env struct {
		Results []findOut `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		if code != 0 {
			return code, findOut{Status: "failed", Error: out}, out
		}
		t.Fatalf("stdout 不是结果 JSON：%v\n%s", err, out)
	}
	if len(env.Results) == 0 {
		t.Fatalf("results 为空：%s", out)
	}
	return code, env.Results[0], out
}

// normPath 归一化输出行路径（反斜杠 → 斜杠、清理）。结果 rel 是相对搜索根的路径。
func normPath(s string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(s)))
}

// assertFileSet 断言 items 模式结果 = 期望路径集合（顺序无关、数量一致、无重复/多余项）。
// 空期望时断言零条目。
func assertFileSet(t *testing.T, code int, res findOut, out string, want []string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit=%d 应成功；out=%q", code, out)
	}
	got := map[string]bool{}
	for _, it := range res.Items {
		got[normPath(it.Rel)] = true
	}
	if len(want) == 0 {
		if len(got) != 0 {
			t.Fatalf("期望无匹配，got=%v (out=%q)", sortedKeys(got), out)
		}
		return
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

// makeDatedTree 建 mtime 固定日期的目录树（date-only period 过滤用）：
//
//	a.txt 2026-06-15 / sub/e.txt 2026-06-14 / b.txt 2026-06-16 / c.txt 2026-06-17 / sub/d.txt 2026-06-18
func makeDatedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	at := func(day int) time.Time { return time.Date(2026, 6, day, 10, 0, 0, 0, time.Local) }
	files := map[string]time.Time{
		"a.txt":     at(15),
		"b.txt":     at(16),
		"c.txt":     at(17),
		"sub/d.txt": at(18),
		"sub/e.txt": at(14),
	}
	for rel, mt := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(rel), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		if err := os.Chtimes(abs, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", rel, err)
		}
	}
	return root
}

// —— 回归基线：无过滤 / glob / depth / grep / limit / tree / 无匹配 ——

func TestFindBaseline(t *testing.T) {
	exe := toolsExe(t)
	root, _ := makeTree(t)

	t.Run("全部递归列出", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root})
		assertFileSet(t, code, res, out, []string{"a.txt", "b.txt", "c.txt", "sub/d.txt", "sub/e.txt"})
	})
	t.Run("glob 后缀全命中", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "glob": "*.txt"})
		assertFileSet(t, code, res, out, []string{"a.txt", "b.txt", "c.txt", "sub/d.txt", "sub/e.txt"})
	})
	t.Run("glob 递归 **/", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "glob": "**/*.txt"})
		assertFileSet(t, code, res, out, []string{"sub/d.txt", "sub/e.txt"})
	})
	t.Run("glob 仅根", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "glob": "a.*"})
		assertFileSet(t, code, res, out, []string{"a.txt"})
	})
	t.Run("depth=1 仅当前目录", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "depth": 1})
		assertFileSet(t, code, res, out, []string{"a.txt", "b.txt", "c.txt"})
	})
	t.Run("glob 无匹配", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "glob": "*.nomatch"})
		assertFileSet(t, code, res, out, nil)
	})
	t.Run("limit 截断", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "limit": 2})
		if code != 0 {
			t.Fatalf("exit=%d out=%q", code, out)
		}
		if len(res.Items) != 2 {
			t.Fatalf("limit=2 应只返回 2 项，got=%d out=%q", len(res.Items), out)
		}
		if res.MatchedTotal != 5 {
			t.Fatalf("matched_total 应为精确值 5，got=%d out=%q", res.MatchedTotal, out)
		}
		if !res.Truncated {
			t.Fatalf("超限应置 truncated=true，out=%q", out)
		}
		assertFileSet(t, code, res, out, []string{"a.txt", "b.txt"})
	})
	t.Run("grep 命中", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "grep": "TODO"})
		if code != 0 {
			t.Fatalf("exit=%d out=%q", code, out)
		}
		if len(res.Items) != 1 || normPath(res.Items[0].Rel) != "c.txt" {
			t.Fatalf("grep 应只命中 c.txt，got=%+v out=%q", res.Items, out)
		}
		if len(res.Items[0].Hits) == 0 {
			t.Fatalf("grep 结果应含命中行，out=%q", out)
		}
		if !strings.Contains(res.Items[0].Hits[0].Text, "TODO in c") {
			t.Fatalf("命中行应含原文，got=%q", res.Items[0].Hits[0].Text)
		}
	})
	t.Run("tree 输出", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{"path": root, "output": "tree"})
		if code != 0 {
			t.Fatalf("exit=%d out=%q", code, out)
		}
		for _, want := range []string{"a.txt", "e.txt", "  d.txt"} {
			if !strings.Contains(res.Text, want) {
				t.Fatalf("tree 缺 %q，text=%q", want, res.Text)
			}
		}
	})
	t.Run("tree 空目录", func(t *testing.T) {
		empty := t.TempDir()
		code, res, out := runFind(t, exe, map[string]any{"path": empty, "output": "tree"})
		if code != 0 {
			t.Fatalf("exit=%d out=%q", code, out)
		}
		if strings.TrimSpace(res.Text) != "" || res.MatchedTotal != 0 {
			t.Fatalf("空目录应无条目，got text=%q matched_total=%d out=%q", res.Text, res.MatchedTotal, out)
		}
	})
}

// —— period：date-only（YYYY-MM-DD）区间 / 边界 / 单端 / 组合 ——

func TestFindPeriod(t *testing.T) {
	exe := toolsExe(t)
	root := makeDatedTree(t)

	t.Run("闭区间（含两端）", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": "2026-06-16", "to": "2026-06-17"},
		})
		assertFileSet(t, code, res, out, []string{"b.txt", "c.txt"})
	})
	t.Run("仅 from（≥）", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": "2026-06-17"},
		})
		assertFileSet(t, code, res, out, []string{"c.txt", "sub/d.txt"})
	})
	t.Run("仅 to（≤）", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"to": "2026-06-16"},
		})
		assertFileSet(t, code, res, out, []string{"a.txt", "b.txt", "sub/e.txt"})
	})
	t.Run("区间无文件", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": "2026-06-20", "to": "2026-06-21"},
		})
		assertFileSet(t, code, res, out, nil)
	})
	t.Run("与 glob 组合", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"glob":   "*.txt",
			"period": map[string]any{"to": "2026-06-16"},
		})
		assertFileSet(t, code, res, out, []string{"a.txt", "b.txt", "sub/e.txt"})
	})
}

func TestFindPeriodDateOnly(t *testing.T) {
	exe := toolsExe(t)
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
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": "2026-06-15"},
		})
		// in1(6-15 10:00) 与 in2(6-16 23:00) ≥ 6-15 00:00 均含；out(6-14) 排除
		assertFileSet(t, code, res, out, []string{"in1.txt", "in2.txt"})
	})
	t.Run("date-only to 含当日整天", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"to": "2026-06-15"},
		})
		// in1 与 out ≤ 6-15 23:59:59 含；in2(6-16) 排除
		assertFileSet(t, code, res, out, []string{"in1.txt", "out.txt"})
	})
	t.Run("date-only 区间单日", func(t *testing.T) {
		code, res, out := runFind(t, exe, map[string]any{
			"path":   root,
			"period": map[string]any{"from": "2026-06-16", "to": "2026-06-16"},
		})
		assertFileSet(t, code, res, out, []string{"in2.txt"})
	})
}

// —— period 非法输入：格式错误 → 失败；非字符串 / 非对象 → 宽松忽略 ——

func TestFindPeriodInvalid(t *testing.T) {
	exe := toolsExe(t)
	root := makeDatedTree(t)

	invalid := []struct {
		name string
		args map[string]any
		want string
	}{
		{"from 无法解析", map[string]any{"path": root, "period": map[string]any{"from": "not-a-date"}}, "bad from"},
		{"to 无法解析", map[string]any{"path": root, "period": map[string]any{"to": "2026-13-01"}}, "bad to"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			code, res, out := runFind(t, exe, tc.args)
			if code == 0 {
				t.Fatalf("非法输入应失败退出，got code=0 out=%q", out)
			}
			if !strings.Contains(res.Error, tc.want) {
				t.Fatalf("错误消息应含 %q，got out=%q", tc.want, out)
			}
		})
	}

	// 宽松容忍：period 非对象 / 非字符串值 → 视为不过滤（列出全部）。
	tolerant := []struct {
		name string
		args map[string]any
	}{
		{"period 非对象", map[string]any{"path": root, "period": "2026-09-01"}},
		{"from 非字符串", map[string]any{"path": root, "from": 20260901}},
	}
	for _, tc := range tolerant {
		t.Run(tc.name, func(t *testing.T) {
			code, res, out := runFind(t, exe, tc.args)
			assertFileSet(t, code, res, out, []string{"a.txt", "b.txt", "c.txt", "sub/d.txt", "sub/e.txt"})
		})
	}
}
