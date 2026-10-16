<template>
  <!-- F10 · 启动提醒对话框内容（单聊 §3.4.7 / D38）：列出**全局未结束**子聊天；点一条 → 打开该
       子聊天窗口并关闭本对话框；底部文本引导去会话列表。**不提供**「全部打开」「终止」。 -->
  <div class="subchat-startup">
    <div class="subchat-startup-list">
      <div
        v-for="it in items"
        :key="it[FieldKeys.session_id]"
        class="subchat-startup-row"
        :class="{ ready: it[FieldKeys.lifecycle] === SUBCHAT_READY }"
        @click="onOpen && onOpen(it)"
      >
        <Icon :name="isGroupChatKind(it[FieldKeys.kind]) ? 'group' : 'chat-dot-round'" :size="12" class="subchat-startup-icon" />
        <span class="subchat-startup-name">{{ subChatName(it) }}</span>
        <span v-if="it[FieldKeys.parent_session_title]" class="subchat-startup-parent">
          {{ it[FieldKeys.parent_session_title] }}
        </span>
        <span class="subchat-startup-state">{{ $t(subChatStateKey(it[FieldKeys.lifecycle])) }}</span>
      </div>
    </div>
    <div class="subchat-startup-foot">{{ $t('chat.subchat_startup_foot') }}</div>
  </div>
</template>

<script setup>
import { FieldKeys } from '../../events/msgkeys.js'
import Icon from '../../components/icon/Icon.vue'
import { SUBCHAT_READY, subChatName, subChatStateKey } from '../../utils/subchat.js'
import { isGroupChatKind } from '../../utils/groupchat.js'

defineProps({
  /** 未结束子聊天清单（`subchat-list{}` 结果项，§3.4.7）。 */
  items: { type: Array, default: () => [] },
  /** 点击一条 → 打开该子聊天窗口（由调用方关闭对话框）。 */
  onOpen: { type: Function, default: null },
})
</script>

<style scoped>
.subchat-startup {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.subchat-startup-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.subchat-startup-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  cursor: pointer;
  transition: border-color var(--transition-fast);
}
.subchat-startup-row:hover {
  border-color: var(--accent);
  background: var(--bg-hover);
}
.subchat-startup-icon {
  flex-shrink: 0;
  color: var(--text-muted);
}
.subchat-startup-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  color: var(--text-primary);
}
.subchat-startup-row.ready .subchat-startup-name {
  color: var(--accent);
  font-weight: 600;
}
.subchat-startup-parent {
  flex-shrink: 0;
  max-width: 40%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  color: var(--text-muted);
}
.subchat-startup-state {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-secondary);
}
.subchat-startup-foot {
  font-size: 11px;
  color: var(--text-muted);
}
</style>
