// cleanupStaleToolPairs 启动清理单测（A-17）：遗留非终态 tool_pair → interrupted（状态列与
// content.result.status 同源同步），行内其余字段保留；终态行不受影响。
package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestCleanupStaleToolPairs 覆盖三种非终态（pending/running/provisional）→ interrupted，
// 以及 completed 行不被波及。
func TestCleanupStaleToolPairs(t *testing.T) {
	db, err := data.Open(filepath.Join(t.TempDir(), "prjusr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tb := db.Table("messages")
	rows := map[string]data.Record{
		// 有 result 段 → 状态列与 result.status 一并改
		"m1": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "pending",
			"content":    `{"call":{"tool_call_id":"c1","name":"w1"},"result":{"content":"r1","status":"pending"}}`,
			"created_at": "2026-01-01T00:00:00.000000001Z"},
		// 无 result 段 → 仅改状态列（视图回退读状态列）
		"m2": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "running",
			"content":    `{"call":{"tool_call_id":"c2","name":"w2"}}`,
			"created_at": "2026-01-01T00:00:00.000000002Z"},
		"m3": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "provisional",
			"content":    `{"call":{"tool_call_id":"c3","name":"w3"},"result":{"content":"r3","status":"provisional"}}`,
			"created_at": "2026-01-01T00:00:00.000000003Z"},
		// 终态行 → 不在清理候选（按状态等值查询），不被波及
		"m4": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "completed",
			"content":    `{"call":{"tool_call_id":"c4","name":"w4"},"result":{"content":"r4","status":"completed"}}`,
			"created_at": "2026-01-01T00:00:00.000000004Z"},
	}
	for k, r := range rows {
		if err := tb.Insert(k, r); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}

	// R8-A-5：cleanupStaleToolPairs 改为 Service 方法（Warnf 留痕）；空 Warnf = 静默，行为不变。
	(&Service{Base: kernel.NewBase(nil, kernel.Options{})}).cleanupStaleToolPairs(db)

	for _, id := range []string{"m1", "m2", "m3"} {
		var rec data.Record
		if ok, err := tb.Get(id, &rec); err != nil || !ok {
			t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
		}
		if got, _ := rec["tool_call_status"].(string); got != "interrupted" {
			t.Fatalf("%s 状态列应为 interrupted：got=%q", id, got)
		}
		// 行内其余字段保留（RMW 收敛不丢字段）
		if got, _ := rec["session_id"].(string); got != "s" {
			t.Fatalf("%s 其余字段被破坏：session_id=%q", id, got)
		}
		// result 段存在时 result.status 同步为 interrupted（m2 无 result 段不判）
		if id == "m2" {
			continue
		}
		tc, ok := kernel.ParseToolContent(kernel.Sval(rec["content"]))
		if !ok || tc.Result == nil {
			t.Fatalf("%s content 解析失败：%v", id, rec["content"])
		}
		if tc.Result.Status != "interrupted" {
			t.Fatalf("%s result.status 应同步 interrupted：got=%q", id, tc.Result.Status)
		}
		if tc.Result.Content == "" {
			t.Fatalf("%s result.content 不应被清空", id)
		}
	}

	var rec data.Record
	if ok, err := tb.Get("m4", &rec); err != nil || !ok {
		t.Fatalf("get m4: ok=%v err=%v", ok, err)
	}
	if got, _ := rec["tool_call_status"].(string); got != "completed" {
		t.Fatalf("终态行不应被改：got=%q", got)
	}
}

// newSessionEnv 构造测试服务与实例 Scope（目录先于 data.Reset 的 Cleanup 创建，见 tasktree 同型）。
func newSessionEnv(t *testing.T) (*Service, facade.Scope) {
	t.Helper()
	usrDir := t.TempDir()
	wd := t.TempDir()
	dd := t.TempDir()
	data.Reset()
	t.Cleanup(data.Reset)
	s := New(kernel.NewBase(nil, kernel.Options{UsrPath: filepath.Join(usrDir, "usr.db"), AppDir: usrDir}))
	return s, facade.Scope{WorkDir: wd, DataDir: dd}
}

