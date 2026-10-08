package bridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestAgentWizardSubjectsRouted（场景向导接线守卫，2026-10-04）：前端四个向导方法面
// （agent-wizard-probe / agent-wizard-compose / agent-wizard-generate / agent-wizard-skip）必须登记在桥的
// frontMethodSubjects —— 否则 PublishEvent 对未登记的单字方法 type **静默丢弃**
// （返回 nil,nil）→ 前端「探测 / 合成 / 生成 / 稍后」点了没反应且无报错。
//
// 黑盒断言方式：在总线上订阅同名相对主题并写回 v.Result（对齐 server 方法面 handler 形态），
// 再经 PublishEvent 驱动 —— 结果非 nil 且带本主题订阅者写回 = 已登记 + 路由到正确主题。
func TestAgentWizardSubjectsRouted(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()

	for _, subj := range []string{"agent-wizard-probe", "agent-wizard-compose", "agent-wizard-generate", "agent-wizard-skip"} {
		t.Run(subj, func(t *testing.T) {
			got := make(chan map[string]any, 1)
			_, err := bus.On(subj, 0, func(_ context.Context, _ string, v *mq.Value) error {
				var req map[string]any
				_ = json.Unmarshal(v.Payload, &req)
				select {
				case got <- req:
				default:
				}
				v.Result = map[string]any{"ok": true, "subject": subj}
				return nil
			})
			if err != nil {
				t.Fatalf("subscribe %s: %v", subj, err)
			}

			res, errs := br.PublishEvent(subj, "{}")
			if len(errs) != 0 {
				t.Fatalf("PublishEvent(%s) errs=%v（应被登记并透传）", subj, errs)
			}
			m, ok := res.(map[string]any)
			if !ok || m["ok"] != true {
				t.Fatalf("PublishEvent(%s) result=%v（nil,nil = 主题被 frontMethodSubjects 静默丢弃）", subj, res)
			}
			if m["subject"] != subj {
				t.Fatalf("PublishEvent(%s) 结果非本主题订阅者写回：%+v", subj, m)
			}
			req := <-got
			if req["instance_id"] != "inst-test" {
				t.Fatalf("%s 请求未注入 instance_id：%+v", subj, req)
			}
		})
	}
}
