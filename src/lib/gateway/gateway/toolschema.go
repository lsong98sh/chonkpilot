// 工具 schema 归一（原 meta_tools.go 的通用助手，GATEWAY-2 L5 纯化后仅保留注册面需要的部分）。
package mcpgateway

import "fmt"

// normalizeInputSchema 校验/补齐工具 schema：顶层 type 必须为 object（缺失补 {"type":"object"}，
// 其它 type 报错）——官方 SDK AddTool 对非 object/缺失 schema 直接 panic（参考 mcp-server contract.go buildTool）。
func normalizeInputSchema(name string, schema map[string]any) (map[string]any, error) {
	if len(schema) == 0 {
		return map[string]any{"type": "object"}, nil
	}
	switch typ, _ := schema["type"].(string); typ {
	case "object":
		return schema, nil
	case "":
		out := map[string]any{"type": "object"}
		for k, v := range schema {
			out[k] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tool %s: input schema must have type \"object\", got %q", name, typ)
	}
}
