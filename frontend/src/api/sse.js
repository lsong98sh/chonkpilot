/**
 * SSE → Bridge Push
 *
 * 原先通过 HTTP EventSource 连接 /api/events 获取实时推送，
 * 现在改为通过 Bridge Push 接收 Go 端主动推送的事件。
 *
 * 使用方式:
 *   sse.on("chat:token", data => console.log(data))
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
    const bridgeEvent = event === 'message' ? 'chat:*' : event
    if (bridgeEvent === 'chat:*') {
      // 监听所有 chat: 前缀的事件
      const unsub1 = bridge.on('chat:token', (data) => {
        if (data.content) callback({ type: 'token', payload: data })
      })
      const unsub2 = bridge.on('chat:done', (data) => {
        callback({ type: 'done', payload: data })
      })
      const unsub3 = bridge.on('chat:error', (data) => {
        callback({ type: 'error', payload: data })
      })
      this._unsubs.push(unsub1, unsub2, unsub3)
      return () => { unsub1(); unsub2(); unsub3() }
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
