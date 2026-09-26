// 会话「复制」口径（用户 2026-09-24 定，A3/P3-C1）：
//   ① **不含 system** —— 只导出对话本身；
//   ② 内容 = **全部对话**（逐轮读原始消息表 `data-session-load-messages`，**非压缩后**的
//      快照摘要；即快照里被 `[已压缩早前对话]` 取代的轮次仍导出原文）；
//   ③ 形式 = **与发给 LLM 一致**（ChatMsg 的线格式字段），整体为 **json-array**。

// toLlmMessage 把门面 ChatMsg（facade.Message：role/kind/content/tool_call_id/tool_calls/
// reasoning/meta）映射为「发给 LLM」的线格式子集 —— 只保留协议字段：
// role + content（非空时）+ tool_calls（有则带）+ tool_call_id（有则带）。
// `kind`（用户消息来源标记、`_meta` 等内部标记）不外发（协议不消费）。
export function toLlmMessage(m) {
  if (!m || typeof m !== 'object') return null
  const role = String(m.role || '')
  if (!role) return null
  const out = { role }
  if (typeof m.content === 'string' && m.content !== '') out.content = m.content
  if (Array.isArray(m.tool_calls) && m.tool_calls.length > 0) out.tool_calls = m.tool_calls
  if (m.tool_call_id) out.tool_call_id = m.tool_call_id
  return out
}

// buildCopyMessages 按轮次顺序拼接全部对话（turnMessageLists = 各轮的 ChatMsg 数组，升序），
// 过滤 system 消息并映射为线格式。返回数组（调用方负责序列化/写剪贴板）。
export function buildCopyMessages(turnMessageLists) {
  const out = []
  for (const msgs of turnMessageLists || []) {
    for (const m of (msgs || [])) {
      if (!m || String(m.role || '') === 'system') continue
      const mapped = toLlmMessage(m)
      if (mapped) out.push(mapped)
    }
  }
  return out
}

// serializeCopyMessages 序列化为 json-array（缩进 2 = 可读；解析回来即对象数组）。
export function serializeCopyMessages(messages) {
  return JSON.stringify(messages || [], null, 2)
}
