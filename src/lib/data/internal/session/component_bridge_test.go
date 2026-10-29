// component_bridge_test.go —— P4-2 批次二：组件调用 + 事件映射**薄适配注入点**的 L1。
//
// 覆盖：`componentEnsureSession`（首次 Created → 发一次 `session-new`；二次不重复、行不变；
// 缺对话库报错且不建库；子会话/属性落行）· `componentSetTitle`（主 session 同步 `dialogs.title` +
// 发 `data-session-title-changed`；子会话不同步；未知会话报错不发事件）。
package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// newBridgeEnv 复用 `setup` 的隔离环境（实例 + 对话 m1/c1），另建**带总线**的服务
// （`setup` 返回的服务无 Bus → 发不出订阅面事件）。
func newBridgeEnv(t *testing.T) (*Service, mq.Bus, string, string) {
	t.Helper()
	_, instID, _, dlgID := setup(t)
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return New(kernel.NewBase(bus, kernel.Options{})), bus, instID, dlgID
}

// watchTopic 订阅某主题，返回载荷通道（缓冲 4；订阅顺序 0 = 立即派发）。
func watchTopic(t *testing.T, bus mq.Bus, topic string) <-chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 4)
	if _, err := bus.On(topic, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			select {
			case ch <- m:
			default:
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("订阅 %s: %v", topic, err)
	}
	return ch
}

// waitEvent 等一条事件（超时 = 失败）。
func waitEvent(t *testing.T, ch <-chan map[string]any, topic string) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatalf("未收到 %s", topic)
		return nil
	}
}

// expectNoEvent 断言指定时间内无事件（幂等 / 失败路径不广播）。
func expectNoEvent(t *testing.T, ch <-chan map[string]any, topic string) {
	t.Helper()
	select {
	case m := <-ch:
		t.Fatalf("%s 不应广播: %+v", topic, m)
	case <-time.After(200 * time.Millisecond):
	}
}

// sessionRowOf 直读对话库 `sessions` 行（断言落库形状）。
func sessionRowOf(t *testing.T, prjUsr, dialogID, sessionID string) (data.Record, bool) {
	t.Helper()
	db, release, err := data.OpenExistingDialog(prjUsr, dialogID)
	if err != nil {
		t.Fatalf("OpenExistingDialog: %v", err)
	}
	defer release()
	var rec data.Record
	ok, err := db.Table("sessions").Get(sessionID, &rec)
	if err != nil {
		t.Fatalf("读 sessions 行: %v", err)
	}
	return rec, ok
}

