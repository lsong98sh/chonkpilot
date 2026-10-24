//go:build windows && !debugport

package gui

// release 构建（未加 `-tags debugport`）下 `testserver.go` 不参与编译；本文件提供**空实现**，
// 唯一目的是让仍引用 `testServer`/`newTestServer` 的调用点（hostapi.AttachTestServer、remote.go
// 的 flash 接线）保持可编译。
//
// 取舍：release 下不注册 `--test-port`（host/main.go 由 gui.DebugPortEnabled 门控），故这些
// 调用点**恒不可达**；`newTestServer` 恒返回 nil，即便被误调用也不会打开任何监听端口。
// 相对于「把 hostEnv.testSrv 改为接口 + 门控全部构造点」，本空实现改动面最小、回归风险最低。

import (
	"context"
	"errors"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
)

// testServer 是 release 构建下 `*testServer` 的空占位（无任何测试通道逻辑）。
type testServer struct {
	hwnd    uintptr
	destroy func()
	handler *appHandler
}

// newTestServer 在 release 构建下恒返回 nil（不构造任何测试通道）。
func newTestServer(w interface{}, br *bridge.Bridge) *testServer { return nil }

// Start 在 release 构建下不会被调用（flag 未注册 → testPort 恒 0）；返回错误仅作兜底。
func (s *testServer) Start(port int) error {
	return errors.New("--test-port 未编入本构建（release）；请使用 -tags debugport 构建")
}

// Shutdown 兜底空实现（host 收尾对 nil/占位通道调用）。
func (s *testServer) Shutdown(ctx context.Context) error { return nil }

// SetReady 兜底空实现。
func (s *testServer) SetReady() {}

// RegisterChat 兜底空实现。
func (s *testServer) RegisterChat(h *windowHost) {}

// UnregisterChat 兜底空实现。
func (s *testServer) UnregisterChat(windowID string) {}

// InjectConsoleCapture 兜底空实现。
func (s *testServer) InjectConsoleCapture(chromium interface{ Init(script string) }) {}

// SetQuit 兜底空实现。
func (s *testServer) SetQuit(fn func()) {}
