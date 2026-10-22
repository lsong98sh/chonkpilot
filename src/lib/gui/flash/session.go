package flash

// flash 无窗口宿主（flash-cli）的会话驱动（🆕 2026-10-10；🅐 flash-cli）。
//
// 逐字对齐原 CLI 单体（2026-10-22 已删除）的**命令面与输出格式**（话题/payload 一字不改）：
// 发起 `session-start` + `session-send`，打印 `session-receive` / `session-complete` /
// `task-done`。输出模式 = sse（默认，流式）/ final（仅最终文本）/ verbose（全报文 dump）。
//
// 与 CLI 单体的唯一差别 = 总线为**远程 `uds.Client`**（业务消息经 UDS 转发常驻 server），
// 本文件不感知传输实现（只依赖 `mq.Bus`）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/idgen"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// 输出模式（与 CLI 单体一致）。
const (
	OutputSSE     = "sse"
	OutputFinal   = "final"
	OutputVerbose = "verbose"
)

// sessionStartSubject / sessionSendSubject 是一轮会话的发起主题（与方法面同字面；位于
// llm server 的订阅面，见 61 §4.5。既有 CLI 亦以字面量发起，本处保持一致）。
const (
	sessionStartSubject = "session-start"
	sessionSendSubject  = "session-send"
)

// sessionWaitCeiling 是 ctx 无 deadline 时等待 session-complete 的默认上界（兜底）：
// 防止 session-send 被静默丢弃（turn 已回收）时 headless 会话无限挂起。取值宽松
// （远大于常规一轮会话），仅作兜底而非限流；调用方自带 ctx deadline 时以调用方为准。
const sessionWaitCeiling = 30 * time.Minute

// SessionOptions 是 headless 会话参数（flash-cli 解析后传入）。
type SessionOptions struct {
	InstanceID string
	Prompt     string
	Scenario   string
	LLM        string
	Think      string
	Effort     string
	// Output = sse（默认）/ final / verbose。
	Output string
	// Out 是输出目标；nil = os.Stdout。
	Out io.Writer
}

