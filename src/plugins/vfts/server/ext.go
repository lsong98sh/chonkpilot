package server

import (
	"strings"
)

// defaultExts 默认参与索引的扩展名（含点、小写）：源码 + 纯文本（.txt / .md）。
// 可用 vfts_configure / vfts_index 的 exts 参数整体替换。
func defaultExts() []string {
	return []string{
		// Go
		".go",
		// JS / TS / Vue
		".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".vue",
		// Python
		".py", ".pyw",
		// Rust / Java / Kotlin / C 系 / C# / PHP / Ruby
		".rs", ".java", ".kt", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".php", ".rb",
		// 脚本 / 查询 / 标记
		".sh", ".ps1", ".bat", ".sql", ".html", ".css", ".scss",
		".json", ".yaml", ".yml", ".toml", ".xml",
		// 纯文本
		".txt", ".md",
	}
}

// newExtSet 扩展名集合（小写归一）。
func newExtSet(exts []string) map[string]bool {
	m := make(map[string]bool, len(exts))
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		m[e] = true
	}
	return m
}

// isBinary 粗判二进制：前 8KB 出现 NUL 字节即视为二进制（跳过不索引）。
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// normalizeText 统一换行为 '\n'（避免 CRLF 影响行号与片段）。
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
