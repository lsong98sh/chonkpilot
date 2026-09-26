// project-id：项目库 config 表内的 uuid，用于绑定 prjusr 数据目录
// （12-数据层）。目录移动/改名后随项目库一起走，prjusr 数据仍可找回。
package data

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// ProjectIDKey 是项目库 config 表中 project-id 的键名。
const ProjectIDKey = "project-id"

// ReadProjectID 读项目库的 project-id（不存在 → ok=false）。
func ReadProjectID(db *DB) (string, bool) {
	return GetConfig(db, ProjectIDKey)
}

// EnsureProjectID 读 project-id；不存在则生成 uuid 写入并返回（幂等，仅首次生成）。
func EnsureProjectID(db *DB) (string, error) {
	if id, ok := ReadProjectID(db); ok && id != "" {
		return id, nil
	}
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	if err := SetConfig(db, ProjectIDKey, id); err != nil {
		return "", err
	}
	return id, nil
}

// newUUID 生成 uuid v4 文本（crypto/rand；无外部依赖）。
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("data: gen uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
