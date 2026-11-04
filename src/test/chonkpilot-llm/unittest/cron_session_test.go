// cron_session_test.go —— 定时任务会话归属（SANDBOX-WRITE-1 P4 / C11）的 L2 用例。
//
// 驱动 = 插件桥能力面（`plugin/v1/session.{ensure,ensure-turn,complete-turn}`，即 crontab
// 插件的真实调用路径：宿主 `rememberDialog` + 数据面落库）；断言 = **mq 消息面**
// （`data-session-get` / `data-session-list` / `data-session-history`）。
package llmtest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/transport"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// dataReq 发一条 data-* 请求并等 persist 应答（应答 = 同主题 + `ok` 字段）。
func dataReq(t *testing.T, bus mq.Bus, subject string, payload map[string]any) map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 1)
	sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if _, hasOK := m["ok"]; !hasOK {
			return nil // 请求无 ok，应答有
		}
		select {
		case ch <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", subject, err)
	}
	defer sub.Unsubscribe()
	if err := bus.Emit(context.Background(), subject, jb(payload)).Wait().Err(); err != nil {
		t.Fatalf("emit %s: %v", subject, err)
	}
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatalf("等待 %s 应答超时", subject)
		return nil
	}
}

// dataResultOf 取应答的 result 对象（persist 统一信封 {req_id, ok, result}）。
func dataResultOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	return res
}

