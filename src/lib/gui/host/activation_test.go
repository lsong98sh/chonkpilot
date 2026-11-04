//go:build windows

package host

import (
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/singleinstance"
)

// fakeActivationEnv 只实现 handleActivation 依赖的窗口面（不起真实窗口）。
type fakeActivationEnv struct {
	activated []string
	hasWindow bool
}

func (f *fakeActivationEnv) ActivateProjectAny(preferred string) bool {
	f.activated = append(f.activated, preferred)
	return f.hasWindow
}

// TestHandleActivation：49 §2.22.6-5 —— 有窗口则只激活；一个都没开才执行「打开工作目录」。
func TestHandleActivation(t *testing.T) {
	cases := []struct {
		name        string
		cmd         singleinstance.Command
		hasWindow   bool
		wantActivat []string
		wantOpen    []string
	}{
		{
			name:        "有窗口：只激活，不打开",
			cmd:         singleinstance.Command{Cmd: singleinstance.CmdActivate, WorkDir: `E:\work\demo`},
			hasWindow:   true,
			wantActivat: []string{`E:\work\demo`},
		},
		{
			name:        "无窗口：执行打开工作目录",
			cmd:         singleinstance.Command{Cmd: singleinstance.CmdActivate, WorkDir: `E:\work\demo`},
			wantActivat: []string{`E:\work\demo`},
			wantOpen:    []string{`E:\work\demo`},
		},
		{
			name:        "命令不带 workdir：回落启动 work_dir",
			cmd:         singleinstance.Command{Cmd: singleinstance.CmdActivate},
			wantActivat: []string{"E:\\startup"},
			wantOpen:    []string{"E:\\startup"},
		},
		{
			name:      "未知命令：忽略",
			cmd:       singleinstance.Command{Cmd: "unknown"},
			hasWindow: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := &fakeActivationEnv{hasWindow: tc.hasWindow}
			var opened []string
			handleActivation(env, func(dir string) error {
				opened = append(opened, dir)
				return nil
			}, `E:\startup`, tc.cmd)
			if len(env.activated) != len(tc.wantActivat) {
				t.Fatalf("ActivateProjectAny 调用次数 = %d，期望 %d", len(env.activated), len(tc.wantActivat))
			}
			for i, want := range tc.wantActivat {
				if env.activated[i] != want {
					t.Fatalf("ActivateProjectAny[%d] = %q，期望 %q", i, env.activated[i], want)
				}
			}
			if len(opened) != len(tc.wantOpen) {
				t.Fatalf("open 调用 = %v，期望 %v", opened, tc.wantOpen)
			}
			for i, want := range tc.wantOpen {
				if opened[i] != want {
					t.Fatalf("open[%d] = %q，期望 %q", i, opened[i], want)
				}
			}
		})
	}
}

// TestHandleActivationOpenErrorDoesNotPanic：打开失败只记日志，不 panic（第二实例已退出，无回执对象）。
func TestHandleActivationOpenErrorDoesNotPanic(t *testing.T) {
	env := &fakeActivationEnv{}
	handleActivation(env, func(string) error { return errTestOpen }, `E:\startup`,
		singleinstance.Command{Cmd: singleinstance.CmdActivate, WorkDir: `E:\work\demo`})
}

type testOpenError struct{}

func (testOpenError) Error() string { return "open failed" }

var errTestOpen = testOpenError{}
