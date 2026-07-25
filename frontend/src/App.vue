<template>
  <MainLayout />
</template>

<script setup>
import MainLayout from './components/layout/MainLayout.vue'
import { onMounted, onUnmounted } from 'vue'
import { logError } from './api/log'

let unsubErrors = null

onMounted(() => {
  // Global error handler — writes to .ide/log/web.log via logWeb
  function handleError(event) {
    const msg = event.error?.message || event.message || 'Unknown error'
    const stack = event.error?.stack || ''
    logError('window.onerror', event.error || event)
    console.error('[App] 全局捕获:', msg, stack)
  }
  function handleRejection(event) {
    const msg = event.reason?.message || event.reason || 'Unknown rejection'
    const stack = event.reason?.stack || ''
    logError('window.unhandledrejection', event.reason)
    console.error('[App] 未处理的Promise错误:', msg, stack)
  }
  window.addEventListener('error', handleError)
  window.addEventListener('unhandledrejection', handleRejection)
  unsubErrors = () => {
    window.removeEventListener('error', handleError)
    window.removeEventListener('unhandledrejection', handleRejection)
  }
})

onUnmounted(() => {
  if (unsubErrors) unsubErrors()
})
</script>
