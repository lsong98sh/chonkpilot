/**
 * Bridge — Wails Go → JS 事件通信层 + 前端 EventBus
 *
 * 内部维护回调注册表，每个事件名称只注册一个 Wails handler。
 * bridge.on('event', cb) 注册回调，返回的 unsub() 只移除自己的回调，
 * 不会影响其他组件对该事件的监听。
 * bridge.emit('event', data) 用于前端组件间通信（EventBus），
 * 同时支持 Wails runtime 事件和前端自定义事件。
 */

// 回调注册表: { eventName: Set<callback> }
const _callbackRegistry = {}

// 是否为每个事件注册过 Wails 级别的 handler
function ensureWailsHandler(event) {
  if (_callbackRegistry[event]?._wailsRegistered) return
  if (!_callbackRegistry[event]) {
    _callbackRegistry[event] = new Set()
  }
  _callbackRegistry[event]._wailsRegistered = true

  if (window.runtime?.EventsOn) {
    window.runtime.EventsOn(event, (data) => {
      _callbackRegistry[event].forEach(cb => {
        try { cb(data) } catch (e) { console.warn('[bridge] handler error:', e) }
      })
    })
  }
}

function needsFallback() {
  return !window.runtime?.EventsOn
}

function ensureFallbackHandler(event) {
  if (_callbackRegistry[event]?._fallbackRegistered) return
  if (!_callbackRegistry[event]) {
    _callbackRegistry[event] = new Set()
  }
  _callbackRegistry[event]._fallbackRegistered = true

  if (needsFallback()) {
    const handler = (e) => {
      _callbackRegistry[event].forEach(cb => {
        try { cb(e.detail) } catch (err) { console.warn('[bridge] fallback handler error:', err) }
      })
    }
    window.addEventListener('bridge:' + event, handler)
    // 存储 handler 引用以便移除
    _callbackRegistry[event]._fallbackHandler = handler
  }
}

const bridge = {
  /**
   * 监听事件。返回取消监听的函数，调用后只移除当前回调。
   */
  on(event, callback) {
    if (needsFallback()) {
      ensureFallbackHandler(event)
    } else {
      ensureWailsHandler(event)
    }

    _callbackRegistry[event].add(callback)

    // 返回 unsub 函数，只移除自己的回调
    return () => {
      _callbackRegistry[event]?.delete(callback)
      // 没有回调了 → 清理 Wails handler
      if (_callbackRegistry[event]?.size === 0) {
        if (!needsFallback() && window.runtime?.EventsOff) {
          window.runtime.EventsOff(event)
        }
        delete _callbackRegistry[event]
      }
    }
  },

  /**
   * 移除特定回调（兼容旧用法）。
   */
  off(event, callback) {
    _callbackRegistry[event]?.delete(callback)
    if (_callbackRegistry[event]?.size === 0) {
      if (!needsFallback() && window.runtime?.EventsOff) {
        window.runtime.EventsOff(event)
      }
      delete _callbackRegistry[event]
    }
  },

  /**
   * 前端 EventBus — 组件间通信
   * 向所有注册了该事件的回调广播数据，无需经过 Wails runtime。
   * 支持所有通过 bridge.on() 注册的回调（包括 Wails 事件和纯前端事件）。
   */
  emit(event, data) {
    const cbs = _callbackRegistry[event]
    if (!cbs || cbs.size === 0) return
    cbs.forEach(cb => {
      try { cb(data) } catch (e) { console.warn('[bridge] emit handler error:', e) }
    })
  },
}

export default bridge