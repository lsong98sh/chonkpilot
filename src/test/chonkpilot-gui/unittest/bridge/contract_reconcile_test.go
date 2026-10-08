// contract_reconcile_test.go — B0 契约面试点 · 生产端对账（2026-10-05）。
//
// 目标：把 `docs/spec/60-reference/61-messages.schema.json`（由 61-消息一览 抽取的机器可读
// 契约）作为唯一基准，用**最小合法输入**驱动桥（src/lib/gui/bridge）导出的消息面处理，
// 断言**实际发出的键 ⊆ 契约声明**（多出的键 = 红）。覆盖：
//   - gui.* 本地面 result 键（§1）
//   - 事件面：桥 forwardEvent 转前端的事件载荷键（§4.3/§5.3/§2.4）
//   - 客户端 topic ↔ 总线相对主题 映射（§4.5；断言确实发布到 61 声明的相对主题）
//
// 契约保障：契约文件必须存在且结构合法；每个 topic 名唯一；文档缺口的 topic 存在。
//
// 局限（覆盖边界，见 docs/spec/50-testing/50-测试体系.md §8）：
//   - 只驱动**可在内存总线 + 临时工作目录下**跑通的处理（其余列于 notDriven，不硬凑）；
//   - 只对账 gui/filesys/事件/映射面；§3 数据面（生产者 = src/lib/data）留待 B1。
package bridge_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// ── 契约结构（与 61-messages.schema.json 对齐）─────────────────────────────

type fieldSpec struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type topicSpec struct {
	Topic       string               `json:"topic"`
	Face        string               `json:"face"`
	Direction   string               `json:"direction"`
	BusTopic    string               `json:"busTopic"`
	ClientTopic string               `json:"clientTopic"`
	Payload     map[string]fieldSpec `json:"payload"`
	Result      map[string]fieldSpec `json:"result"`
	Event       map[string]fieldSpec `json:"event"`
}

type docGap struct {
	ID     string `json:"id"`
	Topic  string `json:"topic"`
	Detail string `json:"detail"`
}

type messageContract struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Source  string      `json:"source"`
	Topics  []topicSpec `json:"topics"`
	DocGaps []docGap    `json:"docGaps"`
}

// fieldTypeVocabulary 契约允许的字段类型集合（契约自洽校验用）。
var fieldTypeVocabulary = map[string]bool{
	"string": true, "bool": true, "int": true, "number": true,
	"object": true, "array": true, "any": true,
}

// findContractFile 从本测试源文件目录向上查找契约文件（支持任意运行 cwd）。
func findContractFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	for d := filepath.Dir(file); ; {
		cand := filepath.Join(d, "docs", "spec", "60-reference", "61-messages.schema.json")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("未找到 61-messages.schema.json（自 %s 向上）", filepath.Dir(file))
		}
		d = parent
	}
}

func loadContract(t *testing.T) *messageContract {
	t.Helper()
	raw, err := os.ReadFile(findContractFile(t))
	if err != nil {
		t.Fatalf("读取契约失败: %v", err)
	}
	var c messageContract
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("契约 JSON 解析失败: %v", err)
	}
	return &c
}

func (c *messageContract) byTopic() map[string]topicSpec {
	m := make(map[string]topicSpec, len(c.Topics))
	for _, ts := range c.Topics {
		m[ts.Topic] = ts
	}
	return m
}

// ── 对账判定 ──────────────────────────────────────────────────────────────

// assertKeysSubset 断言 actual 的键集合 ⊆ declared（error 为通用失败信封键，恒放行）。
// 多出的键 = 红（无豁免机制：契约=61，须改契约或改代码，见 50 §8.4）。
// which 仅用于失败信息（"result"/"event"）。
func assertKeysSubset(t *testing.T, topic, which string, declared map[string]fieldSpec, actual map[string]any) {
	t.Helper()
	var extra []string
	for k := range actual {
		if k == "error" { // 通用失败信封键（见契约 conventions.failureEnvelope）
			continue
		}
		if _, ok := declared[k]; ok {
			continue
		}
		extra = append(extra, k)
	}
	if len(extra) > 0 {
		t.Errorf("%s.%s 出现契约未声明的键（多键 = 红）: %v（契约声明=%v）",
			topic, which, extra, keysOf(declared))
	}
}

