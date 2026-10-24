/**
 * ck-mq.js —— 插件 iframe 的受控 mq 入口（PLUGIN-UI-1 第二段）
 *
 * 用法（插件页引入即可，宿主注入前后都能调用）：
 *   <script src="/shared/ck-mq.js"></script>
 *   ckMq.on('plugin-status', (p) => { ... })
 *   ckMq.emit('plugin-list', {}).then((res) => { ... })
 *
 * 宿主在 iframe `load` 后注入实现（调用 `window.__ckMqBind({on,off,emit})`）：本文件在注入前
 * 把 on/off/emit **排队**，注入后按调用序补投（订阅立即生效、emit 立即发送）——插件页无需等 load。
 *
 * 暴露面**只有** on / off / emit（宿主不透出整个 mq 对象）。
 * 退订由宿主统一负责（frame 重载 / 组件卸载 / 页面 beforeunload 时全部 off）。
 */
(function () {
  var HOST = null
  var queue = []

  function flush() {
    while (queue.length) {
      var it = queue.shift()
      try {
        it.fn()
      } catch (e) {
        console.warn('[ck-mq] flush error:', e)
      }
    }
  }

  var ckMq = {
    /** 订阅宿主 mq 主题；返回取消订阅函数（注入前排队，注入后立即生效）。 */
    on: function (topic, cb) {
      if (HOST) return HOST.on(topic, cb)
      queue.push({ fn: function () { return ckMq.on(topic, cb) } })
      return function () { ckMq.off(topic, cb) }
    },
    /** 取消订阅：带 cb 只撤该回调，不带 cb 撤该主题全部。 */
    off: function (topic, cb) {
      if (HOST) return HOST.off(topic, cb)
      queue.push({ fn: function () { return ckMq.off(topic, cb) } })
    },
    /** 发布一条 mq 消息（返回宿主 `{payload, results, backend}` 信封的 Promise）。 */
    emit: function (topic, payload) {
      if (HOST) return HOST.emit(topic, payload)
      return new Promise(function (resolve) {
        queue.push({ fn: function () { Promise.resolve(ckMq.emit(topic, payload)).then(resolve) } })
      })
    },
  }

  // 宿主注入点：绑定实现后立即补投排队调用。
  window.__ckMqBind = function (impl) {
    HOST = impl
    flush()
  }
  window.ckMq = ckMq
})()
