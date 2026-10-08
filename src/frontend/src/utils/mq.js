/**
 * MQ — 对称消息中心（前后端统一 publish/on promise 语义；对齐 61-消息一览 §0）
 *
 * 后端 → 前端：Go 事件经 Eval 直调 window.mq.emitRemote(envelope)，信封为
 *   { type, payload(JSON 字符串), src, event_id, time }；
 *   emitRemote 解析 payload 后按 type 分发（同 order 有序）。
 * 前端 → 后端：mq.emit(topic, payload) = 唯一发送方式（publish）。
 *   本地按 order 分发 on 订阅者（一疑问多答，可各自写回）+ 经 fetch POST /publish 发后端；
 *   HTTP 响应 = 后端结果信封 { ok, result, errors }（errors 收集、恒 accept，发送端自查）。
 *   await mq.emit(...) = 收集回复（{ payload, results, backend }）；
 *   不 await = 事件（fire-and-forget）。无 request/RPC 方法（61-消息一览 §0.1）。
 *
 * 操作生命周期（本地请求-响应）：pre → on(main) → post/error → final。
 * 示例：
 *   mq.pre('config-save', showLoading)
 *   mq.on('config-save', async (d) => await saveConfigLocal(d))
 *   mq.post('config-save', showSuccess)
 *   mq.emit('config-save', payload)
 *
 * Vue 指令 v-mq：
 *   v-mq:[topic].[phase].[stop].[prevent].[once]="handler"
 *   phase 默认为 on（订阅）；emit 触发 mq.emit。
 */

import { currentInstanceId } from './instanceState.js'

// 回调注册表: { topic: { pre:Set, on:[{cb,order}], post:Set, error:Set, final:Set } }
const _registry = {}

function getTopic(topic) {
  if (!_registry[topic]) {
    _registry[topic] = {
      pre: new Set(),
      on: [], // 数组：保注册序；{ cb, order }
      post: new Set(),
      error: new Set(),
      final: new Set(),
    }
  }
  return _registry[topic]
}

/** 将 payload 统一为可 dispatch 的值：跨端传输的 payload 是 JSON 字符串，自行解析。 */
function normalizePayload(payload) {
  if (typeof payload === 'string' && payload !== '') {
    try { return JSON.parse(payload) } catch (e) { return payload }
  }
  return payload
}

/**
 * 实例 id：**认领优先**（`instance-claim` 应答，服务端绑定为准），未认领时回落首屏注入的
 * `window.__chonkpilotInstanceId`（GUI 桥 / httpapi 在 index.html 内联注入）。
 * 业务 payload 一律必带 instance_id（61-消息一览 §0 第 21/45 行）；唯一豁免 topic =
 * server-starting（服务器级就绪广播，发布时实例尚未注册）。
 * 实现见 utils/instanceState.js（叶子模块，避免与 mq 循环依赖）；此处 re-export 兼容既有引用。
 */
export { currentInstanceId }

/** 是否普通对象（排除 null / 数组 / Date 等其它对象）。 */
function isPlainObject(v) {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return false
  const proto = Object.getPrototypeOf(v)
  return proto === Object.prototype || proto === null
}

/**
 * 给"前端 → 后端 / 纯前端"事件 payload 统一补 instance_id（前端 mq.emit 统一注入）：
 *   - 已存在 instance_id → 原样返回（不覆盖）；
 *   - 非普通对象（undefined / null / 字符串 / 数组…）或无实例 id → 原样返回（不强行包装）；
 *   - 需注入 → 返回浅拷贝（不改调用方传入的引用），本地分发与后端发布共用同一份。
 */
function withInstanceId(topic, payload) {
  if (topic === 'server-starting') return payload
  if (!isPlainObject(payload)) return payload
  if (payload.instance_id) return payload
  const id = currentInstanceId()
  if (!id) return payload
  return { ...payload, instance_id: id }
}

/**
 * 发给后端：POST /publish，解析结果信封 { ok, result, errors }。
 * 网络失败 → resolve(null)（发送端自查；事件类调用方忽略）。
 */
