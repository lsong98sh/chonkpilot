# 基本信息
```yaml
name: codegraph_analyze_complexity
title: 圈复杂度
args: codegraph_analyze_complexity --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-complexity-executor.exe
timeout: 60
```

# 摘要
圈复杂度分析：默认返回项目内复杂度最高 topN 的 func/method；file 限定单文件。复杂度为决策点启发式。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" },
    "file": { "type": "string", "description": "只看该文件" },
    "topN": { "type": "integer", "description": "返回前 N（默认 20）" },
    "minCc": { "type": "integer", "description": "最低复杂度（默认 1）" }
  }
}
```