// TestComponentEnsureSessionBridge：组件版幂等建会话 —— Created 才广播 session-new，载荷不变。
func TestComponentEnsureSessionBridge(t *testing.T) {
	svc, bus, instID, dlgID := newBridgeEnv(t)
	prj, err := data.PrjUsr(instID)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	newCh := watchTopic(t, bus, "session-new")

	// ① 首次：子会话 + kind + 一个属性。
	res, err := svc.componentEnsureSession(prj, facade.SessionEnsureRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c2", ParentSessionID: "m1",
		Kind: "sub_chat", Lifecycle: facade.SessionLifecycleChatting, Agent: "ag",
	})
	if err != nil {
		t.Fatalf("componentEnsureSession: %v", err)
	}
	if !res.OK || res.DialogID != dlgID {
		t.Fatalf("出参不符: %+v", res)
	}
	m := waitEvent(t, newCh, "session-new")
	if m["instance_id"] != instID || m["session_id"] != "c2" ||
		m["parent_session_id"] != "m1" || m["dialog_id"] != dlgID {
		t.Fatalf("session-new 载荷不符合 61 §4.1: %+v", m)
	}
	row, ok := sessionRowOf(t, prj.Path(), dlgID, "c2")
	if !ok {
		t.Fatal("会话行未落库")
	}
	if kernel.Sval(row["parent_id"]) != "m1" || kernel.Sval(row["kind"]) != "sub_chat" ||
		kernel.Sval(row["lifecycle"]) != facade.SessionLifecycleChatting ||
		kernel.Sval(row["agent"]) != "ag" || kernel.Sval(row["title"]) != "c2" {
		t.Fatalf("会话行字段不符: %+v", row)
	}
	if _, has := row["goal"]; has {
		t.Fatalf("空属性不得落键: %+v", row)
	}

	// ② 幂等：二次调用**不重复广播**，行不变（title / updated_at 均不改）。
	beforeTitle, beforeUpdated := kernel.Sval(row["title"]), kernel.Sval(row["updated_at"])
	if _, err := svc.componentEnsureSession(prj, facade.SessionEnsureRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c2", ParentSessionID: "m1",
		Kind: "sub_chat", Goal: "不该落库",
	}); err != nil {
		t.Fatalf("componentEnsureSession 二次: %v", err)
	}
	expectNoEvent(t, newCh, "session-new")
	after, _ := sessionRowOf(t, prj.Path(), dlgID, "c2")
	if kernel.Sval(after["title"]) != beforeTitle || kernel.Sval(after["updated_at"]) != beforeUpdated {
		t.Fatalf("已存在时行不得被改: %+v → %+v", row, after)
	}
	if _, has := after["goal"]; has {
		t.Fatalf("已存在时不得补写属性: %+v", after)
	}

	// ③ 对话库缺失 → 报错、不广播、**不建库**。
	missing, _ := data.NewDialogID()
	if _, err := svc.componentEnsureSession(prj, facade.SessionEnsureRequest{
		InstanceID: instID, DialogID: missing, SessionID: "x",
	}); err == nil {
		t.Fatal("对话库缺失应报错")
	}
	expectNoEvent(t, newCh, "session-new")
	if _, release, err := data.OpenExistingDialog(prj.Path(), missing); err == nil {
		release()
		t.Fatal("不得静默建空库")
	}
}

// TestComponentSetTitleBridge：组件版改名 —— 主 session 同步 dialogs.title + 广播
// data-session-title-changed（不带 instance_id）；子会话不同步；未知会话报错不发事件。
func TestComponentSetTitleBridge(t *testing.T) {
	svc, bus, instID, dlgID := newBridgeEnv(t)
	prj, err := data.PrjUsr(instID)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	titleCh := watchTopic(t, bus, "data-session-title-changed")

	// ① 主 session：同步 dialogs.title + 事件（全局：无 instance_id）。
	if _, err := svc.componentSetTitle(prj, facade.SessionTitleRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1", Title: "新主标题",
	}); err != nil {
		t.Fatalf("componentSetTitle(main): %v", err)
	}
	m := waitEvent(t, titleCh, "data-session-title-changed")
	if m["session_id"] != "m1" || m["title"] != "新主标题" || m["dialog_id"] != dlgID {
		t.Fatalf("标题事件载荷不符: %+v", m)
	}
	if _, has := m["instance_id"]; has {
		t.Fatalf("该主题不带 instance_id（61 §3.2 全局）: %+v", m)
	}
	var row data.Record
	if ok, _ := prj.Table("dialogs").Get(dlgID, &row); !ok || kernel.Sval(row["title"]) != "新主标题" {
		t.Fatalf("dialogs.title 未同步: %+v", row)
	}

	// ② 子会话：不同步 dialogs.title，但照发事件。
	if _, err := svc.componentSetTitle(prj, facade.SessionTitleRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c1", Title: "新子标题",
	}); err != nil {
		t.Fatalf("componentSetTitle(child): %v", err)
	}
	m2 := waitEvent(t, titleCh, "data-session-title-changed")
	if m2["session_id"] != "c1" || m2["title"] != "新子标题" {
		t.Fatalf("子会话事件载荷不符: %+v", m2)
	}
	if ok, _ := prj.Table("dialogs").Get(dlgID, &row); !ok || kernel.Sval(row["title"]) != "新主标题" {
		t.Fatalf("子会话改名不应动 dialogs.title: %+v", row)
	}

	// ③ 未知会话 → 报错且不发事件。
	if _, err := svc.componentSetTitle(prj, facade.SessionTitleRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "ghost", Title: "x",
	}); err == nil {
		t.Fatal("未知会话应报错")
	}
	expectNoEvent(t, titleCh, "data-session-title-changed")
}
