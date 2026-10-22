# 基本信息
```yaml
name: mcp_find
title: 资产检索
args: mcp_find --input={RAW-INPUT-FILE}
async: never
category: meta
core-only: true
hot: true
output: stdout
runtime: builtin
timeout: 0
```

# 摘要
检索/推荐工具与资产：按查询词（query，本地精确匹配）或任务目标（purpose，LLM 语义推荐——**对所有 type 生效**，可返回组合）在当前可见的**工具与资产**（skill/resource，含知识库原语）中查找匹配。**query/purpose 均省略时返回该类型全部条目**（受 limit）。返回匹配条目列表（名称、kind、描述、来源节点）或 LLM 推荐 JSON。

# 参数
```json
{
  "type": "object",
  "properties": {
    "purpose": {
      "type": "string",
      "description": "任务目标（自然语言），如：把当前项目文档批量转成 markdown 并全文检索——语义推荐（可组合，候选按 type 收敛）；提供时优先 LLM，LLM 不可用降级本地"
    },
    "query": {
      "type": "string",
      "description": "检索关键词（本地精确匹配；purpose 未提供时生效；省略则不按关键词过滤=返回该类型全部）"
    },
    "type": {
      "type": "string",
      "description": "检索类型：tool（工具）/skill（技能）/resource（资源）/all（全部，默认）。其它取值一律报错。",
      "default": "all"
    },
    "limit": {
      "type": "integer",
      "description": "最大返回数",
      "default": 20
    }
  }
}
```
