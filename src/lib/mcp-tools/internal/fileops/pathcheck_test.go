package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// TestValidateFilePath R-11 强校验：绝对 / ~ 合法，相对 / 空 / ~foo 非法。
func TestValidateFilePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	abs := filepath.Join(t.TempDir(), "a.txt")

	t.Run("绝对路径通过", func(t *testing.T) {
		got, msg := ValidateFilePath(abs)
		if msg != "" {
			t.Fatalf("绝对路径应通过，got msg=%q", msg)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("归一化结果应为绝对路径，got %q", got)
		}
	})

	t.Run("~/ 开头通过并展开", func(t *testing.T) {
		got, msg := ValidateFilePath("~/data/x.txt")
		if msg != "" {
			t.Fatalf("~/ 路径应通过，got msg=%q", msg)
		}
		want := filepath.Join(home, "data", "x.txt")
		if got != want {
			t.Fatalf("展开结果 = %q, want %q", got, want)
		}
	})

	t.Run("!/ 开头映射临时目录", func(t *testing.T) {
		root, err := paths.SetTempRoot("unittest-l1")
		if err != nil {
			t.Fatalf("SetTempRoot: %v", err)
		}
		got, msg := ValidateFilePath("!/x.txt")
		if msg != "" {
			t.Fatalf("!/ 路径应通过，got msg=%q", msg)
		}
		if want := filepath.Join(root, "x.txt"); got != want {
			t.Fatalf("!/ 解析 = %q, want %q", got, want)
		}
	})

	t.Run("相对路径拒绝且消息含原值与示例", func(t *testing.T) {
		_, msg := ValidateFilePath("src/main.py")
		if msg == "" {
			t.Fatal("相对路径应被拒绝")
		}
		if !strings.Contains(msg, "src/main.py") {
			t.Fatalf("消息应含原值，got %q", msg)
		}
		if !strings.Contains(msg, "~/data/x.txt") || !strings.Contains(msg, "绝对路径") {
			t.Fatalf("消息应含正确写法示例，got %q", msg)
		}
	})

	t.Run("./ 相对路径拒绝", func(t *testing.T) {
		if _, msg := ValidateFilePath("./a.txt"); msg == "" {
			t.Fatal("./a.txt 应被拒绝")
		}
	})

	t.Run("~foo 非 ~/ 前缀按相对拒绝", func(t *testing.T) {
		if _, msg := ValidateFilePath("~foo/a.txt"); msg == "" {
			t.Fatal("~foo 不应展开，应被拒绝")
		}
	})

	t.Run("空值不报错（必填校验优先）", func(t *testing.T) {
		got, msg := ValidateFilePath("")
		if got != "" || msg != "" {
			t.Fatalf("空值应返回 (\"\",\"\")，got (%q,%q)", got, msg)
		}
	})

	t.Run("ResolvePath 收敛点同样拒绝相对", func(t *testing.T) {
		if _, msg := ResolvePath("rel.txt", ""); msg == "" {
			t.Fatal("ResolvePath 应拒绝相对路径")
		}
		if got, msg := ResolvePath(abs, ""); msg != "" || !filepath.IsAbs(got) {
			t.Fatalf("ResolvePath 绝对路径应通过，got (%q,%q)", got, msg)
		}
	})
}

// TestHandleReadFileRejectsRelative file_read files[].path 相对 → 顶层错误。
func TestHandleReadFileRejectsRelative(t *testing.T) {
	res := HandleReadFile("", map[string]interface{}{
		"files": []interface{}{map[string]interface{}{"path": "a.txt"}},
	})
	if res.Success {
		t.Fatalf("相对路径应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "files[0].path") || !strings.Contains(res.Error, "a.txt") {
		t.Fatalf("错误消息应指明位置与原值，got %q", res.Error)
	}
}

// TestHandleFindRejectsRelative file_find path 相对 → 顶层错误。
func TestHandleFindRejectsRelative(t *testing.T) {
	res := HandleFind("", map[string]interface{}{"path": "src"})
	if res.Success {
		t.Fatalf("相对路径应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "path") || !strings.Contains(res.Error, "src") {
		t.Fatalf("错误消息应指明参数与原值，got %q", res.Error)
	}
}

// TestHandleDiffRejectsRelative file_diff 各入参形态相对 → 顶层错误并指明来源。
func TestHandleDiffRejectsRelative(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"file1相对", map[string]interface{}{"file1": "a.txt", "file2": "b.txt"}, "file1"},
		{"file2相对", map[string]interface{}{"file1": absTmp(t, "a.txt"), "file2": "b.txt"}, "file2"},
		{"files相对", map[string]interface{}{"files": []interface{}{
			map[string]interface{}{"path": "a.txt", "path2": absTmp(t, "b.txt")}}}, "files[0].path"},
		{"path数组相对", map[string]interface{}{"path": []interface{}{"a.txt", absTmp(t, "b.txt")}}, "path[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := HandleDiff("", tc.args)
			if res.Success {
				t.Fatalf("相对路径应失败，got %+v", res)
			}
			if !strings.Contains(res.Error, tc.want) {
				t.Fatalf("错误消息应指明 %q，got %q", tc.want, res.Error)
			}
		})
	}
}

