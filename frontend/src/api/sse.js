/**
 * SSE → Bridge Push
 *
 * 原先通过 HTTP EventSource 连接 /api/events 获取实时推送，
 * 现在改为通过 Bridge Push 接收 Go 端主动推送的事件（统一 llm:event 通道）。
 *
 * 使用方式:
 *   sse.on("message", data => console.log(data))
 *   sse.connect(sessionId)  // 保留兼容性
 *   sse.disconnect()
 */

import bridge from '../utils/bridge'

const listeners = {}

class SSEConnection {
  constructor() {
    this._unsubs = []
  }

  connect(sessionId) {
    // Bridge Push 不需要连接——事件会自动推送到前端
    // 只需要设置监听即可
    console.log('[bridge] connected (session:', sessionId, ')')
    this._emit('connected', {})
  }

  disconnect() {
    this._unsubs.forEach(fn => fn())
    this._unsubs = []
  }

  on(event, callback) {
    // 映射 SSE 事件名到 bridge 事件名
    // 统一 llm:event 通道，通过 _event_type 区分事件类型
    if (event === 'message') {
      const unsub = bridge.on('llm:event', (data) => {
        const et = data?._event_type || ''
        if (et === 'message_chunk') {
          if (data.content) callback({ type: 'token', payload: data })
        } else if (et === 'complete') {
          callback({ type: 'done', payload: data })
        } else if (et === 'error') {
          callback({ type: 'error', payload: data })
        }
      })
      this._unsubs.push(unsub)
      return unsub
    }
    const unsub = bridge.on(event, callback)
    this._unsubs.push(unsub)
    return unsub
  }

  _emit(event, data) {
    const cbs = this._unsubs[event] || []
    cbs.forEach(cb => cb(data))
  }

  _scheduleReconnect() {
    // Bridge Push 自动工作，无需重连
  }
}

export default new SSEConnection()
