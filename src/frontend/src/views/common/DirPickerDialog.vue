<template>
  <div class="dir-picker">
    <!-- 面包屑：work_dir → 当前层（层级路径全部取自 /dirs 清单，只读） -->
    <div class="dir-picker-crumbs">
      <template v-for="(c, i) in levels.crumbs" :key="c.path">
        <span v-if="i > 0" class="dir-picker-sep">/</span>
        <button class="dir-picker-crumb" :title="c.path" @click="enter(c.path)">{{ c.name }}</button>
      </template>
    </div>

    <!-- 当前层路径 + 返回上级（不超过 work_dir 根） -->
    <div class="dir-picker-bar">
      <Button size="mini" class="dir-picker-up" :disabled="!levels.canUp" @click="up">
        <Icon name="arrow-up" />
        <span>{{ $t('common.dir_picker_up') }}</span>
      </Button>
      <span class="dir-picker-current" :title="levels.current">{{ levels.current }}</span>
    </div>

    <!-- 子目录列表（逐级进入） -->
    <div class="dir-picker-list">
      <div v-if="loading" class="dir-picker-hint">{{ $t('common.loading') }}</div>
      <div v-else-if="error" class="dir-picker-error">{{ $t('common.dir_picker_load_failed', { error }) }}</div>
      <div v-else-if="levels.children.length === 0" class="dir-picker-empty">{{ $t('common.dir_picker_empty') }}</div>
      <button
        v-for="d in levels.children"
        :key="d.path"
        class="dir-picker-item"
        :title="d.path"
        @click="enter(d.path)"
      >
        <Icon name="folder" />
        <span class="dir-picker-name">{{ d.name }}</span>
      </button>
    </div>

    <div class="dir-picker-footer">
      <Button size="small" class="dir-picker-cancel" @click="cancel">{{ $t('common.cancel') }}</Button>
      <Button
        size="small"
        type="primary"
        class="dir-picker-confirm"
        :disabled="loading || !!error"
        @click="confirm"
      >{{ $t('common.confirm') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Button } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { fetchDirs } from '../../api/dirs'
import { buildDirLevels, parentOf } from '../../utils/dirPicker'

// 事件（与 DialogManager 组合使用）：confirm(path) 选择确认；cancel 取消；dismiss 因外部关闭
// （标题栏 X / 卸载）而结束 —— 用于把未决 Promise 收口（不静默挂起）。
const emit = defineEmits(['confirm', 'cancel', 'dismiss'])

const loading = ref(true)
const error = ref('')
const workDir = ref('')
const dirs = ref([])
const current = ref('')

// 按层浏览视图（纯函数计算，无 watch）
const levels = computed(() => buildDirLevels(dirs.value, workDir.value, current.value))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetchDirs()
    workDir.value = res.workDir
    dirs.value = res.dirs
    current.value = res.workDir
    if (!res.workDir) error.value = 'work_dir empty'
  } catch (e) {
    // 明确提示（不静默）：错误文案由模板的 dir_picker_load_failed 渲染
    error.value = (e && e.message) ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function enter(path) {
  current.value = path
}

function up() {
  if (levels.value.canUp) current.value = parentOf(levels.value.current)
}

function confirm() {
  if (loading.value || error.value) return
  emit('confirm', levels.value.current)
}

function cancel() {
  emit('cancel')
}

onMounted(load)
onUnmounted(() => emit('dismiss'))
</script>

<style scoped>
.dir-picker {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  gap: 8px;
}
.dir-picker-crumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
  font-size: var(--font-size-sm);
  color: var(--text-secondary);
  flex-shrink: 0;
}
.dir-picker-sep {
  color: var(--text-muted, #909399);
}
.dir-picker-crumb {
  border: none;
  background: transparent;
  color: var(--accent);
  cursor: pointer;
  padding: 1px 4px;
  border-radius: 4px;
  font: inherit;
}
.dir-picker-crumb:hover {
  background: var(--bg-hover);
}
.dir-picker-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.dir-picker-current {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text-muted, #909399);
}
.dir-picker-list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 4px;
}
.dir-picker-item {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: none;
  background: transparent;
  color: var(--text-primary);
  padding: 6px 8px;
  border-radius: 4px;
  cursor: pointer;
  font: inherit;
  text-align: left;
}
.dir-picker-item:hover {
  background: var(--bg-hover);
}
.dir-picker-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dir-picker-hint,
.dir-picker-empty {
  padding: 16px;
  text-align: center;
  color: var(--text-muted, #909399);
  font-size: var(--font-size-sm);
}
.dir-picker-error {
  padding: 12px;
  color: var(--danger);
  font-size: var(--font-size-sm);
  word-break: break-all;
}
.dir-picker-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  flex-shrink: 0;
}
</style>