// TestSessionTitleUpdateInPreservesFields（A-23）：SessionTitle 读改写收进 UpdateIn 单事务——
// 只改 title/updated_at，其余字段（history/snapshot_turn 等）保留；行不存在 → 报错且不建行；
// updated_at 为纳秒口径（A-11）。
func TestSessionTitleUpdateInPreservesFields(t *testing.T) {
	s, scope := newSessionEnv(t)
	inst := "ins-title"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table("sessions")
	const created = "2026-01-01T00:00:00.000000000Z"
	if err := tb.Insert("s1", data.Record{
		"session_id": "s1", "parent_id": "", "title": "old",
		"created_at": created, "updated_at": created,
		"history": `[{"x":1}]`, "snapshot_turn": "t9",
	}); err != nil {
		t.Fatalf("insert s1: %v", err)
	}

	resp, err := s.SessionTitle(facade.SessionTitleRequest{InstanceID: inst, Scope: scope, SessionID: "s1", Title: "new"})
	if err != nil || !resp.OK {
		t.Fatalf("SessionTitle: err=%v resp=%+v", err, resp)
	}
	var rec data.Record
	if ok, err := tb.Get("s1", &rec); err != nil || !ok {
		t.Fatalf("get s1: ok=%v err=%v", ok, err)
	}
	if got := kernel.Sval(rec["title"]); got != "new" {
		t.Fatalf("title 未更新：got=%q", got)
	}
	if got := kernel.Sval(rec["history"]); got != `[{"x":1}]` {
		t.Fatalf("history 被整行覆盖丢失：got=%q", got)
	}
	if got := kernel.Sval(rec["snapshot_turn"]); got != "t9" {
		t.Fatalf("snapshot_turn 被整行覆盖丢失：got=%q", got)
	}
	if _, err := time.Parse(kernel.RFC3339FixedNano, kernel.Sval(rec["updated_at"])); err != nil {
		t.Fatalf("updated_at 应为纳秒口径：%q (%v)", rec["updated_at"], err)
	}

	// 行不存在 → 保持原 not found 语义（报错、不建行）
	if _, err := s.SessionTitle(facade.SessionTitleRequest{InstanceID: inst, Scope: scope, SessionID: "ghost", Title: "x"}); err == nil {
		t.Fatal("会话不存在应报错")
	}
	var ghost data.Record
	if ok, _ := tb.Get("ghost", &ghost); ok {
		t.Fatal("not found 路径不应建行")
	}
}

// TestTurnSetSummaryCompleteNoOrphanRow（A-34）：TurnSetSummary / TurnComplete 对**不存在**的 turn
// 放弃写入（UpdateIn 的 fn 返回 nil → 不建缺 session_id/created_at/turn_id 的半成品残行）；
// 行存在时照常更新且保留其余字段。
func TestTurnSetSummaryCompleteNoOrphanRow(t *testing.T) {
	s, scope := newSessionEnv(t)
	inst := "ins-turn"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table("turns")

	// 行不存在：两个写面都不得建残行
	if _, err := s.TurnSetSummary(facade.TurnSetSummaryRequest{InstanceID: inst, Scope: scope, TurnID: "ghost", Summary: "s"}); err != nil {
		t.Fatalf("TurnSetSummary(ghost): %v", err)
	}
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{InstanceID: inst, Scope: scope, TurnID: "ghost2", Status: "done"}); err != nil {
		t.Fatalf("TurnComplete(ghost2): %v", err)
	}
	var rec data.Record
	if ok, _ := tb.Get("ghost", &rec); ok {
		t.Fatal("turn 不存在不应建残行（summary）")
	}
	if ok, _ := tb.Get("ghost2", &rec); ok {
		t.Fatal("turn 不存在不应建残行（complete）")
	}

	// 行存在：照常更新（RMW 保留其余字段）
	if err := tb.Insert("t1", data.Record{
		"turn_id": "t1", "session_id": "s1", "status": "running",
		"created_at": "2026-01-01T00:00:00.000000000Z",
	}); err != nil {
		t.Fatalf("insert t1: %v", err)
	}
	if _, err := s.TurnSetSummary(facade.TurnSetSummaryRequest{InstanceID: inst, Scope: scope, TurnID: "t1", Summary: "summary"}); err != nil {
		t.Fatalf("TurnSetSummary(t1): %v", err)
	}
	done := 7
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{InstanceID: inst, Scope: scope, TurnID: "t1", Status: "done", FullTokens: &done}); err != nil {
		t.Fatalf("TurnComplete(t1): %v", err)
	}
	if ok, err := tb.Get("t1", &rec); err != nil || !ok {
		t.Fatalf("get t1: ok=%v err=%v", ok, err)
	}
	if kernel.Sval(rec["session_id"]) != "s1" || kernel.Sval(rec["summary"]) != "summary" || kernel.Sval(rec["status"]) != "done" {
		t.Fatalf("行存在时更新异常：%v", rec)
	}
}

