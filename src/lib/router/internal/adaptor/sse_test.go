// LR-5 白盒：SSE 行解析共享件（sse.go）—— 载荷重组、注释/心跳跳过、跨 chunk 半包、CRLF、
// `[DONE]`、流中断。
package adaptor

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// decodeAll 读空解码器，返回载荷序列；正常结束 → (payloads, nil)，异常 → (已得载荷, err)。
func decodeAll(t *testing.T, r io.Reader) ([]string, error) {
	t.Helper()
	dec := NewSSEDecoder(r)
	var got []string
	for {
		p, err := dec.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return got, nil
			}
			return got, err
		}
		got = append(got, string(p))
	}
}

// chunkReader 严格按给定分片逐片返回（验证与上游 chunk 切分位置无关）。
type chunkReader struct {
	chunks []string
	i      int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for c.i < len(c.chunks) && len(c.chunks[c.i]) == 0 {
		c.i++
	}
	if c.i >= len(c.chunks) {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[c.i])
	c.chunks[c.i] = c.chunks[c.i][n:]
	return n, nil
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("载荷条数=%d want %d\ngot=%q\nwant=%q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("载荷[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

// TestSSEDecoderMultiLineData：一条事件的多个 `data:` 行按规范以 `\n` 连接。
func TestSSEDecoderMultiLineData(t *testing.T) {
	got, err := decodeAll(t, strings.NewReader("data: a\ndata: b\n\n"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	eq(t, got, []string{"a\nb"})
}

// TestSSEDecoderSkipsCommentsHeartbeatAndEventOnly：注释行 / 心跳（空行）/ 仅 `event:` 行不产生载荷。
func TestSSEDecoderSkipsCommentsHeartbeatAndEventOnly(t *testing.T) {
	in := ":comment\n\n" + "event: ping\n\n" + "data: x\n\n" + ":trailing\n"
	got, err := decodeAll(t, strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	eq(t, got, []string{"x"})
}

// TestSSEDecoderLeadingWhitespace：行首空白容忍（兼容端点偶发 `  data: …` / 空白终结行）。
func TestSSEDecoderLeadingWhitespace(t *testing.T) {
	got, err := decodeAll(t, strings.NewReader("  data: x\n \n\tevent: ping\n\n"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	eq(t, got, []string{"x"})
}

// TestSSEDecoderCRLF：CRLF 行尾正确剥离（含终结空行）。
func TestSSEDecoderCRLF(t *testing.T) {
	got, err := decodeAll(t, strings.NewReader("data: x\r\n\r\ndata: [DONE]\r\n\r\n"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	eq(t, got, []string{"x", "[DONE]"})
}

// TestSSEDecoderChunkBoundaryIndependence：同一素材按「逐字节」与「按原分片」两种切分解析结果一致
// （覆盖半包：行被切开、终止空行被切开）。
func TestSSEDecoderChunkBoundaryIndependence(t *testing.T) {
	chunks := []string{
		"data: {\"a\":1}\n",
		"\ndata: {\"b",
		"\":2}\n\n",
		"data: [DONE]\n\n",
	}
	want := []string{`{"a":1}`, `{"b":2}`, "[DONE]"}

	byChunk, err := decodeAll(t, &chunkReader{chunks: append([]string(nil), chunks...)})
	if err != nil {
		t.Fatalf("按分片 decode: %v", err)
	}
	eq(t, byChunk, want)

	byByte, err := decodeAll(t, iotest.OneByteReader(strings.NewReader(strings.Join(chunks, ""))))
	if err != nil {
		t.Fatalf("逐字节 decode: %v", err)
	}
	eq(t, byByte, want)
}

// TestSSEDecoderTruncated：事件累积了 data 行却缺终结空行即断流 → ErrTruncated（半截事件不静默吞掉）。
func TestSSEDecoderTruncated(t *testing.T) {
	got, err := decodeAll(t, strings.NewReader("data: ok\n\ndata: cut"))
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err=%v want ErrTruncated", err)
	}
	eq(t, got, []string{"ok"})
}

// TestSSEDecoderCleanEOF：以空行正常收尾 → io.EOF（非 ErrTruncated）。
func TestSSEDecoderCleanEOF(t *testing.T) {
	got, err := decodeAll(t, strings.NewReader("data: x\n\n"))
	if err != nil {
		t.Fatalf("err=%v want nil(EOF)", err)
	}
	eq(t, got, []string{"x"})
}

// TestSSEDecoderEmptyStream：空流 → 直接 EOF、零载荷。
func TestSSEDecoderEmptyStream(t *testing.T) {
	got, err := decodeAll(t, strings.NewReader(""))
	if err != nil {
		t.Fatalf("err=%v want nil(EOF)", err)
	}
	eq(t, got, nil)
}

// TestIsDone：`[DONE]` 识别（含首尾空白容忍），其它载荷不误判。
func TestIsDone(t *testing.T) {
	if !IsDone([]byte("[DONE]")) || !IsDone([]byte("  [DONE]\n")) {
		t.Fatal("IsDone 应识别 [DONE]")
	}
	if IsDone([]byte("[DONE]x")) || IsDone([]byte("done")) || IsDone(nil) {
		t.Fatal("IsDone 不应误判")
	}
}
