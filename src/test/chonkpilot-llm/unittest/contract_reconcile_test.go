// contract_reconcile_test.go — B2 契约面 · server 生产端对账（2026-10-05）。
//
// 目标（延续 B0/B1 机制）：以 `docs/spec/60-reference/61-messages.schema.json`（61 §4 server
// 方法面/事件的机器可读抽取）为**唯一基准**，用**最小合法输入**经内存总线驱动真实 llm server
// （相对主题），断言**实际发出的 result / event 键 ⊆ 契约声明**（多出 = 红）。
//
// 覆盖：
//   - §4.2 方法面 result：llm-start（总线 session-start ack）· llm-simple
//   - §4.3 事件载荷：session-receive · session-complete · session-turn-start（一轮文本会话）
//
// 局限（覆盖边界，见 docs/spec/50-testing/50-测试体系.md §8.6）：
//   - 只驱动**可在内存总线 + mock LLM**下跑通的方法/事件（其余列于 TestServerNotDrivenRegistry）；
//   - 只对账**顶层键**；断言强度 = 存在级（键子集）。
package llmtest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// ── 契约结构（与 61-messages.schema.json 对齐）─────────────────────────────

type srvField struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type srvTopic struct {
	Topic  string              `json:"topic"`
	Face   string              `json:"face"`
	Result map[string]srvField `json:"result"`
	Event  map[string]srvField `json:"event"`
}

type srvDoc struct {
	Version string     `json:"version"`
	Topics  []srvTopic `json:"topics"`
}

func (c *srvDoc) byTopic() map[string]srvTopic {
	m := make(map[string]srvTopic, len(c.Topics))
	for _, ts := range c.Topics {
		m[ts.Topic] = ts
	}
	return m
}

func srvFindFile(t *testing.T) string {
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

func srvLoad(t *testing.T) *srvDoc {
	t.Helper()
	raw, err := os.ReadFile(srvFindFile(t))
	if err != nil {
		t.Fatalf("读取契约失败: %v", err)
	}
	var c srvDoc
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("契约 JSON 解析失败: %v", err)
	}
	if len(c.Topics) == 0 {
		t.Fatal("契约未收录任何主题")
	}
	return &c
}

func srvAssertKeys(t *testing.T, topic, which string, declared map[string]srvField, actual map[string]any) {
	t.Helper()
	var extra []string
	for k := range actual {
		if k == "error" {
			continue
		}
		if _, ok := declared[k]; ok {
			continue
		}
		extra = append(extra, k)
	}
	if len(extra) > 0 {
		t.Errorf("%s.%s 出现契约未声明的键（多键 = 红）: %v（契约声明=%v）",
			topic, which, extra, srvKeys(declared))
	}
	// required 缺失 = 红：契约声明 required=true 的键必须存在（error 信封下整体豁免）。
	if _, isErr := actual["error"]; isErr {
		return
	}
	var missing []string
	for k, f := range declared {
		if !f.Required {
			continue
		}
		if _, ok := actual[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%s.%s 缺契约声明的 required 键（缺失 = 红）: %v", topic, which, missing)
	}
}

func srvKeys(m map[string]srvField) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── 对账主体 ──────────────────────────────────────────────────────────────

// TestServerResultKeysSubsetOfContract 驱动 llm-start（总线 session-start）与 llm-simple，断言 result 键 ⊆ 契约。
func TestServerResultKeysSubsetOfContract(t *testing.T) {
	c := srvLoad(t)
	byTopic := c.byTopic()

	llm := textLLMServer("你好")
	defer llm.Close()
	bus := newBus(t)
	startLLM(t, bus, llm.URL, false)
	registerInstance(t, bus, "ins-cr")
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("llm-start", func(t *testing.T) {
		ts, ok := byTopic["llm-start"]
		if !ok {
			t.Fatal("契约未收录 llm-start")
		}
		v := bus.Emit(ctx, "session-start", map[string]any{
			"instance_id": "ins-cr", "session": "s-cr", "turn": "t-cr",
		}).Wait()
		if err := v.Err(); err != nil {
			t.Fatalf("session-start: %v", err)
		}
		res, _ := v.Result.(map[string]any)
		srvAssertKeys(t, "llm-start", "result", ts.Result, res)
	})

	t.Run("llm-simple", func(t *testing.T) {
		ts, ok := byTopic["llm-simple"]
		if !ok {
			t.Fatal("契约未收录 llm-simple")
		}
		v := bus.Emit(ctx, "llm-simple", map[string]any{"prompt": "hi"}).Wait()
		if err := v.Err(); err != nil {
			t.Fatalf("llm-simple: %v", err)
		}
		res, _ := v.Result.(map[string]any)
		srvAssertKeys(t, "llm-simple", "result", ts.Result, res)
	})
}

// TestServerEventKeysSubsetOfContract 驱动一轮文本会话，断言 session-* 事件载荷键 ⊆ 契约（§4.3）。
func TestServerEventKeysSubsetOfContract(t *testing.T) {
	c := srvLoad(t)
	byTopic := c.byTopic()

	llm := textLLMServer("你好，我是助手")
	defer llm.Close()
	bus := newBus(t)
	startLLM(t, bus, llm.URL, false)
	registerInstance(t, bus, "ins-cr2")
	time.Sleep(500 * time.Millisecond)

	const session, turn = "s-cr2", "t-cr2"
	startTurn(t, bus, session, turn, "ins-cr2")

	type ev struct {
		subject string
		payload map[string]any
	}
	got := make(chan ev, 64)
	_, err := bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		// 只收集**契约主题**（相对主题 = topic）；request 侧总线相对主题（session-start/send 等）
		// 不在 61 主题集合 → 跳过（非事件面）。
		if _, ok := byTopic[subject]; !ok {
			return nil
		}
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil || m["turn"] != turn {
			return nil
		}
		select {
		case got <- ev{subject: subject, payload: m}:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-cr2", "session": session, "turn": turn,
		"type": "text-user", "content": "hello",
	}))

	deadline := time.After(10 * time.Second)
	seen := map[string]bool{}
	for {
		select {
		case e := <-got:
			ts, ok := byTopic[e.subject]
			if !ok {
				t.Errorf("契约未收录相对主题 %s", e.subject)
				continue
			}
			if len(ts.Event) == 0 {
				t.Errorf("契约主题 %s 未声明 event 侧", e.subject)
				continue
			}
			srvAssertKeys(t, e.subject, "event", ts.Event, e.payload)
			seen[e.subject] = true
		case <-deadline:
			if !seen["session-receive"] || !seen["session-complete"] {
				t.Fatalf("未观测到必要事件（receive=%v complete=%v）", seen["session-receive"], seen["session-complete"])
			}
			return
		}
	}
}

