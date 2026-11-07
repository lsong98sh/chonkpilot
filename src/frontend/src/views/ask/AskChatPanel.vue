<template>
  <section class="ask-chat-panel">
    <header class="ask-chat-head">
      <Button text size="small" class="ask-chat-back" @click="$emit('back')">
        <Icon name="arrow-left" :size="12" />
        <span>{{ $t('chat.ask_chat_back') }}</span>
      </Button>
      <span class="ask-chat-title">{{ $t('chat.ask_chat_title') }}</span>
      <span class="ask-chat-question" :title="ask.question">{{ ask.question }}</span>
      <span class="ask-chat-spacer" />
      <Button
        type="primary"
        size="small"
        class="ask-chat-done"
        :loading="resolving"
        :disabled="resolving"
        @click="$emit('done')"
      >
        {{ $t('chat.ask_chat_done') }}
      </Button>
    </header>

    <div class="ask-chat-body">
      <MessageList
        :session-id="ask.session"
        :dialog-id="ask.dialog_id"
        direction="right"
      />
    </div>

    <footer class="ask-chat-composer">
      <textarea
        v-model="text"
        class="ask-chat-input"
        rows="2"
        :placeholder="$t('chat.ask_chat_placeholder')"
        @keydown.enter.exact.prevent="send"
      />
      <Button size="small" type="primary" class="ask-chat-send" :disabled="!text.trim()" @click="send">
        {{ $t('common.send') }}
      </Button>
    </footer>
  </section>
</template>

<script setup>
import { ref } from 'vue'
import mq from '@ckfw/utils/mq.js'
import Icon from '@ckfw/components/icon/Icon.vue'
import { Button } from '@ckfw/components/ui/index.js'
import { EventNames } from '../../events/event-names'
import MessageList from '../chat/MessageList.vue'

const props = defineProps({
  /** 当前待办（归一后的 ask 对象：session / dialog_id / owner_instance / instance_id / question）。 */
  ask: { type: Object, required: true },
  resolving: { type: Boolean, default: false },
})
defineEmits(['back', 'done'])

const text = ref('')

/**
 * 发送讨论消息：**以源会话实例发送**（Ask 中心是独立窗口，用自身实例会被 owner conflict 拒绝）。
 * 源会话仍由原窗口持有（ask 未解除时任务节点 running → owner 保持活动）。
 */
function send() {
  const content = text.value.trim()
  if (!content || !props.ask.session) return
  mq.emit(EventNames.messageSend, {
    sessionId: props.ask.session,
    text: content,
    llm: '',
    thinkFlag: 'on',
    effortLevel: 'high',
    scenarioId: '',
    instanceId: props.ask.owner_instance || props.ask.instance_id || '',
  })
  text.value = ''
}
</script>

<style scoped>
.ask-chat-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.ask-chat-head {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--border);
}

.ask-chat-back {
  flex-shrink: 0;
}

.ask-chat-title {
  font-size: 13px;
  font-weight: 600;
  flex-shrink: 0;
}

.ask-chat-question {
  font-size: 12px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ask-chat-spacer {
  flex: 1;
  min-width: 0;
}

.ask-chat-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  margin: 8px 0;
}

.ask-chat-body :deep(.chat-panel) {
  flex: 1;
  min-height: 0;
}

.ask-chat-composer {
  flex-shrink: 0;
  display: flex;
  align-items: flex-end;
  gap: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
}

.ask-chat-input {
  flex: 1;
  min-width: 0;
  resize: vertical;
  max-height: 140px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 13px;
  line-height: 1.5;
  outline: none;
  box-sizing: border-box;
}

.ask-chat-input:focus {
  border-color: var(--accent);
}
</style>