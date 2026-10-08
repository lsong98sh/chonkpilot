// 摘要 system 提示词来源（对齐 docs/spec/60-reference/64-配置项一览 §7；OP-01/OP-02，2026-10-06）：
// compress 请求摘要经 llm-simple 时带 system，来源 = capability/system/summary.md（**纯文本**），
// 按级 fallback：项目级 <workDir>/.chonkpilot/capability/system/ → 用户级
// ~/.chonkpilot/capability/system/ → 系统级 <exeDir>/capability/system/；
// 三级皆缺 → 回落 data.SystemDoc("summary")（出厂文件 embed 内置，**同一份出厂内容**）。
//
// 2026-09-11 接线：原硬编码 summarySystemPrompt 转为默认常量；2026-10-06（OP-02）默认改为
// **出厂文件**（删除 data.DefaultSummaryPrompt 代码常量），统一经 data 门面 SystemDoc 读 embed 内置。
package compress

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
)

// summaryPromptFileName 摘要提示词文件名（system 根下，纯文本，无 `.prompt` 后缀）。
const summaryPromptFileName = "summary.md"

// summarySystemDir 某级 capability 的 system 文档目录名（与 prompts/ 等并列，非原语类型目录）。
const summarySystemDir = "system"

// summaryPromptPaths 按级 fallback 的摘要提示词文件路径：项目级 → 用户级 → 系统级。
// workDir 空 → 不含项目级；exeDir 不可用 → 不含系统级。
func summaryPromptPaths(workDir string) []string {
	var out []string
	if workDir != "" {
		out = append(out, filepath.Join(workDir, ".chonkpilot", "capability", summarySystemDir, summaryPromptFileName))
	}
	out = append(out, filepath.Join(filepath.Dir(data.UserPath()), "capability", summarySystemDir, summaryPromptFileName))
	if dir, err := exedir.Dir(); err == nil {
		out = append(out, filepath.Join(dir, "capability", summarySystemDir, summaryPromptFileName))
	}
	return out
}

// resolveSummaryPrompt 按级读摘要 system：项目级 → 用户级 → 系统级；皆缺失/空 → embed 内置
// （data.SystemDoc("summary") = 出厂文件 src/initdata/capability/system/summary.md）。
// 每次压缩读文件（无缓存——保存提示词后下一次压缩即生效）。
func (c *Compressor) resolveSummaryPrompt(workDir string) string {
	for _, p := range summaryPromptPaths(workDir) {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if content := strings.TrimSpace(string(raw)); content != "" {
			return content
		}
	}
	return data.SystemDoc("summary")
}
