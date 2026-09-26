// P1-3 白盒：内置兜底 echo provider（代码写死；D-30 后**不再作为可配置 provider 暴露**，
// 仅保留名可解析）——协议 echo：
//
//	① 不发任何 HTTP（-llm-base 端点 0 次请求），回复文本 == 固定文案（收到<用户输入>，目前无法回复，请设置LLM。）；
//	② 系统注入消息（Kind=continue/resume/notify）不被取文；
//	③ 未命中保留名（近似名 echoo）→ 仍回落 exe flags（回归，回落语义未变）；
//	④ 保留名可解析 / 非保留名不解析 / 每次返回新副本（D-30 配置面收窄，兜底语义保留）。
package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-router"
)

// echoTextOf 拼接 llm-receive 事件里 type=text 的增量（= 本轮回复文本）。
func echoTextOf(events []map[string]any) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev["type"] != NotifyTypeText {
			continue
		}
		p, _ := ev["payload"].(map[string]any)
		s, _ := p["text"].(string)
		sb.WriteString(s)
	}
	return sb.String()
}

// TestEchoProviderNoHTTPEchoesUserInput（断言 ①）：llm=echo（usr llms 无该记录）→ 内置兜底保留名
// 命中：不发任何 HTTP，回复文本 == 固定文案（收到<用户输入>，目前无法回复，请设置LLM。），轮次 complete。
func TestEchoProviderNoHTTPEchoesUserInput(t *testing.T) {
	flagRec, flagSrv := newProviderServer(t) // = exe flag -llm-base（echo 不应请求它）
	s := newTestServer(t, flagSrv)
	registerProviderInstance(t, s)
	startTurnLLM(t, s, "s-echo", "t-echo", "echo", "on", "high")

	const input = "只回复我的输入内容"
	const want = "收到只回复我的输入内容，目前无法回复，请设置LLM。"
	wt := watchTurn(t, s.bus, "t-echo")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-echo", "turn": "t-echo",
		"type": "text-user", "content": input,
	}))
	evs := wt.waitComplete(10 * time.Second)

	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("echo 轮次未 complete: %+v", evs)
	}
	if got := echoTextOf(evs); got != want {
		t.Fatalf("echo 回复=%q want %q（固定文案：收到<用户输入>，目前无法回复，请设置LLM。）", got, want)
	}
	if n := flagRec.count(); n != 0 {
		t.Fatalf("-llm-base 端点被请求 %d 次（echo 不应发任何 HTTP）", n)
	}
}

// TestEchoIgnoresSystemInjectedMessages（断言 ②）：末尾的系统注入消息（continue 自动续写 /
// resume 恢复续轮 / notify 工具通知）不参与取文——取文取最近一条真实用户消息（Kind=text）。
func TestEchoIgnoresSystemInjectedMessages(t *testing.T) {
	msgs := []ChatMsg{
		{Role: "system", Content: "sys"},
		{Role: "user", Kind: "text", Content: "问1"},
		{Role: "assistant", Content: "答1"},
		{Role: "user", Kind: "text", Content: "问2"},
		{Role: "user", Kind: assembleContinueKind, Content: assembleContinueText},
		{Role: "user", Kind: "notify", Content: "[工具通知] 完成"},
		{Role: "user", Kind: assembleResumeKind, Content: "恢复"},
	}
	// 保留名 `echo` → router 内置兜底（不发 HTTP；忽略 tools）。
	evs, err := chatTest(t, router.Spec{Name: builtinFallbackName, Protocol: ProtocolEcho, DefaultModel: builtinFallbackName},
		msgs, []ToolDef{{Name: "read_file"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	content, _, calls, finish, serr := collectStreamEvents(evs)
	if serr != nil {
		t.Fatalf("echo 不应产生错误事件: %v", serr)
	}
	if want := "收到问2，目前无法回复，请设置LLM。"; content != want {
		t.Fatalf("echo 回复=%q want %q（跳过 continue/notify/resume 系统注入）", content, want)
	}
	if finish != "stop" {
		t.Fatalf("finish_reason=%q want stop", finish)
	}
	if len(calls) != 0 {
		t.Fatalf("echo 不应有工具调用，实得 %+v", calls)
	}
}

// TestEchoBuiltinFallbackNotConfigurable（D-30 语义）：echo **不再作为「可配置 provider」暴露**，
// 但**兜底语义保留** —— 保留名 `echo` 仍解析出只读 echo 配置（protocol=echo、不发 HTTP）；
// 非保留名（含近似名 / 已失效名）不解析 → nil（调用方回落 exe flags）。
func TestEchoBuiltinFallbackNotConfigurable(t *testing.T) {
	fb := builtinFallbackProvider(builtinFallbackName)
	if fb == nil || fb.Protocol != ProtocolEcho || fb.Model != builtinFallbackName {
		t.Fatalf("builtinFallbackProvider(%q)=%+v want {protocol:echo model:echo}（兜底语义保留）", builtinFallbackName, fb)
	}
	if fb.Name != builtinFallbackName {
		t.Fatalf("兜底 Name=%q want %q", fb.Name, builtinFallbackName)
	}
	for _, name := range []string{"", "echoo", "Echo", "openai"} {
		if got := builtinFallbackProvider(name); got != nil {
			t.Fatalf("builtinFallbackProvider(%q)=%+v want nil（仅保留名可解析）", name, got)
		}
	}
	// 返回副本：多次调用互不共享（调用方改配置不影响兜底）。
	a, b := builtinFallbackProvider(builtinFallbackName), builtinFallbackProvider(builtinFallbackName)
	if a == b {
		t.Fatalf("builtinFallbackProvider 每次须返回新副本（零共享）")
	}
}

// TestEchoNearMissNameFallsBackToFlags（断言 ③）：未命中保留名（近似名 echoo）→ 仍回落 exe
// flags 现状（请求打到 -llm-base、body.model = 传入值、无 Authorization）。
func TestEchoNearMissNameFallsBackToFlags(t *testing.T) {
	flagRec, flagSrv := newProviderServer(t)
	s := newTestServer(t, flagSrv)
	registerProviderInstance(t, s)
	startTurnLLM(t, s, "s-near", "t-near", "echoo", "on", "high")
	sendAndWait(t, s, "s-near", "t-near", "near miss")

	body, auth, _ := flagRec.first(t, 0)
	if got, _ := body["model"].(string); got != "echoo" {
		t.Fatalf("body.model=%v want echoo（未命中保留名：传入值即 model，现状）", body["model"])
	}
	if auth != "" {
		t.Fatalf("未命中 provider 不应带 Authorization，实得 %q", auth)
	}
}