func keysOf(m map[string]fieldSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("结果非对象: %T", v)
	}
	return m
}

// ── 0. 契约自洽 ───────────────────────────────────────────────────────────

func TestContractIntegrity(t *testing.T) {
	c := loadContract(t)
	if c.Name == "" || c.Version == "" || c.Source == "" {
		t.Fatalf("契约缺 name/version/source: %+v", c)
	}
	if len(c.Topics) == 0 {
		t.Fatal("契约未收录任何主题")
	}
	seen := map[string]bool{}
	for _, ts := range c.Topics {
		if ts.Topic == "" {
			t.Fatal("存在空 topic 名")
		}
		if seen[ts.Topic] {
			t.Fatalf("topic 名重复: %s", ts.Topic)
		}
		seen[ts.Topic] = true
		for which, fields := range map[string]map[string]fieldSpec{"payload": ts.Payload, "result": ts.Result, "event": ts.Event} {
			for name, fs := range fields {
				if !fieldTypeVocabulary[fs.Type] {
					t.Errorf("topic %s 的 %s.%s 类型非法: %q", ts.Topic, which, name, fs.Type)
				}
			}
		}
	}
	for _, dg := range c.DocGaps {
		if !seen[dg.Topic] {
			t.Errorf("docGap %s 指向未知 topic: %s", dg.ID, dg.Topic)
		}
	}
	t.Logf("契约 %s：%d 主题 · %d 文档缺口", c.Version, len(c.Topics), len(c.DocGaps))
}

// ── 0b. 未驱动清单（不硬凑；清单化以便后续批次补）────────────────────────

// notDrivenEntry 记录本批「生产者不可在 Go 测试中低成本驱动」的处理及原因。
type notDrivenEntry struct {
	face   string // 归属面
	driver string // 生产端（handler / 模块）
	reason string // 未驱动原因
}

func TestNotDrivenRegistry(t *testing.T) {
	entries := []notDrivenEntry{
		{"server", "src/lib/llm/server · session-* / task-* / prompt-* / tool-notify 生产者", "需完整 server 装配（session store + data 门面 + gateway + llm provider），单测级最小驱动成本高；其事件载荷键已由事件面转发对账（TestForwardedEventKeysSubsetOfContract）覆盖"},
		{"server", "src/lib/llm/server · agent-wizard-probe/compose/generate/skip", "依赖 data 门面（ProjectProbe/ScenarioSave/MemorySave/…）与工作目录写盘；留待 B1"},
		{"httpapi", "src/lib/llm/httpapi · publish（browser 上行入口）", "client→bus 映射已由桥 frontMethodSubjects 对账（TestClientTopicToBusSubjectMapping）覆盖；入口需起 HTTP 服务，重复覆盖收益低"},
		{"data", "src/lib/data · data-<domain>-* 生产者", "不在本批 Go 驱动面（§3 数据面留待 B1）"},
	}
	for _, e := range entries {
		if e.face == "" || e.driver == "" || e.reason == "" {
			t.Errorf("未驱动登记项不完整: %+v", e)
		}
		t.Logf("[未驱动] face=%s | driver=%s | 理由=%s", e.face, e.driver, e.reason)
	}
}

// ── 1. gui.* 本地面 result 键对账（§1）────────────────────────────────────

