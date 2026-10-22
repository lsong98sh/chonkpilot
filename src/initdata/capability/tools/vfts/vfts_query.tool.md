# 基本信息
```yaml
name: vfts_query
title: 全文检索
args: vfts_query --input={RAW-INPUT-FILE}
async: never
category: vfts
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/vfts/chonkpilot-vfts-query-executor.exe
timeout: 60
```

# 摘要
在某 workdir 的 vfts 全文索引中检索：match 为自然语言匹配串，query 为布尔/高级表达式（二者至少给其一，query 不支持字段前缀）；返回命中文件 + 片段/行号（topK，按相关度）。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "match": { "type": "string", "description": "自然语言匹配串（如：全文索引）" },
    "query": { "type": "string", "description": "布尔/高级表达式（如：fox AND brown；不支持 content: 字段前缀）" },
    "topK": { "type": "integer", "description": "返回命中文件数上限（默认 20）" },
    "path": { "type": "string", "description": "文件路径子串过滤（可选）" }
  }
}
```
