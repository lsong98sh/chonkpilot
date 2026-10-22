package chat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// recordHook 记录被调用的节点（用于顺序 / 帧区分断言）。
type recordHook struct {
	name   string
	calls  *[]string
	before Action
	bt     Action // BeforeTool 的返回（默认 Continue）
	bsErr  error  // BeforeStep 错误
	atErr  error  // AfterTurn 错误
}

func (h *recordHook) rec(node string) { *h.calls = append(*h.calls, h.name+":"+node) }

func (h *recordHook) BeforeTurn(context.Context, *TurnInfo) (Action, error) {
	h.rec("before_turn")
	return h.before, nil
}
func (h *recordHook) AfterTurn(context.Context, *TurnResult) error {
	h.rec("after_turn")
	return h.atErr
}
func (h *recordHook) BeforeStep(context.Context, *ExecContext, *StepRequest) (Action, error) {
	h.rec("before_step")
	return h.before, h.bsErr
}
func (h *recordHook) AfterStep(context.Context, *ExecContext, *StepResult) error {
	h.rec("after_step")
	return nil
}
func (h *recordHook) BeforeTool(context.Context, *ExecContext, *ToolCall) (Action, error) {
	h.rec("before_tool")
	return h.bt, nil
}
func (h *recordHook) AfterTool(context.Context, *ExecContext, *ToolResult) error {
	h.rec("after_tool")
	return nil
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mainFrame() *ExecContext { return &ExecContext{TurnID: "t1", FrameKind: FrameMain} }
func insertFrame() *ExecContext {
	return &ExecContext{TurnID: "t1", FrameID: "inbox-1", FrameKind: FrameInsert, Depth: 1}
}

// TestLifecycleOrder：priority 升序、同 priority 按注册序。
func TestLifecycleOrder(t *testing.T) {
	l := NewLifecycle(discardLogger())
	var calls []string
	l.Register(&recordHook{name: "b", calls: &calls}, 10, FailOpen)
	l.Register(&recordHook{name: "a", calls: &calls}, 0, FailOpen)
	l.Register(&recordHook{name: "c", calls: &calls}, 10, FailOpen)

	if act, err := l.BeforeTurn(context.Background(), mainFrame(), &TurnInfo{TurnID: "t1"}); err != nil || act != ActionContinue {
		t.Fatalf("BeforeTurn = %v err=%v", act, err)
	}
	if got := strings.Join(calls, ","); got != "a:before_turn,b:before_turn,c:before_turn" {
		t.Fatalf("执行顺序 = %q", got)
	}
}

// TestLifecycleTurnHooksSkipInsertFrame：插入帧不触发 turn 级钩子，step/tool 级照常。
func TestLifecycleTurnHooksSkipInsertFrame(t *testing.T) {
	l := NewLifecycle(discardLogger())
	var calls []string
	l.Register(&recordHook{name: "h", calls: &calls}, 0, FailOpen)

	ctx := context.Background()
	f := insertFrame()
	if act, err := l.BeforeTurn(ctx, f, &TurnInfo{TurnID: "t1"}); err != nil || act != ActionContinue {
		t.Fatalf("插入帧 BeforeTurn 应 no-op：%v %v", act, err)
	}
	if err := l.AfterTurn(ctx, f, &TurnResult{TurnID: "t1"}); err != nil {
		t.Fatalf("插入帧 AfterTurn 应 no-op：%v", err)
	}
	if _, err := l.BeforeStep(ctx, f, &StepRequest{}); err != nil {
		t.Fatalf("插入帧 BeforeStep：%v", err)
	}
	if err := l.AfterStep(ctx, f, &StepResult{}); err != nil {
		t.Fatalf("插入帧 AfterStep：%v", err)
	}
	if _, err := l.BeforeTool(ctx, f, &ToolCall{ID: "c"}); err != nil {
		t.Fatalf("插入帧 BeforeTool：%v", err)
	}
	if err := l.AfterTool(ctx, f, &ToolResult{CallID: "c"}); err != nil {
		t.Fatalf("插入帧 AfterTool：%v", err)
	}

	want := "h:before_step,h:after_step,h:before_tool,h:after_tool"
	if got := strings.Join(calls, ","); got != want {
		t.Fatalf("插入帧触发集合 = %q，want %q（turn 级必须不触发）", got, want)
	}
}

// TestLifecycleFailPolicy：默认 fail-open 不阻断；显式 fail-closed 阻断。
func TestLifecycleFailPolicy(t *testing.T) {
	ctx := context.Background()

	open := NewLifecycle(discardLogger())
	open.Register(&recordHook{name: "o", calls: new([]string), bsErr: errors.New("boom")}, 0, FailOpen)
	if act, err := open.BeforeStep(ctx, mainFrame(), &StepRequest{}); err != nil || act != ActionContinue {
		t.Fatalf("fail-open BeforeStep = %v err=%v（应继续）", act, err)
	}

	closed := NewLifecycle(discardLogger())
	closed.Register(&recordHook{name: "c", calls: new([]string), bsErr: errors.New("boom")}, 0, FailClosed)
	act, err := closed.BeforeStep(ctx, mainFrame(), &StepRequest{})
	if err == nil || act != ActionDrop {
		t.Fatalf("fail-closed BeforeStep = %v err=%v（应 Drop + error）", act, err)
	}

	// After* fail-closed 返回错误（After* 必达由调用方保证）。
	closed2 := NewLifecycle(discardLogger())
	closed2.Register(&recordHook{name: "c2", calls: new([]string), atErr: errors.New("after")}, 0, FailClosed)
	if err := closed2.AfterTurn(ctx, mainFrame(), &TurnResult{}); err == nil {
		t.Fatal("fail-closed AfterTurn 应返回错误")
	}
}

// TestLifecycleDropStopsChain：Before* 返回 Drop → 后续 Hook 不再执行。
func TestLifecycleDropStopsChain(t *testing.T) {
	l := NewLifecycle(discardLogger())
	var calls []string
	l.Register(&recordHook{name: "first", calls: &calls, bt: ActionDrop}, 0, FailOpen)
	l.Register(&recordHook{name: "second", calls: &calls}, 1, FailOpen)

	act, err := l.BeforeTool(context.Background(), mainFrame(), &ToolCall{ID: "c"})
	if err != nil || act != ActionDrop {
		t.Fatalf("BeforeTool = %v err=%v", act, err)
	}
	if got := strings.Join(calls, ","); got != "first:before_tool" {
		t.Fatalf("Drop 后不应继续执行：%q", got)
	}
}

// TestLifecycleNoHooksAndNil：无 Hook / nil Hook → 安全 no-op。
func TestLifecycleNoHooksAndNil(t *testing.T) {
	l := NewLifecycle(nil)
	l.Register(nil, 0, FailOpen)
	ctx := context.Background()
	if act, err := l.BeforeTurn(ctx, mainFrame(), &TurnInfo{}); err != nil || act != ActionContinue {
		t.Fatalf("无 Hook BeforeTurn = %v err=%v", act, err)
	}
	if _, err := l.BeforeStep(ctx, mainFrame(), &StepRequest{}); err != nil {
		t.Fatalf("无 Hook BeforeStep：%v", err)
	}
	if err := l.AfterTool(ctx, mainFrame(), &ToolResult{}); err != nil {
		t.Fatalf("无 Hook AfterTool：%v", err)
	}
}

// TestExecContextIsInsert：空 FrameKind 等价主帧。
func TestExecContextIsInsert(t *testing.T) {
	if (*ExecContext)(nil).IsInsert() {
		t.Fatal("nil ExecContext 不应视为插入帧")
	}
	if (&ExecContext{}).IsInsert() {
		t.Fatal("空 FrameKind 为主帧")
	}
	if !insertFrame().IsInsert() {
		t.Fatal("FrameKind=insert 应识别为插入帧")
	}
}
