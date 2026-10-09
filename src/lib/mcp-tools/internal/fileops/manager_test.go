package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// tmpFile 建临时文件返回绝对路径。
func tmpFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runManager 执行 filesys_run 脚本，返回 RawResult 顶层字段。
func runManager(t *testing.T, args map[string]interface{}) (map[string]interface{}, bool) {
	t.Helper()
	res := HandleFileManager("", args)
	raw, _ := res.RawResult.(map[string]interface{})
	return raw, res.Success
}

// asStrings 兼容 RawResult 中 []string 或 JSON 化后的 []interface{}。
func asStrings(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func strList(raw map[string]interface{}, key string) []string {
	return asStrings(raw[key])
}

// entriesOf 取 modified / created 条目（底层 []fileEntry 或 JSON 化后的 []interface{}）。
func entriesOf(raw map[string]interface{}, key string) []fileEntry {
	switch v := raw[key].(type) {
	case []fileEntry:
		return v
	case []interface{}:
		var out []fileEntry
		for _, x := range v {
			m, ok := x.(map[string]interface{})
			if !ok {
				continue
			}
			e := fileEntry{}
			e.Path, _ = m["path"].(string)
			e.Type, _ = m["type"].(string)
			switch n := m["size"].(type) {
			case float64:
				e.Size = int64(n)
			case int64:
				e.Size = n
			}
			e.Mtime, _ = m["mtime"].(string)
			e.MD5, _ = m["md5"].(string)
			e.Diff, _ = m["diff"].(string)
			out = append(out, e)
		}
		return out
	}
	return nil
}

// assertEntryMeta 校验条目 meta：type=file、size、mtime 为 RFC3339、md5（wantMD5 非空时须相等）。
func assertEntryMeta(t *testing.T, e fileEntry, wantSize int64, wantMD5 string) {
	t.Helper()
	if e.Type != "file" {
		t.Fatalf("entry type = %q, want file (%+v)", e.Type, e)
	}
	if e.Size != wantSize {
		t.Fatalf("entry size = %d, want %d (%+v)", e.Size, wantSize, e)
	}
	if _, err := time.Parse(time.RFC3339, e.Mtime); err != nil {
		t.Fatalf("entry mtime %q not RFC3339: %v (%+v)", e.Mtime, err, e)
	}
	if wantMD5 != "" {
		if e.MD5 != wantMD5 {
			t.Fatalf("entry md5 = %q, want %q (%+v)", e.MD5, wantMD5, e)
		}
	} else if e.MD5 == "" {
		t.Fatalf("entry md5 empty (%+v)", e)
	}
}

// failsOf 取 fails 列表（底层 []failItem 或 JSON 化后的 []interface{}）。
func failsOf(raw map[string]interface{}) []failItem {
	switch v := raw["fails"].(type) {
	case []failItem:
		return v
	case []interface{}:
		var out []failItem
		for _, x := range v {
			if m, ok := x.(map[string]interface{}); ok {
				f := failItem{}
				f.File, _ = m["file"].(string)
				f.Op, _ = m["op"].(string)
				f.Error, _ = m["error"].(string)
				out = append(out, f)
			}
		}
		return out
	}
	return nil
}

// modsOf 取 modified 列表。
func modsOf(raw map[string]interface{}) []fileEntry {
	return entriesOf(raw, "modified")
}

// TestFileManagerDSL_CreateModifyAppendDelete 基础：创建 → 替换 → 追加 → 删行 → 删文件。
func TestFileManagerDSL_CreateModifyAppendDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	script := "INS #" + q(path) + " \"hello world\"\n" +
		"RPL #" + q(path) + " \"world\" \"chonkpilot\"\n" +
		"APD #" + q(path) + " \"!\"\n"
	raw, ok := runManager(t, map[string]interface{}{"script": script})
	if !ok {
		t.Fatalf("expected success, raw=%v", raw)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if got != "hello chonkpilot\n!" {
		t.Fatalf("content = %q, want %q", got, "hello chonkpilot\n!")
	}
	created := entriesOf(raw, "created")
	if len(created) != 1 {
		t.Fatalf("created = %v, want 1", raw["created"])
	}
	// 新增条目须含 size / mtime / md5（md5 = 盘上最终内容）
	wantMD5, _ := fileMD5(path)
	assertEntryMeta(t, created[0], int64(len("hello chonkpilot\n!")), wantMD5)

	mods := modsOf(raw)
	if len(mods) == 0 {
		t.Fatalf("modified empty: %v", raw)
	}
	if !strings.Contains(mods[0].Diff, "-world") {
		t.Fatalf("diff missing before content: %v", mods[0])
	}
	// 修改条目同样含 size / mtime / md5
	assertEntryMeta(t, mods[0], int64(len("hello chonkpilot\n!")), wantMD5)

	// DEL 删行 + DEL 删文件
	p2 := filepath.Join(dir, "b.txt")
	os.WriteFile(p2, []byte("keep\nremove me\nkeep2\n"), 0644)
	script2 := "DEL #" + q(p2) + " \"remove\"\nDEL #" + q(path) + "\n"
	raw2, ok2 := runManager(t, map[string]interface{}{"script": script2})
	if !ok2 {
		t.Fatalf("expected success, raw=%v", raw2)
	}
	d2, _ := os.ReadFile(p2)
	if string(d2) != "keep\nkeep2\n" {
		t.Fatalf("del lines content = %q", string(d2))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a.txt should be deleted")
	}
	if len(strList(raw2, "deleted")) != 1 {
		t.Fatalf("deleted = %v", raw2["deleted"])
	}
}

// TestFileManagerDSL_MD5Fail md5 不一致 → fails，不修改。
func TestFileManagerDSL_MD5Fail(t *testing.T) {
	path := tmpFile(t, "m.txt", "original")
	md5v, _ := fileMD5(path)
	script := "RPL #" + q(path) + " \"original\" \"changed\""
	// 期望错误 md5
	raw, ok := runManager(t, map[string]interface{}{
		"script": script,
		"md5":    map[string]interface{}{path: "deadbeef"},
	})
	if !ok {
		t.Fatalf("expected overall success even with md5 mismatch, raw=%v", raw)
	}
	fails := failsOf(raw)
	if len(fails) == 0 {
		t.Fatalf("expected fails")
	}
	if !strings.Contains(fails[0].Error, "MD5 不一致") {
		t.Fatalf("unexpected fail: %v", fails[0])
	}
	data, _ := os.ReadFile(path)
	if string(data) != "original" {
		t.Fatalf("file modified despite md5 mismatch: %q", string(data))
	}

	// 正确 md5 → 成功
	raw2, ok2 := runManager(t, map[string]interface{}{
		"script": script,
		"md5":    map[string]interface{}{path: md5v},
	})
	if !ok2 || len(failsOf(raw2)) != 0 {
		t.Fatalf("expected success with correct md5, raw=%v", raw2)
	}
}

// TestFileManagerDSL_PartialFail 单操作失败不整体失败、不中断其余。
func TestFileManagerDSL_PartialFail(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "ok.txt")
	missPath := filepath.Join(dir, "miss.txt")
	os.WriteFile(okPath, []byte("target here"), 0644)
	os.WriteFile(missPath, []byte("nothing"), 0644)
	script := "RPL #" + q(missPath) + " \"absent\" \"x\"\n" +
		"RPL #" + q(okPath) + " \"target\" \"done\"\n"
	raw, ok := runManager(t, map[string]interface{}{"script": script})
	if !ok {
		t.Fatalf("expected overall success, raw=%v", raw)
	}
	fails := failsOf(raw)
	if len(fails) != 1 {
		t.Fatalf("fails = %v, want 1", raw["fails"])
	}
	data, _ := os.ReadFile(okPath)
	if string(data) != "done here" {
		t.Fatalf("second op not applied: %q", string(data))
	}
}

