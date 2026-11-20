package appwire

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
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

// TestBuildSpineWithServer 校验 persist 真节点：agent（server）借用注入的 persist.Service，
// Start/Stop 由 Kernel persist 插件编排 —— server.Start/Stop 都不动 persist（借用不持有）。
// 断言口径：① Start 后 persist 在线（data-* 请求面应答）；② Shutdown 幂等不 panic。
func TestBuildSpineWithServer(t *testing.T) {
	app, err := Build(Snapshot(BootConfig{Form: "desktop", Filesys: true}), BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if app.Server == nil {
		t.Fatal("缺省装配应有 server（agent 插件）")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// persist 真节点在线：user-config 域（全局级，无需 instance_id）load 走总线应能应答。
	// persist 的请求-应答 = 同主题回执（req_id 关联）；测试订阅一次捕获回执。
	type reply struct {
		OK     bool `json:"ok"`
		Result any  `json:"result"`
	}
	got := make(chan reply, 1)
	sub, err := app.Bus.On("data-user-config-load", 1, func(_ context.Context, _ string, v *mq.Value) error {
		if len(v.Payload) > 0 && v.Payload[0] == '{' {
			var r reply
			if err := json.Unmarshal(v.Payload, &r); err == nil && r.OK {
				select {
				case got <- r:
				default:
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	_ = app.Bus.Publish("data-user-config-load", []byte(`{"id":"llms"}`))
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("persist 未应答 user-config load（真节点未在线？）")
	}
	app.Shutdown()
	app.Shutdown() // 幂等
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
