/**
 * HTTP/SSE 版桥 shim（browser 形态；19 §8 方案 A 的最小切片 D）。
 *
 * GUI 形态：后端经 WebView2 `w.Eval` 调 `window.mq.emitRemote(envelope)`（下行）；
 * browser 形态：本 shim 打开 SSE（`GET /events`）并把同一信封投到**同一入口**
 * `window.mq.emitRemote` —— 前端源码与既有 mq 客户端**零改动**。
 *
 * 上行不变：前端 `mq.emit` → `fetch('/publish')`（mq.js 既有实现，相对路径）。
 * 实例 id 由服务端在 index.html 内联注入（`window.__chonkpilotInstanceId`，与 GUI 同法），
 * 本 shim 不负责注入；instance 过滤在服务端完成（复用桥的 acceptEventInstance 判据）。
 *
 * 加载方式：服务端提供 `/__ck_bridge.js` 并在 index.html 的 `<head>` 后插入
 * `<script src="./__ck_bridge.js"></script>`（经典脚本先于模块脚本执行）。
 */
(function () {
  'use strict'
  if (window.__ckHttpBridge) return
  window.__ckHttpBridge = true

  // 模块脚本（window.mq 由 main.js 挂载）尚未执行时暂存事件，就绪后按序补投。
  var pending = []
  var timer = 0

  function ready() {
    return !!(window.mq && typeof window.mq.emitRemote === 'function')
  }

  function dispatch(env) {
    try {
      window.mq.emitRemote(env)
    } catch (e) {
      console.warn('[ck-http-bridge] dispatch error:', e)
    }
  }

  function flush() {
    timer = 0
    if (!ready()) {
      if (pending.length) arm()
      return
    }
    var queue = pending
    pending = []
    for (var i = 0; i < queue.length; i++) dispatch(queue[i])
  }

  function arm() {
    if (!timer) timer = setTimeout(flush, 50)
  }

  function deliver(env) {
    if (ready()) {
      dispatch(env)
      return
    }
    pending.push(env)
    arm()
  }

  // SSE：服务端每条事件为一行 `data: <信封 JSON>`（默认 event = message）。
  var es = new EventSource('/events')
  es.onmessage = function (e) {
    if (!e || !e.data) return
    var env
    try {
      env = JSON.parse(e.data)
    } catch (_) {
      return
    }
    deliver(env)
  }
  // es.onerror：EventSource 自带重连（服务端 20s 发一次注释行保活），不额外处理。
})()