// TestGuiFaceResultKeysSubsetOfContract 驱动可行 gui.* 处理，断言 result 键 ⊆ 契约。
func TestGuiFaceResultKeysSubsetOfContract(t *testing.T) {
	c := loadContract(t)
	byTopic := c.byTopic()

	cases := []struct {
		topic   string
		payload string
		mocks   []mockSpec
	}{
		{topic: "gui.init-data", payload: `{}`, mocks: []mockSpec{
			{topic: "data-prj-config-list", reply: map[string]any{"ok": true, "result": map[string]any{"list": map[string]any{}}}},
			{topic: "data-user-config-load", reply: map[string]any{"ok": true, "result": map[string]any{"data": map[string]any{}}}},
		}},
		{topic: "gui.ui.save", payload: `{"layout":{"x":1}}`, mocks: []mockSpec{
			{topic: "data-prj-config-save", reply: map[string]any{"ok": true, "result": map[string]any{"ok": true, "id": "layout.x"}}},
		}},
		{topic: "gui.recent.list", payload: `{}`, mocks: []mockSpec{
			{topic: "data-user-config-load", reply: map[string]any{"ok": true, "result": map[string]any{"data": map[string]any{}}}},
		}},
		{topic: "gui.recent.remove", payload: `{"path":"D:/nope"}`, mocks: []mockSpec{
			{topic: "data-user-config-load", reply: map[string]any{"ok": true, "result": map[string]any{"data": map[string]any{"recent_dirs": "[]"}}}},
			{topic: "data-user-config-save", reply: map[string]any{"ok": true, "result": map[string]any{"ok": true, "id": "user_config"}}},
		}},
		{topic: "gui.vcs.info", payload: `{}`},
		{topic: "gui.search", payload: `{"query":"zzz-no-such-symbol"}`},
		{topic: "gui.upload", payload: `{"name":"a.txt","data":"` + base64.StdEncoding.EncodeToString([]byte("hi")) + `","kind":"file"}`},
		{topic: "gui.system.builtins", payload: `{}`},
		{topic: "gui.toolchain.detect", payload: `{}`},
	}

	driven := 0
	for _, tc := range cases {
		tc := tc
		t.Run(tc.topic, func(t *testing.T) {
			ts, ok := byTopic[tc.topic]
			if !ok {
				t.Fatalf("契约未收录 %s", tc.topic)
			}
			br, bus := newTestBridge(t, func(string) {})
			defer bus.Close()
			for _, m := range tc.mocks {
				mockPersist(t, bus, m.topic, m.reply)
			}
			res, errs := br.PublishEvent(tc.topic, tc.payload)
			if len(errs) > 0 {
				t.Fatalf("%s 驱动失败（errs）: %v", tc.topic, errs)
			}
			if res == nil {
				t.Logf("%s 无返回（本地事件，跳过键对账）", tc.topic)
				return
			}
			assertKeysSubset(t, tc.topic, "result", ts.Result, toMap(t, res))
			driven++
		})
	}
	if driven == 0 {
		t.Fatal("未驱动任何 gui.* 用例")
	}
}

type mockSpec struct {
	topic string
	reply map[string]any
}

// TestGuiInitDataEmitsAgentWizardEvent 事件面：init-data 检测到 project_spec.md 缺失时
// 下发 agent-wizard（宿主 → 前端），事件载荷键 ⊆ 契约（§1）。
func TestGuiInitDataEmitsAgentWizardEvent(t *testing.T) {
	c := loadContract(t)
	ts, ok := c.byTopic()["agent-wizard"]
	if !ok {
		t.Fatal("契约未收录 agent-wizard")
	}
	evals := make(chan string, 16)
	br, bus := newTestBridge(t, evalCollect(evals))
	defer bus.Close()
	mockPersist(t, bus, "data-prj-config-list", map[string]any{"ok": true, "result": map[string]any{"list": map[string]any{}}})
	mockPersist(t, bus, "data-user-config-load", map[string]any{"ok": true, "result": map[string]any{"data": map[string]any{}}})

	if _, errs := br.PublishEvent("gui.init-data", `{}`); len(errs) > 0 {
		t.Fatalf("init-data errs: %v", errs)
	}
	env := recvEnvelopeTyp(t, evals, "agent-wizard", time.Second)
	var p map[string]any
	if err := json.Unmarshal([]byte(env.Payload), &p); err != nil {
		t.Fatalf("agent-wizard payload 解析失败: %v", err)
	}
	assertKeysSubset(t, "agent-wizard", "event", ts.Event, p)
}

// ── 2. 事件面：桥转发到前端的事件载荷键对账（§4.3/§5.3/§2.4）────────────

