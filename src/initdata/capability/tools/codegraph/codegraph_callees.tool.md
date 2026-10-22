# 基本信息
```yaml
name: codegraph_callees
title: 被调用方
args: codegraph_callees --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-callees-executor.exe
timeout: 60
```

# 摘要
查「它调用了谁」：按 id 或 file+name 定位符号，返回其直接调用的目标名（含 resolved 与解析到的 file/line）。语义为调用点文本的**名字级启发式**（无类型/重载解析）。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "id": { "type": "string", "description": "符号 id（file:line:name:kind）" },
    "file": { "type": "string", "description": "符号所在文件（配 name 定位）" },
    "name": { "type": "string", "description": "符号名（配 file 定位）" },
    "limit": { "type": "integer", "description": "返回上限（默认 50）" }
  }
}
```
