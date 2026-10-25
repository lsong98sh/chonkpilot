package bridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	data "github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newFacadeBridgeEnv 建「桥 + 同总线 persist」测试环境。
//
// 2026-10-25 单一入口改造后，桥的 data-* 一律走总线（不再 inline 直调门面），故夹具 =
// 真 persist 服务 + 一条 instance-register；返回 (桥, 总线, instanceID, persist 服务)。
func newFacadeBridgeEnv(t *testing.T) (*Bridge, mq.Bus, string, *persist.Service) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	svc := persist.New(bus, persist.Options{UsrPath: filepath.Join(t.TempDir(), "usr.db")})
	if err := svc.Start(); err != nil {
		t.Fatalf("persist.Start: %v", err)
	}
	t.Cleanup(func() {
		svc.Stop()
		data.Reset()
		_ = bus.Close()
	})
	const instanceID = "ins-bridge-test"
	wd := t.TempDir()
	body, _ := json.Marshal(map[string]any{"instance_id": instanceID, "work_dir": wd, "data_dir": ""})
	if err := bus.Emit(context.Background(), "instance-register", body).Wait().Err(); err != nil {
		t.Fatalf("instance-register: %v", err)
	}
	br := New(instanceID, wd, "", func(string) {}, bus)
	return br, bus, instanceID, svc
}
