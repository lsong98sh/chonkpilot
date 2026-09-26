package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// TestMain 注入内部 executor 的子进程上下文（CHONKPILOT_INSTANCE）并设置临时根：宿主 spawn
// executor 时 cli.Run 会以该 instance 设置 paths.TempRoot；`!/` 预校验依赖它（缺 instance 属异常）。
func TestMain(m *testing.M) {
	os.Setenv("CHONKPILOT_INSTANCE", "unittest-desktop")
	_, _ = paths.SetTempRoot("unittest-desktop")
	os.Exit(m.Run())
}

// prevalidateDesktop 解析 desktop 脚本并返回 DSL 字面落盘路径的预校验违规消息。
func prevalidateDesktop(t *testing.T, script string) []string {
	t.Helper()
	ctx := &runCtx{vars: map[string]float64{}}
	ast, err := dsl.Parse(script, desktopActions(ctx))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	return prevalidateScriptPaths(ast)
}

// TestDesktopRejectsRelativeDSLWritePaths R-11：SHT / WIN 链内 SHT / 行尾 `=> #"file"` 字面相对 → 预校验拒绝。
func TestDesktopRejectsRelativeDSLWritePaths(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{"SHT 整屏", `SHT "shot.png"`, "shot.png"},
		{"SHT 区域", `SHT 0,0,100,100 "shot.png"`, "shot.png"},
		{"WIN 链内 SHT", `WIN "记事本" sht "win.png"`, "win.png"},
		{"重定向目标", `WIN list => #"wins.txt"`, "wins.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := prevalidateDesktop(t, tc.script)
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

// TestDesktopAcceptsAbsoluteDSLWritePaths 绝对路径、!/ 临时目录与含 {{}} 插值的路径不被预校验拒绝。
func TestDesktopAcceptsAbsoluteDSLWritePaths(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "shot.png")
	script := "SHT \"" + abs + "\"\nSHT \"!/shot.png\"\nWIN list => #\"{{dst}}\"\n"
	if msgs := prevalidateDesktop(t, script); len(msgs) > 0 {
		t.Fatalf("绝对路径 / !/ / {{}} 插值不应被预校验拒绝，got %v", msgs)
	}
}

// TestDesktopRunRejectsRelativeSHTPath 顶层入口：SHT 字面相对路径 → 失败（不执行任何指令）。
func TestDesktopRunRejectsRelativeSHTPath(t *testing.T) {
	res := HandleDesktopRun(map[string]interface{}{"script": `SHT "shot.png"`})
	if res.Success {
		t.Fatalf("相对落盘路径应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "shot.png") || !strings.Contains(res.Error, "绝对路径") {
		t.Fatalf("错误消息应含原值与约束，got %q", res.Error)
	}
}

// TestDesktopRunRejectsRelativeScriptFile 顶层入口：file 相对路径 → 失败。
func TestDesktopRunRejectsRelativeScriptFile(t *testing.T) {
	res := HandleDesktopRun(map[string]interface{}{"file": "desktop.script"})
	if res.Success || !strings.Contains(res.Error, "desktop.script") {
		t.Fatalf("相对 file 应失败并含原值，got %+v", res)
	}
}

// TestDesktopRejectsRelativeDSLDataSources R-11：DSL 核心语句文件句柄（LOOP 数据源、IF exist
// 的句柄与字符串路径）字面相对 → 执行前预校验拒绝。
func TestDesktopRejectsRelativeDSLDataSources(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{"LOOP 数据源", "LOOP row=#\"rows.csv\".lines\n   SLP 10\nEND\n", "rows.csv"},
		{"IF exist 句柄", "IF exist #\"done.flag\"\n   SLP 10\nEND\n", "done.flag"},
		{"IF exist 字符串", "IF exist \"done.flag\"\n   SLP 10\nEND\n", "done.flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := prevalidateDesktop(t, tc.script)
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
