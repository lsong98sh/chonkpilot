// 出厂内置 system 文档（capability/system/）的唯一读取口（OP-02，2026-10-06）。
//
// 旧 `DefaultSummaryPrompt` 代码常量已删除：摘要提示词的出厂默认现为**文件**
// `src/initdata/capability/system/summary.md`（构建脚本同步到 data 模块内 embed 落点，
// 见 internal/systemfs）。经本门面 `SystemDoc(kind)` 读 embed 内置副本，供 persist（设置页
// 有效值回落）与 compress 插件（压缩摘要 system 回落）共用——**同一份出厂内容**。
package data

import "github.com/chonkpilot/chonkpilot-data/internal/systemfs"

// SystemDoc 返回出厂内置 system 文档正文（kind = 文件名去 `.md`，如 "summary"）；缺失 → 空串。
// 消费方读序（自行实现）：项目级 → 用户级 → 系统级磁盘 → 本 embed 内置（同一份出厂内容）。
func SystemDoc(kind string) string {
	s, _ := systemfs.Doc(kind)
	return s
}