function publishToBackend(topic, payload, opts = {}) {
  let str = ''
  if (payload !== undefined && payload !== null) {
    str = typeof payload === 'string' ? payload : JSON.stringify(payload)
  }
  // 超时 AbortController：保存定时器句柄，请求收尾（finally）clearTimeout——
  // 否则计时器在请求完成后仍存活（默认 15s/30s），高频请求累积、可能对已完成请求再 abort（E5-⑤）。
  let ctl
  let timer
  if (typeof AbortController !== 'undefined' && opts.timeout) {
    ctl = new AbortController()
    timer = setTimeout(() => ctl.abort(), opts.timeout)
  }
  return fetch('/publish', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ type: topic, payload: str }),
    signal: ctl && ctl.signal,
  }).then(async (r) => {
    // HTTP 状态非 2xx（r.ok === false）→ 失败信封（不再误判成功）；
    // 2xx 但应答非 JSON（网关 HTML/文本错误页等）→ 同样回失败信封并带上状态码（E-02）。
    if (r.ok === false) {
      return { ok: false, result: null, errors: ['HTTP ' + r.status + ' non-JSON response'] }
    }
    try {
      return await r.json()
    } catch (e) {
      return { ok: false, result: null, errors: ['HTTP ' + r.status + ' non-JSON response'] }
    }
  }).catch((e) => {
    console.warn('[mq] publish to backend error:', e)
    return null
  }).finally(() => {
    if (timer) clearTimeout(timer)
  })
}

/** 按 (order, 注册序) 收集 on 回调；返回该 topic 的 cb 快照数组（顺序已排好）。 */
function orderedOnCallbacks(entry) {
  if (!entry || entry.on.length === 0) return []
  return entry.on.map((x, i) => ({ cb: x.cb, order: x.order, seq: i }))
    .sort((a, b) => (a.order !== b.order ? a.order - b.order : a.seq - b.seq))
    .map(x => x.cb)
}

/**
 * 后端 → 前端唯一入口：Go 端广播（EventBus → Eval）把 WireEvent 信封传进来，
 * 按 type 分发给对应 topic 的 on 回调（回调参数为解析后的 payload）。
 * llm-receive 拆分：主主题分发后再按 payload.type 二次分发子主题。
 */
function emitRemote(envelope) {
  if (!envelope || typeof envelope !== 'object') return
  const topic = envelope.type || envelope.Type
  const payload = normalizePayload(envelope.payload !== undefined ? envelope.payload : envelope.Payload)
  dispatch(topic, payload)
  if (topic === 'llm-receive' && payload && typeof payload === 'object' && payload.type) {
    dispatch(topic + '.' + payload.type, payload)
  }
}

/** dispatch 把 payload 按 order 分发给 topic 的 on 回调（emitRemote 内部用）。 */
function dispatch(topic, payload) {
  const entry = _registry[topic]
  if (!entry) return
  for (const cb of orderedOnCallbacks(entry)) {
    try { cb(payload) } catch (e) { console.warn('[mq] remote event error:', e) }
  }
}

const mq = {
  /**
   * 后端 → 前端入口（Go 广播经 Eval 调用本方法）。只做本地 dispatch，不再发回后端（防环）。
   */
  emitRemote,

  /** 注册 pre 阶段回调（操作开始前）。 */
  pre(topic, callback) {
    getTopic(topic).pre.add(callback)
    return () => getTopic(topic).pre.delete(callback)
  },

  /**
   * 注册 main 阶段回调（核心操作/事件处理），支持执行顺序：
   *   mq.on(topic, cb, order) —— order 升序执行（同 order 按注册序）；缺省 order=0。
   * 对 Go 流事件，这是唯一需要注册的阶段；对操作生命周期，这是主要业务逻辑。
   * 返回取消监听的函数。
   */
  on(topic, callback, order = 0) {
    const entry = getTopic(topic)
    const idx = entry.on.findIndex(x => x.cb === callback)
    if (idx >= 0) {
      entry.on[idx].order = order // 重复注册 → 仅更新 order
    } else {
      entry.on.push({ cb: callback, order })
    }
    const unsub = () => {
      const i = entry.on.findIndex(x => x.cb === callback)
      if (i >= 0) entry.on.splice(i, 1)
    }
    return unsub
  },

  /** 注册 post 阶段回调（操作成功完成后）。 */
  post(topic, callback) {
    getTopic(topic).post.add(callback)
    return () => getTopic(topic).post.delete(callback)
  },

  /** 注册 error 阶段回调（操作出错时）。 */
  error(topic, callback) {
    getTopic(topic).error.add(callback)
    return () => getTopic(topic).error.delete(callback)
  },

  /** 注册 final 阶段回调（无论成功失败，始终执行）。 */
  final(topic, callback) {
    getTopic(topic).final.add(callback)
    return () => getTopic(topic).final.delete(callback)
  },

  /**
   * 统一 publish：本地按 order 分发 pre/on/post|error/final + 发后端，
   * 返回 { payload, results, backend }（await = 收集回复；不 await = 事件）：
   *   - results：本地 on 回调返回值数组（一疑问多答，多订阅者各自写回）；
   *   - backend：后端结果信封 { ok, result, errors }（网络失败 = null）。
   * 查询类（data-* / filesys.* / gui.* 读写）用 await 收集；事件类忽略返回值。
   */
  async emit(topic, payload, context) {
    // 统一注入 instance_id（纯前端事件 + 路由到后端的事件；已存在不覆盖，非对象原样）。
    payload = withInstanceId(topic, payload)
    const backendP = publishToBackend(topic, payload, context && context.timeout ? { timeout: context.timeout } : undefined)
    const entry = _registry[topic]
    if (!entry) {
      const backend = await backendP
      return { payload, results: [], backend }
    }

    const results = []
    let hasError = false

    for (const cb of entry.pre) {
      try { await cb(payload, context) } catch (e) { console.warn('[mq] pre error:', e) }
    }

    // async 包装：把 cb 的同步抛出也归一为 rejected promise（Promise.allSettled 统一收集）。
    const onPromises = orderedOnCallbacks(entry).map(cb => (async () => cb(payload, context))())

    if (onPromises.length > 0) {
      const settled = await Promise.allSettled(onPromises)
      for (const r of settled) {
        if (r.status === 'fulfilled') {
          results.push(r.value)
        } else {
          hasError = true
          console.warn('[mq] on error:', r.reason)
        }
      }
    }

    if (hasError) {
      for (const cb of entry.error) {
        try { await cb(payload, context) } catch (e) { console.warn('[mq] error handler error:', e) }
      }
    } else {
      for (const cb of entry.post) {
        try { await cb(payload, context) } catch (e) { console.warn('[mq] post error:', e) }
      }
    }

    for (const cb of entry.final) {
      try { await cb(payload, context) } catch (e) { console.warn('[mq] final error:', e) }
    }

    const backend = await backendP
    return { payload, results, backend }
  },

  /** 移除特定回调（兼容旧用法）。 */
  off(topic, callback) {
    const entry = _registry[topic]
    if (!entry) return
    const i = entry.on.findIndex(x => x.cb === callback)
    if (i >= 0) entry.on.splice(i, 1)
  },

  /** 移除 topic 的所有回调。 */
  clear(topic) {
    const entry = _registry[topic]
    if (!entry) return
    entry.pre.clear()
    entry.on = []
    entry.post.clear()
    entry.error.clear()
    entry.final.clear()
    delete _registry[topic]
  },
}

