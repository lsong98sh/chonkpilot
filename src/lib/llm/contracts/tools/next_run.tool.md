# 基本信息
```yaml
name: next_run
title: next_run
args: next_run --input={RAW-INPUT-FILE}
async: never
category: server
core-only: true
hot: true
output: stdout
runtime: builtin
timeout: 0
```

# 摘要
把**下一轮要执行的提示词**登记为排队输入：本轮结束后系统会自动起一个新轮次执行它（类似排队输入，与「继续」不同——「继续」只在本轮内追加）。适用场景：
- **输出 token 预算即将耗尽（约不足 5%）但任务尚未完成**；
- **一个阶段已完成、且明确还有下一阶段**（分段交付）。

`prompt` 必须是**下一轮的完整提示词**（自成一体：目标、已知进展、约束、验收标准），不要依赖本轮未输出的内容。调用成功后本轮请直接收尾并简要说明已登记下一轮，**不要重复输出下一轮内容**。同一轮多次调用以**最后一次**为准（覆盖前一条，计数按次累加）。到达连续自动接续上限时返回 `已自动接续 N 次，需用户判断是否继续`——此时请收尾并提示用户。仅主会话可用（子聊天 / 群聊 / 子会话由各自调度器驱动）。

# 参数
```json
{
  "properties": {
    "prompt": {
      "description": "下一轮要执行的完整提示词（自成一体；包含目标、已知进展、约束与验收标准）",
      "type": "string"
    }
  },
  "required": [
    "prompt"
  ],
  "type": "object"
}
```
