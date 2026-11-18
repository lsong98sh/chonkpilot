/**
 * useMessageActions — 消息列表项动作总线（模块级单例）
 *
 * 长会话性能（评审第 11 条）：消息卡此前**每条消息**按自身 type 分别 `mq.on` 订阅
 * 再在回调里按 `id` 过滤（N 条消息 → N 个全局订阅/每次事件 N 次回调）。现改为每个
 * topic **全应用只订阅一次**，回调按 `{id}` 查表分发到对应消息实例。
 *
 * 结构：
 *   - handlers：topic → Map<messageId, fn>
 *   - unsubs：  topic → mq.on 返回的取消函数（该 topic 首个 handler 注册时建立）
 *
 * `registerMessageActions(messageId, {topic: fn})` 返回 cleanup（幂等），组件卸载时调用；
 * 某 topic 的最后一个 handler 移除时取消该 topic 的唯一订阅。事件载荷形状不变：`{id}`。
 */
import mq from '@ckfw/utils/mq.js'

const handlers = new Map() // topic -> Map<messageId, fn>
const unsubs = new Map() // topic -> unsubscribe fn

/** 移除一条登记；topic 无 handler 时取消唯一订阅并清空。 */
function removeHandler(topic, messageId) {
  const byId = handlers.get(topic)
  if (!byId) return
  byId.delete(messageId)
  if (byId.size > 0) return
  handlers.delete(topic)
  const off = unsubs.get(topic)
  if (off) {
    unsubs.delete(topic)
    off()
  }
}

/**
 * 为一条消息登记动作：actions = { topic: fn }。
 * 每个 topic 的首个 handler 时建立**唯一**订阅，载荷 `{id}` 命中才调用（行为与旧逐条过滤一致）。
 *
 * @param {string} messageId 消息实例 id（事件载荷 id）
 * @param {Record<string, Function>} actions topic → 处理函数
 * @returns {() => void} cleanup（幂等）
 */
export function registerMessageActions(messageId, actions) {
  if (!actions || typeof actions !== 'object') return () => {}
  const topics = Object.keys(actions)
  const registered = []
  for (const topic of topics) {
    const fn = actions[topic]
    if (typeof fn !== 'function') continue
    let byId = handlers.get(topic)
    if (!byId) {
      byId = new Map()
      handlers.set(topic, byId)
    }
    byId.set(messageId, fn)
    if (!unsubs.has(topic)) {
      unsubs.set(topic, mq.on(topic, (payload) => {
        const id = payload && payload.id
        const byMessage = handlers.get(topic)
        const cb = byMessage && byMessage.get(id)
        if (cb) cb()
      }))
    }
    registered.push(topic)
  }

  let cleaned = false
  return () => {
    if (cleaned) return
    cleaned = true
    for (const topic of registered) removeHandler(topic, messageId)
  }
}