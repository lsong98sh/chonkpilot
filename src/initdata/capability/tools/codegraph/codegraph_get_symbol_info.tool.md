# 基本信息
```yaml
name: codegraph_get_symbol_info
title: 符号详情
args: codegraph_get_symbol_info --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-symbol-info-executor.exe
timeout: 60
```

# 摘要
查询单个符号详情（id 或 file+name 定位）：kind/行号/复杂度/签名。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "id": { "type": "string", "description": "symbol_search 返回的 id（file:line:name:kind）" },
    "file": { "type": "string", "description": "文件绝对路径" },
    "name": { "type": "string", "description": "符号名" }
  }
}
```
