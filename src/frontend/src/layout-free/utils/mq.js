/**
 * MQ — 对称消息中心（publish/on promise 语义）
 *
 * 用法（与 chonkpilot 同构）：
 *   mq.on('layout-open-dialog', (d) => openDialog(d))   // 订阅（返回取消订阅函数）
 *   mq.emit('layout-open-dialog', { viewKey, instanceKey, title })  // 发布
 *   await mq.emit(...)  // 收集订阅者返回值 → { payload, results }
 *
 * 操作生命周期：pre → on(main) → post/error → final。
 *   mq.pre('editor-save', showLoading)
 *   mq.on('editor-save', doSave)
 *   mq.post('editor-save', showSuccess)
 *   mq.emit('editor-save', payload)
 *
 * Vue 指令 v-mq（本工程**所有点击/交互一律走它**，模板里不出现 @click）：
 *   v-mq:[topic].[phase].[stop].[prevent].[once]="payload|handler"
 *   - .on/.pre/.post/.error/.final → 订阅模式，绑定值为处理函数；
 *   - 其它（缺省 .click）→ 触发模式，绑定值为 payload，DOM 事件触发 mq.emit(topic, payload)。
 *
 * 该端为纯前端（无后端），emit 只做本地分发；保留 publishToBackend 钩子，
 * 需要接后端时替换实现即可（调用方语义不变）。
 */

// ── 注册表: { topic: { pre:Set, on:[{cb,order}], post:Set, error:Set, final:Set } } ──
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
    try {
      return JSON.parse(payload)
    } catch {
      return payload
    }
  }
  return payload
}

/**
 * 发给后端：预留钩子。纯前端形态直接 resolve(null)（事件类调用方忽略返回值）。
 * 接入后端时改为 POST /publish 并解析结果信封 { ok, result, errors }。
 */
function publishToBackend() {
  return Promise.resolve(null)
}

/** 按 (order, 注册序) 收集 on 回调。 */
function orderedOnCallbacks(entry) {
  if (!entry || entry.on.length === 0) return []
  return entry.on
    .map((x, i) => ({ cb: x.cb, order: x.order, seq: i }))
    .sort((a, b) => (a.order !== b.order ? a.order - b.order : a.seq - b.seq))
    .map((x) => x.cb)
}

/** dispatch 把 payload 按 order 分发给 topic 的 on 回调。 */
function dispatch(topic, payload) {
  const entry = _registry[topic]
  if (!entry) return
  for (const cb of orderedOnCallbacks(entry)) {
    try {
      cb(payload)
    } catch (e) {
      console.warn('[mq] dispatch error:', topic, e)
    }
  }
}

