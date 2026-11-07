package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// TestAskCenterSnapshotOrder：快照按 workdir + seq 升序（每 wd 有序队列）。
func TestAskCenterSnapshotOrder(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	mk := func(askID, wd string, seq int64) *askWaiter {
		return &askWaiter{
			askID: askID, instanceID: "ins-a", workDir: wd, seq: seq, receivedAt: seq,
			payload: []byte(`{"ask_id":"` + askID + `","question":"q"}`),
		}
	}
	s.mu.Lock()
	s.asks[instKey("ins-a", "a-w1-2")] = mk("a-w1-2", "W1", 2)
	s.asks[instKey("ins-a", "a-w2-1")] = mk("a-w2-1", "W2", 1)
	s.asks[instKey("ins-a", "a-w1-1")] = mk("a-w1-1", "W1", 1)
	s.mu.Unlock()

	got := s.askCenterSnapshot("", "")
	if len(got) != 3 {
		t.Fatalf("snapshot len=%d want 3", len(got))
	}
	want := []string{"a-w1-1", "a-w1-2", "a-w2-1"}
	for i, w := range want {
		if got[i]["ask_id"] != w {
			t.Fatalf("snapshot[%d]=%v want %s", i, got[i]["ask_id"], w)
		}
		if got[i]["work_dir"] == "" || got[i]["seq"] == nil {
			t.Fatalf("snapshot[%d] 缺 work_dir/seq: %v", i, got[i])
		}
	}
}

// TestAskCenterSubscribeSnapshot：订阅登记返回快照；退订后不再保留。
func TestAskCenterSubscribeSnapshot(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	s.mu.Lock()
	s.asks[instKey("ins-a", "a1")] = &askWaiter{
		askID: "a1", instanceID: "ins-a", workDir: "W1", seq: 1, receivedAt: 1,
		payload: []byte(`{"ask_id":"a1","question":"q1"}`),
	}
	s.mu.Unlock()

	v := &mq.Value{Payload: jb(map[string]any{"instance_id": "askc-1"})}
	if err := s.onAskCenterSubscribe(context.Background(), "", v); err != nil {
		t.Fatal(err)
	}
	res, _ := v.Result.(map[string]any)
	if res == nil || res["ok"] != true {
		t.Fatalf("subscribe result=%v", v.Result)
	}
	asks, _ := res["asks"].([]map[string]any)
	if len(asks) != 1 || asks[0]["ask_id"] != "a1" {
		t.Fatalf("subscribe asks=%v", res["asks"])
	}
	s.mu.Lock()
	if _, ok := s.askCenterSubs[""]; !ok {
		t.Fatal("订阅未登记")
	}
	s.mu.Unlock()

	v2 := &mq.Value{Payload: jb(map[string]any{"instance_id": "askc-1"})}
	if err := s.onAskCenterUnsubscribe(context.Background(), "", v2); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if _, ok := s.askCenterSubs[""]; ok {
		t.Fatal("退订后订阅仍存在")
	}
	s.mu.Unlock()
}

// ── Ask 中心分发边界（2026-11-07 补齐）：载荷可选字段 / workdir 作用域 / 同 user 后订阅覆盖 ──

// registerUserInstance 注册一个带 `user` 的实例并等实例视图可见（instance-register 同步派发）。
func registerUserInstance(t *testing.T, s *Server, instanceID, user, workDir string) {
	t.Helper()
	_ = s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": instanceID, "client_type": "unittest", "work_dir": workDir, "user": user,
	})).Wait()
	waitCond(t, 10*time.Second, "实例注册生效 "+instanceID, func() bool {
		info, ok := s.im.Lookup(instanceID)
		return ok && info.User == user && info.WorkDir == workDir
	})
}

