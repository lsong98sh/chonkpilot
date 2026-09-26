// 系统级内置项（gui.system.builtins）：OEM/发布资源中的只读 MCP 定义 + 只读 LLM 条目。
// 来源 = exe 同目录 config.json 的 mcpServers 段（系统级资源，12-数据层「系统默认 = 资源/常量」）；
// 文件缺失/解析失败 → 返回空集合（前端展示空态）。不落库、不可编辑。
// 注：原 config.json `llms` 段展示已移除（llms 配置以 usr 库为准，见 36-配置）；
// builtinLLMs（2026-09-15 新增）是**代码常量**来源的只读条目（启动参数隐含默认，
// main 经 SetLLMBuiltins 注入），与已移除的「config.json llms 段」不是同一来源；
// **内置 provider（echo）自 D-30（2026-09-22）起不再注入**（降为 router 内置兜底）。
package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// callSystemBuiltins 读取 exe 同目录 config.json 的 mcpServers 段，并叠加注入的只读 LLM 条目
// → {mcpServers:[...], builtinLLMs:[...]}（缺失 → 空数组）。
// builtinLLMs 条目：kind=default（启动参数 -llm-base/-llm-model 的隐含默认，未进 usr llms 表）；
// **D-30（2026-09-22 拍板）后内置 provider（echo，kind=builtin）不再注入** —— echo 降为
// router 内置兜底（无可用 provider 时启用），不再作为可配置项列出（前端仍按 kind 分派展示）。
func callSystemBuiltins(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	mcps := []any{}

	exe, err := os.Executable()
	if err == nil {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "config.json"))
		if err == nil {
			var doc map[string]any
			if json.Unmarshal(raw, &doc) == nil {
				if v, ok := doc["mcpServers"].([]any); ok {
					mcps = v
				}
			}
		}
	}

	llms := make([]map[string]any, 0, len(b.builtinLLMs))
	for _, it := range b.builtinLLMs {
		llms = append(llms, map[string]any{
			"kind": it.Kind, "name": it.Name, "protocol": it.Protocol,
			"model": it.Model, "baseUrl": it.BaseURL,
		})
	}
	return json.Marshal(map[string]any{"mcpServers": mcps, "builtinLLMs": llms})
}
