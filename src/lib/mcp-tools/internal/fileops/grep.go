package fileops

import (
	"path/filepath"
	"strings"
)

// globMatch 判断 name 是否匹配一个或多个逗号/竖线分隔的 glob。
func globMatch(name, pattern string) bool {
	if pattern == "" {
		return true
	}
	for _, p := range strings.FieldsFunc(pattern, func(r rune) bool { return r == ',' || r == '|' }) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}
