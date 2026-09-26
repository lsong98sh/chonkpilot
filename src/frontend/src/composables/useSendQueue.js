import { ref } from 'vue'

// 用户消息发送队列（send-after-turn-finish），按 LLM（而非会话）独立分桶。
//
// 域化（阶段三 / 附录 A）：队列只承载用户消息（message）——
//   - notify 归后端（LLM 域监听 tool-notify → 空闲自动发通知轮次），前端不再入队；
//   - ask_user 回答走 ask-user-reply 事件直达后端（活跃回填当前 turn / 超时转普通 user 消息），
//     不再经 sendQueue 兜底（附录 A）。
//
// 设计（docs/spec/00-overview/01-端到端数据流.md §5.3 · docs/spec/30-function-points/34-任务.md §七）：
//   - 入队是纯前端操作，不写 DB、不启动 turn。
//   - 每个 LLM 管理自己独立的待发送队列，互不干扰；切换 LLM 时队列跟随 LLM。
//   - LLM 结束事件驱动出队（llm-complete → drain 自动发送）；
//     canceled → 队列消息写回 textarea（MessageList 处理）。
//   - 队列项操作：撤回（删除 + 写回 textarea）/ 删除（popover 单项）。
class SendQueue {
  // 桶：buckets[llm] = { items: [] }
  buckets = ref({})

  _bucket(llm) {
    const key = llm || ''
    if (!this.buckets.value[key]) {
      this.buckets.value[key] = { items: [] }
    }
    return this.buckets.value[key]
  }

  // 入队：按 item.llm（或 payload.llm）分桶。
  // 队列项：{ id, type: 'message', payload: { sessionId, text, llm, thinkFlag, effortLevel, scenarioId } }
  enqueue(item) {
    const llm = item.llm || item.payload?.llm || ''
    this._bucket(llm).items.push({
      ...item,
      id: item.id || (Date.now().toString(36) + Math.random().toString(36).slice(2, 8)),
    })
  }

  countOf(llm) {
    const b = this.buckets.value[llm || '']
    return b ? b.items.length : 0
  }

  // 待发条目副本（供队列指示器 popover 预览，外部只读）
  itemsOf(llm) {
    const b = this.buckets.value[llm || '']
    return b ? b.items.map((i) => i) : []
  }

  // canceled 简化：取出指定 LLM 桶中全部待发文本（写回 textarea 用），并从队列移除。
  takeTexts(llm) {
    const b = this.buckets.value[llm || '']
    if (!b) return []
    const texts = b.items.map((it) => (it.payload?.text || ''))
    b.items = []
    return texts.filter((s) => s && s.trim())
  }

  // 按 id 跨桶取出并删除队列项（用户撤回 / 删除）
  takeById(id) {
    if (!id) return null
    for (const key of Object.keys(this.buckets.value)) {
      const b = this.buckets.value[key]
      const idx = b.items.findIndex((i) => i.id === id)
      if (idx !== -1) return b.items.splice(idx, 1)[0]
    }
    return null
  }

  hasPending(llm) {
    return this.countOf(llm) > 0
  }

  // 取下一批待发内容：message 单条原样返回（doSend 直接读 batch.text/llm/thinkFlag/…）。
  shiftBatch(llm) {
    const b = this._bucket(llm || '')
    if (b.items.length === 0) return null
    const item = b.items.shift()
    return { ...item.payload, type: item.type, llm: llm || item.payload?.llm || '' }
  }
}

// 队列项展示文本（popover 预览 / 撤回写回用）：message → payload.text 折叠空白。
export function formatItemText(item) {
  if (!item) return ''
  const p = item.payload || {}
  return (p.text || '').replace(/\s+/g, ' ').trim() || item.type
}

export const sendQueue = new SendQueue()
