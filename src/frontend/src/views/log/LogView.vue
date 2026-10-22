<template>
  <!-- 日志窗口（PRODSIM-1）：宿主以 `<appOrigin>/#log` 打开、默认隐藏；关闭 = 收起回托盘。
       只读本进程日志文件尾部（`/api/log/tail`），不参与项目视图 / 会话绑定。 -->
  <div class="log-view">
    <div class="log-view__bar">
      <span class="log-view__title">Chonkpilot 日志</span>
      <span class="log-view__spacer" />
      <label class="log-view__auto">
        <input v-model="autoScroll" type="checkbox" />
        自动滚动
      </label>
      <button class="log-view__btn" type="button" @click="refresh">刷新</button>
    </div>
    <pre ref="body" class="log-view__body">{{ text || '（暂无日志）' }}</pre>
  </div>
</template>

<script setup>
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'

const POLL_MS = 2000
const LINES = 1000

const text = ref('')
const autoScroll = ref(true)
const body = ref(null)
let timer = null

async function refresh() {
  try {
    const res = await fetch(`/api/log/tail?lines=${LINES}`, { cache: 'no-store' })
    text.value = res.ok ? await res.text() : `（读取日志失败：HTTP ${res.status}）`
  } catch (e) {
    text.value = `（读取日志失败：${e && e.message ? e.message : e}）`
  }
}

function scrollToBottom() {
  if (!autoScroll.value || !body.value) return
  body.value.scrollTop = body.value.scrollHeight
}

watch(text, () => nextTick(scrollToBottom))

onMounted(() => {
  refresh()
  timer = setInterval(refresh, POLL_MS)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.log-view {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--bg, #1e1e1e);
  color: var(--fg, #ddd);
  font-size: 12px;
}

.log-view__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--border, #333);
  flex-shrink: 0;
}

.log-view__title {
  font-weight: 600;
}

.log-view__spacer {
  flex: 1;
}

.log-view__auto {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  user-select: none;
}

.log-view__btn {
  padding: 2px 10px;
  border: 1px solid var(--border, #333);
  border-radius: 4px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.log-view__btn:hover {
  background: var(--hover, rgba(255, 255, 255, 0.08));
}

.log-view__body {
  flex: 1;
  margin: 0;
  padding: 8px 10px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  font-family: Consolas, 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.5;
}
</style>
