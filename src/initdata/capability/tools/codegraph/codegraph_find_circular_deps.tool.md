# 基本信息
```yaml
name: codegraph_find_circular_deps
title: 循环依赖
args: codegraph_find_circular_deps --input={RAW-INPUT-FILE}
async: never
category: codegraph
hot: true
internal: true
idempotent: true
output: stdout
runtime: ../../../mcps/codebase/chonkpilot-codegraph-circular-deps-executor.exe
timeout: 60
```

# 摘要
在可解析为仓库内文件的导入边上找环（JS/TS 相对、Go module 前缀、Py/Java 包路径、Rust crate 尽力）；解析不到的不参与。

# 参数
```json
{
  "type": "object",
  "properties": {
    "workdir": { "type": "string", "description": "项目根目录（由插件注入，LLM 无需填写）" }
  }
}
```
