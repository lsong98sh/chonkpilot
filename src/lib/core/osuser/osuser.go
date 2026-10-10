// Package osuser 提供「本机 OS 用户名」的单一口径取值（instance-register / flash claim /
// server 单例互斥体名等共用；取不到 → "unknown"，不置空）。收敛自
// bridge.currentUserName / flash.CurrentUser / server.currentUserName 三处同构实现（R7-D-9）。
package osuser

import (
	"os/user"
	"strings"
)

// CurrentUser 返回本机 OS 用户名（取不到 → "unknown"）。
func CurrentUser() string {
	if u, err := user.Current(); err == nil && u != nil && strings.TrimSpace(u.Username) != "" {
		return u.Username
	}
	return "unknown"
}
