# 基本信息
```yaml
name: demo_echo
title: 回显文本
async: sync
timeout: 30
on_timeout: fail
category: demo
hot: true
```

# 摘要

回显调用方提供的文本（演示 MCP 工具、`.tool.md` 契约与配置读取）。输出是**纯文本**，恒为 `"<greeting>: <text>"`，`greeting` 默认 `hi from python`。

**`text` 必填且必须是非空字符串**：缺失、`null`、空串（含仅空白）或非字符串一律以 `isError=true` 返回参数错误，不会静默返回空回显。

**截断**：文本超过 `echoLimit`（默认 200 字符）时只保留前 `echoLimit` 个字符并追加 `…(truncated)`；不返回原长度，也不支持 offset 续读。

本工具无副作用、幂等，超时按 `on_timeout: fail` 报错。

# 参数
```json
{
  "type": "object",
  "properties": {
    "text": {
      "type": "string",
      "description": "要回显的文本"
    }
  },
  "required": ["text"]
}
```