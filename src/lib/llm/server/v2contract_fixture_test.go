// v2 契约夹具助手（2026-10-14）：按「H1 分区 + yaml/json 代码块」构造 *.tool.md / *.agent.md 正文，
// 与生产解析器 contractmd 一致（旧方括号分区已废除）。metaLines 为 `k=v` 形式。
package server

import (
	"sort"
	"strings"
)

// v2MetaBlock 渲染「基本信息」yaml（name + title + 各 meta）。
// 值与 contractmd.Build 一致：**无引号裸值**（解析器为朴素行解析，不做引号反转义）。
func v2MetaBlock(name, title string, metaLines []string) string {
	var b strings.Builder
	b.WriteString("# 基本信息\n```yaml\nname: " + name + "\n")
	if title != "" {
		b.WriteString("title: " + title + "\n")
	}
	for _, ln := range metaLines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		i := strings.IndexByte(ln, '=')
		if i <= 0 {
			continue
		}
		b.WriteString(strings.TrimSpace(ln[:i]) + ": " + strings.TrimSpace(ln[i+1:]) + "\n")
	}
	b.WriteString("```\n\n")
	return b.String()
}

// v2RefsBlock 渲染「参考」yaml（kind → 名单；如 skills / tools / resources）。
func v2RefsBlock(refs map[string][]string) string {
	keys := make([]string, 0, len(refs))
	for k, v := range refs {
		if len(v) > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# 参考\n```yaml\n")
	for _, k := range keys {
		b.WriteString(k + ":\n")
		for _, it := range refs[k] {
			b.WriteString("  - " + it + "\n")
		}
	}
	b.WriteString("```\n\n")
	return b.String()
}

// buildV2AgentDoc 构造 *.agent.md 正文（name/title/desc/正文/delegate/meta/refs）。
func buildV2AgentDoc(name, desc, prompt, delegate string, metaLines []string, refs map[string][]string) string {
	b := v2MetaBlock(name, name, metaLines)
	if strings.TrimSpace(desc) != "" {
		b += "# 摘要\n" + desc + "\n\n"
	}
	b += v2RefsBlock(refs)
	if strings.TrimSpace(delegate) != "" {
		b += "# 委托\n" + delegate + "\n\n"
	}
	if strings.TrimSpace(prompt) != "" {
		b += "# 正文\n" + prompt + "\n"
	}
	return b
}

// buildV2ToolDoc 构造 *.tool.md 正文（name/desc/JSON Schema 参数/meta）。
func buildV2ToolDoc(name, desc, paramsJSON string, metaLines []string) string {
	b := v2MetaBlock(name, "", metaLines)
	if strings.TrimSpace(desc) != "" {
		b += "# 摘要\n" + desc + "\n\n"
	}
	if strings.TrimSpace(paramsJSON) != "" {
		b += "# 参数\n```json\n" + paramsJSON + "\n```\n"
	}
	return b
}