// TestFileManagerDSL_MoveFile 文件移动：created + deleted 双条。
func TestFileManagerDSL_MoveFile(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "f1.txt")
	to := filepath.Join(dir, "sub", "f2.txt")
	os.WriteFile(from, []byte("mv"), 0644)
	script := "MOV #" + q(from) + " #" + q(to)
	raw, ok := runManager(t, map[string]interface{}{"script": script})
	if !ok {
		t.Fatalf("expected success, raw=%v", raw)
	}
	created := entriesOf(raw, "created")
	if len(created) != 1 || len(strList(raw, "deleted")) != 1 {
		t.Fatalf("created=%v deleted=%v", raw["created"], raw["deleted"])
	}
	// 移动后的目标文件条目含 size / mtime / md5
	wantMD5, _ := fileMD5(to)
	assertEntryMeta(t, created[0], int64(len("mv")), wantMD5)
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("source should be gone")
	}
	if d, _ := os.ReadFile(to); string(d) != "mv" {
		t.Fatalf("target content = %q", string(d))
	}
}

// TestFileManagerDSL_MoveDir 目录移动 = 复制 + 删源；created/deleted 记目录。
func TestFileManagerDSL_MoveDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "srcdir")
	dst := filepath.Join(dir, "dstdir")
	os.MkdirAll(filepath.Join(src, "nested"), 0755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("A"), 0644)
	os.WriteFile(filepath.Join(src, "nested", "b.txt"), []byte("B"), 0644)
	script := "MOV #" + q(src) + " #" + q(dst)
	raw, ok := runManager(t, map[string]interface{}{"script": script})
	if !ok {
		t.Fatalf("expected success, raw=%v", raw)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source dir should be gone")
	}
	if d, _ := os.ReadFile(filepath.Join(dst, "nested", "b.txt")); string(d) != "B" {
		t.Fatalf("target tree wrong")
	}
	if len(failsOf(raw)) != 0 {
		t.Fatalf("unexpected fails: %v", raw["fails"])
	}
	// 目录条目：type=dir、size=0、无 md5
	created := entriesOf(raw, "created")
	if len(created) != 1 || created[0].Type != "dir" || created[0].Size != 0 || created[0].MD5 != "" {
		t.Fatalf("dir entry = %+v", created)
	}
}

