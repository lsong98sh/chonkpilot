package pluginbridge

import "strings"

// matchSubject 判定 subject 是否命中 pattern（`>` 任意剩余多段 / `*` 恰好一段）。
func matchSubject(pattern, subject string) bool {
	return matchTokens(split(pattern), split(subject))
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ".")
}

func matchTokens(pattern, subject []string) bool {
	if len(pattern) == 0 {
		return len(subject) == 0
	}
	switch pattern[0] {
	case ">":
		return true
	case "*":
		return len(subject) > 0 && matchTokens(pattern[1:], subject[1:])
	default:
		return len(subject) > 0 && pattern[0] == subject[0] && matchTokens(pattern[1:], subject[1:])
	}
}
