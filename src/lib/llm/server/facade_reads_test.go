// 场景 / 记忆类别 / ask 回查的门面直调回归（统一异步第二批补遗）：轮次构造、提示词组装与
// ask 恢复路径不得再走 `data-*` 总线同步应答（Emit().Wait）。
//
// 判据：门面非空 → 全部动作直调；此处用**未启动 persist 的总线**构造，若任一路径回退总线，
// 应答超时（3s）后取值/报错，断言即失败。
package server

import (
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

func TestServerDataReadsFacadeDirect(t *testing.T) {
	data.Reset()
	t.Cleanup(data.Reset)
	wd := t.TempDir()
	data.Register("ins-facade-read", wd, "")

	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	api := inline.NewWithOptions(nil, persist.Options{UsrPath: t.TempDir() + "/usr.db"})
	s := &Server{cfg: api, bus: bus}

	if _, err := api.ScenarioSave(facade.ScenarioSaveRequest{
		InstanceID: "ins-facade-read",
		Scenario: facade.Scenario{
			ID:          "facade-read",
			Level:       "user",
			Description: "场景描述",
			Agents: []facade.ScenarioAgent{
				{Name: "main", IsMain: true, Prompt: "p"},
			},
		},
	}); err != nil {
		t.Fatalf("ScenarioSave: %v", err)
	}

	start := time.Now()
	desc, level, agents := s.loadScenario("ins-facade-read", "facade-read")
	if desc != "场景描述" || level != "user" || len(agents) != 1 || agents[0].Name != "main" || !agents[0].IsMain {
		t.Fatalf("loadScenario 未直读门面: desc=%q level=%q agents=%+v", desc, level, agents)
	}
	if names := s.memoryCategoryNames("ins-facade-read", wd); len(names) == 0 {
		t.Fatalf("memoryCategoryNames 未读到类别清单")
	}
	if items, err := s.dataAskPending("ins-facade-read", "", true); err != nil || len(items) != 0 {
		t.Fatalf("dataAskPending: items=%+v err=%v", items, err)
	}
	// 回退总线的每条请求会等 3s；直调应远低于该量级。
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("门面读取耗时异常（疑似回退总线）: %v", elapsed)
	}
	t.Cleanup(closeTestDataStore)
}
