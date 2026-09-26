<template>
  <div class="hex-view">
    <div class="hex-toolbar">
      <span class="hex-info">{{ totalBytes }} {{ $t('common.bytes') }}</span>
      <span v-if="truncated" class="hex-truncated">{{ $t('common.showing_first_bytes', { count: displayedBytes }) }}</span>
      <Button v-if="hasMore" text type="primary" v-mq:[EventNames.hexLoadMore].click>
        {{ $t('common.load_next_bytes', { count: PAGE_SIZE }) }}
      </Button>
    </div>
    <div class="hex-content" ref="hexContainer">
      <div class="hex-header">
        <span class="hex-offset-header">{{ $t('common.hex_offset') }}</span>
        <span class="hex-bytes-header">{{ $t('common.hex_hex') }}</span>
        <span class="hex-ascii-header">{{ $t('common.hex_ascii') }}</span>
      </div>
      <div v-for="(row, i) in rows" :key="i" class="hex-row">
        <span class="hex-offset">{{ row.offset }}</span>
        <span class="hex-bytes">{{ row.hex }}</span>
        <span class="hex-ascii">{{ row.ascii }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { getFileUrl } from '../../api/file.js'
import { Button } from '../../components/ui'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const props = defineProps({
  path: { type: String, required: true },
})

const PAGE_SIZE = 4096 // bytes per page
const MAX_ROWS = 600   // max rows to render (~38KB displayed at once)

const hexContainer = ref(null)
const data = ref(new Uint8Array(0))
const totalBytes = ref(0)
const hasMore = ref(false)

const truncated = computed(() => hasMore.value)
const displayedBytes = computed(() => data.value.length)

const rows = computed(() => {
  const result = []
  const len = data.value.length
  for (let i = 0; i < len; i += 16) {
    const slice = data.value.slice(i, Math.min(i + 16, len))
    // The buffer always starts at byte offset 0 (append-only), so row offsets
    // are the byte indices into data.
    const offset = i.toString(16).padStart(8, '0')
    const hex = Array.from(slice).map(b => b.toString(16).padStart(2, '0').toUpperCase()).join(' ')
    const ascii = Array.from(slice).map(b => (b >= 32 && b <= 126) ? String.fromCharCode(b) : '.').join('')
    result.push({ offset, hex, ascii })
    if (result.length >= MAX_ROWS) break
  }
  return result
})

async function fetchChunk(url, start, end) {
  const res = await fetch(url, {
    headers: { Range: `bytes=${start}-${end}` },
  })
  if (!res.ok && res.status !== 206) throw new Error(`HTTP ${res.status}`)
  const buf = await res.arrayBuffer()
  return new Uint8Array(buf)
}

async function loadMore() {
  const url = getFileUrl(props.path)
  if (!url) return
  try {
    const start = data.value.length
    const end = start + PAGE_SIZE - 1
    const chunk = await fetchChunk(url, start, end)
    if (chunk.length === 0) {
      hasMore.value = false
      return
    }
    // Append. The buffer always starts at byte offset 0 (append-only), so the
    // row offsets stay correct.
    const newBuf = new Uint8Array(data.value.length + chunk.length)
    newBuf.set(data.value)
    newBuf.set(chunk, data.value.length)
    data.value = newBuf
    if (data.value.length >= totalBytes.value) hasMore.value = false
  } catch (e) {
    console.error('[HexView] loadMore error:', e)
  }
}

async function init() {
  const url = getFileUrl(props.path)
  if (!url) return
  try {
    // Get total size via HEAD
    const headRes = await fetch(url, { method: 'HEAD' })
    const contentRange = headRes.headers.get('Content-Range')
    if (contentRange) {
      const match = contentRange.match(/\/(\d+)$/)
      if (match) totalBytes.value = parseInt(match[1], 10)
    }
    if (!totalBytes.value) {
      const res = await fetch(url)
      const buf = await res.arrayBuffer()
      totalBytes.value = buf.byteLength
      data.value = new Uint8Array(buf)
      hasMore.value = false
      return
    }
    // Load first chunk
    const start = 0
    const end = Math.min(PAGE_SIZE * (MAX_ROWS / 10), totalBytes.value - 1)
    const chunk = await fetchChunk(url, start, end)
    data.value = chunk
    hasMore.value = end < totalBytes.value - 1
  } catch (e) {
    console.error('[HexView] init error:', e)
  }
}

let unsubLoadMore = null

onMounted(() => {
  init()
  unsubLoadMore = mq.on(EventNames.hexLoadMore, loadMore)
})

onUnmounted(() => {
  if (unsubLoadMore) unsubLoadMore()
})
</script>

<style scoped>
.hex-view {
  height: 100%;
  display: flex;
  flex-direction: column;
  font-family: var(--font-mono, 'Cascadia Code', 'Consolas', monospace);
  font-size: 13px;
}
.hex-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.hex-info {
  color: var(--text-secondary);
  font-weight: 600;
}
.hex-truncated {
  color: var(--text-muted);
  font-size: 12px;
}
.hex-content {
  flex: 1;
  overflow: auto;
  padding: 8px 12px;
}
.hex-header {
  display: flex;
  gap: 16px;
  padding-bottom: 4px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 4px;
  color: var(--text-muted);
  font-weight: 600;
}
.hex-row {
  display: flex;
  gap: 16px;
  line-height: 1.6;
}
.hex-row:hover {
  background: var(--bg-surface);
}
.hex-offset {
  color: var(--text-muted);
  min-width: 72px;
  user-select: none;
}
.hex-offset-header {
  min-width: 72px;
}
.hex-bytes {
  color: var(--text-primary);
  min-width: 400px;
  word-break: break-all;
}
.hex-bytes-header {
  min-width: 400px;
}
.hex-ascii {
  color: var(--text-secondary);
}
.hex-ascii-header {
}
</style>
