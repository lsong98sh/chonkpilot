<template>
  <el-dialog
    v-model="visible"
    :title="dialogTitle"
    :close-on-click-modal="false"
    :close-on-press-escape="false"
    :show-close="false"
    width="480px"
    top="25vh"
    draggable
    class="ask-user-dialog"
  >
    <div v-if="subSessionId" class="session-tag">
      <el-tag size="small" type="info" effect="plain">
        Session: #{{ subSessionId.slice(0, 8) }}
      </el-tag>
    </div>
    <div class="question-content">{{ question }}</div>

    <div v-if="options && options.length > 0" class="options-list">
      <el-button
        v-for="(opt, idx) in options"
        :key="idx"
        :type="selectedOption === opt ? 'primary' : 'default'"
        size="large"
        class="option-btn"
        @click="selectOption(opt)"
      >
        {{ opt }}
      </el-button>
    </div>

    <div v-if="custom" class="custom-input">
      <el-input
        v-model="customAnswer"
        type="textarea"
        :rows="3"
        placeholder="Type your custom answer..."
        :disabled="selectedOption !== null"
      />
    </div>

    <template #footer>
      <div class="dialog-footer">
        <el-button
          type="primary"
          size="large"
          :disabled="!canSubmit"
          @click="submitAnswer"
        >
          Send Answer
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RespondAskUser } from '../../../wailsjs/go/main/App'
import bridge from '../../utils/bridge'

const visible = ref(false)
const question = ref('')
const options = ref([])
const custom = ref(false)
const pipeAddr = ref('')
const subSessionId = ref('')
const selectedOption = ref(null)
const customAnswer = ref('')

const dialogTitle = computed(() => {
  return subSessionId.value
    ? `🤔 Sub-Session #${subSessionId.value.slice(0, 8)} Asks`
    : '🤔 AI Asks You'
})

const canSubmit = computed(() => {
  return !!selectedOption.value || (custom.value && customAnswer.value.trim())
})

function selectOption(opt) {
  selectedOption.value = opt
  customAnswer.value = ''
}

function reset() {
  visible.value = false
  question.value = ''
  options.value = []
  custom.value = false
  pipeAddr.value = ''
  subSessionId.value = ''
  selectedOption.value = null
  customAnswer.value = ''
}

async function submitAnswer() {
  const answer = selectedOption.value || customAnswer.value.trim()
  if (!answer) return

  try {
    await RespondAskUser({
      answer: answer,
      custom: selectedOption.value ? '' : customAnswer.value.trim(),
      pipe_addr: pipeAddr.value,
    })
    reset()
  } catch (e) {
    console.error('Failed to send ask_user response:', e)
    reset()
  }
}

function handleAskUser(data) {
  if (!data?.question) return
  reset()
  question.value = data.question
  const opts = data.options || []
  options.value = opts
  // 没有预定义选项时，自动启用自定义输入
  custom.value = opts.length === 0 ? true : !!data.custom
  pipeAddr.value = data.pipe_addr || ''
  subSessionId.value = data.sub_session_id || ''
  visible.value = true
}

let unsubAskUser = null

onMounted(() => {
  unsubAskUser = bridge.on('ask_user', handleAskUser)
})

onUnmounted(() => {
  if (unsubAskUser) unsubAskUser()
})
</script>

<style scoped>
.ask-user-dialog :deep(.el-dialog__body) {
  padding: 20px 24px;
}

.question-content {
  font-size: 15px;
  line-height: 1.6;
  color: var(--text-primary);
  margin-bottom: 20px;
  white-space: pre-wrap;
}

.session-tag {
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
  padding-left: 16px;
}

.custom-input {
  margin-bottom: 8px;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
}
</style>
