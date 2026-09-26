// 一致性校验（21 §9.2 P2 · verify.go，**只读**）：对比「层内记录（热视图）」与**权威表
// tasktree** 的行，输出差异（供测试与诊断）——P2 单写者后用于证明「层与库一致」。
package task

import (
	"fmt"
	"sort"
	"strings"
)

// verifyFields 是层记录与权威行的**可比字段**（两边同源：层落库行的构造见 store.row；
// updated_at 由写时刻决定（层内为事件处理时刻、库为落库时刻）→ 不参与比较）。
var verifyFields = []string{
	"node_id", "task_id", "top_session", "session_id", "kind", "node_type",
	"parent_node_id", "title", "status", "created_at", "finished_at", "tool_call_id",
	"workdir", "closed",
}

// Diff 是单字段差异（task_id + 字段 + 两侧取值）。
type Diff struct {
	TaskID        string
	Field         string
	Layer         string
	Authoritative string
}

// VerifyReport 是 Verify 的差异报告（Match() = 无任何差异）。
type VerifyReport struct {
	TopSession          string
	LayerRows           int
	AuthoritativeRows   int
	OnlyInLayer         []string // 层有、权威无
	OnlyInAuthoritative []string // 权威有、层无
	Diffs               []Diff
	LayerErr            string // 层读失败（只读校验不 panic，记错误文本）
	AuthoritativeErr    string
}

// Match 无任何差异（且两侧均读取成功）。
func (r *VerifyReport) Match() bool {
	return r.LayerErr == "" && r.AuthoritativeErr == "" &&
		len(r.OnlyInLayer) == 0 && len(r.OnlyInAuthoritative) == 0 && len(r.Diffs) == 0
}

