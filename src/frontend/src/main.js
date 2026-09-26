import { createApp } from 'vue'
import App from './App.vue'
import './assets/styles/global.css'
import { i18n } from './plugins/i18n'
import { logError } from './api/log'
import mq, { mqPlugin } from './utils/mq'
import { dialog } from './components/dialog'

// Go 端广播经 Eval 调用 window.mq.emitRemote(envelope)。
window.mq = mq

const app = createApp(App)
app.use(i18n)
app.use(mqPlugin)
app.mount('#app')

// Pass main app context to DialogManager so dialog content
// inherits all plugins (i18n, mqPlugin, etc.)
dialog.setApp(app)

// ─── Disable webview zoom (Ctrl+wheel / Ctrl+'+'/Ctrl+'-') ───
window.addEventListener('wheel', (e) => {
  if (e.ctrlKey || e.metaKey) { e.preventDefault() }
}, { passive: false })
window.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && (e.key === '=' || e.key === '-' || e.key === '0')) {
    e.preventDefault()
  }
})
document.addEventListener('gesturestart', (e) => e.preventDefault())

// ─── Global error handlers ───

// Vue 组件渲染错误 — 同时记录到日志文件
app.config.errorHandler = (err, vm, info) => {
  logError('Vue.errorHandler', err)
  console.error('[Vue error]', err, info)
}

// 全局 JS 错误 — 同时记录到日志文件
window.addEventListener('error', (event) => {
  const msg = event.error?.message || event.message || 'Unknown error'
  const stack = event.error?.stack || ''
  logError('window.onerror', event.error || event)
  console.error('[Global error]', msg, stack)
})

// 未处理 Promise 错误
window.addEventListener('unhandledrejection', (event) => {
  const msg = event.reason?.message || event.reason || 'Unknown rejection'
  const stack = event.reason?.stack || ''
  logError('window.unhandledrejection', event.reason)
  console.error('[Unhandled rejection]', msg, stack)
})
