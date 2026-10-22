# 基本信息
```yaml
name: codegraph_symbol_search
title: 符号搜索
args: codegraph_symbol_search --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-symbol-search-executor.exe
timeout: 60
```

# 摘要
在已索引的某项目内按符号名（子串/前缀，忽略大小写）与 kind/文件过滤搜索。kind: func/method/class/type/interface/enum/trait/constructor。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "query": { "type": "string", "description": "符号名关键字" },
    "kind": { "type": "string", "description": "符号类别过滤" },
    "file": { "type": "string", "description": "文件路径子串过滤" },
    "limit": { "type": "integer", "description": "返回上限（默认 50）" }
  }
}
```
