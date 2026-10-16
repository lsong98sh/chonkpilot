package fileops

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCountLinesConsistency 验证 countLines / countFileLines 与 readContent 同语义：
// 仅去掉**一个**尾换行（结尾多个空行各计一行），消除旧实现 TrimRight 去全部尾换行造成的矛盾。
func TestCountLinesConsistency(t *testing.T) {
	cases := []struct {
		content string
		want    int
	}{
		{"", 0},
		{"\n", 1},
		{"\n\n", 2},
		{"\n\n\n", 3},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"a\nb\n\n", 3},
		{"a\nb\n\n\n", 4},
	}
	for _, tc := range cases {
		if got := countLines([]byte(tc.content)); got != tc.want {
			t.Errorf("countLines(%q) = %d, want %d", tc.content, got, tc.want)
		}
		p := tmpFile(t, "c.txt", tc.content)
		got, err := countFileLines(p)
		if err != nil {
			t.Fatalf("countFileLines(%q): %v", tc.content, err)
		}
		if got != tc.want {
			t.Errorf("countFileLines(%q) = %d, want %d", tc.content, got, tc.want)
		}
	}
}

// TestReadLargeFileSemantics 覆盖 >32MB 文件的 total_lines、ranges/start 报错、tail+line_numbers 行号基线。
// 复用 readOne（READ 动作的核心读取逻辑）。
func TestReadLargeFileSemantics(t *testing.T) {
	// 每行 64 字节（63 个 'x' + '\n'）→ 540000 行 ≈ 34.5MB（> maxReadBytes=32MB）。
	const lineCount = 540000
	// 已读前缀 = 32MB = 64 * 524288 → 恰 524288 个完整行。
	const prefixLines = (32 << 20) / 64
	path := writeBigFile(t, lineCount, 64)

	t.Run("info 给 total_lines 与截断前缀行数", func(t *testing.T) {
		info, err := readOne(readReq{path: path, infoOnly: true}, "")
		if err != nil {
			t.Fatalf("readOne(info): %v", err)
		}
		if info["truncated"] != true {
			t.Fatalf("应 truncated=true, got %v", info["truncated"])
		}
		if got := info["total_lines"]; got != lineCount {
			t.Fatalf("total_lines = %v, want %d", got, lineCount)
		}
		if got := info["lines"]; got != prefixLines {
			t.Fatalf("lines（前缀）= %v, want %d", got, prefixLines)
		}
	})

	t.Run("ranges 报错", func(t *testing.T) {
		_, err := readOne(readReq{path: path, ranges: [][2]int{{1, 3}}}, "")
		if err == nil || !strings.Contains(err.Error(), "ranges/start") {
			t.Fatalf(">32MB 文件 ranges 应报错且提示 ranges/start, got %v", err)
		}
	})

	t.Run("start 报错", func(t *testing.T) {
		_, err := readOne(readReq{path: path, start: 5}, "")
		if err == nil || !strings.Contains(err.Error(), "ranges/start") {
			t.Fatalf(">32MB 文件 start 应报错且提示 ranges/start, got %v", err)
		}
	})

	t.Run("limit 取前缀仍可读", func(t *testing.T) {
		info, err := readOne(readReq{path: path, limit: 3}, "")
		if err != nil {
			t.Fatalf("readOne(limit): %v", err)
		}
		if got := strings.Count(asStr(info["content"]), "\n") + 1; got != 3 {
			t.Fatalf("limit=3 应返回 3 行, got %d（content=%q）", got, info["content"])
		}
	})

	t.Run("tail+line_numbers 用全文行号基线", func(t *testing.T) {
		info, err := readOne(readReq{path: path, tail: 3, lineNumbers: true}, "")
		if err != nil {
			t.Fatalf("readOne(tail): %v", err)
		}
		content := asStr(info["content"])
		// 末尾 3 行 → 全文行号 539998..540000
		for _, want := range []string{"539998", "539999", "540000"} {
			if !strings.Contains(content, want) {
				t.Fatalf("行号 %s 缺失, content=%q", want, content)
			}
		}
	})
}

// TestReadFileTailDropsPartialHead 验证反向扫描窗口耗尽（>maxTailScan）时，buf 首行是半行 → 丢弃，
// 不把截断半行当成「末尾某行」返回（旧实现会返回被截断的半行）。
func TestReadFileTailDropsPartialHead(t *testing.T) {
	// 9MB 单行、无换行：反向扫描在 maxTailScan(8MB) 停止，buf 首行必为半行 → 应返回空。
	p := filepath.Join(t.TempDir(), "oneline.txt")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.WriteString(strings.Repeat("A", 9<<20)); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := readFileTail(p, 5)
	if err != nil {
		t.Fatalf("readFileTail: %v", err)
	}
	if got != "" {
		t.Fatalf("窗口耗尽时不应返回截断半行, got len=%d", len(got))
	}
}

// ─── 测试辅助 ───

// writeBigFile 写一个每行 lineBytes 字节（含 '\n'）的大文件，返回绝对路径。
func writeBigFile(t *testing.T, lineCount, lineBytes int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "big.txt")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := strings.Repeat("x", lineBytes-1) + "\n"
	w := bufio.NewWriterSize(f, 1<<20)
	for i := 0; i < lineCount; i++ {
		if _, err := w.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return p
}

// asStr 把 interface{} 断言为 string（非字符串返回空）。
func asStr(v interface{}) string {
	s, _ := v.(string)
	return s
}
