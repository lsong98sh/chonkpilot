<template>
  <!-- 记忆分类列表（A4，2026-09-24 用户口径）：底部「记忆总 token 数」→ 本列表 → 选中一项 →
       内容编辑弹框（TextEditDialog，可编辑、可保存）。只读展示，不改数据。 -->
  <div class="mem-cat-body">
    <div class="mem-cat-total">
      <span>{{ $t('projectConfig.memory_total_tokens') }}</span>
      <span class="mem-cat-total-value">{{ total }}</span>
    </div>
    <div class="mem-cat-list">
      <button
        v-for="c in categories"
        :key="c.category"
        type="button"
        class="mem-cat-item"
        @click="onPick(c)"
      >
        <span class="mem-cat-name">{{ c.category }}</span>
        <span class="mem-cat-level">{{ levelText(c.level) }}</span>
        <span class="mem-cat-tokens">{{ c.tokens }}</span>
      </button>
      <div v-if="categories.length === 0" class="mem-cat-empty">{{ $t('projectConfig.memory_empty') }}</div>
    </div>
  </div>
</template>

<script setup>
import { useI18n } from 'vue-i18n'

const props = defineProps({
  // 记忆类别清单（data-memory-list 形态：{ category, level, tokens }）
  categories: { type: Array, default: () => [] },
  total: { type: Number, default: 0 },
  // 选中回调（父组件关闭本弹框并打开该类别的内容编辑弹框）
  onPick: { type: Function, required: true },
})

const { t } = useI18n()

function levelText(level) {
  return level === 'user' ? t('projectConfig.memory_level_user') : t('projectConfig.memory_level_project')
}
</script>

<style scoped>
.mem-cat-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.mem-cat-total {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  color: var(--text-secondary);
  flex-shrink: 0;
}
.mem-cat-total-value {
  font-weight: 600;
  color: var(--text-primary);
}
.mem-cat-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.mem-cat-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 13px;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
}
.mem-cat-item:hover {
  border-color: var(--accent);
  background: var(--bg-hover);
}
.mem-cat-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mem-cat-level {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.mem-cat-tokens {
  flex-shrink: 0;
  font-size: 12px;
  color: var(--text-secondary);
}
.mem-cat-empty {
  padding: 8px 0;
  font-size: 12px;
  color: var(--text-muted);
}
</style>
