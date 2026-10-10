// flash_server_test.go — flash 自动拉起服务端（`flash.ServerOptions.Ensure`）纯逻辑验证
// （🆕 2026-10-10；62 §3A / 19 §8.11）。
//
// 覆盖：UDS 已连通 → 复用不拉起；未连通 → spawn（--autoexit）后有界重试；超时 → 明确错误；
// spawn 失败 → 错误透传。
package flash_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/flash"
)

// TestEnsureServerReusesRunningServer：UDS 已连通 → (started=false, nil)，且**不 spawn**。
func TestEnsureServerReusesRunningServer(t *testing.T) {
	spawned := 0
	started, err := flash.ServerOptions{
		ServerExe: "C:/x/chonkpilot-server.exe",
		Probe:     func(string) error { return nil }, // 已连通
		Spawn:     func(string, []string) error { spawned++; return nil },
		Sleep:     func(time.Duration) {},
	}.Ensure()
	if err != nil || started {
		t.Fatalf("Ensure = (started=%v, err=%v)，期望 (false, nil)", started, err)
	}
	if spawned != 0 {
		t.Fatalf("已连通不应拉起 server，实际 spawn %d 次", spawned)
	}
}

// TestEnsureServerSpawnsWithAutoExit：未连通 → spawn `server --autoexit`，随后重试探测成功。
func TestEnsureServerSpawnsWithAutoExit(t *testing.T) {
	var gotExe string
	var gotArgs []string
	probe := 0
	started, err := flash.ServerOptions{
		ServerExe: "C:/x/chonkpilot-server.exe",
		Probe: func(string) error {
			probe++
			if probe == 1 { // 首次探测：未连通
				return errors.New("no server")
			}
			return nil
		},
		Spawn: func(exe string, args []string) error {
			gotExe, gotArgs = exe, args
			return nil
		},
		Sleep:    func(time.Duration) {},
		Timeout:  time.Second,
		Interval: 10 * time.Millisecond,
	}.Ensure()
	if err != nil || !started {
		t.Fatalf("Ensure = (started=%v, err=%v)，期望 (true, nil)", started, err)
	}
	if gotExe != "C:/x/chonkpilot-server.exe" {
		t.Fatalf("spawn exe = %q", gotExe)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "--autoexit" {
		t.Fatalf("spawn args = %v，期望 [--autoexit]", gotArgs)
	}
}

// TestEnsureServerTimeout：spawn 后始终连不上 → 有界重试（timeout/interval）后返回明确错误。
func TestEnsureServerTimeout(t *testing.T) {
	probes := 0
	started, err := flash.ServerOptions{
		ServerExe: "C:/x/chonkpilot-server.exe",
		Probe:     func(string) error { probes++; return errors.New("no server") },
		Spawn:     func(string, []string) error { return nil },
		Sleep:     func(time.Duration) {},
		Timeout:   50 * time.Millisecond,
		Interval:  10 * time.Millisecond,
	}.Ensure()
	if !started {
		t.Fatal("应报告已拉起（started=true）")
	}
	if err == nil || !strings.Contains(err.Error(), "仍无法连接") {
		t.Fatalf("超时应返回明确错误，得 %v", err)
	}
	// 1 次前置探测 + 5 次重试（50ms/10ms）。
	if probes != 6 {
		t.Fatalf("探测次数 = %d，期望 6（有界重试）", probes)
	}
}

// TestEnsureServerSpawnError：spawn 失败 → 错误透传、不进入重试。
func TestEnsureServerSpawnError(t *testing.T) {
	started, err := flash.ServerOptions{
		ServerExe: "C:/x/chonkpilot-server.exe",
		Probe:     func(string) error { return errors.New("no server") },
		Spawn:     func(string, []string) error { return errors.New("boom") },
		Sleep:     func(time.Duration) {},
	}.Ensure()
	if started || err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Ensure = (started=%v, err=%v)，期望 (false, spawn err)", started, err)
	}
}

// TestServerExeName 断言产物命名（供 flash 同目录定位）。
func TestServerExeName(t *testing.T) {
	if flash.ServerExeName() == "" {
		t.Fatal("ServerExeName 不应为空")
	}
}

// TestEnsureServerFindsSiblingServerDir：未给 --server-exe → 由 flash exe 推导，
// 同目录无 server 时回落到**同级 `server/` 子目录**（随 build-gui.ps1 产物布局），并 spawn 之。
func TestEnsureServerFindsSiblingServerDir(t *testing.T) {
	base := t.TempDir()
	flashDir, srvDir := filepath.Join(base, "gui"), filepath.Join(base, "server")
	mustMkdir(t, flashDir)
	mustMkdir(t, srvDir)
	flashExe := filepath.Join(flashDir, "chonkpilot-flash.exe")
	srvExe := filepath.Join(srvDir, flash.ServerExeName())
	if err := os.WriteFile(flashExe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srvExe, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	probe := 0
	var gotExe string
	started, err := flash.ServerOptions{
		ExePath: flashExe,
		Probe: func(string) error {
			probe++
			if probe == 1 {
				return errors.New("no server")
			}
			return nil
		},
		Spawn:    func(exe string, _ []string) error { gotExe = exe; return nil },
		Sleep:    func(time.Duration) {},
		Timeout:  time.Second,
		Interval: time.Millisecond,
	}.Ensure()
	if err != nil || !started {
		t.Fatalf("Ensure = (started=%v, err=%v)，期望 (true, nil)", started, err)
	}
	if gotExe != srvExe {
		t.Fatalf("spawn exe = %q，期望 %q（同级 server/ 回落）", gotExe, srvExe)
	}
}

// TestEnsureServerNoCandidate：flash 同目录与同级 server/ 均无 server exe → 明确错误。
func TestEnsureServerNoCandidate(t *testing.T) {
	flashExe := filepath.Join(t.TempDir(), "chonkpilot-flash.exe")
	started, err := flash.ServerOptions{
		ExePath: flashExe,
		Probe:   func(string) error { return errors.New("no server") },
		Spawn:   func(string, []string) error { return nil },
		Sleep:   func(time.Duration) {},
	}.Ensure()
	if started || err == nil || !strings.Contains(err.Error(), "未找到服务端 exe") {
		t.Fatalf("Ensure = (started=%v, err=%v)，期望「未找到服务端 exe」", started, err)
	}
}
