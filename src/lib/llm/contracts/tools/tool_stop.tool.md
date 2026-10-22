# 基本信息
```yaml
name: tool_stop
title: tool_stop
args: tool_stop --input={RAW-INPUT-FILE}
async: never
category: server
core-only: true
hot: true
output: stdout
runtime: builtin
timeout: 0
```

# 摘要
停止任务：task_id（server 任务节点 id，级联取消整棵子树，含转后台 gateway 任务）或 process_id（按 OS 进程 PID 杀进程）；两者都传时 process_id 优先。用于中止超时/失控/不需要继续的任务。

# 参数
```json
{
  "properties": {
    "process_id": {
      "description": "按 OS 进程 PID 杀进程",
      "type": "number"
    },
    "task_id": {
      "description": "停止的任务 id（server 任务节点，如转后台任务节点 / 子会话任务节点）",
      "type": "string"
    }
  },
  "type": "object"
}
```
