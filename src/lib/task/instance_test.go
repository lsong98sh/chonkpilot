// MW-11 / MW-T23（24 §7 / §9）：实例**显式透传** —— 多实例下 task 数据面不再以空
// instance_id 走 data 面「唯一实例回退」（否则报 ErrInstanceIDRequired 或跨实例串库）。
//
// 覆盖：① 显式 instance → 各读各库（零差异，无 ErrInstanceIDRequired 语义）；
// ② 层内事件可解析 → 缺省 instance 仍有来源（回落 top_session 推实例，行为不变）；
// ③ 不可解析 → **明确报错**（Verify / VerifyPayload），不静默空串；
// ④ 数据面入口守卫：空 instance 一律拒绝（store.list / upsertRow / listTasks）；
// ⑤ 取消判定不可解析 instance → 按层不可知处理（不跨实例回读）；
// ⑥ 全程**零**空 instance 请求触达数据面（层不再产生空串）。
package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// scopedData 是**实例分库**的数据面桩（MW-T23）：行按 instance_id 分区；请求空 instance_id
// 且已存在多个实例 → 复刻 data `View.Resolve` 报 `persist: instance_id required (multiple instances)`；
// 仅 0/1 个实例时保留「唯一实例回退」。
type scopedData struct {
	mu       sync.Mutex
	rows     map[string]map[string]map[string]any // instance_id → task_id → row
	bus      mq.Bus
	subs     []mq.Sub
	emptyHit int // 收到空 instance_id 的请求数（断言「层不再以空串走数据面」）
}

func newScopedData(t *testing.T, bus mq.Bus) *scopedData {
	t.Helper()
	d := &scopedData{rows: map[string]map[string]map[string]any{}, bus: bus}
	for _, s := range []string{subjectTasktreeUpsert, subjectTasktreeList, subjectTasktreeTasks} {
		subj := s
		sh, err := bus.On(subj, 0, func(_ context.Context, _ string, v *mq.Value) error {
			d.handle(subj, v.Payload)
			return nil
		})
		if err != nil {
			t.Fatalf("scopedData 订阅 %s: %v", subj, err)
		}
		d.subs = append(d.subs, sh)
	}
	t.Cleanup(func() {
		for _, sh := range d.subs {
			_ = sh.Unsubscribe()
		}
	})
	return d
}

// emptyHits 空 instance 请求计数。
func (d *scopedData) emptyHits() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.emptyHit
}

func (d *scopedData) handle(subject string, payload []byte) {
	var probe struct {
		OK *bool `json:"ok"`
	}
	if json.Unmarshal(payload, &probe) == nil && probe.OK != nil {
		return // 应答自回环忽略
	}
	var req struct {
		ReqID      string         `json:"req_id"`
		InstanceID string         `json:"instance_id"`
		Data       map[string]any `json:"data"`
	}
	if json.Unmarshal(payload, &req) != nil || req.ReqID == "" {
		return
	}
	d.mu.Lock()
	inst := req.InstanceID
	if inst == "" {
		d.emptyHit++
		if len(d.rows) > 1 {
			d.mu.Unlock()
			d.replyErr(subject, req.ReqID, "persist: instance_id required (multiple instances)")
			return
		}
		for id := range d.rows {
			inst = id
		}
	}
	bucket := d.rows[inst]
	var result map[string]any
	switch subject {
	case subjectTasktreeUpsert:
		if bucket == nil {
			bucket = map[string]map[string]any{}
			d.rows[inst] = bucket
		}
		row := buildRow(req.Data)
		bucket[sval(row["task_id"])] = row
		result = map[string]any{"ok": true}
	case subjectTasktreeList, subjectTasktreeTasks:
		top := sval(req.Data["top_session"])
		includeClosed := boolVal(req.Data[includeClosedField])
		nodes := []any{}
		for _, row := range bucket {
			if top != "" && sval(row["top_session"]) != top {
				continue
			}
			if !includeClosed && scopedRowClosed(row) {
				continue
			}
			nodes = append(nodes, row)
		}
		key := "nodes"
		if subject == subjectTasktreeTasks {
			key = "list"
		}
		result = map[string]any{key: nodes}
	}
	d.mu.Unlock()
	if result != nil {
		d.reply(subject, req.ReqID, result)
	}
}

// scopedRowClosed 复刻 persist.closedRow。
func scopedRowClosed(row map[string]any) bool {
	if v, ok := row["closed"].(bool); ok && v {
		return true
	}
	return sval(row["deleted_at"]) != ""
}

