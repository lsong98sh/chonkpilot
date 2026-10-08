// protocol.go — chonkpilot-dsl-executor 与 gateway 之间的 stdio 协议（行分隔 JSON）。
//
// 传输 = 行分隔 JSON：每条消息一行 UTF-8，字符串内的换行由 JSON 转义承担。stdout 只出协议
// 行（llm_call / tree / step / result / log）；进程级诊断错误走 stderr。一作业一进程。
//
// 下行（gateway → 本进程 stdin）：
//
//	{"t":"run","job":"<jobid>","script":"<DSL>","file":"<脚本文件绝对路径，script 为空时>",
//	 "instance":"<instanceID>","work_dir":"...","data_dir":"...","return_file":"<绝对路径>"}
//	{"t":"llm_result","call":"<callid>","text":"<结果文本>","error":"<错误或空>"}
//
// 上行（本进程 stdout → gateway）：
//
//	{"t":"llm_call","call":"<callid>","agent":"...","prompt":"...","purpose":"...","session":"<jobid>-N"}
//	{"t":"tree","job":"<jobid>","nodes":[{"id":"...","parent":"","kind":"dsl_loop|dsl_parallel","label":"...","loop_current":N,"loop_total":N}]}
//	{"t":"step","job":"<jobid>","no":N,"status":"running|done|error|cancelled","purpose":"...","elapsed_ms":N,"session":"<sid>","statement":"<静态语句 id 或空>"}
//	{"t":"result","job":"<jobid>","ok":true,"text":"...","file":"","size":0,"error":"","overflow":false,"return_used":false}
//	{"t":"log","level":"info","msg":"..."}
//
// DSL 展示（DSL-3）：建引擎前预走 AST 产**静态容器节点**（tree，各建一次、不随迭代增长）；
// 每个 LLM 步骤开始/结束发 step（含所属静态语句 id）；容器进度变化重发 tree（**同 id → 只更新
// loop_current，不新增节点**）。gateway 据此转成既有 task-* 事件（经任务层单写者落库）。
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// inMessage 是**下行**协议消息（run / llm_result 共用；字段并集，按 t 区分）。
// 字段顺序仅影响编码（本结构只解码），保持与规范一致的命名。
type inMessage struct {
	T          string `json:"t"`
	Job        string `json:"job"`
	Script     string `json:"script"`
	File       string `json:"file"` // 脚本文件绝对路径（script 为空时；执行器受沙箱读盘）
	Instance   string `json:"instance"`
	WorkDir    string `json:"work_dir"`
	DataDir    string `json:"data_dir"`
	ReturnFile string `json:"return_file"`
	Call       string `json:"call"`
	Text       string `json:"text"`
	Error      string `json:"error"`
}

// llmCallMsg 是**上行** llm_call（执行器 → gateway；请求执行一次 LLM 子轮次）。
type llmCallMsg struct {
	T       string `json:"t"` // "llm_call"
	Call    string `json:"call"`
	Agent   string `json:"agent"`
	Prompt  string `json:"prompt"`
	Purpose string `json:"purpose"`
	Session string `json:"session"`
}

// resultMsg 是**上行** result（执行器 → gateway；作业最终结果）。
type resultMsg struct {
	T        string `json:"t"` // "result"
	Job      string `json:"job"`
	OK       bool   `json:"ok"`
	Text     string `json:"text"`
	File     string `json:"file"`
	Size     int    `json:"size"`
	Error    string `json:"error"`
	Overflow bool   `json:"overflow"`
	// ReturnUsed = 脚本是否使用了 `$RETURN` 通道：false → text/file 为**回落汇总**（非返回值），
	// gateway 据此**不写** return_*（DSL-2 两态语义；与 llm_run 的 markReturn 同口径）。
	ReturnUsed bool `json:"return_used"`
}

// treeNode 是上行 tree 的一个**静态容器节点**（作业根由 gateway 建，不在此列）。
type treeNode struct {
	ID          string `json:"id"`           // 执行器内静态节点 id（容器：c<N>）
	Parent      string `json:"parent"`       // 父容器 id；空 = 直接挂作业根
	Kind        string `json:"kind"`         // dsl_loop / dsl_parallel
	Label       string `json:"label"`        // 展示名（LOOP <var> / PARALLEL）
	LoopCurrent int    `json:"loop_current"` // 当前轮次（1 起；0 = 未开始）
	LoopTotal   int    `json:"loop_total"`   // 总轮数（未知 = 0 缺省，不臆造）
}

// treeMsg 是**上行** tree（执行器 → gateway；静态容器树全量快照，重发即更新 loop_current）。
type treeMsg struct {
	T     string     `json:"t"` // "tree"
	Job   string     `json:"job"`
	Nodes []treeNode `json:"nodes"`
}

// stepMsg 是**上行** step（执行器 → gateway；一次 LLM 步骤的开始/结束进度）。
type stepMsg struct {
	T         string `json:"t"` // "step"
	Job       string `json:"job"`
	No        int    `json:"no"`         // 跨迭代累计序号（1 起）
	Status    string `json:"status"`     // running / done / error / cancelled
	Purpose   string `json:"purpose"`    // 该步运行目的（展示名）
	ElapsedMs int64  `json:"elapsed_ms"` // 耗时（毫秒；running = 0）
	Session   string `json:"session"`    // 该步子会话 id（<job>-N）
	Statement string `json:"statement"`  // 所属静态语句 id（无 = 空）
}

// logMsg 是**上行** log（结构化日志，属协议行）。
type logMsg struct {
	T     string `json:"t"` // "log"
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// lineWriter 串行化协议行写入：PARALLEL/并发 LOOP 期间多条分支可能并发写 llm_call，
// stdin 读协程也可能并发写 log，故用互斥锁保证「一条消息一行、不交错」。
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newLineWriter(w io.Writer) *lineWriter { return &lineWriter{w: w} }

// send 编码 v 为单行 JSON 并写入（json.Marshal 不产生裸换行，字符串内换行已转义）。
func (lw *lineWriter) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	lw.mu.Lock()
	_, _ = lw.w.Write(b)
	lw.mu.Unlock()
}

// sendLLMCall 发 llm_call（t 由本方法补全，调用方无需设置）。
func (lw *lineWriter) sendLLMCall(m llmCallMsg) {
	m.T = "llm_call"
	lw.send(m)
}

// sendResult 发 result（t 由本方法补全）。
func (lw *lineWriter) sendResult(m resultMsg) {
	m.T = "result"
	lw.send(m)
}

// sendTree 发 tree（t 由本方法补全；静态容器树全量快照）。
func (lw *lineWriter) sendTree(job string, nodes []treeNode) {
	lw.send(treeMsg{T: "tree", Job: job, Nodes: nodes})
}

// sendStep 发 step（t 由本方法补全；一次 LLM 步骤的进度）。
func (lw *lineWriter) sendStep(m stepMsg) {
	m.T = "step"
	lw.send(m)
}

// logf 发一条 log 协议行（level=info/warn/error）。
func (lw *lineWriter) logf(level, format string, args ...any) {
	lw.send(logMsg{T: "log", Level: level, Msg: fmt.Sprintf(format, args...)})
}
