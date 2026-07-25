<template>
  <div class="statusbar">
    <!-- Codebase index -->
    <div class="sb-section" @click="openCodebaseConfig" :title="statusTitle">
      <svg class="sb-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
        <polyline points="14 2 14 8 20 8" />
        <line x1="16" y1="13" x2="8" y2="13" />
        <line x1="16" y1="17" x2="8" y2="17" />
        <polyline points="10 9 9 9 8 9" />
      </svg>
      <template v-if="!loading">
        <span class="sb-label">{{ files }} 索引</span>
        <div v-if="!ok" class="b-progress sb-progress">
          <div class="b-progress-bar" :style="{ width: progress + '%' }"></div>
        </div>
      </template>
      <span v-else class="sb-label sb-loading">加载中</span>
    </div>

    <!-- Divider -->
    <span class="sb-sep">|</span>

    <!-- Config icon -->
    <div class="sb-section" @click="openConfig" title="打开全局配置">
      <svg class="sb-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="3" />
        <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
      </svg>
    </div>

    <!-- Divider -->
    <span class="sb-sep">|</span>

    <!-- Debug icon -->
    <div class="sb-section" @click="openDevTools" title="打开 DevTools">
      <svg class="sb-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M8 3L5 6l3 3" />
        <path d="M16 3l3 3-3 3" />
        <path d="M14 3l-4 18" />
      </svg>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onUnmounted, h, defineComponent } from 'vue'
import { Button, message } from '../ui'
import * as cs from '../../utils/codebaseStatus'
import { dialog } from '../dialog'

const emit = defineEmits(['open-config'])
const { files, pending, indexing, failed, failedExhausted, totalFiles, total, progress, ok, loading, resetFailed } = cs

const statusTitle = computed(() => {
  if (loading.value) return '代码索引：加载中'
  let s = `已完成: ${files.value}`
  if (pending.value > 0) s += ` · 待索引: ${pending.value}`
  if (indexing.value > 0) s += ` · 索引中: ${indexing.value}`
  s += ` · 合计: ${totalFiles.value}`
  return s
})

const reindexing = ref(false)
const enabled = ref(true)


function openDevTools() {
  window.go.main.App.OpenDevTools()
}