// TestFileManagerDSL_Patch PTC 应用 unified diff（diff 换行以字面 \n 转义为单行参数）。
func TestFileManagerDSL_Patch(t *testing.T) {
	path := tmpFile(t, "p.txt", "line1\nline2\nline3\n")
	diffParam := `--- a\n+++ b\n@@ -1,3 +1,3 @@\n line1\n-line2\n+LINE2\n line3\n`
	script := "PTC #" + q(path) + " \"" + diffParam + "\""
	raw, ok := runManager(t, map[string]interface{}{"script": script})
	if !ok {
		t.Fatalf("expected success, raw=%v", raw)
	}
	if len(failsOf(raw)) != 0 {
		t.Fatalf("unexpected fails: %v", raw["fails"])
	}
	data, _ := os.ReadFile(path)
	if string(data) != "line1\nLINE2\nline3\n" {
		t.Fatalf("patched = %q", string(data))
	}
}

// q 包裹双引号（路径含反斜杠原样保留，引号内转义不改变普通反斜杠）。
func q(s string) string { return "\"" + s + "\"" }

// TestRejectSelfOrNestedTarget（C-36）：源与目标同路径、或目标位于源目录内部 → 拒绝。
func TestRejectSelfOrNestedTarget(t *testing.T) {
	base := t.TempDir()
	f := filepath.Join(base, "a.txt")
	d := filepath.Join(base, "dir")
	if msg := rejectSelfOrNestedTarget(f, f, false); msg == "" {
		t.Fatalf("文件同路径应拒绝")
	}
	if msg := rejectSelfOrNestedTarget(d, d, true); msg == "" {
		t.Fatalf("目录同路径应拒绝")
	}
	if msg := rejectSelfOrNestedTarget(d, filepath.Join(d, "sub"), true); msg == "" {
		t.Fatalf("目标在源目录内部应拒绝")
	}
	// 正常目标不应误拒
	if msg := rejectSelfOrNestedTarget(d, filepath.Join(base, "other"), true); msg != "" {
		t.Fatalf("正常目录目标不应拒绝：%s", msg)
	}
	if msg := rejectSelfOrNestedTarget(f, filepath.Join(base, "b.txt"), false); msg != "" {
		t.Fatalf("正常文件目标不应拒绝：%s", msg)
	}
}

