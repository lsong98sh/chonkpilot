// Package systemfs 是 capability/system/ 出厂内置文档的 **embed 回落唯一入口**（OP-02，2026-10-06）。
//
// 出厂唯一源 = `src/initdata/capability/system/**`。各构建脚本（build-desktop.ps1 / build-gui.ps1）
// 在 go build 前把该源**覆盖式**同步到本包 embedded/system/ 的 embed 落点，保证编入二进制的
// 内置副本与出厂源一致（此副本即"磁盘 capability/system 缺失"时的兜底）。
//
// 读取链由消费方自行按级 fallback：项目级 → 用户级 → 系统级磁盘 → **本包 embed 内置**
// （persist config 域 / compress 插件各持其读点；见 docs/spec/60-reference/64-配置项一览 §7）。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package systemfs

import (
	"embed"
	"path/filepath"
	"strings"
)

//go:embed all:embedded/system
var factoryFS embed.FS

// embeddedRoot 是 embed 内的 system 文档根（相对本包）；落点内容 = `src/initdata/capability/system/**`。
const embeddedRoot = "embedded/system"

// Doc 返回出厂内置 system 文档正文：kind = 相对 system 根的文件路径去 `.md`（如 "summary"；
// 未来子目录如 "memory/用户偏好"）。文件为**纯文本**（无契约分区）。内容去首尾空白（与文件末尾
// 换行无关）。缺失 → ("", false)。
func Doc(kind string) (string, bool) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "", false
	}
	raw, err := factoryFS.ReadFile(embeddedRoot + "/" + filepath.ToSlash(kind) + ".md")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}
