# tool_stop

[meta]
category=server
async=never
timeout=0
hot=true

[description]
停止任务：task_id（server 任务节点 id，级联取消整棵子树，含转后台 gateway 任务）或 process_id（按 OS 进程 PID 杀进程）；两者都传时 process_id 优先。用于中止超时/失控/不需要继续的任务。

[parameters]
{"type":"object","properties":{"task_id":{"type":"string","description":"停止的任务 id（server 任务节点，如 llm_run 作业节点 / 转后台任务节点）"},"process_id":{"type":"number","description":"按 OS 进程 PID 杀进程"}}}
