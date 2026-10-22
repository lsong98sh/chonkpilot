package appwire

import (
	"context"
	"testing"
)

// TestBuildSpineNoServer 校验共用脊柱的最小装配：NoServer → 仅总线 + 门面；
// Start 为空操作；Shutdown 可安全收尾（幂等前置一次）。
func TestBuildSpineNoServer(t *testing.T) {
	app, err := Build(Snapshot(BootConfig{Form: "desktop", NoServer: true}), BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if app.Bus == nil || app.Facade == nil {
		t.Fatal("bus/facade must be non-nil")
	}
	if app.Server != nil || app.Filesys != nil {
		t.Fatal("server/filesys must be nil when disabled")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	app.Shutdown()
}