// ─── Vue 指令: v-mq ───
// 用法:
//   触发模式: v-mq:topic.click="payload"  →  监听 DOM 事件 → mq.emit(topic, payload)
//   订阅模式: v-mq:topic.on="handler"      →  mq.on(topic, handler)（挂载时注册，卸载时取消）
//   标准修饰符: .stop .prevent .once

const MQ_SUB_PHASES = ['on', 'pre', 'post', 'error', 'final']
const RESERVED_MODS = [...MQ_SUB_PHASES, 'emit', 'once', 'stop', 'prevent']

const mqPlugin = {
  install(app) {
    app.directive('mq', {
      mounted(el, binding) {
        const topic = binding.arg
        if (!topic) {
          console.warn('[v-mq] 缺少 topic 参数，用法: v-mq:topic-name.event')
          return
        }

        const mods = Object.keys(binding.modifiers || {})

        // 1) 先检查 MQ 订阅阶段
        const mqPhase = MQ_SUB_PHASES.find(p => mods.includes(p))

        if (mqPhase) {
          // ── 订阅模式：mq.on / mq.pre / mq.post / mq.error / mq.final ──
          const cb = binding.value
          if (typeof cb !== 'function') {
            console.warn(`[v-mq] phase "${mqPhase}" 需要一个函数作为值`)
            return
          }

          let unsub
          if (mods.includes('once')) {
            const onceCb = (...args) => { cb(...args); unsub() }
            unsub = mq[mqPhase](topic, onceCb)
          } else {
            unsub = mq[mqPhase](topic, cb)
          }
          el._mqCleanup = unsub
          return
        }

        // 2) 触发模式：查找 DOM 事件名修饰符
        //    优先取第一个非保留字的 modifier，兼容 .emit（当作 .click）
        const domEvent = mods.find(m => !RESERVED_MODS.includes(m)) || 'click'

        // stop / prevent 用相同的 DOM 事件
        if (mods.includes('prevent')) {
          el.addEventListener(domEvent, (e) => e.preventDefault())
        }
        if (mods.includes('stop')) {
          el.addEventListener(domEvent, (e) => e.stopPropagation())
        }

        const handler = () => {
          mq.emit(topic, binding.value)
          if (mods.includes('once')) {
            el.removeEventListener(domEvent, handler)
          }
        }
        el.addEventListener(domEvent, handler)
        el._mqCleanup = () => el.removeEventListener(domEvent, handler)
      },

      unmounted(el) {
        if (typeof el._mqCleanup === 'function') {
          el._mqCleanup()
        }
      },
    })
  },
}

export default mq
export { mqPlugin }