// subscribeAskCenter 走**真实方法面** `ask-center-subscribe` 登记订阅并返回应答。
func subscribeAskCenter(t *testing.T, s *Server, payload map[string]any) map[string]any {
	t.Helper()
	v := s.bus.Emit(context.Background(), "ask-center-subscribe", jb(payload)).Wait()
	if v.Err() != nil {
		t.Fatalf("ask-center-subscribe 失败: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("ask-center-subscribe 无应答: %v", v.Result)
	}
	return res
}

// waitCond 轮询等待条件成立（mq 事件回调时序断言）。
func waitCond(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

// askCenterRecorder 记录 `ask-center-add` / `ask-center-remove` 广播（mq 断言，不读实现状态）。
type askCenterRecorder struct {
	mu      sync.Mutex
	adds    []map[string]any
	removes []map[string]any
}

func watchAskCenter(t *testing.T, s *Server) *askCenterRecorder {
	t.Helper()
	r := &askCenterRecorder{}
	appendEv := func(dst *[]map[string]any) mq.VHandler {
		return func(_ context.Context, _ string, v *mq.Value) error {
			var m map[string]any
			if json.Unmarshal(v.Payload, &m) != nil {
				return nil
			}
			r.mu.Lock()
			*dst = append(*dst, m)
			r.mu.Unlock()
			return nil
		}
	}
	subAdd, err := s.bus.On("ask-center-add", 0, appendEv(&r.adds))
	if err != nil {
		t.Fatalf("订阅 ask-center-add: %v", err)
	}
	subRemove, err := s.bus.On("ask-center-remove", 0, appendEv(&r.removes))
	if err != nil {
		t.Fatalf("订阅 ask-center-remove: %v", err)
	}
	t.Cleanup(func() { _ = subAdd.Unsubscribe(); _ = subRemove.Unsubscribe() })
	return r
}

func (r *askCenterRecorder) snapshotAdds() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.adds...)
}

func (r *askCenterRecorder) snapshotRemoves() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.removes...)
}

// askPayloadOf 取 `ask-center-add.ask`（= session-ask 原载荷）。
func askPayloadOf(add map[string]any) map[string]any {
	p, _ := add["ask"].(map[string]any)
	return p
}

func (r *askCenterRecorder) addFor(askID string) map[string]any {
	if askID == "" {
		return nil
	}
	for _, a := range r.snapshotAdds() {
		if str(askPayloadOf(a)["ask_id"]) == askID {
			return a
		}
	}
	return nil
}

func (r *askCenterRecorder) anyAdd() map[string]any {
	adds := r.snapshotAdds()
	if len(adds) == 0 {
		return nil
	}
	return adds[0]
}

func (r *askCenterRecorder) countAddsFor(askID string) int {
	n := 0
	for _, a := range r.snapshotAdds() {
		if str(askPayloadOf(a)["ask_id"]) == askID {
			n++
		}
	}
	return n
}

func (r *askCenterRecorder) countAddsForInstance(instanceID string) int {
	n := 0
	for _, a := range r.snapshotAdds() {
		if str(a["instance_id"]) == instanceID {
			n++
		}
	}
	return n
}

func (r *askCenterRecorder) countRemovesFor(askID string) int {
	n := 0
	for _, m := range r.snapshotRemoves() {
		if str(m["ask_id"]) == askID {
			n++
		}
	}
	return n
}

func (r *askCenterRecorder) countRemovesTotal() int { return len(r.snapshotRemoves()) }

func (r *askCenterRecorder) reasonsFor(askID string) []string {
	var out []string
	for _, m := range r.snapshotRemoves() {
		if str(m["ask_id"]) == askID {
			out = append(out, str(m["reason"]))
		}
	}
	return out
}

