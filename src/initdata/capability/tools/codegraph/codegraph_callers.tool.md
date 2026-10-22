# 基本信息
```yaml
name: codegraph_callers
title: 调用方
args: codegraph_callers --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-callers-executor.exe
timeout: 60
```

# 摘要
查「谁调用了它」：在已索引项目内按被调名（精确名或最后一段，忽略大小写，故 pkg.Foo 与 Foo 同目标）找调用方符号。语义为调用点文本的**名字级启发式**（无类型/重载解析，仅覆盖已索引文件中的直接调用）。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "name": { "type": "string", "description": "被调名（如 Foo 或 pkg.Foo）" },
    "id": { "type": "string", "description": "目标符号 id（file:line:name:kind）；与 name 二选一，id 优先" },
    "file": { "type": "string", "description": "只保留该文件内的调用方（路径子串过滤）" },
    "limit": { "type": "integer", "description": "返回上限（默认 50）" }
  }
}
```
