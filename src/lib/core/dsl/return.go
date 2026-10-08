// return.go — `$RETURN` 结果通道（决策 42 §2 (249)；语法扩展，见 63-DSL语法）。
//
// `$RETURN` 是宿主注入的**只写保留变量**（与只读的 `env` 相对）：脚本用
// `SET 值 => $RETURN` 或 `动作 … => $RETURN` 把内容**累计追加**进去（非覆盖），
// 作为脚本的结构化产出通道，取代「宿主另设约定汇总」的做法。
//
// 两态（防大循环撑爆内存）：累计超过 ReturnInlineLimit 字节即转**文件流式追加**
// （落盘路径由宿主经 Options.ReturnFile 注入，通常为 `!/` 临时根下的
// `dsl-return-<作业id>.md`）——此时返回文件名而非内容。
//
// 只写不可读：脚本内读取 `$RETURN`（`{{$RETURN}}` / `SET $RETURN => x`）一律报错，
// 避免「读 + 累计」语义自相缠绕。
package dsl

import (
	"strings"
	"sync"
)

// returnVar 是 `$RETURN` 保留目标名（宿主注入的只写结果通道）。
const returnVar = "$RETURN"

// ReturnInlineLimit 是 `$RETURN` 的**内联上限（字节）**：累计超过即转文件流式追加
// （决策 42 §2 (249)：阈值先固定常量、不进配置）。
const ReturnInlineLimit = 65536

// returnSegSep 是 `$RETURN` 段间分隔符（累计追加时非首段前置换行）。
const returnSegSep = "\n"

// returnNoRead 是读取 `$RETURN` 的统一错误文案。
const returnNoRead = "$RETURN 是宿主注入的只写结果通道，不能读取（唯一用法：SET 值 => $RETURN）"

// ReturnValue 报告 `$RETURN` 的最终结果（两态）。
type ReturnValue struct {
	Used     bool   // 脚本中是否出现过 => $RETURN
	Overflow bool   // 是否已超阈值转文件（true = Text 为空、看 File）
	Text     string // !Overflow 时的累计内容（inline 态）
	File     string // Overflow 时的落盘路径（file 态）
	Size     int    // 累计总字节
}

// returnState 是 `$RETURN` 的累计状态（并发安全：PARALLEL 分支可能同时写入）。
type returnState struct {
	mu       sync.Mutex
	used     bool
	overflow bool
	file     string
	size     int
	buf      strings.Builder
}

// append 累计一段文本：未超阈值时留在内存；超过则先把已有内容 flush 到文件、
// 之后转为直接向文件流式追加（不等脚本结束再判）。
//   - files：宿主注入的文件系统（用于落盘）；
//   - file：落盘目标路径（空串 = 无落盘目标，仅在内存累计，供测试/向后兼容）。
func (r *returnState) append(text string, files FileSystem, file string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.used {
		text = returnSegSep + text // 段间换行分隔（首段无前缀）
	}
	r.used = true
	r.size += len(text)
	if r.overflow {
		return files.Open(file).Append(text)
	}
	if r.size <= ReturnInlineLimit || file == "" {
		// 未超阈值（或无落盘目标）→ 继续内存累计。
		r.buf.WriteString(text)
		return nil
	}
	// 首超阈值：把已累计内容整写文件，随后转流式追加。
	if err := files.Open(file).WriteAll(r.buf.String()); err != nil {
		return err
	}
	r.buf.Reset()
	r.overflow = true
	r.file = file
	return files.Open(file).Append(text)
}

// snapshot 取当前两态结果。
func (r *returnState) snapshot() ReturnValue {
	r.mu.Lock()
	defer r.mu.Unlock()
	rv := ReturnValue{Used: r.used, Overflow: r.overflow, File: r.file, Size: r.size}
	if !r.overflow {
		rv.Text = r.buf.String()
	}
	return rv
}
