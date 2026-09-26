// 系统级内置项（gui.system.builtins）：OEM/发布资源中的只读 MCP 定义。
// 来源 = exe 同目录 config.json 的 mcpServers 段（系统级资源，12-数据层「系统默认 = 资源/常量」）；
// 文件缺失/解析失败 → 返回空集合（前端展示空态）。不落库、不可编辑。
// 注：原 `llms` 段展示已移除（llms 配置以 usr 库为准）；只读 LLM 条目（builtinLLMs）亦已移除
// （2026-09-26：启动参数隐含默认不再作为可配置项列出，见 64 §11.1）。
package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// callSystemBuiltins 读取 exe 同目录 config.json 的 mcpServers 段 → {mcpServers:[...]}
// （缺失 → 空数组）。
func callSystemBuiltins(_ *Bridge, _ context.Context, _ []json.RawMessage) ([]byte, error) {
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

	return json.Marshal(map[string]any{"mcpServers": mcps})
}
