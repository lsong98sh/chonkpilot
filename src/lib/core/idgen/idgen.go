// Package idgen 提供跨入口/业务复用的随机标识生成（B-13：llm server 与 httpapi 的
// newUUID 重复实现合并到此单一实现，避免各处复制粘贴导致行为漂移）。
package idgen

import (
	"crypto/rand"
	"fmt"
)

// NewUUID 生成 RFC 4122 v4 风格 UUID（crypto/rand，不引入额外依赖）。
//
// 无 rand 失败回退分支：Go 1.24+ 起 crypto/rand 不会返回错误（失败直接 panic），
// 原各处 `if _, err := rand.Read(buf); err != nil { ... }` 回退为**死分支**，已删除。
func NewUUID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