// fixedParamKeys 是子聊天固定参数列（单聊方案 §3.2 / §5.4；**仅非空才写键**）：
// kind/lifecycle/agent/scenario_id/llm + purpose（= gateway 注入的 tool_call_display_name）。
var fixedParamKeys = []string{"kind", "lifecycle", "agent", "scenario_id", "llm", "purpose"}

// newSessionEnvBus 同 newSessionEnv，但注入内存总线 —— SessionEnsure 首次落库会广播 session-new
// （emitSessionNew 无 nil 守卫），故建会话类用例须带总线。
func newSessionEnvBus(t *testing.T) (*Service, facade.Scope) {
	t.Helper()
	usrDir, wd, dd := t.TempDir(), t.TempDir(), t.TempDir()
	data.Reset()
	t.Cleanup(data.Reset)
	bus, err := mq.New(mq.Options{Prefix: "cktest."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	s := New(kernel.NewBase(bus, kernel.Options{UsrPath: filepath.Join(usrDir, "usr.db"), AppDir: usrDir}))
	return s, facade.Scope{WorkDir: wd, DataDir: dd}
}

// TestSessionEnsureFixedParamsNonEmptyOnly（A1 · SUBCHAT-2 阶段 A）：子聊天固定参数由 SessionEnsure
// **一次性写入且仅非空才写键** —— 普通顶层会话行形状与既有逐字节一致（多一个空键即红）；
// 幂等二次调用不覆盖既有行（含固定参数）。
func TestSessionEnsureFixedParamsNonEmptyOnly(t *testing.T) {
	s, scope := newSessionEnvBus(t)
	inst := "ins-fixed"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table("sessions")

	// ① 普通顶层会话：不得出现任何固定参数键（行形状不变）
	if _, err := s.SessionEnsure(facade.SessionEnsureRequest{InstanceID: inst, Scope: scope, SessionID: "s-plain"}); err != nil {
		t.Fatalf("ensure plain: %v", err)
	}
	var rec data.Record
	if ok, err := tb.Get("s-plain", &rec); err != nil || !ok {
		t.Fatalf("get s-plain: ok=%v err=%v", ok, err)
	}
	for _, k := range fixedParamKeys {
		if _, has := rec[k]; has {
			t.Fatalf("普通顶层会话不应写 %q 键（行形状须与既有一致）：%v", k, rec)
		}
	}
	if got := kernel.Sval(rec["parent_id"]); got != "" {
		t.Fatalf("顶层会话 parent_id 应为空串：%q", got)
	}

	// ② 子聊天：固定参数一次性写入（kind + lifecycle + agent/scenario_id/llm + purpose）
	if _, err := s.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: inst, Scope: scope, SessionID: "s-chat", ParentSessionID: "s-plain",
		Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleChatting,
		Agent: "agent-x", ScenarioID: "scn-x", LLM: "llm-x", Purpose: "整理当日要点",
	}); err != nil {
		t.Fatalf("ensure subchat: %v", err)
	}
	if ok, err := tb.Get("s-chat", &rec); err != nil || !ok {
		t.Fatalf("get s-chat: ok=%v err=%v", ok, err)
	}
	for k, want := range map[string]string{
		"parent_id": "s-plain", "kind": facade.SessionKindSubChat,
		"lifecycle": facade.SessionLifecycleChatting, "agent": "agent-x",
		"scenario_id": "scn-x", "llm": "llm-x", "purpose": "整理当日要点",
	} {
		if got := kernel.Sval(rec[k]); got != want {
			t.Fatalf("固定参数 %q = %q want %q（%v）", k, got, want, rec)
		}
	}

	// ③ 幂等：二次 ensure 不覆盖既有行（lifecycle/agent 等保持首次值）
	if _, err := s.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: inst, Scope: scope, SessionID: "s-chat", ParentSessionID: "s-plain",
		Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleFinished, Agent: "other",
	}); err != nil {
		t.Fatalf("ensure subchat 2nd: %v", err)
	}
	if ok, _ := tb.Get("s-chat", &rec); !ok {
		t.Fatal("get s-chat（二次）失败")
	}
	if kernel.Sval(rec["lifecycle"]) != facade.SessionLifecycleChatting || kernel.Sval(rec["agent"]) != "agent-x" {
		t.Fatalf("幂等 ensure 不应覆盖既有行：%v", rec)
	}
}

