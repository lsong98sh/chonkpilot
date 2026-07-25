import { createApp } from 'vue'
import App from './App.vue'
import './assets/styles/global.css'

const app = createApp(App)
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