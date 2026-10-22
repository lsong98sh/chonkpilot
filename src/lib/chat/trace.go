// 影子重放（MOD-1 M4）：只记录、只比对，不接管生产。
//
// 用法（宿主 / 测试）：
//
//	rec := chat.NewRecorder()
//	shadow := &chat.ShadowTurnStore{Inner: realStore, Rec: rec} // 生产写入照常，旁路记录
//	... 引擎跑一遍（StepRequest 由调用点 rec.RecordStep）...
//	diff := chat.Diff(want, rec.Trace())                        // 逐项比对，不改变任何状态
//
// 硬约束：
//   - 记录器只追加，不修改入参、不决定执行路径（不接管生产）；
//   - 比较按 canonical JSON（结构体字段序固定 + map 键排序）逐项相等；
//   - 影子层失败不得影响生产（本层只追加内存切片，不返回错误）。
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// WriteRecord 是一次落库写入（影子记录；Data 为写入值的 canonical JSON）。
type WriteRecord struct {
	Kind string          `json:"kind"` // message | inbox | event
	Key  string          `json:"key,omitempty"`
	Data json.RawMessage `json:"data"`
}

// Trace 是一次运行的影子轨迹：StepRequest 序列 + 写入序列。
type Trace struct {
	Steps  []StepRequest `json:"steps"`
	Writes []WriteRecord `json:"writes"`
}

// Divergence 是一处影子比对差异。
type Divergence struct {
	Seq  string `json:"seq"`  // 如 steps[2] / writes[0]
	Want string `json:"want"` // 期望（canonical JSON）
	Got  string `json:"got"`  // 实际（canonical JSON）
}

// Recorder 是影子记录器（并发安全；只追加）。
type Recorder struct {
	mu     sync.Mutex
	steps  []StepRequest
	writes []WriteRecord
}

// NewRecorder 构造空记录器。
func NewRecorder() *Recorder { return &Recorder{} }

// RecordStep 记录一次 LLM 请求（由引擎调用点旁路调用）。
func (r *Recorder) RecordStep(req StepRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, req)
}

// RecordWrite 记录一次写库（value 经 canonical JSON 固化）。
func (r *Recorder) RecordWrite(kind, key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"marshal_error":%q}`, err.Error()))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, WriteRecord{Kind: kind, Key: key, Data: data})
}

// Trace 返回轨迹快照（切片拷贝；Data 视为不可变）。
func (r *Recorder) Trace() Trace {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Trace{
		Steps:  append([]StepRequest(nil), r.steps...),
		Writes: append([]WriteRecord(nil), r.writes...),
	}
	return out
}

// Reset 清空（复用同一记录器做多轮影子）。
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = nil
	r.writes = nil
}

// Len 返回 (steps, writes) 数量（测试 / 观测用）。
func (r *Recorder) Len() (steps, writes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.steps), len(r.writes)
}

// ShadowTurnStore 是 TurnStore 的影子装饰器：先记录、再原样委托 Inner。
// **不接管生产**——返回值、错误、顺序均与 Inner 一致；记录失败不阻断。
type ShadowTurnStore struct {
	Inner TurnStore
	Rec   *Recorder
}

func (s *ShadowTurnStore) SaveMessage(ctx context.Context, msg Message) error {
	s.record("message", "", msg)
	return s.Inner.SaveMessage(ctx, msg)
}

func (s *ShadowTurnStore) CommitInbox(ctx context.Context, inboxID string, msg Message) error {
	s.record("inbox", inboxID, msg)
	return s.Inner.CommitInbox(ctx, inboxID, msg)
}

func (s *ShadowTurnStore) AppendEvent(ctx context.Context, ev TurnEvent) error {
	s.record("event", fmt.Sprintf("%d", ev.Seq), ev)
	return s.Inner.AppendEvent(ctx, ev)
}

func (s *ShadowTurnStore) record(kind, key string, value any) {
	if s == nil || s.Rec == nil {
		return
	}
	s.Rec.RecordWrite(kind, key, value)
}

// Diff 逐项比较两条轨迹（先 steps 后 writes；顺序敏感）。
func Diff(want, got Trace) []Divergence {
	var out []Divergence
	n := len(want.Steps)
	if len(got.Steps) > n {
		n = len(got.Steps)
	}
	for i := 0; i < n; i++ {
		w, g := "<missing>", "<missing>"
		if i < len(want.Steps) {
			w = canonical(want.Steps[i])
		}
		if i < len(got.Steps) {
			g = canonical(got.Steps[i])
		}
		if w != g {
			out = append(out, Divergence{Seq: fmt.Sprintf("steps[%d]", i), Want: w, Got: g})
		}
	}

	n = len(want.Writes)
	if len(got.Writes) > n {
		n = len(got.Writes)
	}
	for i := 0; i < n; i++ {
		w, g := "<missing>", "<missing>"
		if i < len(want.Writes) {
			w = canonical(want.Writes[i])
		}
		if i < len(got.Writes) {
			g = canonical(got.Writes[i])
		}
		if w != g {
			out = append(out, Divergence{Seq: fmt.Sprintf("writes[%d]", i), Want: w, Got: g})
		}
	}
	return out
}

// Verify 返回 nil（逐项相等）或包含全部差异的错误（最多展示前 5 处）。
func Verify(want, got Trace) error {
	diffs := Diff(want, got)
	if len(diffs) == 0 {
		return nil
	}
	const maxShow = 5
	var b strings.Builder
	fmt.Fprintf(&b, "chat replay diverged at %d point(s)", len(diffs))
	for i, d := range diffs {
		if i >= maxShow {
			fmt.Fprintf(&b, "\n  ... (%d more)", len(diffs)-maxShow)
			break
		}
		fmt.Fprintf(&b, "\n  %s:\n    want %s\n    got  %s", d.Seq, d.Want, d.Got)
	}
	return fmt.Errorf("%s", b.String())
}

// canonical 返回稳定 JSON 文本（结构体字段序固定；map 键由 encoding/json 排序）。
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal_error:%v>", err)
	}
	return string(b)
}
