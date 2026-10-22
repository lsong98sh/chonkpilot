# 基本信息
```yaml
name: codegraph_get_module_summary
title: 模块摘要
args: codegraph_get_module_summary --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-module-summary-executor.exe
timeout: 60
```

# 摘要
目录/项目摘要：文件数、语言分布、符号数、最高复杂度函数（基于索引）。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "path": { "type": "string", "description": "目录前缀（可选）" }
  }
}
```
