package mqpolicy

import (
	"encoding/json"
	"hash/fnv"
	"strings"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/dispatch"
)

// 本文件是 S1 的**逐消息等价证据**（49 §2.22.7「S1 验收 = 与改造前逐消息等价」）：
// 把改造前 `gui/bridge` 的判定逻辑**逐字复制**为基准（legacyWindow，2026-10-29 之前的
// `forwardEvent` / `isSelfPublished` / `isDataReply` / `acceptEventInstance` /
// `eventInstanceID`），对同一组输入分别求「改造前每窗口是否收到」与「改造后策略投给谁」，
// 断言两侧完全一致。
//
// 输入覆盖四条规则的每一分支：自发布（带/不带 instance_id）、data-* 应答/广播、
// instance_id 匹配/不匹配/缺失、非 JSON 载荷、同主题不同载荷（不得误吞）、未标识窗口。
//
// **S2 后仍成立的口径**：本文件所有用例的载荷**都不带 `work_dir`**（且未注入 instance 反查视图）
// → workdir 级主题的收紧判定不触发（workdir 未知 = 保守全投）→ 与改造前逐消息等价。
// S2 收紧（workdir 级主题只投同 workdir）的证据见 workdir_test.go。

// ── 改造前判定（基准；逐字复制，勿改口径）────────────────────────────

// legacyWindow 是改造前「每窗口一个桥、各自订阅 ">" 各自过滤」的等价体。
type legacyWindow struct {
	id        string
	published map[string]legacyStamp
}

type legacyStamp struct {
	fp uint64
	at time.Time
}

func newLegacyWindow(id string) *legacyWindow {
	return &legacyWindow{id: id, published: map[string]legacyStamp{}}
}

func (w *legacyWindow) mark(subject string, payload []byte) {
	w.published[subject] = legacyStamp{fp: legacyFingerprint(subject, payload), at: time.Now()}
}

// forward 是改造前 bridge.forwardEvent 的前置判定（true = 转发给本窗口前端）。
func (w *legacyWindow) forward(subject string, payload []byte) bool {
	if w.isSelfPublished(subject, payload) {
		return false
	}
	if legacyIsDataReply(subject, payload) {
		return false
	}
	return w.acceptEventInstance(legacyEventInstanceID(payload))
}

func (w *legacyWindow) isSelfPublished(subject string, payload []byte) bool {
	st, ok := w.published[subject]
	if !ok {
		return false
	}
	if time.Since(st.at) >= time.Second {
		delete(w.published, subject)
		return false
	}
	return st.fp == legacyFingerprint(subject, payload)
}

func (w *legacyWindow) acceptEventInstance(evInstance string) bool {
	if w.id == "" || evInstance == "" {
		return true
	}
	return evInstance == w.id
}

// legacyFingerprint 是改造前 bridge.pubFingerprint（subject + 载荷指纹，D-11）。
func legacyFingerprint(subject string, payload []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(subject))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return h.Sum64()
}

// legacyIsDataReply 是改造前 bridge.isDataReply。
func legacyIsDataReply(subject string, payload []byte) bool {
	if !strings.HasPrefix(subject, "data-") {
		return false
	}
	var probe struct {
		ReqID  string          `json:"req_id"`
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
		Err    json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &probe) != nil {
		return false
	}
	return probe.ReqID != "" && probe.OK != nil && (probe.Result != nil || probe.Err != nil)
}

// legacyEventInstanceID 是改造前 bridge.eventInstanceID。
func legacyEventInstanceID(payload []byte) string {
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	return probe.InstanceID
}

// ── 用例 ─────────────────────────────────────────────

// publishStep 模拟「某窗口发布一条消息」（改造前 = 该桥 markPublished + 总线 Emit）。
type publishStep struct{ owner, subject, payload string }

type eqCase struct {
	name    string
	publish []publishStep
	// subject/payload = 总线派发到各窗口的那条事件（自环时与 publish 相同）。
	subject string
	payload string
}

