// llmref 取值折算（12-数据层 / 40-演进计划 §SL）：`llmref` 是 LLM 选择引用的配置值形态
// （usr `defaultLLM` 与 5 个 `llm.<子系统>` 键；读侧见 internal/config/userconfig.go）——
// 取值二态：**provider name 字符串**（新形态；空串 = 显式「系统默认（启动参数）」）
// 与**旧 int 索引**（2026-09-15 前的历史记录；或键缺失时数据层计算的「第一个可用 LLM」下标）。
//
// 消费方（LLM 侧只认 provider name）须先折算；本函数 = 该折算的**单一来源**
// （口径对齐 GUI 桥 `resolveDefaultLLMIndex`，见 src/lib/gui/bridge/optimize.go）。
//
// 注：「键缺失/空串 → 回落 defaultLLM」已由数据层读侧完成（SL-1），本函数**不重复**该回落。
package data

import "strings"

// LLMRefName 把 llmref 取值折算为 provider name：
//   - **字符串** → 原样（trim）；空串保持空串（= 显式「系统默认（启动参数）」，调用方据此不传）；
//   - **数字**（int / int64 / float64；旧 int 索引）→ `llms[idx]` 的 name；越界 / 负数 →
//     首个可用（下标 0，沿用既有默认 LLM 语义）；
//   - **其余**（键缺失 / nil / 无 llms）→ 同「下标 0」语义；`llms` 为空 → ""。
//
// llms 兼容两种形态：`[]any`（MQ/JSON 往返后的解码结果）与 `[]map[string]any`
// （data 门面 inline 绑定直返的内核形态）。
func LLMRefName(v any, llms any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	idx := 0
	switch n := v.(type) {
	case int:
		idx = n
	case int64:
		idx = int(n)
	case float64:
		idx = int(n)
	}
	switch list := llms.(type) {
	case []any:
		if len(list) == 0 {
			return ""
		}
		if idx < 0 || idx >= len(list) {
			idx = 0
		}
		m, _ := list[idx].(map[string]any)
		name, _ := m["name"].(string)
		return strings.TrimSpace(name)
	case []map[string]any:
		if len(list) == 0 {
			return ""
		}
		if idx < 0 || idx >= len(list) {
			idx = 0
		}
		name, _ := list[idx]["name"].(string)
		return strings.TrimSpace(name)
	}
	return ""
}