// String 差异报告（人类可读；零差异 → "Verify(top_session): 零差异 (n 行)"）。
func (r *VerifyReport) String() string {
	if r.LayerErr != "" || r.AuthoritativeErr != "" {
		return fmt.Sprintf("Verify(%s): 读取失败 layer=%q authoritative=%q", r.TopSession, r.LayerErr, r.AuthoritativeErr)
	}
	if r.Match() {
		return fmt.Sprintf("Verify(%s): 零差异 (%d 行)", r.TopSession, r.AuthoritativeRows)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Verify(%s): 层 %d 行 / 权威 %d 行", r.TopSession, r.LayerRows, r.AuthoritativeRows)
	for _, id := range r.OnlyInLayer {
		fmt.Fprintf(&b, "\n  仅层有: %s", id)
	}
	for _, id := range r.OnlyInAuthoritative {
		fmt.Fprintf(&b, "\n  仅权威有: %s", id)
	}
	for _, d := range r.Diffs {
		fmt.Fprintf(&b, "\n  字段差异 %s.%s: 层=%q 权威=%q", d.TaskID, d.Field, d.Layer, d.Authoritative)
	}
	return b.String()
}

// Verify 对比某主会话的「层记录」与权威表行（只读）。
//
// 实例解析（MW-11：显式透传，**不再**以空串走数据面「唯一实例回退」）：
//   - instanceID 非空 → 直接用（调用方显式携带，如方法面 task-verify 的 `instance_id`）；
//   - 空 → 回落「从层内事件记录解析该主会话的来源实例」；
//   - 仍解析不到 → 明确报错（写入 AuthoritativeErr，令校验以失败收场），不静默回退。
//
// closed 行一并读取（逻辑删除不参与差异判定为「缺失」）。
func (l *Layer) Verify(instanceID, topSession string) (*VerifyReport, error) {
	rep := &VerifyReport{TopSession: topSession}
	if l.bus == nil || topSession == "" {
		return rep, nil
	}
	if instanceID == "" {
		instanceID, _ = l.instanceOf(topSession)
	}
	if instanceID == "" {
		rep.AuthoritativeErr = "instance_id 不可解析（层内无该 top_session 的事件记录；须显式携带 instance_id）"
		return rep, nil
	}
	rows, err := l.store.list(instanceID, topSession, true)
	if err != nil {
		rep.AuthoritativeErr = err.Error()
		return rep, nil
	}
	rep.AuthoritativeRows = len(rows)
	authByID := indexRows(rows)

	l.mu.Lock()
	layerByID := map[string]map[string]any{}
	for id, rec := range l.recs {
		if rec.TopSession != topSession {
			continue
		}
		layerByID[id] = layerRow(rec)
	}
	l.mu.Unlock()
	rep.LayerRows = len(layerByID)

	for id, lr := range layerByID {
		ar, ok := authByID[id]
		if !ok {
			rep.OnlyInLayer = append(rep.OnlyInLayer, id)
			continue
		}
		for _, f := range verifyFields {
			lv, av := sval(lr[f]), sval(ar[f])
			if lv != av {
				rep.Diffs = append(rep.Diffs, Diff{TaskID: id, Field: f, Layer: lv, Authoritative: av})
			}
		}
	}
	for id := range authByID {
		if _, ok := layerByID[id]; !ok {
			rep.OnlyInAuthoritative = append(rep.OnlyInAuthoritative, id)
		}
	}
	sort.Strings(rep.OnlyInLayer)
	sort.Strings(rep.OnlyInAuthoritative)
	sort.SliceStable(rep.Diffs, func(i, j int) bool {
		if rep.Diffs[i].TaskID != rep.Diffs[j].TaskID {
			return rep.Diffs[i].TaskID < rep.Diffs[j].TaskID
		}
		return rep.Diffs[i].Field < rep.Diffs[j].Field
	})
	return rep, nil
}

// ── 方法面（`task-verify`，I-87）────────────────────────────────────────────
//
// 层已实现 Verify（对比层记录与权威表行），但**无 MQ 面** → 活进程内无法远程调用（诊断缺口）。
// 2026-09-18 用户确认新增方法面主题 **`task-verify`**（**非** `data-*` 域）：校验需要**层内运行态
// 热视图**（本包 Layer.recs），data 模块只看库、单独无法完成 → 由**层宿主**（chonkpilot-llm/server，
// 层在该进程内）注册并转发到本方法。载荷解析 / 返回构造统一在层内，宿主只做收发（口径单源）。
//
// 载荷：{instance_id, top_session, include_closed?}；返回：见 VerifyResult。**只读**、无副作用。

// VerifyRequest 是 `task-verify` 方法面载荷。
type VerifyRequest struct {
	InstanceID string `json:"instance_id,omitempty"` // 消息面必带（61 §0.1）；显式透传（缺省回落 top_session 推实例，解析不到即报错）
	TopSession string `json:"top_session"`           // 校验的主会话（树隔离维度，D7）
	// IncludeClosed 为前向兼容字段：校验**恒读** closed 行（逻辑删除行必须参与比对，否则会把
	// 「库中仍在但已关闭」误判为「仅层有」）→ 该字段目前不改变行为，缺省/任意值均可。
	IncludeClosed *bool `json:"include_closed,omitempty"`
}

// VerifyDiff 是差异条目（JSON 稳定形态；由 VerifyReport.Diffs 映射）。
type VerifyDiff struct {
	TaskID        string `json:"task_id"`
	Field         string `json:"field"`
	Layer         string `json:"layer"`
	Authoritative string `json:"authoritative"`
}

// VerifyResult 是 `task-verify` 返回载荷（字段名稳定，供运维 / 测试断言）：
//   - match：无任何差异（且两侧读取均成功）；
//   - diffs：字段级差异（恒为非 null 数组：零差异 → `[]`）；
//   - rows：权威表行数；cached_rows：层内热视图行数；
//   - only_in_layer / only_in_authoritative：两侧独有节点（诊断用）；
//   - error：读取失败原因（非空 = 校验未完成，match=false）。
type VerifyResult struct {
	Match               bool         `json:"match"`
	Diffs               []VerifyDiff `json:"diffs"`
	Rows                int          `json:"rows"`
	CachedRows          int          `json:"cached_rows"`
	OnlyInLayer         []string     `json:"only_in_layer,omitempty"`
	OnlyInAuthoritative []string     `json:"only_in_authoritative,omitempty"`
	Error               string       `json:"error,omitempty"`
}

// VerifyPayload 执行 `task-verify` 方法面语义（只读）：载荷 → `Verify(instance_id, top_session)`
// → 稳定返回载荷。缺 top_session / 载荷为空 → 返回 error 文本（match=false，**不 panic**）。
// `instance_id` 优先（消息面必带，61 §0.1）；缺省则回落层内事件记录解析，仍不可解析 → error 文本。
func (l *Layer) VerifyPayload(req *VerifyRequest) *VerifyResult {
	if req == nil || req.TopSession == "" {
		return &VerifyResult{Diffs: []VerifyDiff{}, Error: "top_session required"}
	}
	rep, err := l.Verify(req.InstanceID, req.TopSession)
	if err != nil {
		return &VerifyResult{Diffs: []VerifyDiff{}, Error: err.Error()}
	}
	out := &VerifyResult{
		Match:               rep.Match(),
		Diffs:               make([]VerifyDiff, 0, len(rep.Diffs)),
		Rows:                rep.AuthoritativeRows,
		CachedRows:          rep.LayerRows,
		OnlyInLayer:         rep.OnlyInLayer,
		OnlyInAuthoritative: rep.OnlyInAuthoritative,
	}
	for _, d := range rep.Diffs {
		out.Diffs = append(out.Diffs, VerifyDiff{TaskID: d.TaskID, Field: d.Field, Layer: d.Layer, Authoritative: d.Authoritative})
	}
	if rep.LayerErr != "" || rep.AuthoritativeErr != "" {
		out.Error = "layer=" + rep.LayerErr + " authoritative=" + rep.AuthoritativeErr
	}
	return out
}

// layerRow 把层记录映射为**可比字段视图**（键 = 权威行同名字段；node_id 与 task_id 同值；
// node_type 与落库派生同源：kind=llm → session，其余 → tool）。
// status 与落库同一口径（`frontVisibleStatus`，P3 相位门控）→ 两侧比较的仍是**前端可见状态**，
// 不会因 detached / awaiting 而误报差异。
func layerRow(rec *Record) map[string]any {
	nodeType := NodeTypeTool
	if rec.NodeType == NodeTypeLLM {
		nodeType = "session"
	}
	return map[string]any{
		"node_id": rec.TaskID, "task_id": rec.TaskID,
		"top_session": rec.TopSession, "session_id": rec.SessionID,
		"kind": rec.NodeType, "node_type": nodeType, "parent_node_id": rec.ParentID,
		"title": rec.Title, "status": frontVisibleStatus(rec.State), "created_at": rec.CreatedAt,
		"finished_at": rec.DoneAt, "tool_call_id": rec.ToolCallID,
		"workdir": rec.WorkDir, "closed": rec.Closed,
	}
}

// indexRows 按节点 id（node_id → task_id 回落）建索引。
func indexRows(rows []map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		id := sval(row["node_id"])
		if id == "" {
			id = sval(row["task_id"])
		}
		if id != "" {
			out[id] = row
		}
	}
	return out
}