// TestServerNotDrivenRegistry 登记本批「不可在内存总线低成本驱动」的 server 生产端及原因（不硬凑），
// 并**与契约主题集合做差集断言**：server 面（face=server）全部主题必须 = 本套件已驱动 ∪ 本登记表；
// 差集非空（契约有、既未驱动又未登记）= 红，必须在下方显式列白。
func TestServerNotDrivenRegistry(t *testing.T) {
	c := srvLoad(t)

	// 本套件**已驱动**的 server 面主题（见 TestServerResultKeysSubsetOfContract /
	// TestServerEventKeysSubsetOfContract 实际驱动的主题）。
	driven := map[string]bool{
		"llm-start": true, "llm-simple": true,
		"session-receive": true, "session-complete": true, "session-turn-start": true,
	}

	entries := []struct {
		topic  string
		reason string
	}{
		{"agent-wizard-probe", "依赖 data 门面 ProjectProbe + 工作目录；驱动需装配 data 门面（留待 data/gui 侧或专测）"},
		{"agent-wizard-compose", "依赖门面知识库（KnowledgeRoot/Read）读 archetype；装配成本高"},
		{"agent-wizard-generate", "一次写场景 + 记忆 + prj 配置 + 工程规格 + git init；副作用面广，留待专测"},
		{"agent-wizard-skip", "受理即返回 {ok}；可驱动但价值低（无载荷），暂不纳入"},
		{"prompt-optimise", "经默认 LLM 流式优化（异步事件 prompt-optimised），时序依赖强，留待专测"},
		{"task-verify", "需任务层宿主 + 权威表行；装配成本高"},
		{"instance-claim", "依赖认证域（令牌/claim 校验），需 auth 库装配；其广播 instance-register 已由桥/持久面对账覆盖"},
		{"login-register", "依赖 auth 库（bcrypt/令牌落盘）与入口承载；需专测"},
		{"login-in", "同上（认证域）"},
		{"login-out", "同上（认证域）"},
		{"llm.test-connection", "需 mock LLM 探活 + provider 装配；留待专测（其消费端键已由前端对账覆盖）"},
		{"memory.flush", "由 memory 插件消费（非 server 生产）；不在本面对账"},
		{"session-ask", "需 ask_user 工具调用链路（mock LLM 触发 tool-call）；留待专测"},
		// 差集补齐：server 面其余主题——B2 未直接驱动，但均由 B0 桥面/映射覆盖或需多窗口装配。
		{"llm-send", "client→bus 映射由 B0 TestClientTopicToBusSubjectMapping 覆盖（llm-send→session-send）"},
		{"llm-cancel", "client→bus 映射由 B0 TestClientTopicToBusSubjectMapping 覆盖（llm-cancel→session-cancel）"},
		{"ask-user-reply", "client→bus 映射由 B0 TestClientTopicToBusSubjectMapping 覆盖（ask-user-reply→session-ask-reply）"},
		{"tool-retry", "client→bus 映射由 B0 TestClientTopicToBusSubjectMapping 覆盖"},
		{"task-stop", "client→bus 映射由 B0 TestClientTopicToBusSubjectMapping 覆盖"},
		{"task-background", "client→bus 映射由 B0 TestClientTopicToBusSubjectMapping 覆盖"},
		{"instance-heartbeat", "instance 生命周期主题；需多实例/心跳装配，单测级未驱动"},
		{"instance-exit", "instance 生命周期主题；需多实例装配，单测级未驱动"},
		{"instance-bring-to-top", "instance 生命周期主题；需多窗口装配，单测级未驱动"},
		{"instance-goto-top", "instance 生命周期广播；需多窗口装配，单测级未驱动"},
		// 单聊 subchat-*（2026-10-16）：B-1 登记消息面 + 跨窗口投递基础设施；**B-2 已落 handler**
		// （生产/消费 = `subchat_open`/`subchat_finish` handler + `subchat-list`/`subchat-finish`
		// 订阅，见 `src/lib/llm/server/subchat.go`）。本套件不伪造业务链路 → 仍登记；键/契约/文档
		// 一致性由 chonkpilot-lib msgkeys subchat_keys_test 覆盖，**handler 行为由
		// chonkpilot-llm/server 包 L1 单测（subchat_test.go，mq 驱动 + mq 监听）覆盖**，
		// 定向投递由 chonkpilot-gui bridge subchat_delivery_test 覆盖。
		{"subchat-opened", "生产者 = subchat_open handler（B-2 已落，见 server/subchat.go）；行为由 server 包 L1 subchat_test 覆盖"},
		{"subchat-state", "生产者 = 子聊天状态变更路径（B-2 已落）；定向投递能力另由 chonkpilot-gui subchat_delivery_test 覆盖"},
		{"subchat-list", "前端 → server 方法面（B-2 已落 handler）；同名映射由 chonkpilot-gui bridge wiring 覆盖"},
		{"subchat-finish", "前端 → server 方法面（B-2 已落 handler）；同名映射由 chonkpilot-gui bridge wiring 覆盖"},
		// 群聊「继续」续期（2026-10-16 · SUBCHAT-3 阶段 F）：「继续」= 前端 → server 方法面；本套件不
		// 伪造群聊调度链路 → 仍登记；handler 行为由 server 包 L1 单测（groupchat_sched_test.go，mq 驱动）覆盖。
		{"subchat-continue", "生产者 = subchat.go onSubchatContinue（阶段 F 已落）；同名映射由 chonkpilot-gui bridge wiring 覆盖"},
		// 群聊参与者状态（2026-10-16 · 阶段 E）：契约/文档/键已落（61 §4.3 + schema + genmsg）；生产者 =
		// 群聊参与者状态变更路径（**阶段 F 调度器**），消费者 = 前端头像。阶段 E 不含调度器 → 仍登记；
		// 键一致性由 chonkpilot-lib msgkeys 覆盖，转发对账由 chonkpilot-gui bridge contract_reconcile 覆盖。
		{"subchat-participant-state", "阶段 E 已落契约（server→client 下行）；生产者 = 群聊参与者状态路径（阶段 F 调度器），消费者 = 前端头像；转发对账由 chonkpilot-gui bridge contract_reconcile 覆盖"},
		{"instance-register", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖"},
		{"session-compress", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖"},
		{"session-new", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖"},
		{"server-starting", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖"},
		{"prompt-optimised", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖（prompt-optimise 异步产物）"},
		{"tool-notify", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖"},
		{"task-started", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖"},
		{"task-updated", "事件载荷键由前端/任务区对账覆盖（B2 未直接驱动）"},
		{"task-done", "事件转发由 B0 TestForwardedEventKeysSubsetOfContract 覆盖"},
	}
	declared := c.byTopic()
	registered := map[string]bool{}
	for _, e := range entries {
		if e.topic == "" || e.reason == "" {
			t.Errorf("未驱动登记项不完整: %+v", e)
		}
		if _, ok := declared[e.topic]; !ok {
			t.Errorf("登记项非契约收录主题（须为 61 主题）: %s", e.topic)
		}
		registered[e.topic] = true
		t.Logf("[未驱动] topic=%s | 理由=%s", e.topic, e.reason)
	}
	// 差集断言：server 面（face=server）全部主题须为「已驱动 ∪ 已登记」。
	var missing []string
	for _, ts := range c.Topics {
		if ts.Face != "server" {
			continue
		}
		if driven[ts.Topic] || registered[ts.Topic] {
			continue
		}
		missing = append(missing, ts.Topic)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("server 面契约主题既未驱动又未登记（契约有 = 红，须显式列白）: %v", missing)
	}
}
