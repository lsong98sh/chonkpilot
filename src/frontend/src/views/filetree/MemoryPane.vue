<template>
  <!-- 项目记忆（左侧资源面板第 4 模式，位于「知识库」之后）：
       按配置列出**已启用**记忆类别（prj 键 `memory.category.<类别名>`，缺省启用）+ 唯一 user 级
       「用户偏好」；行 = 类别名 + 级别 + 预估 tokens（样式同知识库/文件树行）。
       点击查看：项目级 → 预览区打开（file-open，`.md` markdown 渲染）；
       「用户偏好」（工作目录之外）→ data-memory-read + 只读弹框。
       记忆库总开关关闭 → 不请求 data-memory-list，显示空态提示并可跳转「上下文管理」。 -->
  <div class="memory-pane">
    <div v-if="items.length > 0" class="mem-list">
      <div
        v-for="it in items"
        :key="it.category"
        class="mem-row"
        :class="{ 'is-user': it.level === 'user' }"
        :data-level="it.level"
        :data-category="it.category"
        :title="it.path || it.category"
        @click="openItem(it)"
      >
        <Icon name="document" :size="14" class="mem-row-icon" />
        <span class="mem-row-name">{{ it.category }}</span>
        <span class="mem-row-level">{{ levelText(it.level) }}</span>
        <span class="mem-row-tokens">{{ it.tokens }}</span>
      </div>
    </div>
    <div v-else class="mem-empty">
      <template v-if="!enabled">
        <div class="mem-empty-title">{{ t('fileTree.memory_disabled') }}</div>
        <div class="mem-empty-hint">{{ t('fileTree.memory_disabled_hint') }}</div>
        <Button size="small" v-mq:[EventNames.previewTabOpen].click="{ kind: 'settings-project' }">
          {{ t('fileTree.memory_open_settings') }}
        </Button>
      </template>
      <div v-else class="mem-empty-title">{{ loading ? t('common.loading') : t('fileTree.memory_empty') }}</div>
    </div>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { Button } from '../../components/ui'
import { onDataRefresh } from '../../utils/dataClient'
import { EventNames } from '../../events/event-names'
import { useMemoryTree } from '../../composables/useMemoryTree'

defineOptions({ name: 'MemoryPane' })

const { t } = useI18n()
const { enabled, items, loading, refresh, openItem } = useMemoryTree()

function levelText(level) {
  return level === 'user' ? t('projectConfig.memory_level_user') : t('projectConfig.memory_level_project')
}

const _unsubs = []
onMounted(() => {
  refresh()
  // data-memory-refresh：记忆沉淀写回后刷新清单（token/类别变化）
  _unsubs.push(onDataRefresh('memory', () => { refresh() }))
  // data-prj-config-refresh：总开关 / 逐类开关变更后重新判定并刷新（关闭态不发 list）
  _unsubs.push(onDataRefresh('prj-config', () => { refresh() }))
})

onUnmounted(() => {
  for (const fn of _unsubs) fn()
  _unsubs.length = 0
})

defineExpose({ refresh })
</script>

<style scoped>
.memory-pane {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  user-select: none;
}
.mem-list {
  display: flex;
  flex-direction: column;
  padding: 2px 0;
}
.mem-row {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 28px;
  padding: 0 8px;
  cursor: pointer;
  border-radius: 3px;
  transition: background 0.1s;
  white-space: nowrap;
}
.mem-row:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.04));
}
.mem-row-icon {
  flex-shrink: 0;
  color: var(--text-secondary, #888);
  margin: 0 2px;
}
.mem-row-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
  line-height: 1.4;
  color: var(--text-primary);
}
.mem-row-level {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.mem-row-tokens {
  flex-shrink: 0;
  font-size: 12px;
  color: var(--text-secondary);
}
.mem-row.is-user .mem-row-icon {
  color: var(--accent, #409eff);
}
.mem-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  height: 100%;
  padding: 0 12px;
  text-align: center;
}
.mem-empty-title {
  font-size: 12px;
  color: var(--text-secondary, #888);
}
.mem-empty-hint {
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-muted);
}
</style>
