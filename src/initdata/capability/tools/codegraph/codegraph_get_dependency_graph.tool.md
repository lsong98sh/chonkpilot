# 基本信息
```yaml
name: codegraph_get_dependency_graph
title: 依赖图
args: codegraph_get_dependency_graph --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-dependency-graph-executor.exe
timeout: 60
```

# 摘要
某项目的文件级依赖：文件 → import/use/require 目标（原始串）。file 过滤可单查。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "file": { "type": "string", "description": "只返回该文件的依赖" }
  }
}
```
