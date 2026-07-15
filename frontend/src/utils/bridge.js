/**
 * Bridge — Wails Go → JS 事件通信层
 *
 * RPC 调用已迁移到 Wails 编译时绑定（wailsjs/go/main/App），
 * 不再通过 bridge.invoke 动态查找。
 *
 * 事件监听 (Wails Events):
 *   const unsub = bridge.on('chat:token', data => { ... })
 *   unsub() // 取消监听
 */

const bridge = {
  /**
   * 监听 Go 端通过 Wails runtime.EventsEmit 推送的事件。
   * 返回取消监听的函数。
   */
  on(event, callback) {
    if (window.runtime?.EventsOn) {
      window.runtime.EventsOn(event, callback)
      return () => {
        if (window.runtime?.EventsOff) {
          window.runtime.EventsOff(event)
        }
      }
    }
    // Fallback for dev mode without Wails runtime
    console.warn('bridge: Wails runtime not available, using CustomEvent fallback')
    const handler = (e) => callback(e.detail)
    window.addEventListener('bridge:' + event, handler)
    return () => window.removeEventListener('bridge:' + event, handler)
  },

  /**
   * 移除监听。
   */
  off(event, callback) {
    if (window.runtime?.EventsOff) {
      window.runtime.EventsOff(event)
    } else {
      window.removeEventListener('bridge:' + event, callback)
    }
  },
}

export default bridge
