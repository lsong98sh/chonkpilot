# 基本信息
```yaml
name: mcp_load
title: 资产加载
args: mcp_load --input={RAW-INPUT-FILE}
async: never
category: meta
core-only: true
hot: true
output: stdout
runtime: builtin
timeout: 0
```

# 摘要
加载条目详情：按名称（kind 可选，缺省 tool）获取其完整定义。tool = 名称/描述/参数 schema/来源 server/meta（hot/category 等）；skill/resource = 资产详情（description/content/uri 等）——skill/resource 为**节点原语实时读取**（与 prompts/get 同语义）。

# 参数
```json
{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "条目名（精确匹配）"
    },
    "kind": {
      "type": "string",
      "description": "条目类型：tool（缺省）/skill/resource"
    }
  },
  "required": [
    "name"
  ]
}
```
