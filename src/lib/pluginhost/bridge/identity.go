package bridge

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// sanitizePluginID 把插件自报 id 归一为实例 id 可用片段。
func sanitizePluginID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// newPluginInstanceID 生成服务端分配的插件实例 id：`plugin:<plugin_id>:<8hex>`。
// 插件不得自报 / 覆盖 GUI 实例 id。
func newPluginInstanceID(pluginID string) string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return "plugin:" + sanitizePluginID(pluginID) + ":" + hex.EncodeToString(buf[:])
}