// RunSession 在给定总线（远程 UDS）上发起一轮会话并打印输出，直到 `session-complete`
// 或 ctx 取消。返回后由调用方关闭总线（连接断开 = 实例退出）。
func RunSession(ctx context.Context, bus mq.Bus, opts SessionOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	mode := opts.Output
	if mode == "" {
		mode = OutputSSE
	}
	switch mode {
	case OutputSSE, OutputFinal, OutputVerbose:
	default:
		return fmt.Errorf("无效输出模式 %q，可选：sse / final / verbose", mode)
	}
	if bus == nil {
		return fmt.Errorf("flash: 会话总线未接线")
	}

	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }
	var finalText string

	if _, err := bus.On(msgkeys.TopicSessionReceive, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var ev struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		_ = json.Unmarshal(v.Payload, &ev)
		switch mode {
		case OutputVerbose:
			fmt.Fprintf(out, "[RECV] %s %s\n", ev.Type, string(v.Payload))
		case OutputSSE:
			switch ev.Type {
			case "text":
				var text string
				_ = json.Unmarshal(ev.Payload, &text)
				fmt.Fprint(out, text)
			case "tool-call":
				var tc struct {
					Tool string `json:"tool"`
				}
				_ = json.Unmarshal(ev.Payload, &tc)
				if tc.Tool != "" {
					fmt.Fprintf(out, "\n[工具] %s\n", tc.Tool)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if _, err := bus.On(msgkeys.TopicSessionComplete, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var ev struct {
			Text string `json:"text,omitempty"`
		}
		_ = json.Unmarshal(v.Payload, &ev)
		finalText = ev.Text
		if mode == OutputVerbose {
			fmt.Fprintf(out, "[COMPLETE] %s\n", string(v.Payload))
		}
		finish()
		return nil
	}); err != nil {
		return err
	}

	if _, err := bus.On(msgkeys.TopicTaskDone, 0, func(_ context.Context, _ string, v *mq.Value) error {
		if mode == OutputVerbose {
			fmt.Fprintf(out, "[TASK] %s\n", string(v.Payload))
			return nil
		}
		if mode != OutputSSE {
			return nil
		}
		var ev struct {
			Tool          string `json:"tool"`
			State         string `json:"state"`
			ResultSummary string `json:"result_summary,omitempty"`
			Error         string `json:"error,omitempty"`
		}
		_ = json.Unmarshal(v.Payload, &ev)
		if ev.State == "done" && ev.ResultSummary != "" {
			summary := ev.ResultSummary
			// 按 rune 截断（避免中文被按字节截出半个码点）。
			if len(summary) > 200 {
				if r := []rune(summary); len(r) > 200 {
					summary = string(r[:200]) + "..."
				}
			}
			fmt.Fprintf(out, "[工具完成] %s → %s\n", ev.Tool, summary)
		} else if ev.State == "error" {
			fmt.Fprintf(out, "[工具错误] %s: %s\n", ev.Tool, ev.Error)
		}
		return nil
	}); err != nil {
		return err
	}

	// session/turn 由客户端分配（服务端 onLLMStart 必填，见 61 §4.5）；对齐
	// 原 CLI 单体的载荷面（此前缺失 → 服务端回 accepted:false 致本进程挂死）。
	session := idgen.NewUUID()
	turn := idgen.NewUUID()
	startPayload := map[string]any{
		"instance_id": opts.InstanceID,
		"session":     session,
		"turn":        turn,
	}
	if opts.LLM != "" {
		startPayload["llm"] = opts.LLM
	}
	if opts.Scenario != "" {
		startPayload["scenario_id"] = opts.Scenario
	}
	if opts.Think != "" {
		startPayload["think"] = opts.Think
	}
	if opts.Effort != "" {
		startPayload["effort"] = opts.Effort
	}
	if raw, err := json.Marshal(startPayload); err == nil && mode == OutputVerbose {
		fmt.Fprintf(out, "[START] %s\n", string(raw))
	}
	if v := bus.Emit(ctx, sessionStartSubject, startPayload).Wait(); v.Err() != nil {
		return fmt.Errorf("session-start 失败：%w", v.Err())
	} else if m, _ := v.Result.(map[string]any); m != nil {
		// 服务端拒绝（如 session/turn/instance 缺失、session busy）以 Result 回写且不置 Err →
		// 必须显式判定，否则会话永不 complete → 本进程静默挂死（61 §4.5）。
		if ok, _ := m["accepted"].(bool); !ok {
			return fmt.Errorf("session-start 被拒绝：%v", m["error"])
		}
	}

	sendPayload := map[string]any{
		"session": session,
		"turn":    turn,
		"type":    "text-user",
		"content": opts.Prompt,
	}
	if raw, err := json.Marshal(sendPayload); err == nil && mode == OutputVerbose {
		fmt.Fprintf(out, "[SEND] %s\n", string(raw))
	}
	// session-send 的成功路径**不回写 Result**（服务端 onLLMSend 命中即 return nil），但 turn
	// 未找到/已回收时服务端回 {accepted:false, error}（R8-D-3）→ 收到即**立即报错**（空 Result
	// = 已受理）。下方有界等待仍保留，兜底其余未回执的极端情形（如传输层丢包）。
	if v := bus.Emit(ctx, sessionSendSubject, sendPayload).Wait(); v.Err() != nil {
		return fmt.Errorf("session-send 失败：%w", v.Err())
	} else if m, _ := v.Result.(map[string]any); m != nil {
		if ok, has := m["accepted"].(bool); has && !ok {
			return fmt.Errorf("session-send 被拒绝：%v", m["error"])
		}
	}

	// 有界等待：session-send 被静默丢弃（turn 在 start/send 间被回收）时不再有
	// session-complete，无界等待将一直挂到调用方 ctx 取消（headless flash-cli 的 ctx 仅由
	// 信号取消 → 会话既不完成也不报错）。故：ctx 无 deadline 时以默认上界兜底；无论何种取消，
	// 未收到 session-complete 一律返回明确错误（而非静默成功）。
	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, sessionWaitCeiling)
		defer cancel()
	}
	select {
	case <-done:
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return fmt.Errorf("会话未完成：上下文已取消（session=%s turn=%s）", session, turn)
		}
		return fmt.Errorf("会话在 %s 内未完成（session-send 可能被服务端静默丢弃）：session=%s turn=%s",
			sessionWaitCeiling, session, turn)
	}
	if mode == OutputFinal && finalText != "" {
		fmt.Fprintln(out, finalText)
	}
	return nil
}
