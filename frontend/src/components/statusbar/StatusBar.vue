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
        <el-progress
          v-if="!ok"
          :percentage="progress"
          :stroke-width="8"
          :show-text="false"
          class="sb-progress"
        />
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

    <!-- Codebase config dialog -->
    <el-dialog
      v-model="dialogVisible"
      title="代码索引配置"
      width="600px"
      :close-on-click-modal="true"
      top="10vh"
      draggable
    >
      <div class="codebase-config-preview">
        <!-- Status summary -->
        <div class="ccp-status">
          <div class="ccp-row">
            <span>待索引: <strong>{{ pending }}</strong></span>
            <span>已完成: <strong>{{ files }}</strong></span>
            <span>索引中: <strong>{{ indexing }}</strong></span>
            <span>合计: <strong>{{ totalFiles }}</strong></span>
            <span v-if="failed > 0" class="status-failed">重试中: <strong>{{ failed }}</strong></span>
            <span v-if="failedExhausted > 0" class="status-exhausted">错误: <strong>{{ failedExhausted }}</strong></span>
          </div>
          <el-progress
            :percentage="progress"
            :status="ok ? 'success' : (failedExhausted > 0 ? 'exception' : undefined)"
            :stroke-width="14"
            :show-text="false"
          />
          <div class="ccp-meta">
            <span v-if="!ok || failedExhausted > 0" class="status-active">
              ⏳ {{ indexing }} 正在索引 · {{ pending }} 待处理
              <template v-if="failed > 0"> · {{ failed }} 重试中</template>
              <template v-if="failedExhausted > 0"> · ❌ {{ failedExhausted }} 错误</template>
            </span>
            <span v-else class="status-done">✓ {{ files }} 个文件已索引</span>
          </div>
        </div>

        <!-- Actions -->
        <div class="ccp-actions">
          <el-button size="small" @click="clearIndex" :disabled="!enabled">清空</el-button>
          <el-button size="small" type="primary" @click="reindex" :loading="reindexing" :disabled="!enabled">重新索引</el-button>
          <el-button v-if="failed > 0 || failedExhausted > 0" size="small" type="warning" @click="retryFailed">重试失败项</el-button>
        </div>

        <p class="ccp-hint">配置更多代码索引选项（排除目录、支持的文件扩展名等），可在 IDE Config → 代码索引 中调整。</p>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { useCodebaseStatus } from '../../composables/useCodebaseStatus'

const emit = defineEmits(['open-config'])
const { files, pending, indexing, failed, failedExhausted, totalFiles, total, progress, ok, loading, teardown, resetFailed } = useCodebaseStatus()

const statusTitle = computed(() => {
  if (loading.value) return '代码索引：加载中'
  let s = `已完成: ${files.value}`
  if (pending.value > 0) s += ` · 待索引: ${pending.value}`
  if (indexing.value > 0) s += ` · 索引中: ${indexing.value}`
  s += ` · 合计: ${totalFiles.value}`
  return s
})

const dialogVisible = ref(false)
const reindexing = ref(false)
const enabled = ref(true)

const storeTeardown = teardown

function openDevTools() {
  window.go.main.App.OpenDevTools()
}

function openCodebaseConfig() {
  dialogVisible.value = true
}

function openConfig() {
  emit('open-config')
}

async function clearIndex() {
  try {
    await window.go.main.App.ClearCodebaseIndex()
    ElMessage.success('索引已清空')
    dialogVisible.value = false
  } catch (e) {
    ElMessage.error('清空失败: ' + (e.message || ''))
  }
}

async function reindex() {
  try {
    reindexing.value = true
    const count = await window.go.main.App.ReindexCodebase()
    ElMessage.success(`重新索引完成，已入队 ${count} 个文件`)
    dialogVisible.value = false
  } catch (e) {
    ElMessage.error('重新索引失败: ' + (e.message || ''))
  } finally {
    reindexing.value = false
  }
}

async function retryFailed() {
  try {
    await resetFailed()
    ElMessage.success('失败项已重置为待索引')
  } catch (e) {
    ElMessage.error('重置失败: ' + (e.message || ''))
  }
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
.sb-progress :deep(.el-progress-bar__outer) {
  background: var(--bg-hover, #e0e0e0);
}
.sb-sep {
  opacity: 0.3;
  margin: 0 2px;
}

/* Dialog styles */
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
.status-active  { color: var(--el-color-warning); }
.status-done    { color: var(--el-color-success); }
.status-failed  { color: var(--el-color-warning); font-weight: 500; }
.status-exhausted { color: var(--el-color-danger); font-weight: 500; }
</style>