function openCodebaseConfig() {
  const handle = dialog.show(defineComponent({
    setup() {
      const { files, pending, indexing, failed, failedExhausted, totalFiles, progress, ok, resetFailed } = cs
      const localReindexing = ref(false)
      const localEnabled = ref(true)

      async function clearIndex() {
        try {
          await window.go.main.App.ClearCodebaseIndex()
          message.success('索引已清空')
          handle.close()
        } catch (e) {
          message.error('清空失败: ' + (e.message || ''))
        }
      }

      async function reindex() {
        try {
          localReindexing.value = true
          const count = await window.go.main.App.ReindexCodebase()
          message.success(`重新索引完成，已入队 ${count} 个文件`)
          handle.close()
        } catch (e) {
          message.error('重新索引失败: ' + (e.message || ''))
        } finally {
          localReindexing.value = false
        }
      }

      async function retryFailed() {
        try {
          await resetFailed()
          message.success('失败项已重置为待索引')
        } catch (e) {
          message.error('重置失败: ' + (e.message || ''))
        }
      }

      return () => h('div', { class: 'codebase-config-preview' }, [
        h('div', { class: 'ccp-status' }, [
          h('div', { class: 'ccp-row' }, [
            h('span', null, [h('span', '待索引: '), h('strong', String(pending.value))]),
            h('span', null, [h('span', '已完成: '), h('strong', String(files.value))]),
            h('span', null, [h('span', '索引中: '), h('strong', String(indexing.value))]),
            h('span', null, [h('span', '合计: '), h('strong', String(totalFiles.value))]),
            failed.value > 0 ? h('span', { class: 'status-failed' }, [h('span', '重试中: '), h('strong', String(failed.value))]) : null,
            failedExhausted.value > 0 ? h('span', { class: 'status-exhausted' }, [h('span', '错误: '), h('strong', String(failedExhausted.value))]) : null,
          ]),
          h('div', { class: 'b-progress', style: { marginBottom: '8px' } }, [
            h('div', { class: 'b-progress-bar', style: { width: progress.value + '%' } }),
          ]),
          h('div', { class: 'ccp-meta' }, [
            (!ok.value || failedExhausted.value > 0)
              ? h('span', { class: 'status-active' }, [
                  `⏳ ${indexing.value} 正在索引 · ${pending.value} 待处理`,
                  failed.value > 0 ? ` · ${failed.value} 重试中` : '',
                  failedExhausted.value > 0 ? ` · ❌ ${failedExhausted.value} 错误` : '',
                ])
              : h('span', { class: 'status-done' }, `✓ ${files.value} 个文件已索引`),
          ]),
        ]),
        h('div', { class: 'ccp-actions' }, [
          h(Button, { size: 'small', onClick: clearIndex, disabled: !localEnabled.value }, () => '清空'),
          h(Button, { size: 'small', type: 'primary', onClick: reindex, loading: localReindexing.value, disabled: !localEnabled.value }, () => '重新索引'),
          (failed.value > 0 || failedExhausted.value > 0)
            ? h(Button, { size: 'small', type: 'warning', onClick: retryFailed }, () => '重试失败项')
            : null,
        ]),
        h('p', { class: 'ccp-hint' }, '配置更多代码索引选项（排除目录、支持的文件扩展名等），可在 IDE Config → 代码索引 中调整。'),
      ])
    }
  }), {
    title: '代码索引配置',
    width: 600,
    closable: true,
    bodyClass: 'codebase-config-body',
    onAction: (action) => {
      if (action === 'close' || action === 'cancel') handle.close()
    }
  })
}

function openConfig() {
  emit('open-config')
}

onUnmounted(() => {
  if (storeTeardown) storeTeardown()
})
</script>

<style scoped>
.statusbar {
  height: 24px;
  background: var(--bg-secondary, #f5f5f5);
  border-top: 1px solid var(--border-color, #e0e0e0);
  display: flex;
  align-items: center;
  padding: 0 12px;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted, #888);
  flex-shrink: 0;
  user-select: none;
}
.sb-section {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  padding: 0 4px;
  border-radius: 3px;
  height: 20px;
}
.sb-section:hover {
  background: var(--bg-hover, #e8e8e8);
}
.sb-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}
.sb-label {
  white-space: nowrap;
}
.sb-loading {
  opacity: 0.5;
}
.sb-progress {
  width: 60px;
  margin-left: 4px;
}
.b-progress {
  height: 8px;
  background: var(--bg-hover, #e0e0e0);
  border-radius: 4px;
  overflow: hidden;
}
.b-progress-bar {
  height: 100%;
  background: var(--accent, #409eff);
  border-radius: 4px;
  transition: width 0.3s ease;
}
.sb-sep {
  opacity: 0.3;
  margin: 0 2px;
}
</style>

<!-- Dialog content styles (unscoped for dialog.show() rendering) -->
<style>
.codebase-config-preview {
  padding: 4px 0;
}
.ccp-status {
  margin-bottom: 12px;
}
.ccp-row {
  display: flex;
  gap: 16px;
  font-size: 13px;
  margin-bottom: 8px;
}
.ccp-row span {
  color: var(--text-muted);
}
.ccp-row strong {
  color: var(--text-primary);
}
.ccp-meta {
  font-size: 12px;
  margin-top: 4px;
}
.ccp-actions {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
}
.ccp-hint {
  font-size: 12px;
  color: var(--text-muted);
  margin: 0;
  opacity: 0.7;
}
.status-active  { color: var(--warning, #e6a23c); }
.status-done    { color: var(--success, #67c23a); }
.status-failed  { color: var(--warning, #e6a23c); font-weight: 500; }
.status-exhausted { color: var(--danger, #f56c6c); font-weight: 500; }
</style>
