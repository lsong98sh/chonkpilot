<!--
  长任务详情页外壳（STB-1）：标题 + 状态徽标 + 时间 + 「去设置」入口 + 错误块。
  三个详情页（记忆提取 / 上下文压缩 / 代码索引）共用；内容由默认插槽给。
  「去设置」= 既有 `project-config-open{tab}`（preview 功能页），配置仍可从详情页到达。
-->
<template>
  <div class="task-detail" :data-detail="detail">
    <header class="td-head">
      <Icon :name="icon" :size="16" class="td-head-icon" />
      <h3 class="td-title">{{ $t(titleKey) }}</h3>
      <span class="td-badge" :class="'is-' + tone">{{ stateText }}</span>
      <span v-if="time" class="td-time">{{ time }}</span>
      <span class="td-spacer"></span>
      <Button
        size="small"
        data-action="go-settings"
        v-mq:[EventNames.projectConfigOpen].click="{ tab: settingsTab }"
      >{{ $t('statusBar.detail.goSettings') }}</Button>
    </header>
    <div v-if="error" class="td-error">
      <div class="td-label">{{ $t('statusBar.detail.error') }}</div>
      <div class="td-error-text">{{ error }}</div>
    </div>
    <slot />
  </div>
</template>

<script setup>
import { Icon } from '../../components/icon'
import { Button } from '../../components/ui'
import { EventNames } from '../../events/event-names'

defineProps({
  /** 详情类型（DOM 断言用：memory | compress | index） */
  detail: { type: String, required: true },
  /** 标题 i18n 键 */
  titleKey: { type: String, required: true },
  icon: { type: String, default: 'document' },
  /** 字色 tone：disabled | progress | ready | error */
  tone: { type: String, default: 'disabled' },
  stateText: { type: String, default: '' },
  time: { type: String, default: '' },
  /** 「去设置」定位的项目配置页签 */
  settingsTab: { type: String, default: 'context' },
  /** 错误原文（非空即渲染错误块，失败必须可见） */
  error: { type: String, default: '' },
})
</script>

<style scoped>
.task-detail {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 12px 16px;
  font-size: 12px;
  color: var(--text-primary);
}
.td-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 12px;
}
.td-head-icon {
  flex-shrink: 0;
  color: var(--fg-secondary);
}
.td-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
}
.td-head :deep(.b-btn) {
  flex-shrink: 0;
}
.td-spacer {
  flex: 1;
}
.td-time {
  color: var(--text-muted);
}
.td-badge {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  white-space: nowrap;
}
.td-badge.is-disabled {
  color: var(--fg-disabled);
  background: var(--bg-secondary);
}
.td-badge.is-progress {
  color: var(--fg-secondary);
  background: var(--bg-secondary);
}
.td-badge.is-ready {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.td-badge.is-error {
  color: var(--fg-important);
  background: var(--bg-secondary);
}
.td-error {
  margin-bottom: 12px;
  padding: 8px 10px;
  border-left: 3px solid var(--fg-important);
  background: var(--bg-secondary);
}
.td-label {
  color: var(--text-muted);
  margin-bottom: 4px;
}
.td-error-text {
  color: var(--fg-important);
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
