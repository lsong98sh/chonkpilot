<template>
  <!-- 提示词「变量插入」统一入口（OP-12）：按钮 → popup 分组列表（含说明 tooltip）→ 点击 emit insert(key)。
       变量清单来自后端只读面 gui.prompt-vars（单一数据源 = 后端常量）；{{env.*}} 仅 dsl=true 时列出。 -->
  <Popover placement="bottom-start" :width="320">
    <template #reference>
      <Button size="small" text :disabled="disabled">
        <Icon name="plus" :size="12" /> {{ $t('scenario.insert_variable') }}
      </Button>
    </template>
    <div class="pv-panel">
      <div class="pv-title">{{ $t('scenario.available_vars') }}</div>
      <div v-for="g in visibleGroups" :key="g.id" class="pv-group">
        <div class="pv-group-label">{{ $t(g.label) }}</div>
        <Tooltip v-for="it in g.items" :key="it.key" :content="$t(it.desc)" placement="right">
          <button type="button" class="pv-item" @click="$emit('insert', it.key)">{{ it.key }}</button>
        </Tooltip>
      </div>
    </div>
  </Popover>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import { Button, Tooltip, Popover } from '../ui'
import Icon from '../icon/Icon.vue'
import { usePromptVariables } from '../../composables/usePromptVariables'

const props = defineProps({
  // 禁用（保存/优化进行中）：禁用时原生 button 不触发 click → 不展开 popup。
  disabled: { type: Boolean, default: false },
  // DSL/脚本类编辑器：true → 额外列出 {{env.*}}（dslOnly）；本仓前端暂无此类编辑器（默认 false）。
  dsl: { type: Boolean, default: false },
})
defineEmits(['insert'])

const { load, visibleGroups: rawGroups } = usePromptVariables()
const visibleGroups = computed(() => rawGroups(props.dsl))

// 首次挂载即拉取清单（模块级缓存，多次挂载只拉一次）。
onMounted(() => { load() })
</script>

<style scoped>
.pv-panel {
  max-height: 320px;
  overflow-y: auto;
}
.pv-title {
  font-size: 12px;
  color: var(--text-muted);
  padding: 2px 4px 6px;
}
.pv-group + .pv-group {
  margin-top: 6px;
  border-top: 1px solid var(--border);
  padding-top: 6px;
}
.pv-group-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  padding: 0 4px 4px;
}
.pv-item {
  display: block;
  width: 100%;
  text-align: left;
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 4px 6px;
  border-radius: var(--border-radius);
  font-family: var(--font-mono, Consolas, monospace);
  font-size: 12px;
  color: var(--text-primary);
}
.pv-item:hover {
  background: var(--accent-bg, #ecf5ff);
  color: var(--accent);
}
</style>
