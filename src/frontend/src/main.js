import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './assets/styles/global.css'
// 自由布局容器主题映射（UILAYOUT-1 §3.10 / P3④）：`--lf-*` → 宿主既有 token（三主题自动跟随）
import '@ckfw/layout/theme.css'
import { i18n } from './plugins/i18n'
import { logError } from './api/log'
import mq, { mqPlugin } from '@ckfw/utils/mq.js'
import { dialog } from '@ckfw/components/dialog/index.js'
import { setHostDeps } from '@ckfw/host-deps.js'
import { registerSelectionSubscribers } from './stores/selectionSubscribers'
import { setupLayoutEngine } from '@ckfw/layout/index.js'
// 业务清单（TOPO-1 §2.22.2「机制在基座、清单在应用」）：视图注册表 / 布局落库 / 新建布局对话框 / 顶栏动作
import { createViewRegistry } from './layout/views.registry.js'
import { loadInitData, saveLayoutState } from './api/file'
import LayoutNewDialog from './layout/bar-views/LayoutNewDialog.vue'
import { installBarActions } from './layout/barActions.js'
import { initPluginUIHost, initPlugins } from './pluginHost'

// Go 端广播经 Eval 调用 window.mq.emitRemote(envelope)。
window.mq = mq

// framework 宿主端口注入（唯一外部耦合点）：framework 不 import 主仓任何业务模块
setHostDeps({
  i18n,
  views: (translate) => createViewRegistry(translate),
  layoutApi: { load: loadInitData, save: saveLayoutState },
  layoutNewDialog: LayoutNewDialog,
  barActions: { install: installBarActions },
})

const app = createApp(App)
app.use(i18n)
// Pinia（UILAYOUT-1 §3.7 / §4.1 ②）：唯一新增运行时依赖；跨 view 共享状态层（P2）与
// 自由布局子系统（平台基座 `chonkpilot-framework/host/web/src/layout-free`）的布局域 store 都挂在它上面。
app.use(createPinia())
// 跨 view 共享状态的唯一写入点（UILAYOUT-1 §7 P2②）：订阅既有主题 → 写 selection store。
// 注册时机 = Pinia 安装后、mount 前（早于任何组件挂载，保证组件首帧即可读到已收敛的状态）。
registerSelectionSubscribers()
app.use(mqPlugin)
// 自由布局容器装配（UILAYOUT-1 §7 P3⑤）：须在 pinia 与 mqPlugin 之后 ——
// 容器的 mq 订阅会取用 Pinia store，且 v-mq 指令复用宿主已注册的那一个。
setupLayoutEngine(app)
// 插件平台（PLUGIN-1）：宿主端口注入 + 快照/事件链 → 动态 view 注册 / 插件管理页
initPluginUIHost()
initPlugins()
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
