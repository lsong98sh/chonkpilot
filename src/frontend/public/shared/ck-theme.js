/**
 * ck-theme.js —— 公共前端资产 · 主题运行态（THEME-1，2026-10-23）
 *
 * **无依赖经典脚本**（IIFE，禁止 import/export/type=module）：宿主页面与插件 webui 都用
 *   <script src="/shared/ck-theme.js"></script>
 * 同步加载（**不得** defer/async），以便在首帧前把 `data-theme` 落实，避免闪白。
 *
 * 对外 API（挂在 `window.ckTheme`）：
 *   version     字符串常量
 *   themes      主题 id 数组（DOMContentLoaded 后从同源样式表的 `[data-theme="x"]` 选择器发现；
 *               `light` 是 `:root` 隐式默认，恒定在首位）
 *   current()   当前主题 id
 *   apply(id, {persist, scheme, tokens})  切换主题（persist=true 时写 localStorage）
 *   subscribe(cb) 订阅变更，返回退订函数（cb 收到 snapshot()）
 *   snapshot()  {id, scheme, tokens}
 *   boot()      脚本加载时自动执行（宿主读 localStorage；iframe 用 prefers-color-scheme 兜底
 *               并监听宿主 postMessage）
 *
 * postMessage 协议（沿用 ckPlugin 信封，见 components/plugin/PluginPageHost.vue）：
 *   宿主 → 插件 iframe：{ckPlugin:1, type:'theme', theme:{id, scheme, tokens}}
 *   插件 iframe 收到后把 tokens 拼成 `:root{...}` 注入 `#ck-theme` style 元素（覆盖默认值）。
 *   （sandbox 无 allow-same-origin → opaque origin，访问 localStorage 会抛 SecurityError，
 *    故 iframe 侧一律不落盘，只用 matchMedia + 宿主消息。）
 */