// TestForwardedEventKeysSubsetOfContract 直接向总线注入 61 声明的相对主题事件，经桥
// forwardEvent 转前端，断言转发载荷键 ⊆ 契约 event 声明。
func TestForwardedEventKeysSubsetOfContract(t *testing.T) {
	c := loadContract(t)
	byTopic := c.byTopic()

	cases := []struct {
		subject     string // 总线相对主题（契约 topic 同名）
		frontType   string // 桥映射后的前端 type
		rawPayload  string
	}{
		{"session-receive", "llm-receive", `{"session":"s","turn":"t","type":"text","payload":{"text":"hi"}}`},
		{"session-complete", "llm-complete", `{"session":"s","turn":"t","status":"complete"}`},
		{"session-compress", "llm-compress", `{"instance_id":"inst-test","session":"s","last_turn":"t","snapshot_turn":"u"}`},
		{"session-ask", "ask-user", `{"instance_id":"inst-test","ask_id":"a","session":"s","turn":"t","expires_at":0,"questions":[]}`},
		{"server-starting", "server-starting", `{"started_at":123}`},
		{"prompt-optimised", "prompt-optimised", `{"instance_id":"inst-test","type":"t","content":"c"}`},
		{"tool-notify", "tool-notify", `{"instance_id":"inst-test","session_id":"s","turn_id":"t","notice":"completion","message":"m","message_id":"mid"}`},
		{"task-started", "tasks.started", `{"task_id":"tk","tool":"x","kind":"tool","state":"running","session":"s","turn":"t","work_dir":"/w"}`},
		{"task-done", "tasks.done", `{"task_id":"tk","tool":"x","kind":"tool","state":"done","session":"s","turn":"t","work_dir":"/w"}`},
		{"mcp-tasks-report", "mcp-tasks-report", `{"task_id":"tk","tool":"x","state":"done","result_summary":"ok","instance_id":"inst-test"}`},
		{"mcp-tools-timeout", "mcp-tools-timeout", `{"instance_id":"inst-test","tool_call_id":"tc","task_id":"tk","tool":"x","reason":"timeout","timeout_s":5,"options":["detach","cancel"]}`},
		{"mcp-gateway-changed", "mcp-gateway-changed", `{"instance_id":"inst-test","kind":"tool"}`},
		{"filesys.changed", "filesys.changed", `{"instance_id":"inst-test","work_dir":"/w","path":"/w/a","operation":"write"}`},
		{"session-new", "session-new", `{"instance_id":"inst-test","session_id":"s"}`},
		{"instance-register", "instance-register", `{"instance_id":"inst-test","work_dir":"/w"}`},
		{"window-maximized-changed", "window-maximized-changed", `{"maximized":true,"instance_id":"inst-test"}`},
		{"gui.window.closed", "gui.window.closed", `{"window_id":"w","session_id":"s"}`},
		{"optimize-token", "optimize-token", `{"content":"x"}`},
		{"optimize-done", "optimize-done", `{"prompt":"p"}`},
		{"optimize-error", "optimize-error", `{"message":"m"}`},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.subject, func(t *testing.T) {
			ts, ok := byTopic[tc.subject]
			if !ok {
				t.Fatalf("契约未收录 %s", tc.subject)
			}
			evals := make(chan string, 32)
			br, bus := newTestBridge(t, evalCollect(evals))
			defer bus.Close()
			if err := br.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			pubFire(bus, tc.subject, []byte(tc.rawPayload))
			env := recvEnvelopeTyp(t, evals, tc.frontType, time.Second)
			var p map[string]any
			if err := json.Unmarshal([]byte(env.Payload), &p); err != nil {
				t.Fatalf("%s payload 解析失败: %v", tc.subject, err)
			}
			assertKeysSubset(t, tc.subject, "event", ts.Event, p)
		})
	}
}

// ── 3. 客户端 topic ↔ 总线相对主题 映射对账（§4.5）─────────────────────────