func eqCases() []eqCase {
	return []eqCase{
		{
			name:    "自发布·带 instance_id（发布窗丢、他窗按归属丢）",
			publish: []publishStep{{"ins-a", "task-started", `{"instance_id":"ins-a","task_id":"t1"}`}},
			subject: "task-started", payload: `{"instance_id":"ins-a","task_id":"t1"}`,
		},
		{
			name:    "自发布·无 instance_id（发布窗丢、他窗全收）",
			publish: []publishStep{{"ins-a", "filesys.changed", `{"path":"/x/y"}`}},
			subject: "filesys.changed", payload: `{"path":"/x/y"}`,
		},
		{
			name:    "他窗发布·带 instance_id（只投该实例）",
			publish: []publishStep{{"ins-b", "task-started", `{"instance_id":"ins-b"}`}},
			subject: "task-started", payload: `{"instance_id":"ins-b"}`,
		},
		{
			name:    "未标识窗口自发布（无 id 桥自吞、他窗全收）",
			publish: []publishStep{{"", "filesys.changed", `{"path":"/anon"}`}},
			subject: "filesys.changed", payload: `{"path":"/anon"}`,
		},
		{
			name:    "data-* persist 成功应答（全丢）",
			subject: "data-session-list", payload: `{"req_id":"r1","ok":true,"result":{"a":1}}`,
		},
		{
			name:    "data-* persist 失败应答（全丢）",
			subject: "data-session-get", payload: `{"req_id":"r2","ok":false,"error":"boom"}`,
		},
		{
			name:    "data-* 变更广播（req_id+ok 无 result/error → 放行）",
			subject: "data-prj-config-refresh", payload: `{"req_id":"r3","ok":true}`,
		},
		{
			name:    "带 instance_id 且不匹配（全丢）",
			subject: "mcp-tools-timeout", payload: `{"instance_id":"ins-b","tool_call_id":"c1"}`,
		},
		{
			name:    "带 instance_id 且匹配（投该窗）",
			subject: "mcp-tools-timeout", payload: `{"instance_id":"ins-a","tool_call_id":"c1"}`,
		},
		{
			name:    "无 instance_id 的 workdir 级事件（全投）",
			subject: "filesys.changed", payload: `{"path":"/x"}`,
		},
		{
			name:    "非 JSON 载荷（无归属 → 全投）",
			subject: "session-receive", payload: `not-json`,
		},
		{
			name:    "同主题不同载荷不误吞（指纹口径）",
			publish: []publishStep{{"ins-a", "task-started", `{"instance_id":"ins-a","task_id":"t1"}`}},
			subject: "task-started", payload: `{"instance_id":"ins-a","task_id":"t2"}`,
		},
	}
}

// TestDeliverEquivalentToLegacy 逐用例对照「改造前每窗判定」与「改造后策略投递集合」。
func TestDeliverEquivalentToLegacy(t *testing.T) {
	ids := []string{"ins-a", "ins-b", ""} // "" = 未标识窗口（兼容旧「桥无 instanceID」分支）
	sinks := make([]dispatch.Sink, 0, len(ids))
	for _, id := range ids {
		sinks = append(sinks, dispatch.Sink{ID: id, WorkDir: "wd", Kind: dispatch.SinkWindow})
	}

	for _, c := range eqCases() {
		t.Run(c.name, func(t *testing.T) {
			p := New()
			legacy := map[string]*legacyWindow{}
			for _, id := range ids {
				legacy[id] = newLegacyWindow(id)
			}
			// 发布侧：改造前 = 各桥自己的账本；改造后 = 共享策略账本（同一口径）。
			for _, st := range c.publish {
				payload := []byte(st.payload)
				p.Mark(st.owner, st.subject, payload)
				legacy[st.owner].mark(st.subject, payload)
			}

			payload := []byte(c.payload)
			got := map[string]bool{}
			for _, s := range p.Deliver(dispatch.Event{Topic: c.subject, Payload: payload}, sinks) {
				got[s.ID] = true
			}
			for _, id := range ids {
				want := legacy[id].forward(c.subject, payload)
				if got[id] != want {
					t.Fatalf("窗口 %q 判定不一致：改造后=%v 改造前=%v（subject=%s payload=%s）",
						id, got[id], want, c.subject, c.payload)
				}
			}
		})
	}
}

// TestDeliverDataReplyDroppedOnce：应答/自发布在策略里做一次（不是每 sink 各判一次），
// 即同一条应答对**全部** sink 返回空（避免「部分窗口收到应答」）。
func TestDeliverDataReplyDroppedOnce(t *testing.T) {
	p := New()
	sinks := []dispatch.Sink{
		{ID: "ins-a", Kind: dispatch.SinkWindow},
		{ID: "ins-b", Kind: dispatch.SinkWindow},
	}
	out := p.Deliver(dispatch.Event{
		Topic:   "data-session-get",
		Payload: []byte(`{"req_id":"r","ok":true,"result":{}}`),
	}, sinks)
	if len(out) != 0 {
		t.Fatalf("persist 应答应整体丢弃，实际投给 %v", out)
	}
}
