// 工具链占位符（{{toolchain.<key>}}）的文本替换 —— 单一实现，供能力原语渲染（本模块
// prompts/get）与 chonkpilot-server（场景 systemPrompt / 记忆指引 / 工具契约文本 /
// llm-simple）共用。
//
// 语义（用户口径）：
//   - 语法 {{toolchain.<key>}}，key ∈ ToolchainKeys；
//   - 已知 key 未配置（值空串）→ 替换为空串；
//   - 未知 key（如 {{toolchain.unknown}}）→ 原样保留（不由本函数处理）；
//   - 仅匹配 {{toolchain.x}} 整串，不影响 {{arg}}（MCP GetPrompt 实参）/ {{env.X}}（DSL）等
//     其它占位符；只作用于文本内容，不涉及消息体结构。
package server

import "strings"

// ToolchainKeys 是 {{toolchain.<key>}} 的 key 全集：与 gui 工具链探测 id（chonkpilot-gui/bridge/
// toolchain.go）及 usr 配置 *Path 键一一对应 ——
// java→javaPath、python→pythonPath、node→nodePath、go→goPath、rust→rustPath、
// c→cCompilerPath、chrome→chromePath。
var ToolchainKeys = []string{"java", "python", "node", "go", "rust", "c", "chrome"}

// ToolchainPlaceholder 是占位符前缀（含 "{{"）：调用方可用它先做廉价短路判断，避免无谓取值。
const ToolchainPlaceholder = "{{toolchain."

// ReplaceToolchain 把 text 中的 {{toolchain.<key>}} 精确整串替换为 vars[key]：
//   - vars 为 nil / 缺 key（未配置）→ 替换为空串；
//   - 非 ToolchainKeys 的 key（未知）→ 原样保留。
//
// text 不含前缀时直接原样返回（零分配短路）。
func ReplaceToolchain(text string, vars map[string]string) string {
	if text == "" || !strings.Contains(text, ToolchainPlaceholder) {
		return text
	}
	for _, k := range ToolchainKeys {
		text = strings.ReplaceAll(text, ToolchainPlaceholder+k+"}}", vars[k])
	}
	return text
}