// TestClientTopicToBusSubjectMapping 断言前端 client topic 经桥确实发布到 61 声明的
// 相对主题（订阅相对主题捕获）。覆盖 §4.5 表逐行。
func TestClientTopicToBusSubjectMapping(t *testing.T) {
	c := loadContract(t)
	byTopic := c.byTopic()
	// 契约已知的「总线相对主题」集合 = 全部 topic 名 ∪ 各 topic 声明的 busTopic（§4.5 两层命名）。
	knownBusSubject := map[string]bool{}
	for name, ts := range byTopic {
		knownBusSubject[name] = true
		if ts.BusTopic != "" {
			knownBusSubject[ts.BusTopic] = true
		}
	}

	cases := []struct {
		clientTopic string
		busSubject  string
	}{
		{"llm-send", "session-send"},
		{"llm-cancel", "session-cancel"},
		{"ask-user-reply", "session-ask-reply"},
		{"tool-retry", "tool-retry"},
		{"task-stop", "task-stop"},
		{"task-background", "task-background"},
		{"prompt-optimise", "prompt-optimise"},
		{"agent-wizard-probe", "agent-wizard-probe"},
		{"agent-wizard-compose", "agent-wizard-compose"},
		{"agent-wizard-generate", "agent-wizard-generate"},
		{"agent-wizard-skip", "agent-wizard-skip"},
		{"instance-claim", "instance-claim"},
		{"instance-heartbeat", "instance-heartbeat"},
		{"tools-list", "mcp-tools-list"},
		{"prompts-list", "mcp-prompts-list"},
		{"resources-list", "mcp-resources-list"},
		{"mcp-tools-wait", "mcp-tools-wait"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.clientTopic, func(t *testing.T) {
			// 契约须声明该相对主题（映射目标必须在消息面清单内）。
			if !knownBusSubject[tc.busSubject] {
				t.Fatalf("契约未收录映射目标相对主题 %s（§4.5）", tc.busSubject)
			}
			br, bus := newTestBridge(t, func(string) {})
			defer bus.Close()
			hit := make(chan map[string]any, 1)
			_, err := subRaw(bus, tc.busSubject, func(_ string, payload []byte) {
				var m map[string]any
				if json.Unmarshal(payload, &m) == nil {
					select {
					case hit <- m:
					default:
					}
				}
			})
			if err != nil {
				t.Fatalf("订阅 %s 失败: %v", tc.busSubject, err)
			}
			_, _ = br.PublishEvent(tc.clientTopic, `{}`)
			select {
			case m := <-hit:
				if m["instance_id"] != "inst-test" {
					t.Errorf("%s → %s 未注入 instance_id: %+v", tc.clientTopic, tc.busSubject, m)
				}
			case <-time.After(time.Second):
				t.Fatalf("前端 type %s 未映射/发布到相对主题 %s（§4.5）", tc.clientTopic, tc.busSubject)
			}
		})
	}
}

// TestLlmStartSplitsIntoSessionStartAndSend 特例：llm-start 桥拆两步（§4.5 备注）。
func TestLlmStartSplitsIntoSessionStartAndSend(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()
	got := make(chan string, 2)
	for _, subj := range []string{"session-start", "session-send"} {
		_, err := subRaw(bus, subj, func(s string, _ []byte) {
			select {
			case got <- s:
			default:
			}
		})
		if err != nil {
			t.Fatalf("订阅 %s 失败: %v", subj, err)
		}
	}
	_, errs := br.PublishEvent("llm-start", `{"session":"s","turn":"t","q":"hi"}`)
	if len(errs) > 0 {
		t.Fatalf("llm-start errs: %v", errs)
	}
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case s := <-got:
			seen[s] = true
		case <-time.After(time.Second):
		}
	}
	if !seen["session-start"] || !seen["session-send"] {
		t.Fatalf("llm-start 应拆为 session-start + session-send，实际 %v", seen)
	}
}

// ── 4. data-session-context 两产者路径键集对账（I-145 回归）──────────────
//
// 同一 61 §3.2a 主题的两条产者路径（MQ 信封 = persist `envelope.go` context / 桥 inline 门面 =
// `facade_session.go` context）**应答键集必须一致**：修复前桥 inline 分支返回
// `wire.MessageLoadResult`（仅 `{messages}`），**丢弃可选伴随数组 turn_tokens**，与 persist 信封的
// `wire.ContextResult`（`{messages, turn_tokens?}`）分叉（I-145）。本用例驱动**非空 turn_tokens**
// 场景，断言两路径键集逐字相等（防再分叉）。

