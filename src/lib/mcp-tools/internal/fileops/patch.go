package fileops

import (
	"fmt"
	"strings"
)

// applyUnifiedDiff 应用 unified diff（支持多个 @@ hunk，按 -start 定位 + context/-/+ 逐行消费）。
// 保留源文本的文件尾换行（无 hunk 覆盖的尾部换行不被 join 吞掉）。
func applyUnifiedDiff(src, diffContent string) (string, error) {
	diffLines := strings.Split(diffContent, "\n")
	srcLines := strings.Split(src, "\n")
	hadTrailing := len(srcLines) > 0 && srcLines[len(srcLines)-1] == ""
	if hadTrailing {
		srcLines = srcLines[:len(srcLines)-1]
	}
	var out []string
	srcIdx := 0
	matchedAny := false
	for _, dl := range diffLines {
		dl = strings.TrimSuffix(dl, "\r")
		switch {
		case strings.HasPrefix(dl, "@@"):
			var oldStart int
			if _, err := fmt.Sscanf(dl, "@@ -%d", &oldStart); err == nil && oldStart > 0 {
				srcIdx = oldStart - 1
			}
			matchedAny = true
		case strings.HasPrefix(dl, "---") || strings.HasPrefix(dl, "+++"):
			// 文件头，忽略
		case strings.HasPrefix(dl, "+"):
			out = append(out, dl[1:])
		case strings.HasPrefix(dl, "-"):
			srcIdx++ // 跳过被删行
		default:
			// context 行（' ' 前缀或空行）
			if srcIdx < len(srcLines) {
				out = append(out, srcLines[srcIdx])
				srcIdx++
			}
		}
	}
	if srcIdx < len(srcLines) {
		out = append(out, srcLines[srcIdx:]...)
	}
	if !matchedAny {
		return "", fmt.Errorf("no hunks in diff")
	}
	result := strings.Join(out, "\n")
	if hadTrailing && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}
