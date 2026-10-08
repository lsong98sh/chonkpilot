// tree.go — DSL 作业的**静态语句树 + 步骤进度**跟踪（执行器侧；DSL-3 展示供数）。
//
// 与 llm_run（llm/server/jobdsl.go）同口径：建引擎前预走 AST 产静态容器节点（LOOP/PARALLEL
// 折叠容器，各建一次、**不随迭代增长**）；每个 LLM 步骤开始/结束发 step；容器进度变化重发
// tree（**同 id → 只更新 loop_current，不新增节点**）。IF 透明（不新增 kind）。语句/容器 id 仅
// 作业内有效，gateway 侧再前缀作业 id 落库。
package main

import (
	"strconv"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// DSL 容器 kind（与 61 §3.4 / llm server TaskKindDsl* 一致）。
const (
	treeKindLoop     = "dsl_loop"
	treeKindParallel = "dsl_parallel"
)

// jobTree 承载一个作业的静态容器树与步骤进度（并发安全：PARALLEL/并发 LOOP 下多分支并发）。
type jobTree struct {
	job string
	lw  *lineWriter

	mu        sync.Mutex
	order     []string             // 容器 id（声明序）
	nodes     map[string]*treeNode // 容器 id → 节点（parent/kind/label）
	lead      map[string]string    // 容器 id → 首步（lead）原始参数（进度计数归属）
	counts    map[string]int       // 容器 id → loop_current
	stmtOwner map[string]string    // LLM 语句原始参数 → 所属容器 id（空 = 作业根）
	stmtID    map[string]string    // LLM 语句原始参数 → 静态语句 id
	stepNo    int                  // 步骤序号（跨迭代累计）
	steps     map[int]*stepMsg     // 序号 → 步骤（回填终态用）
	seq       int                  // 容器/语句 id 序号
}

func newJobTree(job string, lw *lineWriter) *jobTree {
	return &jobTree{
		job: job, lw: lw,
		nodes:     map[string]*treeNode{},
		lead:      map[string]string{},
		counts:    map[string]int{},
		stmtOwner: map[string]string{},
		stmtID:    map[string]string{},
		steps:     map[int]*stepMsg{},
	}
}

// buildStaticTree 预走 AST 建静态容器树（在引擎执行前调用一次）。
func (t *jobTree) buildStaticTree(stmts []dsl.Stmt) {
	_ = t.walkStatic(stmts, "")
}

// walkStatic 递归预走：返回该层**首个 LLM 语句的原始参数**（供容器 lead 判定 loop_current）。
func (t *jobTree) walkStatic(stmts []dsl.Stmt, parentID string) string {
	var lead string
	for _, st := range stmts {
		switch s := st.(type) {
		case *dsl.LoopStmt:
			cid := t.mkContainer(treeKindLoop, loopLabel(s), parentID)
			inner := t.walkStatic(s.Block, cid)
			if inner != "" && t.lead[cid] == "" {
				t.lead[cid] = inner
			}
			if lead == "" {
				lead = inner
			}
		case *dsl.ParallelStmt:
			cid := t.mkContainer(treeKindParallel, "PARALLEL", parentID)
			inner := t.walkStatic(s.Block, cid)
			if inner != "" && t.lead[cid] == "" {
				t.lead[cid] = inner
			}
			if lead == "" {
				lead = inner
			}
		case *dsl.IfStmt:
			// IF 透明：块内语句挂当前容器（与 llm_run 同口径；不新增 dsl_if kind）。
			if inner := t.walkStatic(s.Block, parentID); inner != "" && lead == "" {
				lead = inner
			}
		case *dsl.ActionStmt:
			if strings.EqualFold(s.Verb, "LLM") {
				t.stmtOwner[s.Args] = parentID
				if t.stmtID[s.Args] == "" {
					t.seq++
					t.stmtID[s.Args] = "s" + strconv.Itoa(t.seq)
				}
				if lead == "" {
					lead = s.Args
				}
			}
		}
	}
	return lead
}

// mkContainer 建一个静态容器节点（各建一次）。
func (t *jobTree) mkContainer(kind, label, parentID string) string {
	t.seq++
	id := "c" + strconv.Itoa(t.seq)
	t.nodes[id] = &treeNode{ID: id, Parent: parentID, Kind: kind, Label: label}
	t.order = append(t.order, id)
	return id
}

// emitTree 发一次静态容器树全量快照（loop_current 取当前计数）。无容器节点 → 不发（未使用
// LOOP/PARALLEL 的脚本不产生 tree 行；作业根由 gateway 在首个 step 时建）。
func (t *jobTree) emitTree() {
	t.mu.Lock()
	if len(t.order) == 0 {
		t.mu.Unlock()
		return
	}
	nodes := make([]treeNode, 0, len(t.order))
	for _, id := range t.order {
		n := *t.nodes[id]
		n.LoopCurrent = t.counts[id]
		nodes = append(nodes, n)
	}
	t.mu.Unlock()
	t.lw.sendTree(t.job, nodes)
}

// nextNo 取下一个步骤序号（1 起，跨迭代累计）。
func (t *jobTree) nextNo() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stepNo++
	return t.stepNo
}

// stepStart 记一次步骤开始：推进归属容器 loop_current（lead 命中者；祖先容器由其自己的 lead
// 同源计数）→ 重发 tree（有容器时）→ 发 step{running}。
func (t *jobTree) stepStart(no int, purpose, rawArgs, session string) {
	t.mu.Lock()
	for cid, leadArgs := range t.lead {
		if leadArgs == rawArgs {
			t.counts[cid]++
		}
	}
	stmt := t.stmtID[rawArgs]
	nodes := make([]treeNode, 0, len(t.order))
	for _, id := range t.order {
		n := *t.nodes[id]
		n.LoopCurrent = t.counts[id]
		nodes = append(nodes, n)
	}
	t.steps[no] = &stepMsg{Job: t.job, No: no, Status: "running", Purpose: purpose, Session: session, Statement: stmt}
	snap := *t.steps[no]
	t.mu.Unlock()

	if len(nodes) > 0 {
		t.lw.sendTree(t.job, nodes)
	}
	t.lw.sendStep(snap)
}

// stepEnd 回填步骤终态并重发 step（status/elapsed_ms）。
func (t *jobTree) stepEnd(no int, status string, elapsedMs int64) {
	t.mu.Lock()
	s := t.steps[no]
	if s == nil {
		t.mu.Unlock()
		return
	}
	s.Status = status
	s.ElapsedMs = elapsedMs
	snap := *s
	t.mu.Unlock()
	t.lw.sendStep(snap)
}

// loopLabel 容器展示名（LOOP <迭代变量> / LOOP）。
func loopLabel(t *dsl.LoopStmt) string {
	if t.Var != "" {
		return "LOOP " + t.Var
	}
	return "LOOP"
}