// newContextParityEnv 建两路径对账环境：临时 usr 库的 persist（MQ 面）+ 同总线 inline 门面 +
// 桥（已注入门面）。返回桥、persist 服务、实例 id、工作目录。
func newContextParityEnv(t *testing.T) (*bridge.Bridge, *persist.Service, string, string) {
	t.Helper()
	data.Reset()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	svc := persist.New(bus, persist.Options{UsrPath: t.TempDir() + "/usr.db"})
	if err := svc.Start(); err != nil {
		t.Fatalf("persist.Start: %v", err)
	}
	t.Cleanup(svc.Stop)
	t.Cleanup(data.Reset)

	const instanceID = "ins-ctx"
	wd := t.TempDir()
	body, _ := json.Marshal(map[string]any{"instance_id": instanceID, "work_dir": wd, "data_dir": ""})
	if err := bus.Emit(context.Background(), "instance-register", body).Wait().Err(); err != nil {
		t.Fatalf("instance-register: %v", err)
	}
	data.Register(instanceID, wd, "")
	time.Sleep(20 * time.Millisecond) // 等 persist 自持实例视图登记
	br := bridge.New(instanceID, wd, "", nil, bus)
	br.SetFacade(inline.New(bus))
	return br, svc, instanceID, wd
}

// keySetOf 取 map 键集合（对账用）。
func keySetOf(m map[string]any) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// TestSessionContextTwoPathsKeyParity 断言 data-session-context 两条产者路径键集一致。
func TestSessionContextTwoPathsKeyParity(t *testing.T) {
	br, svc, instanceID, wd := newContextParityEnv(t)

	// 造会话 + 轮次（经桥门面写库）。
	for _, req := range []struct{ typ, payload string }{
		{"data-session-ensure-session", `{"session_id":"s-ctx"}`},
		{"data-session-ensure-turn", `{"turn_id":"t-ctx","session_id":"s-ctx"}`},
	} {
		if _, errs := br.PublishEvent(req.typ, req.payload); len(errs) > 0 {
			t.Fatalf("%s errs=%v", req.typ, errs)
		}
	}
	// 预存该轮 token —— 「非空 turn_tokens」的触发条件（nil = 不写键）。
	full, brief := 123, 45
	if _, err := svc.TurnComplete(facade.TurnCompleteRequest{
		InstanceID: instanceID, TurnID: "t-ctx", Status: "done",
		FullTokens: &full, BriefTokens: &brief, Scope: facade.Scope{WorkDir: wd},
	}); err != nil {
		t.Fatalf("TurnComplete: %v", err)
	}

	// 路径①：桥 inline 门面（同进程直调）。
	resInline, errs := br.PublishEvent("data-session-context", `{"session_id":"s-ctx"}`)
	if len(errs) > 0 {
		t.Fatalf("inline context errs=%v", errs)
	}
	keysInline := keySetOf(toMap(t, resInline))

	// 路径②：MQ 信封（未注入门面 → 回落总线 persist 数据服务）。
	br.SetFacade(nil)
	resMQ, errs := br.PublishEvent("data-session-context", `{"session_id":"s-ctx"}`)
	if len(errs) > 0 {
		t.Fatalf("MQ context errs=%v", errs)
	}
	keysMQ := keySetOf(toMap(t, resMQ))

	// 对账：两路径键集一致，且都含 61 §3.2a 声明的 messages + 可选伴随数组 turn_tokens。
	if !reflect.DeepEqual(keysInline, keysMQ) {
		t.Fatalf("data-session-context 两路径键集不一致（I-145 再分叉）：inline=%v MQ=%v", keysInline, keysMQ)
	}
	if !keysInline["messages"] || !keysInline["turn_tokens"] {
		t.Fatalf("两路径应答应含 {messages, turn_tokens}：%v", keysInline)
	}
}
