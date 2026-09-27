<template>
  <div class="ask-user-content">
    <div v-if="subSessionId" class="session-tag">
      Session: #{{ subSessionId.slice(0, 8) }}
    </div>

    <div v-if="mode === 'busy'" class="busy-banner">
      ⏳ {{ $t('chat.ask_user_busy') }}
    </div>

    <div class="question-content">{{ question }}</div>

    <div v-if="options.length > 0" class="options-list">
      <Button
        v-for="(opt, idx) in options"
        :key="idx"
        class="option-btn"
        :class="{ selected: isSelected(opt) }"
        v-mq:[EventNames.askUserSelectOption].click="{ uid, opt }"
      >
        <span class="option-text">{{ opt }}</span>
        <span v-if="isRecommended(opt)" class="rec-badge">{{ $t('chat.ask_user_recommended') }}</span>
      </Button>
    </div>

    <div v-if="custom" class="custom-input">
      <textarea
        v-model="customAnswer"
        :rows="3"
        :placeholder="$t('chat.custom_answer_placeholder')"
        :disabled="multi ? selectedOptions.length > 0 : selectedOption !== null"
      ></textarea>
    </div>

    <div class="footer">
      <Button
        class="skip-btn"
        v-mq:[EventNames.askUserSkip].click="{ uid }"
      >{{ $t('chat.ask_user_skip') }}</Button>
      <Button
        class="cancel-btn"
        v-mq:[EventNames.askUserCancel].click="{ uid }"
      >{{ $t('chat.ask_user_cancel') }}</Button>
      <Button
        class="submit-btn"
        :disabled="!canSubmit"
        v-mq:[EventNames.askUserSubmit].click="{ uid }"
      >{{ $t('chat.send_answer') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Button } from '../../components/ui'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const props = defineProps({
  uid: { type: String, default: '' }, // 弹窗实例标识（多弹窗并存时按 uid 过滤事件）
  question: String,
  options: { type: Array, default: () => [] },
  custom: { type: Boolean, default: false },
  multi: { type: Boolean, default: false }, // 多选（multi=true 勾选多个选项）
  recommended: { type: Array, default: () => [] }, // 推荐选项 label 列表
  subSessionId: { type: String, default: '' },
  sessionId: { type: String, default: '' },
  deadline: { type: Number, default: 0 },
})

const emit = defineEmits(['answer', 'busy'])

const selectedOption = ref(null)
const selectedOptions = ref([]) // 多选：已勾选选项集合
const customAnswer = ref('')

// mode: 'active' -> still within the pipe timeout window (direct pipe send)
//       'busy'   -> pipe window closed; choices are delivered via chat fallback
const mode = ref('active')
let timeoutHandle = null
const unsubs = []

// ask_user 转异步超时：server 未给 expires_at 时前端默认 5 分钟（FP L362）
const FALLBACK_TIMEOUT_MS = 5 * 60 * 1000

onMounted(() => {
  // 交互事件化：v-mq 触发 → 按 uid 过滤分发到本实例
  unsubs.push(mq.on(EventNames.askUserSelectOption, ({ uid: u, opt }) => {
    if (u === props.uid) selectOption(opt)
  }))
  unsubs.push(mq.on(EventNames.askUserSubmit, ({ uid: u }) => {
    if (u === props.uid) submitAnswer()
  }))
  unsubs.push(mq.on(EventNames.askUserSkip, ({ uid: u }) => {
    if (u === props.uid) emit('skip')
  }))
  unsubs.push(mq.on(EventNames.askUserCancel, ({ uid: u }) => {
    if (u === props.uid) emit('cancel')
  }))
  // 超时基准：无任何鼠标键盘操作开始计时（前端计时，与工具超时无关）
  const deadlineMs = props.deadline > 0 ? props.deadline : Date.now() + FALLBACK_TIMEOUT_MS
  const delay = deadlineMs - Date.now()
  if (delay <= 0) {
    mode.value = 'busy'
    emit('busy')
  } else {
    timeoutHandle = setTimeout(() => {
      mode.value = 'busy'
      emit('busy')
    }, delay)
  }
})

onUnmounted(() => {
  if (timeoutHandle) clearTimeout(timeoutHandle)
  unsubs.forEach(fn => fn())
})

const canSubmit = computed(() => {
  if (props.multi) {
    return selectedOptions.value.length > 0 || (props.custom && customAnswer.value.trim())
  }
  return !!selectedOption.value || (props.custom && customAnswer.value.trim())
})

function isSelected(opt) {
  return props.multi ? selectedOptions.value.includes(opt) : selectedOption.value === opt
}

function isRecommended(opt) {
  return (props.recommended || []).includes(opt)
}

function selectOption(opt) {
  if (props.multi) {
    // 多选：toggle；选择选项时清空"其他"文本（与单选一致：选项与自定义互斥）
    const i = selectedOptions.value.indexOf(opt)
    if (i >= 0) {
      selectedOptions.value.splice(i, 1)
    } else {
      selectedOptions.value.push(opt)
      customAnswer.value = ''
    }
    return
  }
  selectedOption.value = opt
  customAnswer.value = ''
}

function submitAnswer() {
  let answer = ''
  if (props.multi) {
    const opts = [...selectedOptions.value]
    const custom = customAnswer.value.trim()
    if (custom && !opts.includes(custom)) opts.push(custom)
    answer = opts.join('、')
  } else {
    answer = selectedOption.value || customAnswer.value.trim()
  }
  if (!answer) return
  emit('answer', answer)
}
</script>

<style scoped>
.ask-user-content {
  padding: 4px 0;
}

.busy-banner {
  padding: 8px 12px;
  margin-bottom: 12px;
  border: 1px solid var(--warning-border);
  border-radius: 4px;
  background: var(--warning-bg);
  color: var(--warning);
  font-size: 13px;
  line-height: 1.5;
}

.question-content {
  font-size: 15px;
  line-height: 1.6;
  color: var(--text-primary);
  margin-bottom: 20px;
  white-space: pre-wrap;
}

.session-tag {
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 10px;
}

.options-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 16px;
}

