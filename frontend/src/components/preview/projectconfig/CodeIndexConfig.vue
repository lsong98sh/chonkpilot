<template>
  <div class="codebase-root">
    <div class="codebase-tab-content">
      <form class="b-form">
        <div class="form-item" style="flex-direction:column;align-items:stretch">
          <div class="codebase-toggle">
            <label class="form-label" style="width:auto;text-align:left;margin-bottom:0">启用代码索引（消耗 Token）</label>
            <Switch v-model="codebaseIndexEnabled" @change="handleEnabledChange" />
          </div>
          <div class="codebase-reminder">启用后 write_file/replace 自动触发 LLM 分析</div>
          <div class="codebase-reminder">并自动注入 query_codebase 工具</div>
        </div>
        <div class="form-item" style="flex-direction:column;align-items:stretch">
          <label class="form-label" style="width:auto;text-align:left;margin-bottom:4px">索引文件扩展名</label>
          <Input v-model="codebaseIndexExtensions" placeholder=".go,.js,.ts,.vue" @blur="handleExtensionsChange" />
          <span class="form-hint">逗号分隔，支持 .c .cpp .h .java 等</span>
        </div>
        <div class="form-item" style="flex-direction:column;align-items:stretch">
          <label class="form-label" style="width:auto;text-align:left;margin-bottom:4px">跳过目录（每行一个）</label>
          <Textarea v-model="codebaseSkipDirs" :rows="4" placeholder="node_modules&#10;.git&#10;.svn&#10;__pycache__&#10;.next&#10;dist&#10;build&#10;vendor&#10;.tox" @blur="handleSkipDirsChange" />
        </div>
        <hr class="b-divider" />
        <div class="form-item" style="flex-direction:column;align-items:stretch">
          <div class="codebase-status">
            <div class="status-numbers">
              <span>待索引: <strong>{{ pending }}</strong></span>
              <span>已完成: <strong>{{ files }}</strong></span>
              <span>合计: <strong>{{ total }}</strong></span>
              <span v-if="failed > 0" class="status-failed">重试中: <strong>{{ failed }}</strong></span>
              <span v-if="failedExhausted > 0" class="status-exhausted">错误: <strong>{{ failedExhausted }}</strong></span>
            </div>
            <div class="b-progress">
              <div class="b-progress-bar" :style="{ width: progress + '%' }" :class="{ 'is-success': ok, 'is-exception': failedExhausted > 0 }"></div>
            </div>
            <div class="status-meta">
              <span v-if="cbLoading" class="status-loading">获取中...</span>
              <span v-else-if="!ok || failedExhausted > 0" class="status-active">
                ⏳ {{ indexing }} 正在索引 · {{ pending }} 待处理
                <template v-if="failed > 0"> · {{ failed }} 重试中</template>
                <template v-if="failedExhausted > 0"> · ❌ {{ failedExhausted }} 错误</template>
              </span>
              <span v-else class="status-done">✓ {{ files }} 个文件已索引</span>
            </div>
          </div>
        </div>
        <div class="form-item button-group">
          <Button type="danger" size="small" @click="clearIndex" :disabled="!codebaseIndexEnabled">
            清空索引
          </Button>
          <Button type="primary" size="small" @click="reindex" :disabled="!codebaseIndexEnabled" :loading="reindexing">
            重新索引
          </Button>
          <Button v-if="failed > 0 || failedExhausted > 0" type="warning" size="small" @click="resetFailedItems">
            重试失败项
          </Button>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import Icon from '../../icon/Icon.vue'
import { Input, Textarea, Switch, Button } from '../../ui'
import { message } from '../../ui'
import * as cs from '../../../utils/codebaseStatus'
import { getAllConfig, setConfig } from '../../../api/config'

const {
  files, pending, indexing, failed, failedExhausted,
  total, progress, ok, loading: cbLoading, resetFailed,
} = cs

