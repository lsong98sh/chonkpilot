# 基本信息
```yaml
name: subchat_finish
title: subchat_finish
async: never
category: server
hot: true
timeout: 0
```

# 摘要
子 agent 自报：本次子聊天**目标已达成**，建议结束。把该子聊天会话的生命周期置为 `ready`（**建议态，不等于结束**）——它只是「你认为可以收尾了」，**是否结束由用户拍板**（用户点「结束」才进 `finished`）。不关窗、不回流、不结束会话；用户若继续聊，新一轮开始即回到 `chatting`。仅在子聊天会话内有意义。

# 参数
```json
{
  "properties": {},
  "type": "object"
}
```