// absTmp 返回临时目录下的绝对路径（内容不必存在）。
func absTmp(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// TestFileManagerRejectsRelativeDSLPath DSL 内 #"path" 相对 → 整体失败（非 fails）。
func TestFileManagerRejectsRelativeDSLPath(t *testing.T) {
	raw, ok := runManager(t, map[string]interface{}{
		"script": "RPL #\"src/main.py\" \"a\" \"b\"",
	})
	if ok {
		t.Fatalf("DSL 相对路径应整体失败，raw=%v", raw)
	}
	res := HandleFileManager("", map[string]interface{}{
		"script": "RPL #\"src/main.py\" \"a\" \"b\"",
	})
	if res.RawResult != nil {
		t.Fatalf("参数级路径违规不应产出 fails JSON，RawResult=%v", res.RawResult)
	}
	if !strings.Contains(res.Error, "src/main.py") || !strings.Contains(res.Error, "RPL") {
		t.Fatalf("错误消息应含原值与指令名，got %q", res.Error)
	}
}

// TestFileManagerRejectsRelativeMD5Key md5 键相对 → 整体失败。
func TestFileManagerRejectsRelativeMD5Key(t *testing.T) {
	path := tmpFile(t, "ok.txt", "hi")
	res := HandleFileManager("", map[string]interface{}{
		"script": "RPL #" + q(path) + " \"hi\" \"bye\"",
		"md5":    map[string]interface{}{"relative/path.txt": "deadbeef"},
	})
	if res.Success {
		t.Fatalf("md5 键相对应整体失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "relative/path.txt") {
		t.Fatalf("错误消息应含原值键，got %q", res.Error)
	}
}

// TestScriptFSRejectsRelativeDataSource filesys_run 核心句柄（`#"path"` 数据源/访问器）
// 路径相对 → 读取报错；绝对 → 可读（R-11）。
func TestScriptFSRejectsRelativeDataSource(t *testing.T) {
	h := ScriptFS{}.Open("rel.csv")
	if _, err := h.ReadText(); err == nil || !strings.Contains(err.Error(), "rel.csv") {
		t.Fatalf("相对数据源读取应报错并含原值，got %v", err)
	}
	if h.Exists() {
		t.Fatal("相对路径 Exists 应为 false")
	}
	abs := tmpFile(t, "ok.csv", "x\n")
	if s, err := (ScriptFS{}).Open(abs).ReadText(); err != nil || s != "x\n" {
		t.Fatalf("绝对数据源应可读，got (%q,%v)", s, err)
	}
}

// TestFileManagerMD5MismatchStaysFails 与上相反：运行时冲突（md5 不一致）仍只记 fails，整体成功。
func TestFileManagerMD5MismatchStaysFails(t *testing.T) {
	path := tmpFile(t, "m.txt", "original")
	raw, ok := runManager(t, map[string]interface{}{
		"script": "RPL #" + q(path) + " \"original\" \"changed\"",
		"md5":    map[string]interface{}{path: "deadbeef"},
	})
	if !ok {
		t.Fatalf("md5 不一致应整体成功（只记 fails），raw=%v", raw)
	}
	fails := failsOf(raw)
	if len(fails) != 1 || !strings.Contains(fails[0].Error, "MD5 不一致") {
		t.Fatalf("应恰有 1 条 MD5 fails，got %v", fails)
	}
	if data, _ := os.ReadFile(path); string(data) != "original" {
		t.Fatalf("md5 不一致不应写盘，got %q", string(data))
	}
}
