// flash_session_test.go — flash 无窗口宿主（flash-cli）的会话驱动 `flash.RunSession` 验证
// （🆕 2026-10-10）。命令面与输出格式逐字对齐 `src/desktop/cli/main.go`。
//
// 以**进程内 mq** 作被驱动总线：发起 `session-start` / `session-send`，回投
// `session-receive` / `task-done` / `session-complete`，断言 sse / final / verbose 输出。
package flash_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/flash"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// driveSession 用进程内总线驱动一轮会话：订阅其上行发起，回投既有事件；返回会话输出。
func driveSession(t *testing.T, mode string) string {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	started := make(chan struct{})
	var sent []map[string]any
	if _, err := bus.On("session-start", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		close(started)
		return nil
	}); err != nil {
		t.Fatalf("bus.On(start): %v", err)
	}
	if _, err := bus.On("session-send", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		sent = append(sent, m)
		return nil
	}); err != nil {
		t.Fatalf("bus.On(send): %v", err)
	}

	go func() {
		<-started
		bus.Emit(context.Background(), msgkeys.TopicSessionReceive, map[string]any{"type": "text", "payload": "hi"})
		bus.Emit(context.Background(), msgkeys.TopicTaskDone, map[string]any{
			"tool": "read_file", "state": "done", "result_summary": "ok",
		})
		bus.Emit(context.Background(), msgkeys.TopicSessionComplete, map[string]any{"text": "HI"})
	}()

	var buf bytes.Buffer
	if err := flash.RunSession(context.Background(), bus, flash.SessionOptions{
		InstanceID: "i1", Prompt: "p", Output: mode, Out: &buf,
	}); err != nil {
		t.Fatalf("RunSession: %v", err)
	}
	if len(sent) != 1 || sent[0]["content"] != "p" || sent[0]["type"] != "text-user" {
		t.Fatalf("session-send 载荷异常: %#v", sent)
	}
	return buf.String()
}

// TestRunSessionSSE：sse 模式打印流式文本与工具完成摘要。
func TestRunSessionSSE(t *testing.T) {
	got := driveSession(t, flash.OutputSSE)
	if !strings.Contains(got, "hi") {
		t.Fatalf("sse 应含流式文本，得 %q", got)
	}
	if !strings.Contains(got, "[工具完成] read_file → ok") {
		t.Fatalf("sse 应含工具完成摘要，得 %q", got)
	}
}

// TestRunSessionFinal：final 模式仅打印最终文本。
func TestRunSessionFinal(t *testing.T) {
	got := driveSession(t, flash.OutputFinal)
	if !strings.Contains(got, "HI") {
		t.Fatalf("final 应含最终文本，得 %q", got)
	}
	if strings.Contains(got, "[工具完成]") {
		t.Fatalf("final 不应含中间输出，得 %q", got)
	}
}

// TestRunSessionVerbose：verbose 模式 dump 全报文（START/RECV/TASK/COMPLETE）。
func TestRunSessionVerbose(t *testing.T) {
	got := driveSession(t, flash.OutputVerbose)
	for _, want := range []string{"[START]", "[SEND]", "[RECV]", "[TASK]", "[COMPLETE]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("verbose 应含 %s，得 %q", want, got)
		}
	}
}

// TestRunSessionInvalidOutput：非法输出模式 → 明确错误（不发起会话）。
func TestRunSessionInvalidOutput(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	if err := flash.RunSession(context.Background(), bus, flash.SessionOptions{Output: "bogus"}); err == nil {
		t.Fatal("非法输出模式应报错")
	}
}
