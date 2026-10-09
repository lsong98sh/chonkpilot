//go:build windows

package winlog

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// truncateEventMsg 按字节上限截断，且不切断多字节 UTF-8（B-43）。
func TestTruncateEventMsg(t *testing.T) {
	// 未超限 → 原样返回。
	if got := truncateEventMsg("abc", 30000); got != "abc" {
		t.Fatalf("未超限应原样: %q", got)
	}
	// max<=0 → 不截断。
	msg := strings.Repeat("中", 20000)
	if got := truncateEventMsg(msg, 0); got != msg {
		t.Fatalf("max<=0 应不截断")
	}
	// 前缀 1 字节使 30000 字节落在「中」字内部 → 须回退到 rune 边界：结果合法 UTF-8 且不超上限。
	mixed := "a" + msg
	got := truncateEventMsg(mixed, 30000)
	if len(got) > 30000 {
		t.Fatalf("截断后超上限: %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("截断切断了多字节序列（非法 UTF-8）")
	}
	if len(got) < 30000-3 {
		t.Fatalf("回退超过一个 rune: %d", len(got))
	}
}
