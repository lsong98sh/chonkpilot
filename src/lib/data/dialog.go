// dialog.go —— 对话库文件的命名规则（一树一库，SESSION-COMP-1）。
//
// 单一定义点：库文件名 `dialog-<hex(dialog_id)>.db` 与 dialog_id 的生成 / 反解只在本文件；
// 内部（`internal/sessionstore`）与组件（`chonkpilot-session/component`）都复用本文件的导出函数，
// 路径规则仍唯一由 [DialogDBPath] 负责（本文件只给文件名，不含目录）。
package data

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// DialogFileName 返回 dialog_id 对应的库文件名（不含目录）：`dialog-<hex(dialog_id)>.db`。
func DialogFileName(dialogID string) string {
	return "dialog-" + hex.EncodeToString([]byte(dialogID)) + ".db"
}

// DialogIDFromFile 从库文件名反解 dialog_id（不匹配 → ok=false）。
func DialogIDFromFile(name string) (string, bool) {
	const prefix, suffix = "dialog-", ".db"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return "", false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// OpenExistingDialog 打开**已存在**的对话库（不创建新文件；缺失 → 错误）。
//
// 用于 P3 读路径切换：调用方带了 `dialog_id` 却找不到库 = 状态异常（半删除 / 传错 id），
// 必须报错而不是静默新建空库（2G §3.3：不回退 prjusr）。
func OpenExistingDialog(prjUsrDBPath, dialogID string) (*DB, func(), error) {
	if dialogID == "" {
		return nil, nil, fmt.Errorf("data: dialog_id required")
	}
	path := DialogDBPath(prjUsrDBPath, dialogID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("data: 对话库不存在（dialog_id=%s）", dialogID)
		}
		return nil, nil, err
	}
	return OpenSharedLayer(path, LayerDialog)
}

// NewDialogID 生成新的对话 id（UUID v4 文本；与 session_id 无关）。
func NewDialogID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("data: 生成 dialog_id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