// TestCronSessionAttributionViaPluginCapability：根会话 / 子会话 parent 关系 + origin=scheduler +
// 顶层列表排除（C11 / C12）。
func TestCronSessionAttributionViaPluginCapability(t *testing.T) {
	bus := newBus(t)
	llm := startLLM(t, bus, textLLMServer("ok").URL, true)

	// 已知 work_dir 的实例（能力按 work_dir 解析实例 → 与 crontab 插件真实调用一致）。
	wd := t.TempDir()
	t.Cleanup(data.Reset) // 晚于 t.TempDir 注册 → LIFO 先关数据层连接，再删临时目录
	bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-cron", "client_type": "unittest", "work_dir": wd,
	})).Wait()

	pb := pluginbridge.New(pluginbridge.Options{
		Bus: bus, Addr: filepath.Join(t.TempDir(), "plugin.sock"), Version: "test",
	})
	if err := pb.Start(); err != nil {
		t.Fatalf("pluginbridge start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Close() })
	llm.SetPluginRegistry(pb.Registry())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: pb.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "cron.test"}); err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}

	root := "crontab-0a1b2c3d"
	sub := "crontab-ct_cafe0001"

	// ① 根会话：缺 dialog_id → 宿主自举建 crontab 对话并回带 dialog_id。
	resRoot, err := cli.Call(ctx, "plugin/v1/session.ensure", map[string]any{
		"work_dir": wd, "session_id": root, "kind": "crontab",
		"title": "定时任务", "origin": "scheduler",
	})
	if err != nil || !resRoot.OK {
		t.Fatalf("session.ensure(root): %+v %v", resRoot, err)
	}
	dialogID, _ := resRoot.Value.(map[string]any)["dialog_id"].(string)
	if dialogID == "" {
		t.Fatalf("根会话应回带 dialog_id: %+v", resRoot.Value)
	}

	// ② 子会话：显式带 dialog_id + parent_session_id。
	resSub, err := cli.Call(ctx, "plugin/v1/session.ensure", map[string]any{
		"work_dir": wd, "session_id": sub, "parent_session_id": root, "dialog_id": dialogID,
		"kind": "crontab_task", "title": "执行：echo hi", "origin": "scheduler",
	})
	if err != nil || !resSub.OK {
		t.Fatalf("session.ensure(sub): %+v %v", resSub, err)
	}

	// ③ 每轮 = 一个 turn（建轮 → 收轮）。
	if res, err := cli.Call(ctx, "plugin/v1/session.ensure-turn", map[string]any{
		"work_dir": wd, "session_id": sub, "dialog_id": dialogID, "turn_id": "turn-cron-1",
	}); err != nil || !res.OK {
		t.Fatalf("session.ensure-turn: %+v %v", res, err)
	}
	// 不显式带 dialog_id 的收轮路径：宿主应经 rememberDialog 补上（否则 data 面 dialog_required）。
	if res, err := cli.Call(ctx, "plugin/v1/session.complete-turn", map[string]any{
		"work_dir": wd, "turn_id": "turn-cron-1", "status": "done",
	}); err != nil || !res.OK {
		t.Fatalf("session.complete-turn: %+v %v", res, err)
	}

	// ③b 幂等：重复 ensure 根 / 子会话 + 重复 ensure-turn → 同一 dialog_id，不新增会话 / 轮次。
	if res, err := cli.Call(ctx, "plugin/v1/session.ensure", map[string]any{
		"work_dir": wd, "session_id": root, "kind": "crontab",
		"title": "定时任务", "origin": "scheduler",
	}); err != nil || !res.OK {
		t.Fatalf("session.ensure(root 重复): %+v %v", res, err)
	} else if got, _ := res.Value.(map[string]any)["dialog_id"].(string); got != dialogID {
		t.Fatalf("重复 ensure 根会话应回带同一 dialog_id: got=%q want=%q", got, dialogID)
	}
	if res, err := cli.Call(ctx, "plugin/v1/session.ensure", map[string]any{
		"work_dir": wd, "session_id": sub, "parent_session_id": root, "dialog_id": dialogID,
		"kind": "crontab_task", "title": "执行：echo hi", "origin": "scheduler",
	}); err != nil || !res.OK {
		t.Fatalf("session.ensure(sub 重复): %+v %v", res, err)
	}
	if res, err := cli.Call(ctx, "plugin/v1/session.ensure-turn", map[string]any{
		"work_dir": wd, "session_id": sub, "dialog_id": dialogID, "turn_id": "turn-cron-1",
	}); err != nil || !res.OK {
		t.Fatalf("session.ensure-turn(重复): %+v %v", res, err)
	}

	// ④ 经 mq 断言归属字段（子会话 parent = 根会话，origin = scheduler）。
	get := dataResultOf(t, dataReq(t, bus, "data-session-get", map[string]any{
		"req_id": "g1", "instance_id": "ins-cron", "id": sub, "dialog_id": dialogID,
	}))
	row, _ := get["data"].(map[string]any)
	if row == nil || row["kind"] != "crontab_task" || row["origin"] != "scheduler" {
		t.Fatalf("子会话 kind/origin 不符: %+v", get)
	}
	if row["parent_id"] != root || row["dialog_id"] != dialogID {
		t.Fatalf("子会话 parent/dialog 不符: %+v", row)
	}

	// ⑤ 顶层会话列表排除 crontab 对话（C12）。
	list := dataResultOf(t, dataReq(t, bus, "data-session-list", map[string]any{
		"req_id": "ls", "instance_id": "ins-cron",
	}))
	if items, _ := list["list"].([]any); len(items) != 0 {
		t.Fatalf("crontab 会话不应出现在顶层列表: %+v", items)
	}

	// ⑥ 轮次终态已落库（data-session-history）。
	hist := dataResultOf(t, dataReq(t, bus, "data-session-history", map[string]any{
		"req_id": "h1", "instance_id": "ins-cron",
		"data": map[string]any{"session_id": sub, "dialog_id": dialogID},
	}))
	body, _ := hist["messages"].(map[string]any)
	turns, _ := body["turns"].([]any)
	if len(turns) != 1 {
		t.Fatalf("应有 1 轮: %+v", hist)
	}
	turn, _ := turns[0].(map[string]any)
	if turn["status"] != "done" || turn["turn_id"] != "turn-cron-1" {
		t.Fatalf("轮次终态不符: %+v", turn)
	}
}