// TestFileManagerDSL_MoveCopySameOrNested（C-36）：同路径/嵌套复制移动被拒绝且不丢数据、
// 不产生嵌套垃圾目录（此前 MOV 同路径会先复制后删光整棵源树；嵌套复制会无限递归）。
func TestFileManagerDSL_MoveCopySameOrNested(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(f, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(dir, "srcdir")
	if err := os.MkdirAll(filepath.Join(srcDir, "n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "n", "b.txt"), []byte("B"), 0644); err != nil {
		t.Fatal(err)
	}

	// MOV 文件到自身：拒绝，源文件内容不变（防数据丢失）
	raw, ok := runManager(t, map[string]interface{}{"script": "MOV #" + q(f) + " #" + q(f)})
	if !ok {
		t.Fatalf("expected overall success, raw=%v", raw)
	}
	if len(failsOf(raw)) == 0 {
		t.Fatalf("同路径 MOV 应记 fails：%v", raw)
	}
	if d, _ := os.ReadFile(f); string(d) != "data" {
		t.Fatalf("同路径 MOV 不得改动源文件：%q", string(d))
	}

	// CPY 目录到自身：拒绝
	raw2, _ := runManager(t, map[string]interface{}{"script": "CPY #" + q(srcDir) + " #" + q(srcDir)})
	if len(failsOf(raw2)) == 0 {
		t.Fatalf("同路径 CPY 应记 fails：%v", raw2)
	}

	// CPY 目录到其子路径：拒绝，且不创建目标目录（防无限嵌套）
	inner := filepath.Join(srcDir, "inner")
	raw3, _ := runManager(t, map[string]interface{}{"script": "CPY #" + q(srcDir) + " #" + q(inner)})
	if len(failsOf(raw3)) == 0 {
		t.Fatalf("嵌套 CPY 应记 fails：%v", raw3)
	}
	if _, err := os.Stat(inner); !os.IsNotExist(err) {
		t.Fatalf("嵌套 CPY 不得创建目标目录")
	}

	// MOV 目录到其子路径：拒绝，源树完整
	raw4, _ := runManager(t, map[string]interface{}{"script": "MOV #" + q(srcDir) + " #" + q(filepath.Join(srcDir, "sub"))})
	if len(failsOf(raw4)) == 0 {
		t.Fatalf("嵌套 MOV 应记 fails：%v", raw4)
	}
	if d, _ := os.ReadFile(filepath.Join(srcDir, "n", "b.txt")); string(d) != "B" {
		t.Fatalf("嵌套 MOV 不得破坏源树")
	}
}

// TestFileManagerDSL_LoopDataSource filesys_run 核心语句 LOOP 数据源（`#"f.csv".lines`）：
// 绝对路径可读、相对路径整体失败（R-11，含数据源读取）。
func TestFileManagerDSL_LoopDataSource(t *testing.T) {
	// 用正斜杠绝对路径：DSL 字符串内 `\r`/`\n`/`\t` 会被转义解码，正斜杠避免误伤
	csv := filepath.ToSlash(tmpFile(t, "rows.csv", "a\nb\nc\n"))
	body := "   SET \"seen\" => last\nEND\n"

	t.Run("绝对数据源通过", func(t *testing.T) {
		script := "LOOP row=#" + q(csv) + ".lines\n" + body
		res := HandleFileManager("", map[string]interface{}{"script": script})
		if !res.Success {
			t.Fatalf("绝对数据源 LOOP 应成功，err=%q output=%q", res.Error, res.Output)
		}
	})
	t.Run("相对数据源整体失败", func(t *testing.T) {
		script := "LOOP row=#\"rows.csv\".lines\n" + body
		res := HandleFileManager("", map[string]interface{}{"script": script})
		if res.Success {
			t.Fatalf("相对数据源应整体失败，got %+v", res)
		}
		if !strings.Contains(res.Error, "rows.csv") || !strings.Contains(res.Error, "LOOP 数据源") {
			t.Fatalf("消息应含原值与位置，got %q", res.Error)
		}
	})
}

// TestFileManagerEnvWorkdir filesys_run：宿主注入的 {{env.CHONKPILOT_WORKDIR}} 拼绝对路径读数据源（R-11 方案 A）。
func TestFileManagerEnvWorkdir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rows.csv"), []byte("a\nb\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvInstance, "unittest-envworkdir")
	t.Setenv(EnvWorkDir, dir)
	script := "LOOP row=#\"{{env.CHONKPILOT_WORKDIR}}/rows.csv\".lines\n   SET \"seen\" => last\nEND\n"
	res := HandleFileManager("", map[string]interface{}{"script": script})
	if !res.Success {
		t.Fatalf("env 拼绝对路径 LOOP 应成功，err=%q output=%q", res.Error, res.Output)
	}
}

// TestFileManagerTempPrefix filesys_run：!/ 前缀落盘到 <temp>/chonkpilot/<instance>/（R-11 二次升级）。
func TestFileManagerTempPrefix(t *testing.T) {
	t.Setenv(EnvInstance, "unittest-temp")
	res := HandleFileManager("", map[string]interface{}{
		"script": "INS #\"!/r11-tmp.csv\" \"x\"",
	})
	if !res.Success {
		t.Fatalf("!/ 前缀落盘应成功，err=%q output=%q", res.Error, res.Output)
	}
	root, err := paths.SetTempRoot("unittest-temp")
	if err != nil {
		t.Fatalf("SetTempRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "r11-tmp.csv")); err != nil {
		t.Fatalf("!/ 应落到实例临时目录：%v", err)
	}
}
