package fileops

import (
	"fmt"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// maxDiffBytes 是 DIFF 单侧文件尺寸上限（8MB）：diffmatchpatch 为 O(n²) 量级，
// 超限直接报错而非尝试全量 diff，防大文件 OOM（R7-C-3）。
const maxDiffBytes = 8 << 20

// renderUnifiedDiff 生成简化的统一 diff 文本（- / + / 空格前缀行）。
func renderUnifiedDiff(a, b string, diffs []diffmatchpatch.Diff) string {
	var bd strings.Builder
	fmt.Fprintf(&bd, "--- %s\n+++ %s\n", a, b)
	for _, d := range diffs {
		lines := strings.Split(d.Text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for _, ln := range lines {
			switch d.Type {
			case diffmatchpatch.DiffDelete:
				bd.WriteString("-" + ln + "\n")
			case diffmatchpatch.DiffInsert:
				bd.WriteString("+" + ln + "\n")
			default:
				bd.WriteString(" " + ln + "\n")
			}
		}
	}
	return bd.String()
}
