package appwire

import (
	"context"
	"testing"
)

// TestBuildSpineNoServer 校验共用脊柱的最小装配：NoServer → 仅总线 + 门面；
// Start 为空操作；Shutdown 可安全收尾且幂等（Scope.Stop 只执行一次）。
func TestBuildSpineNoServer(t *testing.T) {
	app, err := Build(Snapshot(BootConfig{Form: "desktop", NoServer: true}), BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if app.Bus == nil || app.Facade == nil {
		t.Fatal("bus/facade must be non-nil")
	}
	if app.Scope == nil {
		t.Fatal("app.Scope 必须非 nil（生命周期根）")
	}
	if app.Kernel == nil {
		t.Fatal("app.Kernel 必须非 nil（进程内插件编排）")
	}
	if app.Kernel.Scope() != app.Scope {
		t.Fatal("app.Scope 应等于 Kernel 根 Scope")
	}
	if app.Kernel.Started() {
		t.Fatal("Build 只构造：Kernel 不应已启动")
	}
	if app.Server != nil || app.Filesys != nil {
		t.Fatal("server/filesys must be nil when disabled")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !app.Kernel.Started() {
		t.Fatal("Start 后 Kernel 应为已启动")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("重复 Start（应为幂等 no-op）: %v", err)
	}
	app.Shutdown()
	if !app.Scope.Stopped() {
		t.Fatal("Shutdown 后 Scope 应为已停止")
	}
	if app.Kernel.Started() {
		t.Fatal("Shutdown 后 Kernel 不应为已启动")
	}
	app.Shutdown() // 幂等：不得重复停止 / panic
}

// TestBuildShutdownBeforeStart 校验未 Start 直接 Shutdown：Kernel.Stop 仍停止 root Scope
// 并关闭总线（不清空字段，仅保证资源收口且幂等）。
func TestBuildShutdownBeforeStart(t *testing.T) {
	app, err := Build(Snapshot(BootConfig{Form: "desktop", NoServer: true}), BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	app.Shutdown()
	if !app.Kernel.Scope().Stopped() {
		t.Fatal("未 Start 直接 Shutdown 也应停止 root Scope")
	}
	app.Shutdown() // 幂等
}

// TestBuildSpineWithFilesysNoServer 校验 files 插件（Start/Stop 无 ctx）随 Kernel 启停：
// NoServer + Filesys=true 时 filesys 由 App.Start 启动、App.Shutdown 逆序停止。
func TestBuildSpineWithFilesysNoServer(t *testing.T) {
	app, err := Build(Snapshot(BootConfig{Form: "desktop", NoServer: true, Filesys: true}), BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if app.Filesys == nil {
		t.Fatal("Filesys 应在 Build 内构造（未启动）")
	}
	if app.Server != nil {
		t.Fatal("NoServer 时 Server 应为 nil")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	app.Shutdown()
	app.Shutdown() // 幂等
}