// TestSessionSubChatsAndKindFallback（A1 读侧回退 + A2 列举 · SUBCHAT-2 阶段 A）：
//   - 行缺 `kind` 且 `parent_id` 非空（历史数据）⇒ 读侧视为 `sub_session`；
//   - `SessionSubChats` 只返回 `kind ∈ {sub_chat, group_chat}`（含 finished），按创建时间升序，
//     且**只按父会话过滤**（别的父的不入列）；
//   - `SessionList` 语义不变（仍只返回顶层会话）。
func TestSessionSubChatsAndKindFallback(t *testing.T) {
	s, scope := newSessionEnv(t)
	inst := "ins-subchats"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table("sessions")
	insert := func(id, parent, kind, lifecycle, created string) {
		t.Helper()
		r := data.Record{
			"session_id": id, "title": id, "parent_id": parent,
			"created_at": created, "updated_at": created,
		}
		if kind != "" {
			r["kind"] = kind
		}
		if lifecycle != "" {
			r["lifecycle"] = lifecycle
		}
		if err := tb.Insert(id, r); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	const t0 = "2026-01-01T00:00:00.000000000Z"
	insert("s-top", "", "", "", t0)
	insert("s-legacy", "s-top", "", "", "2026-01-01T00:00:01.000000000Z")                  // 历史子会话（无 kind）
	insert("s-chat1", "s-top", facade.SessionKindSubChat, facade.SessionLifecycleChatting, "2026-01-01T00:00:02.000000000Z")
	insert("s-chat2", "s-top", facade.SessionKindSubChat, facade.SessionLifecycleFinished, "2026-01-01T00:00:03.000000000Z")
	insert("s-group", "s-top", facade.SessionKindGroupChat, facade.SessionLifecycleReady, "2026-01-01T00:00:04.000000000Z")
	insert("s-other", "s-other-parent", facade.SessionKindSubChat, facade.SessionLifecycleChatting, "2026-01-01T00:00:05.000000000Z")

	// ① 读侧回退：历史子会话行（无 kind）视为 sub_session
	got, err := s.SessionGet(facade.SessionGetRequest{InstanceID: inst, Scope: scope, SessionID: "s-legacy"})
	if err != nil || !got.Found {
		t.Fatalf("SessionGet(s-legacy): found=%v err=%v", got.Found, err)
	}
	if got.Session.Kind != facade.SessionKindSubSession {
		t.Fatalf("历史子会话应回退为 sub_session：%q", got.Session.Kind)
	}
	if top, err := s.SessionGet(facade.SessionGetRequest{InstanceID: inst, Scope: scope, SessionID: "s-top"}); err != nil || top.Session.Kind != "" {
		t.Fatalf("顶层会话 kind 应为空：%+v err=%v", top.Session, err)
	}

	// ② 子聊天列举：只 sub_chat / group_chat（含 finished），按创建时间升序，只按父过滤
	sub, err := s.SessionSubChats(facade.SessionSubChatsRequest{InstanceID: inst, Scope: scope, ParentSessionID: "s-top"})
	if err != nil {
		t.Fatalf("SessionSubChats: %v", err)
	}
	var ids []string
	for _, x := range sub.List {
		ids = append(ids, x.ID)
	}
	if want := "s-chat1,s-chat2,s-group"; strings.Join(ids, ",") != want {
		t.Fatalf("子聊天列举不符（应 %q，含 finished、排除 sub_session 与别的父）：got %v", want, ids)
	}
	if sub.List[1].Lifecycle != facade.SessionLifecycleFinished {
		t.Fatalf("finished 子聊天应照常列出（回看入口）：%+v", sub.List[1])
	}

	// ③ data-session-list 语义不变：仍只返回顶层会话
	list, err := s.SessionList(facade.SessionListRequest{InstanceID: inst, Scope: scope})
	if err != nil {
		t.Fatalf("SessionList: %v", err)
	}
	for _, x := range list.List {
		if x.ParentID != "" {
			t.Fatalf("顶层会话列表混入子会话：%+v", x)
		}
	}

	// ④ 父会话必填
	if _, err := s.SessionSubChats(facade.SessionSubChatsRequest{InstanceID: inst, Scope: scope}); err == nil {
		t.Fatal("缺 parent_session_id 应报错")
	}

	// ⑤ 全局列举（SessionSubChatsAll，单聊 §3.4.7 启动提醒）：跨父会话返回全部子聊天
	// （仅 sub_chat / group_chat，含 finished；顶层会话与 sub_session 不入列）。
	all, err := s.SessionSubChatsAll(facade.SessionSubChatsAllRequest{InstanceID: inst, Scope: scope})
	if err != nil {
		t.Fatalf("SessionSubChatsAll: %v", err)
	}
	var allIDs []string
	for _, x := range all.List {
		allIDs = append(allIDs, x.ID)
	}
	if got, want := strings.Join(allIDs, ","), "s-chat1,s-chat2,s-group,s-other"; got != want {
		t.Fatalf("全局子聊天列举不符（应 %q，排除顶层/sub_session）：got %v", want, allIDs)
	}
}

// TestSessionLifecycleSet（A2 · SUBCHAT-2 阶段 A）：三态更新的四个口径 ——
//   - 合法值 → 只改 lifecycle/updated_at（单事务 RMW 保留其余字段，updated_at 纳秒）；
//   - 非法值 → 报错且**不落库**；
//   - 行不存在 → 报错且**不建残行**；
//   - ready 可逆回 chatting。
func TestSessionLifecycleSet(t *testing.T) {
	s, scope := newSessionEnv(t)
	inst := "ins-lifecycle"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table("sessions")
	const created = "2026-01-01T00:00:00.000000000Z"
	if err := tb.Insert("s1", data.Record{
		"session_id": "s1", "title": "子聊天", "parent_id": "s-top",
		"kind": facade.SessionKindSubChat, "lifecycle": facade.SessionLifecycleChatting,
		"history": `[{"x":1}]`, "snapshot_turn": "t9",
		"created_at": created, "updated_at": created,
	}); err != nil {
		t.Fatalf("insert s1: %v", err)
	}

	set := func(lifecycle string) error {
		t.Helper()
		_, err := s.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
			InstanceID: inst, Scope: scope, SessionID: "s1", Lifecycle: lifecycle,
		})
		return err
	}
	if err := set(facade.SessionLifecycleFinished); err != nil {
		t.Fatalf("SessionLifecycleSet(finished): %v", err)
	}
	var rec data.Record
	if ok, _ := tb.Get("s1", &rec); !ok {
		t.Fatal("get s1 失败")
	}
	if kernel.Sval(rec["lifecycle"]) != facade.SessionLifecycleFinished {
		t.Fatalf("lifecycle 未更新：%v", rec)
	}
	if kernel.Sval(rec["history"]) != `[{"x":1}]` || kernel.Sval(rec["snapshot_turn"]) != "t9" || kernel.Sval(rec["kind"]) != facade.SessionKindSubChat {
		t.Fatalf("单事务 RMW 应保留其余字段：%v", rec)
	}
	if _, err := time.Parse(kernel.RFC3339FixedNano, kernel.Sval(rec["updated_at"])); err != nil {
		t.Fatalf("updated_at 应为纳秒口径：%q（%v）", rec["updated_at"], err)
	}

	// ready 可逆：finished → ready（语义上由新一轮开始处写回；此处只验写面）
	if err := set(facade.SessionLifecycleReady); err != nil {
		t.Fatalf("SessionLifecycleSet(ready): %v", err)
	}
	if ok, _ := tb.Get("s1", &rec); !ok || kernel.Sval(rec["lifecycle"]) != facade.SessionLifecycleReady {
		t.Fatalf("ready 写回失败：%v", rec)
	}

	// 非法值 → 报错且不落库
	if err := set("bogus"); err == nil {
		t.Fatal("非法 lifecycle 应报错")
	}
	if ok, _ := tb.Get("s1", &rec); !ok || kernel.Sval(rec["lifecycle"]) != facade.SessionLifecycleReady {
		t.Fatalf("非法值不得落库：%v", rec)
	}

	// 行不存在 → 报错且不建残行
	if _, err := s.SessionLifecycleSet(facade.SessionLifecycleSetRequest{
		InstanceID: inst, Scope: scope, SessionID: "ghost", Lifecycle: facade.SessionLifecycleFinished,
	}); err == nil {
		t.Fatal("会话不存在应报错")
	}
	var ghost data.Record
	if ok, _ := tb.Get("ghost", &ghost); ok {
		t.Fatal("not found 路径不应建行")
	}
}
