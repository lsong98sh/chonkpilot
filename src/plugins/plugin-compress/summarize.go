// 摘要 system 提示词来源（对齐 docs/spec/60-reference/64-配置项一览 §7）：
// compress 请求摘要经 llm-simple 时带 system，来源 = capability/knowledge/prompts/summary.prompt.md，
// 按级 fallback：项目级 <workDir>/.chonkpilot/capability/knowledge/prompts/ → 用户级
// ~/.chonkpilot/capability/knowledge/prompts/ → 系统级 <exeDir>/capability/knowledge/prompts/；
// 三级皆缺 → 回落内置默认（chonkpilot-data.DefaultSummaryPrompt）。
//
// 2026-09-11 接线：原硬编码 summarySystemPrompt 转为默认常量；默认值已提为 chonkpilot-data
// 的单一来源（设置页与压缩共用，I-65 ⑧）；死代码 Summarizer / LLMSummarizer /
// NewLLMSummarizer（从未实例化）已删除（决策记录 D-09）。
package compress

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
)

// summaryPromptFileName 摘要提示词文件名（prompts 根下，12-数据层文件形态）。
const summaryPromptFileName = "summary.prompt.md"

// summaryPromptPaths 按级 fallback 的摘要提示词文件路径：项目级 → 用户级 → 系统级。
// workDir 空 → 不含项目级；exeDir 不可用 → 不含系统级。
func summaryPromptPaths(workDir string) []string {
	var out []string
	if workDir != "" {
		out = append(out, filepath.Join(workDir, ".chonkpilot", "capability", "knowledge", "prompts", summaryPromptFileName))
	}
	out = append(out, filepath.Join(filepath.Dir(data.UserPath()), "capability", "knowledge", "prompts", summaryPromptFileName))
	if dir, err := exedir.Dir(); err == nil {
		out = append(out, filepath.Join(dir, "capability", "knowledge", "prompts", summaryPromptFileName))
	}
	return out
}

// resolveSummaryPrompt 按级读摘要 system：项目级 → 用户级 → 系统级；皆缺失/空 → 内置默认。
// 每次压缩读文件（无缓存——保存提示词后下一次压缩即生效）。
func (c *Compressor) resolveSummaryPrompt(workDir string) string {
	for _, p := range summaryPromptPaths(workDir) {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if content := primitiveContent(string(raw)); content != "" {
			return content
		}
	}
	return data.DefaultSummaryPrompt
}

// primitiveContent 取 capability 原语文本的 [content] 分区正文
// （文件形态 = # 标题 + 可选 [meta]/[description]/[parameters] + [content]，与 persist 的
// buildPrimitive 同构）。无分区标记（用户手写裸文本）→ 去掉首行 "# 标题" 后整体作为正文。
func primitiveContent(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	section := ""
	found := false
	var body []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "[content]" {
			section = "content"
			found = true
			continue
		}
		if section == "content" && strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") && len(t) > 2 {
			section = "" // 进入下一分区，结束正文收集
			continue
		}
		if section == "content" {
			body = append(body, ln)
		}
	}
	if found {
		return strings.TrimSpace(strings.Join(body, "\n"))
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
