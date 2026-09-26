// SSE 行解析共享件（LR-5）—— 三协议适配器共用，纯标准库实现。
//
// 覆盖（见 docs/spec/40-roadmap/40-演进计划.md §LR-5）：
//   - `data:` 前缀解析（含冒号后单个空格的规范剥离）；
//   - 一条事件的多个 `data:` 行按 SSE 规范以 `\n` 连接后重组；
//   - `:` 注释行 / 心跳 / 仅 `event:` 行 → 不产生载荷（跳过）；
//   - **行首空白容忍**（兼容端点偶发 `  data: …`）→ 剥离后再判字段；
//   - **跨 chunk 边界的半包**（含终止空行被切开）→ 内部按需读取、自动缓冲；
//   - `[DONE]` 终止载荷识别；
//   - **流中断**（事件未以空行终结即断流）→ 明确报错，不静默吞掉半截事件。
package adaptor

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// DonePayload 是 OpenAI 兼容流的终止载荷（Anthropic 无该标记，以 `message_stop` 语义收尾 → 由 LR-4 适配器映射）。
const DonePayload = "[DONE]"

// ErrTruncated 表示流在**事件边界前**中断（累积了 data 行却没有终结空行）。
// 消费方据此归类为 network / protocol 错误，而不是把半截事件当完整事件用。
var ErrTruncated = errors.New("sse: 流在事件边界前中断")

// ErrStreamDone 是 `onPayload` 可返回的**正常收尾信号**：载荷本身已表达流结束（OpenAI 家族的
// `[DONE]`、Responses 的终态事件、Anthropic 的 `message_stop`）→ 由 `Exchange` 立即收尾并返回
// 成功，**不再等待上游关闭连接 / EOF**。
//
// 必要性：部分端点（含本仓 L4 的 `mock_llm.py` 等测试桩）在终止标记后**不关闭** HTTP/1.1
// keep-alive 连接 → 若继续等 EOF 会一直挂到 `streamTimeout` 才报超时（旧 llm 侧按行解析、
// 命中终止标记即收尾，故为行为回归）。
var ErrStreamDone = errors.New("sse: 流已按终止标记正常收尾")

// SSEDecoder 把任意切分的 SSE 字节流解析为事件载荷序列。
//
// 设计要点：内部基于 bufio.Reader 按需读取 → **与上游 chunk 切分位置无关**（半包自动缓冲）。
type SSEDecoder struct {
	br   *bufio.Reader
	data []string // 当前事件的 data 行（多行按顺序累积，终结时以 \n 连接）
}

// NewSSEDecoder 构造解码器。
func NewSSEDecoder(r io.Reader) *SSEDecoder {
	return &SSEDecoder{br: bufio.NewReader(r)}
}

// Next 返回下一条事件的 data 载荷（已剥 `data:` 前缀与行尾 CR/LF）：
//
//   - (payload, nil)      一条事件（多 `data:` 行以 `\n` 连接）
//   - (nil, io.EOF)       流正常结束（无待发事件）
//   - (nil, ErrTruncated) 流在事件未终结（缺空行）处中断
func (d *SSEDecoder) Next() ([]byte, error) {
	for {
		line, eof, err := d.readLine()
		if err != nil {
			return nil, err
		}
		if eof {
			// 末行无换行：若仍累积着 data → 事件未终结（流中断）。
			if line != "" {
				d.consumeLine(line)
			}
			if len(d.data) > 0 {
				return nil, ErrTruncated
			}
			return nil, io.EOF
		}
		if d.consumeLine(line) { // 空行 = 事件终结
			payload := strings.Join(d.data, "\n")
			d.data = nil
			return []byte(payload), nil
		}
	}
}

// readLine 读取下一行（已剥行尾 CR/LF）；eof=true 表示已到流末（此时 line 可能为无换行的末行）。
func (d *SSEDecoder) readLine() (line string, eof bool, err error) {
	s, err := d.br.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return trimEOL(s), true, nil
		}
		return trimEOL(s), false, err
	}
	return trimEOL(s), false, nil
}

// consumeLine 处理一行，返回 true 表示该行是「事件终结空行」且当前事件含 data（可分发）。
//
// 行首空白（`  data: …`）由兼容端点偶发 → 先剥离（对齐 llm 侧既有 TrimSpace 口径）；
// 无 data 字段的空行（心跳 / 仅注释）→ 返回 false，继续读。
func (d *SSEDecoder) consumeLine(line string) bool {
	line = strings.TrimLeft(line, " \t")
	if line == "" {
		return len(d.data) > 0
	}
	if strings.HasPrefix(line, ":") {
		return false // 注释行
	}
	field, value := splitField(line)
	if field == "data" {
		d.data = append(d.data, value)
	}
	return false
}

// splitField 按 SSE 规范拆一行：首个 `:` 之前为字段名，之后为值（值前单个空格剥离）。
// 无 `:` 时字段名 = 整行、值 = 空串。
func splitField(line string) (field, value string) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return line, ""
	}
	field = line[:i]
	value = line[i+1:]
	value = strings.TrimPrefix(value, " ")
	return field, value
}

// trimEOL 剥离行尾的 LF（\n）及其前置 CR（\r\n）。
func trimEOL(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

// IsDone 判定 data 载荷是否为终止标记 `[DONE]`（去首尾空白后逐字比较）。
func IsDone(payload []byte) bool {
	return strings.TrimSpace(string(payload)) == DonePayload
}
