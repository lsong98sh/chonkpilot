// Package dsl 实现 ChonkPilot DSL 核心（见 docs/dsl-core.md）。
//
// 语法核心固定（SET/IF/LOOP/PARALLEL/BREAK/CONTINUE/EXIT/END 为保留字），
// 动作动词（LLM/CLK/OPN...）由消费方经 Action 注册表注入；I/O（文件/库句柄）
// 由消费方经 Options 注入（测试使用内存 Mock）。
package dsl

import (
	"errors"
	"fmt"
)

// 流控信号错误：动作可返回它们穿透到引擎。
var (
	// ErrBreak 跳出最近一层 LOOP（仅本执行 goroutine）。
	ErrBreak = errors.New("break")
	// ErrContinue 跳到最近一层 LOOP 的下一次迭代。
	ErrContinue = errors.New("continue")
	// ErrExit 立即终止整个脚本（跨 goroutine）。
	ErrExit = errors.New("exit")
)

// LineError 是带行号的语法/语义错误。
type LineError struct {
	Line int
	Msg  string
}

func (e *LineError) Error() string {
	return fmt.Sprintf("第 %d 行: %s", e.Line, e.Msg)
}

func lineErr(line int, format string, args ...any) error {
	return &LineError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

// RunError 是运行时单步错误（记错不阻塞）。
type RunError struct {
	Line int
	Msg  string
}

// MaxDepth 是块嵌套深度上限（LOOP/IF/PARALLEL 均计入）。
const MaxDepth = 8

// MaxLoopConcurrency 是有参 LOOP 的 `concurrency=N` 上限（B-39：防单个输入派生海量
// goroutine；与 llm 侧 maxActiveTasks=50 / maxTaskNodes=200 同口径的洪泛防护）。
// 超限在**语法期**报错，不进入执行。
const MaxLoopConcurrency = 64
