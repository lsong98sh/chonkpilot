//go:build windows

// 注册表 App Paths 查询：Windows 官方安装器（Chrome/Node/Python 等）会把可执行文件的
// 绝对路径写入 `...\CurrentVersion\App Paths\<exe>`，作为「非标准安装目录」的兜底探测源。
package bridge

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// lookupAppPath 读取 `App Paths\<exe>` 的默认值（HKCU 优先 → HKLM），返回存在的可执行文件路径。
func lookupAppPath(exe string) string {
	const sub = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, sub+exe, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("")
		k.Close()
		if err != nil {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if fileExists(v) {
			return v
		}
	}
	return ""
}
