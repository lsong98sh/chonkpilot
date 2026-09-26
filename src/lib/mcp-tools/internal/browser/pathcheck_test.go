package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// TestMain 注入内部 executor 的子进程上下文（CHONKPILOT_INSTANCE）并设置临时根：宿主 spawn
// executor 时 cli.Run 会以该 instance 设置 paths.TempRoot；`!/` 预校验依赖它（缺 instance 属异常）。
func TestMain(m *testing.M) {
	os.Setenv("CHONKPILOT_INSTANCE", "unittest-browser")
	_, _ = paths.SetTempRoot("unittest-browser")
	os.Exit(m.Run())
}

// TestBrowserRejectsRelativeDSLWritePaths R-11：DSL 内字面本地文件路径为相对 → 执行前预校验拒绝。
// 覆盖 SHT/DOM/DBG 落盘、UPF 上传文件、行尾 `=> #"file"` 重定向目标。
func TestBrowserRejectsRelativeDSLWritePaths(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{"SHT 全页", `SHT "shot.png"`, "shot.png"},
		{"SHT 元素", `SHT css="#x" "shot.png"`, "shot.png"},
		{"DOM", `DOM "page.html"`, "page.html"},
		{"DBG", `DBG "console.log"`, "console.log"},
		{"UPF 上传", `UPF css="#f" "up.txt"`, "up.txt"},
		{"重定向目标", `OPN "https://example.com" => #"out.txt"`, "out.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := prevalidateScript(tc.script)
			if len(msgs) == 0 {
				t.Fatalf("字面相对路径应被预校验拒绝，script=%q", tc.script)
			}
			joined := strings.Join(msgs, "; ")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("消息应含原值 %q，got %v", tc.want, msgs)
			}
			if !strings.Contains(joined, "绝对路径") {
				t.Fatalf("消息应含路径约束说明，got %v", msgs)
			}
		})
	}
}

// TestBrowserAcceptsAbsoluteDSLWritePaths 绝对路径、!/ 临时目录与含 {{}} 插值的路径不被预校验拒绝。
func TestBrowserAcceptsAbsoluteDSLWritePaths(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "shot.png")
	script := "SHT \"" + abs + "\"\nSHT \"!/shot.png\"\nDOM \"{{out}}\"\nOPN \"https://example.com\" => #\"{{dst}}\"\n"
	if msgs := prevalidateScript(script); len(msgs) > 0 {
		t.Fatalf("绝对路径 / !/ / {{}} 插值不应被预校验拒绝，got %v", msgs)
	}
}

// TestBrowserRunRejectsRelativeDSLWritePath 顶层入口：SHT 字面相对路径 → 失败（不启动浏览器）。
func TestBrowserRunRejectsRelativeDSLWritePath(t *testing.T) {
	res := HandleBrowserRun(map[string]interface{}{"script": `SHT "shot.png"`})
	if res.Success {
		t.Fatalf("相对落盘路径应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "shot.png") || !strings.Contains(res.Error, "绝对路径") {
		t.Fatalf("错误消息应含原值与约束，got %q", res.Error)
	}
}

// TestBrowserRunRejectsRelativePathArgs 顶层入口：file / dom_file 相对路径 → 失败。
func TestBrowserRunRejectsRelativePathArgs(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		res := HandleBrowserRun(map[string]interface{}{"file": "browser.script"})
		if res.Success || !strings.Contains(res.Error, "browser.script") {
			t.Fatalf("相对 file 应失败并含原值，got %+v", res)
		}
	})
	t.Run("dom_file", func(t *testing.T) {
		res := HandleBrowserRun(map[string]interface{}{
			"script": `OPN "https://example.com"`, "dom_file": "out.html",
		})
		if res.Success || !strings.Contains(res.Error, "dom_file") || !strings.Contains(res.Error, "out.html") {
			t.Fatalf("相对 dom_file 应失败并含原值，got %+v", res)
		}
	})
	t.Run("chrome_path", func(t *testing.T) {
		res := HandleBrowserRun(map[string]interface{}{
			"script": `OPN "https://example.com"`, "chrome_path": `tools\chrome.exe`,
		})
		if res.Success || !strings.Contains(res.Error, "chrome_path") || !strings.Contains(res.Error, "chrome.exe") {
			t.Fatalf("相对 chrome_path 应失败并含原值，got %+v", res)
		}
	})
}

// TestBrowserRejectsRelativeDSLDataSource R-11：DSL 核心语句文件句柄（LOOP 数据源、IF exist）
// 字面相对 → 执行前预校验拒绝（不启动浏览器）。
func TestBrowserRejectsRelativeDSLDataSource(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{"LOOP 数据源", "LOOP row=#\"queries.csv\".lines\n   SLP 10\nEND\n", "queries.csv"},
		{"IF exist", "IF exist \"done.flag\"\n   SLP 10\nEND\n", "done.flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := prevalidateScript(tc.script)
			if len(msgs) == 0 {
				t.Fatalf("字面相对路径应被预校验拒绝，script=%q", tc.script)
			}
			joined := strings.Join(msgs, "; ")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("消息应含原值 %q，got %v", tc.want, msgs)
			}
			if !strings.Contains(joined, "绝对路径") {
				t.Fatalf("消息应含路径约束说明，got %v", msgs)
			}
		})
	}
}