// TestSessionAskPayloadSessionTitleAndContext：`session-ask` / `ask-center-add.ask` 的载荷边界
// （61 §4.2 / AskUser §4.3）：
//   - `session_title` = 会话标题（server 随题下发，Ask 中心免跨项目取数）；
//   - `args.context` → `context`（仅非空下发）；
//   - `scope:"workdir"` 不带 `work_dir` → 隐式收窄到本实例 work_dir；
//   - 负例：会话行取不到标题 → **不下发** `session_title`；未给 context → 不下发 `context`。
func TestSessionAskPayloadSessionTitleAndContext(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	const sess, turn = "s-ask-meta", "t-ask-meta"
	startTurn(t, s, sess, turn)
	// startTurn 的实例登记不带 user；ask 归属 user 需与订阅一致 → 补登记同一实例。
	registerUserInstance(t, s, "ins-test", "u-meta", testWorkDir)
	registerUserInstance(t, s, "askc-meta", "u-meta", testWorkDir)

	if _, err := s.cfg.SessionTitle(facade.SessionTitleRequest{
		InstanceID: "ins-test", DialogID: s.dialogOf("ins-test", sess), SessionID: sess,
		Title: "发布确认", Scope: s.cfgScope("ins-test"),
	}); err != nil {
		t.Fatalf("设置会话标题失败: %v", err)
	}

	res := subscribeAskCenter(t, s, map[string]any{"instance_id": "askc-meta", "scope": "workdir"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("ask-center-subscribe 失败: %v", res)
	}
	// 隐式收窄：不带 work_dir + scope=workdir → 取本实例登记的 work_dir。
	s.mu.Lock()
	sub, hasSub := s.askCenterSubs["u-meta"]
	s.mu.Unlock()
	if !hasSub || sub.instanceID != "askc-meta" || sub.workDir != testWorkDir {
		t.Fatalf("scope=workdir 未隐式收窄到实例 work_dir：%+v (has=%v)", sub, hasSub)
	}

	rec := watchAskCenter(t, s)
	askID := s.ask(turn, "tc-meta-1", map[string]any{
		"question": "是否发布？",
		"context":  "背景说明",
	}, "")
	waitCond(t, 10*time.Second, "ask-center-add", func() bool { return rec.countAddsFor(askID) == 1 })

	add := rec.addFor(askID)
	if add == nil {
		t.Fatal("ask-center-add 未推送（订阅 workdir 应匹配）")
	}
	if got := str(add["work_dir"]); got != testWorkDir {
		t.Fatalf("ask-center-add.work_dir=%q want %q", got, testWorkDir)
	}
	p := askPayloadOf(add)
	if p == nil {
		t.Fatalf("ask-center-add 缺 ask 载荷: %+v", add)
	}
	if got := str(p["session_title"]); got != "发布确认" {
		t.Fatalf("session-ask.session_title=%q want 发布确认", got)
	}
	if got := str(p["context"]); got != "背景说明" {
		t.Fatalf("session-ask.context=%q want 背景说明", got)
	}
	if got := str(p["owner_instance"]); got != "ins-test" {
		t.Fatalf("session-ask.owner_instance=%q want ins-test", got)
	}
	if got := str(p["instance_id"]); got != "ins-test" {
		t.Fatalf("session-ask.instance_id=%q want ins-test（会话未绑定窗口 → 回落 turn 实例）", got)
	}
	if str(p["dialog_id"]) == "" {
		t.Fatalf("session-ask.dialog_id 不应为空（会话 → 对话映射已自举）: %+v", p)
	}

	// 负例 1：未给 context → 不下发 context。
	askID2 := s.ask(turn, "tc-meta-2", map[string]any{"question": "继续？"}, "")
	waitCond(t, 10*time.Second, "ask-center-add#2", func() bool { return rec.countAddsFor(askID2) == 1 })
	p2 := askPayloadOf(rec.addFor(askID2))
	if _, has := p2["context"]; has {
		t.Fatalf("未给 context 时不应下发该字段: %+v", p2)
	}

	// 负例 2：会话行不存在（映射/标题取不到）→ 不下发 session_title（仅非空下发）。
	tc := s.recoverTurnCtx("ins-test", "s-ask-meta-unknown", "t-ask-meta-unknown")
	t.Cleanup(tc.Close)
	askID3 := s.ask("t-ask-meta-unknown", "tc-meta-3", map[string]any{"question": "未知会话？"}, "")
	waitCond(t, 10*time.Second, "ask-center-add#3", func() bool { return rec.countAddsFor(askID3) == 1 })
	p3 := askPayloadOf(rec.addFor(askID3))
	if _, has := p3["session_title"]; has {
		t.Fatalf("会话行取不到标题时不应下发 session_title: %+v", p3)
	}
	if _, has := p3["context"]; has {
		t.Fatalf("未给 context 时不应下发该字段: %+v", p3)
	}
}

// TestAskCenterSubscribeLaterOverridesSameUser：同一 user 只保留一个订阅实例（后订阅覆盖），
// 第一条订阅不再收到 `ask-center-add`（61 §4.2「后端唯一分发」；避免多端重复分发）。
func TestAskCenterSubscribeLaterOverridesSameUser(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	registerUserInstance(t, s, "ins-ov", "u-ov", testWorkDir)
	registerUserInstance(t, s, "askc-old", "u-ov", testWorkDir)
	registerUserInstance(t, s, "askc-new", "u-ov", testWorkDir)
	tc := s.recoverTurnCtx("ins-ov", "s-ov", "t-ov")
	t.Cleanup(tc.Close)

	if res := subscribeAskCenter(t, s, map[string]any{
		"instance_id": "askc-old", "work_dir": testWorkDir,
	}); res["ok"] != true {
		t.Fatalf("首次订阅失败: %v", res)
	}
	if res := subscribeAskCenter(t, s, map[string]any{
		"instance_id": "askc-new", "work_dir": testWorkDir,
	}); res["ok"] != true {
		t.Fatalf("后订阅失败: %v", res)
	}

	rec := watchAskCenter(t, s)
	askID := s.ask("t-ov", "tc-ov", map[string]any{"question": "Q"}, "")
	waitCond(t, 10*time.Second, "ask-center-add", func() bool { return rec.countAddsFor(askID) == 1 })

	add := rec.addFor(askID)
	if got := str(add["instance_id"]); got != "askc-new" {
		t.Fatalf("后订阅应覆盖同 user 前订阅：推送实例=%q want askc-new", got)
	}
	if n := rec.countAddsForInstance("askc-old"); n != 0 {
		t.Fatalf("第一条订阅不应再收到 ask-center-add：%d", n)
	}
}

// TestAskCenterSubscribeWorkdirScopeNarrowing：`scope:"workdir"`（不带 work_dir）→ 隐式收窄到
// 本实例 work_dir：快照只含该目录，其他项目的 ask 不推送；本项目 ask 正常推送。
func TestAskCenterSubscribeWorkdirScopeNarrowing(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	otherWD := filepath.Join(testWorkDir, "other-project")
	registerUserInstance(t, s, "ins-scope-local", "u-scope", testWorkDir)
	registerUserInstance(t, s, "ins-scope-other", "u-scope", otherWD)
	registerUserInstance(t, s, "askc-scope", "u-scope", testWorkDir)

	tcLocal := s.recoverTurnCtx("ins-scope-local", "s-scope-l", "t-scope-l")
	tcOther := s.recoverTurnCtx("ins-scope-other", "s-scope-o", "t-scope-o")
	t.Cleanup(tcLocal.Close)
	t.Cleanup(tcOther.Close)

	localAsk := s.ask("t-scope-l", "tc-l", map[string]any{"question": "本地"}, "")
	otherAsk := s.ask("t-scope-o", "tc-o", map[string]any{"question": "其他项目"}, "")
	if localAsk == "" || otherAsk == "" {
		t.Fatal("ask 应返回 askID")
	}

	rec := watchAskCenter(t, s)
	res := subscribeAskCenter(t, s, map[string]any{"instance_id": "askc-scope", "scope": "workdir"})
	if res["ok"] != true {
		t.Fatalf("订阅失败: %v", res)
	}
	s.mu.Lock()
	sub := s.askCenterSubs["u-scope"]
	s.mu.Unlock()
	if sub.workDir != testWorkDir {
		t.Fatalf("scope=workdir 应隐式取实例 work_dir=%q，got=%q", testWorkDir, sub.workDir)
	}

	asks, _ := res["asks"].([]map[string]any)
	if len(asks) != 1 || str(asks[0]["ask_id"]) != localAsk {
		t.Fatalf("快照应收窄到本项目（仅 %s）：%+v", localAsk, res["asks"])
	}
	if got := str(asks[0]["work_dir"]); got != testWorkDir {
		t.Fatalf("快照 work_dir=%q want %q", got, testWorkDir)
	}

	// 负例：其他项目的 ask 不推送；本项目 ask 推送。
	otherAsk2 := s.ask("t-scope-o", "tc-o2", map[string]any{"question": "其他项目 2"}, "")
	localAsk2 := s.ask("t-scope-l", "tc-l2", map[string]any{"question": "本地 2"}, "")
	waitCond(t, 10*time.Second, "本项目 ask-center-add", func() bool {
		return rec.countAddsFor(localAsk2) == 1
	})
	if n := rec.countAddsFor(otherAsk2); n != 0 {
		t.Fatalf("workdir 作用域外的 ask 不应推送：%d", n)
	}
	// 订阅前登记的其他项目 ask 也不会出现在快照 / 推送里。
	if n := rec.countAddsFor(otherAsk); n != 0 {
		t.Fatalf("订阅前的其他项目 ask 不应推送：%d", n)
	}
}