;(function () {
  'use strict'

  var VERSION = '1'
  var STORAGE_KEY = 'chonkpilot-theme'
  var STYLE_ID = 'ck-theme'

  /** snapshot 里暴露的 token 白名单（与前端 utils/themeResolve.js 的 THEME_TOKENS 一一对应） */
  var THEME_TOKENS = [
    '--ck-bg',
    '--ck-surface',
    '--ck-border',
    '--ck-text',
    '--ck-text-muted',
    '--ck-accent',
    '--ck-accent-bg',
    '--ck-success',
    '--ck-warning',
    '--ck-danger',
    '--ck-radius',
    '--ck-font-sans',
    '--ck-font-mono',
  ]

  var KNOWN_DARK = { dark: 1, nord: 1, dim: 1, black: 1 }

  var subscribers = []
  var currentId = 'light'
  var themes = ['light']
  var hostSynced = false

  function isEmbedded() {
    try {
      return window.parent && window.parent !== window
    } catch (e) {
      return true
    }
  }

  function schemeOf(id) {
    if (!id) return 'light'
    if (KNOWN_DARK[id]) return 'dark'
    return /(^|[-_])(dark|nord|night|dim|black)($|[-_])/i.test(String(id)) ? 'dark' : 'light'
  }

  function hasDom() {
    return typeof document !== 'undefined' && !!document.documentElement
  }

  function readTokens() {
    var out = {}
    if (!hasDom() || typeof getComputedStyle !== 'function') return out
    var cs
    try {
      cs = getComputedStyle(document.documentElement)
    } catch (e) {
      return out
    }
    for (var i = 0; i < THEME_TOKENS.length; i++) {
      var name = THEME_TOKENS[i]
      var v = ''
      try {
        v = cs.getPropertyValue(name)
      } catch (e) {
        v = ''
      }
      out[name] = (v || '').trim()
    }
    return out
  }

  function snapshot() {
    return { id: currentId, scheme: schemeOf(currentId), tokens: readTokens() }
  }

  function notify() {
    var snap = snapshot()
    var list = subscribers.slice()
    for (var i = 0; i < list.length; i++) {
      try {
        list[i](snap)
      } catch (e) {
        /* 订阅者异常不影响其它订阅者 */
      }
    }
    return snap
  }

  /** 把宿主下发的 tokens 注入 `#ck-theme`（覆盖 themes.css 的默认值）。 */
  function injectTokens(tokens) {
    if (!hasDom() || !tokens) return
    var parts = []
    for (var name in tokens) {
      if (!Object.prototype.hasOwnProperty.call(tokens, name)) continue
      if (name.indexOf('--ck-') !== 0) continue
      if (!tokens[name]) continue
      parts.push(name + ':' + tokens[name])
    }
    if (!parts.length) return
    var el = document.getElementById(STYLE_ID)
    if (!el) {
      el = document.createElement('style')
      el.id = STYLE_ID
      ;(document.head || document.documentElement).appendChild(el)
    }
    el.textContent = ':root{' + parts.join(';') + '}'
  }

  /**
   * 切换主题（唯一 DOM 写点之一）。
   * opts.persist（仅宿主有意义）写 localStorage；
   * opts.scheme 跳过推断直接指定（宿主下发时用）；
   * opts.tokens 存在时额外注入（iframe 侧用）。
   */
  function apply(id, opts) {
    opts = opts || {}
    if (!id || typeof id !== 'string') return snapshot()
    currentId = id
    var scheme = opts.scheme || schemeOf(id)
    if (hasDom()) {
      try {
        document.documentElement.setAttribute('data-theme', id)
        var st = document.documentElement.style
        if (st) st.colorScheme = scheme
      } catch (e) {
        /* DOM 不可写时静默（不影响快照） */
      }
    }
    if (opts.persist) {
      try {
        localStorage.setItem(STORAGE_KEY, id)
      } catch (e) {
        /* 沙箱 / 隐私模式：忽略 */
      }
    }
    if (opts.tokens) injectTokens(opts.tokens)
    return notify()
  }

  function current() {
    return currentId
  }

  function subscribe(cb) {
    if (typeof cb !== 'function') return function () {}
    subscribers.push(cb)
    return function () {
      var i = subscribers.indexOf(cb)
      if (i >= 0) subscribers.splice(i, 1)
    }
  }

  /** 从同源样式表发现 `[data-theme="x"]` 主题（跨源表读 cssRules 会抛 → 忽略）。 */
  function scanThemes() {
    if (typeof document === 'undefined') return themes
    var found = ['light']
    var sheets = document.styleSheets || []
    for (var i = 0; i < sheets.length; i++) {
      var rules
      try {
        rules = sheets[i].cssRules
      } catch (e) {
        continue
      }
      if (!rules) continue
      collectFromRules(rules, found)
    }
    // 原地更新，保证早先拿到 themes 引用的调用方也能看到新值
    themes.length = 0
    for (var j = 0; j < found.length; j++) themes.push(found[j])
    return themes
  }

  function collectFromRules(rules, found) {
    for (var i = 0; i < rules.length; i++) {
      var rule = rules[i]
      if (!rule) continue
      if (rule.selectorText) {
        var re = /\[data-theme\s*=\s*["']?([A-Za-z0-9_-]+)["']?\]/g
        var m
        while ((m = re.exec(rule.selectorText)) !== null) {
          if (found.indexOf(m[1]) < 0) found.push(m[1])
        }
      }
      // @media / @supports 等分组规则：递归
      if (rule.cssRules && rule.cssRules.length) collectFromRules(rule.cssRules, found)
    }
  }

  function onHostMessage(e) {
    if (!isEmbedded()) return
    try {
      if (e.source && e.source !== window.parent) return
    } catch (err) {
      /* opaque origin 下比较失败 → 继续按信封校验 */
    }
    var d = e.data
    if (!d || d.ckPlugin !== 1 || d.type !== 'theme' || !d.theme) return
    hostSynced = true
    apply(d.theme.id || 'light', {
      persist: false,
      scheme: d.theme.scheme,
      tokens: d.theme.tokens,
    })
  }

  /** 启动：宿主读 localStorage（首帧前）；iframe 用系统配色兜底并监听宿主消息。 */
  function boot() {
    scanThemes()
    if (isEmbedded()) {
      var dark = false
      try {
        dark = !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)
      } catch (e) {
        dark = false
      }
      apply(dark ? 'dark' : 'light', { persist: false })
      try {
        var mq = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)')
        if (mq && typeof mq.addEventListener === 'function') {
          mq.addEventListener('change', function (ev) {
            if (hostSynced) return // 宿主已下发主题 → 以宿主为准，忽略系统配色变化
            apply(ev && ev.matches ? 'dark' : 'light', { persist: false })
          })
        }
      } catch (e) {
        /* 老引擎无 addEventListener：忽略 */
      }
      try {
        window.addEventListener('message', onHostMessage)
      } catch (e) {
        /* 忽略 */
      }
      return
    }
    var saved = ''
    try {
      saved = localStorage.getItem(STORAGE_KEY) || ''
    } catch (e) {
      saved = ''
    }
    apply(saved || currentId, { persist: false })
  }

  window.ckTheme = {
    version: VERSION,
    themes: themes,
    tokens: THEME_TOKENS,
    current: current,
    apply: apply,
    subscribe: subscribe,
    snapshot: snapshot,
    boot: boot,
    scan: scanThemes,
  }

  boot()

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', function () {
        scanThemes()
      })
    } else {
      scanThemes()
    }
  }
})()