const mq = {
  /** 外部（宿主/其它端）灌入事件：只做本地 dispatch，不再发出（防环）。 */
  emitRemote(envelope) {
    if (!envelope || typeof envelope !== 'object') return
    const topic = envelope.type || envelope.Type
    const payload = normalizePayload(envelope.payload !== undefined ? envelope.payload : envelope.Payload)
    dispatch(topic, payload)
  },

  /** 注册 pre 阶段回调（操作开始前）。 */
  pre(topic, callback) {
    getTopic(topic).pre.add(callback)
    return () => getTopic(topic).pre.delete(callback)
  },

  /**
   * 注册 main 阶段回调（核心操作/事件处理），支持执行顺序：
   *   mq.on(topic, cb, order) —— order 升序执行（同 order 按注册序）；缺省 order=0。
   * 返回取消监听的函数。
   */
  on(topic, callback, order = 0) {
    const entry = getTopic(topic)
    const idx = entry.on.findIndex((x) => x.cb === callback)
    if (idx >= 0) {
      entry.on[idx].order = order // 重复注册 → 仅更新 order
    } else {
      entry.on.push({ cb: callback, order })
    }
    return () => {
      const i = entry.on.findIndex((x) => x.cb === callback)
      if (i >= 0) entry.on.splice(i, 1)
    }
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
   * 返回 { payload, results, backend }（await = 收集回复；不 await = 事件）。
   */
  async emit(topic, payload, context) {
    const backendP = publishToBackend(topic, payload, context)
    const entry = _registry[topic]
    if (!entry) {
      const backend = await backendP
      return { payload, results: [], backend }
    }

    const results = []
    let hasError = false

    for (const cb of entry.pre) {
      try {
        await cb(payload, context)
      } catch (e) {
        console.warn('[mq] pre error:', topic, e)
      }
    }

    // async 包装：把 cb 的同步抛出也归一为 rejected promise。
    const onPromises = orderedOnCallbacks(entry).map((cb) => (async () => cb(payload, context))())

    if (onPromises.length > 0) {
      const settled = await Promise.allSettled(onPromises)
      for (const r of settled) {
        if (r.status === 'fulfilled') results.push(r.value)
        else {
          hasError = true
          console.warn('[mq] on error:', topic, r.reason)
        }
      }
    }

    if (hasError) {
      for (const cb of entry.error) {
        try {
          await cb(payload, context)
        } catch (e) {
          console.warn('[mq] error handler error:', topic, e)
        }
      }
    } else {
      for (const cb of entry.post) {
        try {
          await cb(payload, context)
        } catch (e) {
          console.warn('[mq] post error:', topic, e)
        }
      }
    }

    for (const cb of entry.final) {
      try {
        await cb(payload, context)
      } catch (e) {
        console.warn('[mq] final error:', topic, e)
      }
    }

    const backend = await backendP
    return { payload, results, backend }
  },

  /** 移除特定回调（兼容旧用法）。 */
  off(topic, callback) {
    const entry = _registry[topic]
    if (!entry) return
    const i = entry.on.findIndex((x) => x.cb === callback)
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

  /** 调试：当前已注册的 topic 列表。 */
  topics() {
    return Object.keys(_registry)
  },
}

// ─── Vue 指令: v-mq ───
// 用法:
//   触发模式: v-mq:topic.click="payload"  →  监听 DOM 事件 → mq.emit(topic, payload)
//   订阅模式: v-mq:topic.on="handler"     →  mq.on(topic, handler)（挂载时注册，卸载时取消）
//   标准修饰符: .stop .prevent .once

const MQ_SUB_PHASES = ['on', 'pre', 'post', 'error', 'final']
const RESERVED_MODS = [...MQ_SUB_PHASES, 'emit', 'once', 'stop', 'prevent']

const mqPlugin = {
  install(app) {
    app.directive('mq', {
      mounted(el, binding) {
        const topic = binding.arg
        if (!topic) {
          // 打印元素片段，便于定位是哪个模板漏了 topic
          console.warn('[v-mq] 缺少 topic 参数，用法: v-mq:topic-name.event', (el.outerHTML || '').slice(0, 160))
          return
        }

        // 订阅模式：挂载时注册一次
        const mods = Object.keys(binding.modifiers || {})
        const mqPhase = MQ_SUB_PHASES.find((p) => mods.includes(p))
        if (mqPhase) {
          const cb = binding.value
          if (typeof cb !== 'function') {
            console.warn(`[v-mq] phase "${mqPhase}" 需要一个函数作为值`)
            return
          }
          let unsub
          if (mods.includes('once')) {
            const onceCb = (...args) => {
              cb(...args)
              unsub()
            }
            unsub = mq[mqPhase](topic, onceCb)
          } else {
            unsub = mq[mqPhase](topic, cb)
          }
          el._mqCleanup = unsub
          return
        }

        const domEvent = mods.find((m) => !RESERVED_MODS.includes(m)) || 'click'

        if (mods.includes('prevent')) {
          el.addEventListener(domEvent, (e) => e.preventDefault())
        }
        if (mods.includes('stop')) {
          el.addEventListener(domEvent, (e) => e.stopPropagation())
        }

        // 关键：payload 在「事件时刻」从最新的 binding 读取（el._mqBinding 由 updated 刷新）。
        // 指令的 binding 对象每次更新都会重建/替换，若在 mounted 缓存 value，
        // 节点/条目切换后 payload 会指向旧对象（stale payload，写错目标）。
        el._mqBinding = binding
        const handler = (e) => {
          const current = el._mqBinding || binding
          const raw = current.value
          const payload = typeof raw === 'function' ? raw(e, current.instance) : raw
          mq.emit(topic, payload)
          if (mods.includes('once')) el.removeEventListener(domEvent, handler)
        }
        el._mqHandler = handler
        el.addEventListener(domEvent, handler)
        el._mqCleanup = () => el.removeEventListener(domEvent, handler)
      },

      updated(el, binding) {
        el._mqBinding = binding
      },

      unmounted(el) {
        if (typeof el._mqCleanup === 'function') el._mqCleanup()
      },
    })
  },
}

export default mq
export { mqPlugin }