.option-btn {
  width: 100%;
  text-align: left;
  padding: 10px 16px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-primary, #fff);
  cursor: pointer;
  font-size: 14px;
  color: var(--text-primary);
  transition: border-color 0.15s, background 0.15s;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.option-btn:hover {
  border-color: var(--accent);
}

.option-btn.selected {
  border-color: var(--accent);
  background: var(--accent-bg, #ecf5ff);
  color: var(--accent);
}

.option-text {
  flex: 1;
  min-width: 0;
}

.rec-badge {
  flex-shrink: 0;
  font-size: 11px;
  padding: 0 6px;
  border-radius: 3px;
  background: var(--warning-bg);
  color: var(--warning);
  border: 1px solid var(--warning-border);
  margin-left: 8px;
}

.custom-input textarea {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: 4px;
  font-size: 14px;
  resize: vertical;
  background: var(--bg-primary, #fff);
  color: var(--text-primary);
  box-sizing: border-box;
  font-family: inherit;
}

.custom-input textarea:disabled {
  opacity: 0.5;
}

.footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
  gap: 8px;
  flex-shrink: 0;
}

.skip-btn {
  padding: 8px 16px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  font-size: 14px;
  color: var(--text-secondary, #606266);
  cursor: pointer;
}

.skip-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.cancel-btn {
  padding: 8px 16px;
  background: transparent;
  border: 1px solid var(--danger);
  border-radius: 4px;
  font-size: 14px;
  color: var(--danger);
  cursor: pointer;
}

.cancel-btn:hover {
  background: var(--danger);
  color: var(--bg-secondary);
}

.submit-btn {
  padding: 8px 20px;
  /* 实心填充：文字用主题最底层色（light = 纯白，与历史 #fff 一致；dark/nord = 深色） */
  background: var(--accent);
  color: var(--bg-secondary);
  border: none;
  border-radius: 4px;
  font-size: 14px;
  cursor: pointer;
  transition: opacity 0.15s;
}

.submit-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
