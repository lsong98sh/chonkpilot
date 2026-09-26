# tool_result

[meta]
category=server
async=never
timeout=30

[description]
获取转后台任务的结果：id 支持转后台任务 id（gateway 异步任务 / server 任务节点）或子会话 turn id（llm_run 委派/DSL 子轮次返回的 sub turn id）；timeout 秒内轮询等待完成，超时返回「任务尚未结束」（可用更长 timeout 再次调用）。

[parameters]
{"type":"object","properties":{"id":{"type":"string","description":"任务 id 或子会话 turn id"},"timeout":{"type":"number","description":"等待秒数（必填；0=立即查询一次）"}},"required":["id","timeout"]}
