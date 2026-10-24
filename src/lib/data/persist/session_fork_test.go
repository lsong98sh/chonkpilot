// data-session-fork 信封测试（SESSION-COMP-1 P4）：自举建对话 → 派生 → 新库落盘 + 回带新 id。
package persist

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// call 发一次 data 请求并等应答（同主题回包，按 req_id 匹配）。
func call(t *testing.T, s *Service, subject string, payload map[string]any) map[string]any {
	t.Helper()
	reqID := "t-" + subject
	payload["req_id"] = reqID
	out := make(chan map[string]any, 4)
	sub, err := s.Bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if m["req_id"] != reqID {
			return nil
		}
		if _, has := m["ok"]; !has {
			return nil
		}
		select {
		case out <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("订阅 %s: %v", subject, err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	b, _ := json.Marshal(payload)
	_ = s.Bus.Emit(context.Background(), subject, b).Wait()
	select {
	case m := <-out:
		return m
	default:
		t.Fatalf("%s 无应答", subject)
		return nil
	}
}

func TestSessionForkMQ(t *testing.T) {
	wd := t.TempDir()
	s := newSweepTestService(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	const inst = "ins-fork"
	s.onInstanceRegister(instanceRegister, instancePayload(inst, wd, ""))

	// 自举建对话（根会话缺 dialog_id → 确定性 dialog_id 回带）。
	ensure := call(t, s, "data-session-ensure-session", map[string]any{
		"instance_id": inst, "session_id": "root-1",
	})
	res, _ := ensure["result"].(map[string]any)
	dialogID, _ := res["dialog_id"].(string)
	if dialogID == "" {
		t.Fatalf("自举应回带 dialog_id: %+v", ensure)
	}

	prj, err := data.PrjUsr(inst)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}

	// 派生 full。
	forked := call(t, s, "data-session-fork", map[string]any{
		"instance_id": inst, "dialog_id": dialogID, "title": "分支",
	})
	if ok, _ := forked["ok"].(bool); !ok {
		t.Fatalf("fork 应成功: %+v", forked)
	}
	fr, _ := forked["result"].(map[string]any)
	newDlg, _ := fr["dialog_id"].(string)
	newSid, _ := fr["session_id"].(string)
	if newDlg == "" || newDlg == dialogID || newSid == "" {
		t.Fatalf("fork 结果不合法: %+v", fr)
	}
	if _, err := os.Stat(data.DialogDBPath(prj.Path(), newDlg)); err != nil {
		t.Fatalf("派生库文件缺失: %v", err)
	}

	// 缺 dialog_id → 明确失败。
	bad := call(t, s, "data-session-fork", map[string]any{"instance_id": inst})
	if ok, _ := bad["ok"].(bool); ok {
		t.Fatalf("缺 dialog_id 应失败: %+v", bad)
	}
}
