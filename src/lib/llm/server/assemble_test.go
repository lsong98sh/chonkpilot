// 上下文拼接规则的**宿主侧**测试（组装内核 / 三段投影 / reasoning 规则的纯函数用例已随实现
// 搬到组件包 `src/lib/chonkpilot-session/project_test.go`；此处只保留需要宿主 Server / 门面的用例）。
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-router"
)

// TestRouterMessagesReasoning：canonical 折算保留 reasoning（→ router 适配器再映射为
// 协议字段 `reasoning_content`；线格式断言归 router 白盒 `internal/adaptor/openai`）。
func TestRouterMessagesReasoning(t *testing.T) {
	msgs := []ChatMsg{
		{Role: "assistant", Content: "c", Reasoning: "R"},
		{Role: "user", Content: "u"},
	}
	w := routerMessages(msgs, nil)
	if w[0].Reasoning != "R" || w[1].Reasoning != "" {
		t.Fatalf("canonical reasoning 映射错: %+v", w)
	}
	if len(w[0].Content) != 1 || w[0].Content[0].Type != router.PartText || w[0].Content[0].Text != "c" {
		t.Fatalf("canonical 内容块错: %+v", w[0].Content)
	}
}

// TestTurnUserMessageNotDuplicated（I-25 回归）：一次用户提问在 LLM 请求里只出现一次。
func TestTurnUserMessageNotDuplicated(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-dedup", "t-dedup")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-dedup", "turn": "t-dedup",
		"type": "text-user", "content": "唯一提问",
	}))
	evs := collectTurn(t, s.bus, "t-dedup", 10*time.Second)
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	reqs := rec.requests()
	if len(reqs) == 0 {
		t.Fatal("no llm request recorded")
	}
	last := reqs[len(reqs)-1]
	n := 0
	for _, m := range last {
		if m.Role == "user" && m.Content == "唯一提问" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("user 消息出现 %d 次，应为 1：%+v", n, last)
	}
}

// TestRecoverTurnCtxMarksForceFull：重启/死 turn 恢复重建的轮次标记 forceFull（该 turn 必须全量）。
func TestRecoverTurnCtxMarksForceFull(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	tc := s.recoverTurnCtx("ins-test", "s-force", "t-force")
	if !tc.forceFull {
		t.Fatal("恢复重建的轮次应标记 forceFull")
	}
	if tc.keepFullTurns != defaultKeepFullTurns {
		t.Fatalf("维持轮数应回落默认 %d：got %d", defaultKeepFullTurns, tc.keepFullTurns)
	}
	tc.Close()
}

// TestRecoverTurnCtxCarriesSystemPromptAndDirs（WP2-4）：恢复轮与新建轮**同口径** —— 系统提示词层
// 注入 hist、WorkDir/DataDir 随 instance 绑定回填（gateway 上下文与记忆/资产指引据此生效）。
func TestRecoverTurnCtxCarriesSystemPromptAndDirs(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	dataDir := t.TempDir()
	// 清理顺序：TempDir 的清理最后注册、最先执行；data.Reset 必须晚于它注册，
	// 才能在删目录前先关闭 DB（否则 Windows 上 db 文件被占用导致 RemoveAll 失败）。
	t.Cleanup(data.Reset)
	// 注册 instance（绑定 work_dir / data_dir，与 onLLMStart 同一事实源）
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest",
		"work_dir": testWorkDir, "data_dir": dataDir,
	})).Wait()
	time.Sleep(50 * time.Millisecond)

	tc := s.recoverTurnCtx("ins-test", "s-rec", "t-rec")
	defer tc.Close()
	if tc.req.WorkDir != testWorkDir {
		t.Fatalf("恢复轮 WorkDir=%q want %q（随 instance 绑定）", tc.req.WorkDir, testWorkDir)
	}
	if tc.req.DataDir != dataDir {
		t.Fatalf("恢复轮 DataDir=%q want %q（随 instance 绑定）", tc.req.DataDir, dataDir)
	}
	if len(tc.hist) == 0 || tc.hist[0].Role != "system" || strings.TrimSpace(tc.hist[0].Content) == "" {
		t.Fatalf("恢复轮 hist 应含非空系统提示词层，got %+v", tc.hist)
	}
}

// TestLoadKeepFullBoundsValueSemantics（P1 口径 W，组装侧读点）：键**存在且为数字** → 原样采用
// （`0` = 该条件不启用、负数 = 非法按不启用）；**缺失/非数字** → 回落默认（10 / 24000）。
// **与压缩侧 `resolveOpts` 同口径**（差异即"两侧判定不一致"，故单列断言）。
func TestLoadKeepFullBoundsValueSemantics(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	// 登记实例（data.Prj / 门面配置读同一份事实）→ 之后才能写 prj 配置。
	data.Register("ins-test", testWorkDir, "")
	t.Cleanup(data.Reset)
	prj, releasePrj, err := data.Prj("ins-test")
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	defer releasePrj() // 短开（D-45）：用完即释
	// 缺失 → 默认。
	if got := s.loadKeepFullTurns("ins-test"); got != defaultKeepFullTurns {
		t.Fatalf("缺失 keep_full_max_turns → 默认 %d：got %d", defaultKeepFullTurns, got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != defaultKeepFullTokens {
		t.Fatalf("缺失 keep_full_max_tokens → 默认 %d：got %d", defaultKeepFullTokens, got)
	}
	// 显式 0 → 原样（该条件不启用，不再回落默认）。
	if err := data.SetConfig(prj, "keep_full_max_turns", "0"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "0"); err != nil {
		t.Fatal(err)
	}
	if got := s.loadKeepFullTurns("ins-test"); got != 0 {
		t.Fatalf("显式 0 应原样采用：got %d", got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != 0 {
		t.Fatalf("显式 0 应原样采用：got %d", got)
	}
	// 负数（非法）→ 原样采用（按不启用处理）。
	if err := data.SetConfig(prj, "keep_full_max_turns", "-3"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "-9"); err != nil {
		t.Fatal(err)
	}
	if got := s.loadKeepFullTurns("ins-test"); got != -3 {
		t.Fatalf("负数应原样采用（按不启用）：got %d", got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != -9 {
		t.Fatalf("负数应原样采用（按不启用）：got %d", got)
	}
	// 非数字 → 回落默认。
	if err := data.SetConfig(prj, "keep_full_max_turns", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetConfig(prj, "keep_full_max_tokens", "x1"); err != nil {
		t.Fatal(err)
	}
	if got := s.loadKeepFullTurns("ins-test"); got != defaultKeepFullTurns {
		t.Fatalf("非数字 → 默认 %d：got %d", defaultKeepFullTurns, got)
	}
	if got := s.loadKeepFullTokens("ins-test"); got != defaultKeepFullTokens {
		t.Fatalf("非数字 → 默认 %d：got %d", defaultKeepFullTokens, got)
	}
}