func (d *scopedData) reply(subject, reqID string, result map[string]any) {
	b, _ := json.Marshal(map[string]any{"req_id": reqID, "ok": true, "result": result})
	d.bus.Emit(context.Background(), subject, b)
}

func (d *scopedData) replyErr(subject, reqID, msg string) {
	b, _ := json.Marshal(map[string]any{"req_id": reqID, "ok": false, "error": msg})
	d.bus.Emit(context.Background(), subject, b)
}

// TestMW11ExplicitInstanceMultiInstance：多实例下显式 instance 各读各库；缺省可从层内事件解析；
// 解析不到即明确报错；数据面入口拒绝空 instance。
func TestMW11ExplicitInstanceMultiInstance(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	d := newScopedData(t, bus)

	var mu sync.Mutex
	var logs []string
	l := New(bus, Options{Logf: func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})
	if err := l.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(l.Stop)

	seed := func(inst, top, tk string) {
		t.Helper()
		if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{
			"instance_id": inst, "top_session": top, "task_id": tk,
			"tool_call_id": "tc-" + tk, "parent_id": "",
		})); err != nil {
			t.Fatalf("Apply(%s): %v", inst, err)
		}
	}
	seed("ins-a", "top-a", "tk-a")
	seed("ins-b", "top-b", "tk-b")

	// ① 显式 instance → 各读各库（零差异；权威行只含本实例）
	for _, c := range []struct{ inst, top string }{{"ins-a", "top-a"}, {"ins-b", "top-b"}} {
		rep, err := l.Verify(c.inst, c.top)
		if err != nil || !rep.Match() {
			t.Fatalf("Verify(%s) 应零差异: err=%v\n%s", c.inst, err, rep.String())
		}
		if rep.AuthoritativeRows != 1 {
			t.Fatalf("Verify(%s) 应只看到本实例 1 行: %d", c.inst, rep.AuthoritativeRows)
		}
	}

	// ② 缺省 instance（层内事件可解析）→ 回落 top_session 推实例，行为不变
	rep, err := l.Verify("", "top-a")
	if err != nil || !rep.Match() {
		t.Fatalf("Verify(缺省, 可解析) 应成功: err=%v\n%s", err, rep.String())
	}

	// ③ 不可解析 → 明确报错（不静默空串、不静默回退）
	rep, _ = l.Verify("", "top-unknown")
	if rep.Match() || rep.AuthoritativeErr == "" {
		t.Fatalf("不可解析应明确报错: %+v", rep)
	}
	if res := l.VerifyPayload(&VerifyRequest{TopSession: "top-unknown"}); res.Match || res.Error == "" {
		t.Fatalf("方法面不可解析应回 error: %+v", res)
	}

	// ④ 数据面入口守卫：空 instance 一律拒绝
	if _, err := l.store.list("", "top-a", true); err == nil {
		t.Fatal("store.list 空 instance 应报错")
	}
	if err := l.store.upsertRow("", map[string]any{}); err == nil {
		t.Fatal("store.upsertRow 空 instance 应报错")
	}
	if _, err := l.store.listTasks("", true); err == nil {
		t.Fatal("store.listTasks 空 instance 应报错")
	}

	// ⑤ 取消判定不可解析 instance → 按层不可知处理（返回 nil，不跨实例回读）
	if ids := l.CancelSubtree("", "tk-missing", "top-unknown"); ids != nil {
		t.Fatalf("不可解析应返回 nil（不串库）: %v", ids)
	}

	// ⑥ 全程零空 instance 请求触达数据面
	if got := d.emptyHits(); got != 0 {
		t.Fatalf("层不应以空 instance 请求数据面: %d 次", got)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, s := range logs {
		if strings.Contains(s, "无法解析 instance_id") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("取消判定不可解析应记告警: %v", logs)
	}
}

// TestMW11SingleInstanceFallbackUnchanged：单实例（层内事件可解析）行为逐条不变 ——
// 缺省 instance 仍可校验 / 取消（硬要求：不破坏单实例）。
func TestMW11SingleInstanceFallbackUnchanged(t *testing.T) {
	l, fake := newLayer(t)
	seedTree(t, l)

	// 缺省 instance：从层内事件解析（ins-test）→ 零差异
	rep, err := l.Verify("", "top-1")
	if err != nil || !rep.Match() {
		t.Fatalf("单实例缺省 instance 应零差异: err=%v\n%s", err, rep.String())
	}
	// 可取消集合（层内热视图判定）不变
	if ids := l.CancelSubtree("", "tk-root", "top-1"); ids == nil {
		t.Fatal("单实例缺省 instance 的可取消集合不应为 nil")
	}
	_ = fake
}