const codebaseIndexEnabled = ref(false)
const codebaseIndexExtensions = ref('.go,.js,.ts,.jsx,.tsx,.vue,.py,.rs,.java,.c,.cpp,.h,.hpp,.cs,.rb,.php,.swift,.kt')
const codebaseSkipDirs = ref('node_modules\n.git\n.svn\n__pycache__\n.next\ndist\nbuild\nvendor\n.tox')
const reindexing = ref(false)

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    if (c['codebase_index.enabled'] !== undefined) codebaseIndexEnabled.value = c['codebase_index.enabled'] === 'true'
    if (c['codebase_index.extensions']) codebaseIndexExtensions.value = c['codebase_index.extensions']
    if (c['codebase_index.skip_dirs']) codebaseSkipDirs.value = c['codebase_index.skip_dirs']
  } catch (e) { console.error('[CodeIndexConfig] loadConfig error:', e) }
}

async function saveConfig() {
  try {
    await setConfig('codebase_index.enabled', String(codebaseIndexEnabled.value))
    await setConfig('codebase_index.extensions', codebaseIndexExtensions.value)
    await setConfig('codebase_index.skip_dirs', codebaseSkipDirs.value)
    await setConfig('codebase_index.temperature', String(codebaseIndexTemperature.value))
  } catch (e) {
    console.warn('[CodeIndexConfig] Failed to save:', e)
  }
}

async function reindex() {
  try {
    reindexing.value = true
    const count = await window.go.main.App.ReindexCodebase()
    message.success(`重新索引完成，已入队 ${count} 个文件`)
  } catch (e) {
    message.error('重新索引失败: ' + (e.message || ''))
  } finally {
    reindexing.value = false
  }
}

async function resetFailedItems() {
  try {
    await resetFailed()
    message.success('失败项已重置为待索引')
  } catch (e) {
    message.error('重置失败: ' + (e.message || ''))
  }
}

async function clearIndex() {
  try {
    await window.go.main.App.ClearCodebaseIndex()
    message.success('索引已清空')
  } catch (e) {
    message.error('清空失败: ' + (e.message || ''))
  }
}

async function handleEnabledChange(val) {
  await saveConfig()
  if (val && window.go?.main?.App?.StartCodebaseIndex) {
    window.go.main.App.StartCodebaseIndex().catch(e => console.warn('[CodeIndexConfig] StartCodebaseIndex error:', e))
  }
}

// Handle config changes via events (not watch)
function handleExtensionsChange() {
  saveConfig()
}

function handleSkipDirsChange() {
  saveConfig()
}

onMounted(loadConfig)
onUnmounted(() => {
  // codebaseStatus is module-level singleton, no teardown needed
})
</script>

<style scoped>
.tab-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  gap: 8px;
  flex-shrink: 0;
}
.tab-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}
.codebase-root {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.codebase-tab-content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-top: 12px;
}
.b-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.form-item {
  display: flex;
  align-items: center;
  gap: 8px;
}
.button-group {
  display: flex;
  flex-direction: row;
  justify-content: flex-start;
  gap: 8px;
}
.form-label {
  flex-shrink: 0;
  font-size: 13px;
  color: var(--text-primary);
  text-align: right;
}
.form-hint {
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
}
.codebase-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}
.codebase-reminder {
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
}
.codebase-status {
  width: 100%;
}
.codebase-status .status-numbers {
  display: flex;
  gap: 16px;
  font-size: 13px;
  margin-bottom: 6px;
}
.codebase-status .status-numbers span {
  color: var(--text-muted);
}
.codebase-status .status-numbers strong {
  color: var(--text-primary);
}
.codebase-status .status-meta {
  font-size: 12px;
  margin-top: 4px;
}
.status-loading { color: var(--text-muted); }
.status-active  { color: #e6a23c; }
.status-done    { color: #67c23a; }
.status-failed  { color: #e6a23c; font-weight: 500; }
.status-exhausted { color: #f56c6c; font-weight: 500; }
.b-progress {
  width: 100%;
  height: 16px;
  background: var(--bg-secondary, #f0f0f0);
  border-radius: 8px;
  overflow: hidden;
}
.b-progress-bar {
  height: 100%;
  background: var(--accent, #409eff);
  border-radius: 8px;
  transition: width 0.3s;
}
.b-progress-bar.is-success {
  background: var(--success, #67c23a);
}
.b-progress-bar.is-exception {
  background: var(--danger, #f56c6c);
}
.b-range {
  width: 100%;
  accent-color: var(--accent, #409eff);
}
.b-divider {
  border: none;
  border-top: 1px solid var(--border, #dee2e6);
  margin: 8px 0;
}
</style>