// 摘要（上下文压缩）提示词的内置默认 —— 单一来源。
//
// 消费方：
//   - compress 插件（chonkpilot-plugin-compress/summarize.go）：capability/knowledge/prompts/summary.prompt.md
//     三级（项目 → 用户 → 系统）皆缺失时回落本默认；
//   - persist（data-prompt load summary_prompt）：项目/系统文件与旧 prj 键皆无时同样兜底，
//     使设置页显示的"有效默认"与压缩实际生效值一致（I-65 ⑧）。
package data

// DefaultSummaryPrompt 内置默认摘要 system 提示词。
const DefaultSummaryPrompt = "你是对话历史摘要器。把以下对话历史压缩成一段简洁中文摘要，保留关键决定、任务状态、已实现内容。"
