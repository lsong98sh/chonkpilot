# 基本信息
```yaml
name: mcp_invoke
title: 工具调用
args: mcp_invoke --input={RAW-INPUT-FILE}
async: never
category: meta
core-only: true
hot: true
output: stdout
runtime: builtin
timeout: 0
```

# 摘要
直接调用工具：按工具名和参数调用，返回执行结果。相当于 tools/call 的封装，用于 LLM 自主调用已知工具。

# 参数
```json
{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "工具名"
    },
    "arguments": {
      "type": "object",
      "description": "工具参数，按工具 schema 传入"
    }
  },
  "required": [
    "name",
    "arguments"
  ]
}
```
