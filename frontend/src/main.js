import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import App from './App.vue'
import './assets/styles/global.css'

const app = createApp(App)

// Register all Element Plus components (simpler, avoids missing component issues)
app.use(ElementPlus)

// Register only used Element Plus icons
const iconNames = [
  'Folder', 'FolderOpened', 'FolderDelete', 'ArrowDown',
  'MessageBox', 'Search', 'MagicStick', 'Setting',
  'ChatDotSquare', 'List', 'Delete', 'Close',
  'Document', 'Check', 'Collection', 'Loading',
  'WarningFilled', 'Refresh', 'Plus', 'Lightning', 'ArrowRight',
]
for (const name of iconNames) {
  app.component(name, ElementPlusIconsVue[name])
}

app.mount('#app')

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
app.config.errorHandler = (err, vm, info) => {
  console.error('[Vue error]', err, info)
}
window.onerror = (msg, url, line, col, err) => {
  console.error('[Global error]', msg, url, line, col, err)
}
