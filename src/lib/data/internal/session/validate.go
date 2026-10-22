// 会话域写侧字段校验（INPUTVAL-1 P0-4）。
//
// 口径：只加在写侧（读侧保持既有兜底，兼容存量脏数据）；失败返回明确错误（不截断、
// 不静默丢弃）。字段基线见 docs/input-validation/输入校验专题.md §3。
package session

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 字段上限（与 config 包同源基线：标题 128 / 正文 256 KB）。
const (
	maxSessionTitleChars = 128
	maxMessageChars      = 262144 // 256 KB 字符（提示词 / 正文 / 记忆内容同口径）
)

// validateSessionTitle 会话标题：空允许（= 未命名），≤128 字符，不含控制字符。
func validateSessionTitle(title string) error {
	if utf8.RuneCountInString(title) > maxSessionTitleChars {
		return fmt.Errorf("session: title 超长（上限 %d 字符）", maxSessionTitleChars)
	}
	for _, r := range title {
		if unicode.IsControl(r) && r != '\t' {
			return fmt.Errorf("session: title 含非法控制字符")
		}
	}
	return nil
}

// turnStatusValid 判定轮次状态是否属既定集合（running / done / complete / incomplete /
// error / interrupted / cancelled）。
func turnStatusValid(st string) bool {
	switch st {
	case "running", "done", "complete", "incomplete", "error", "interrupted", "cancelled":
		return true
	}
	return false
}

// messageRoleValid 判定消息角色（system / user / assistant / tool）。
func messageRoleValid(role string) bool {
	switch role {
	case "system", "user", "assistant", "tool":
		return true
	}
	return false
}

// validateMessageContent 消息正文：role=tool 的载荷结构由内核编码（允许更长，但同样设上限），
// 其余 ≤ 256 KB 字符。
func validateMessageContent(role, content string) error {
	if utf8.RuneCountInString(content) > maxMessageChars {
		return fmt.Errorf("session: message content 超长（上限 %d 字符）", maxMessageChars)
	}
	return nil
}

// validateTurnID 轮次 / 会话 id：非空且不含空白（防手填脏 id 破坏索引键）。
func validateTurnID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("session: turn_id required")
	}
	if strings.ContainsAny(id, " \t\r\n") {
		return fmt.Errorf("session: turn_id 不得包含空白")
	}
	if utf8.RuneCountInString(id) > 128 {
		return fmt.Errorf("session: turn_id 超长（上限 128 字符）")
	}
	return nil
}
