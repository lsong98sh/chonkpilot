<template>
  <!-- 通用「单块文本编辑」弹框内容（A1/A2/A4，2026-09-24 用户口径）：
       多行编辑 + （可选）内嵌【优化】按钮 + 【保存】即落库并由父组件关闭弹框。
       内容撑满弹框、底部不留白（bodyClass = text-edit-dialog-body，见 global.css）。 -->
  <div class="text-edit-body">
    <Textarea
      v-model="text"
      class="text-edit-input"
      :placeholder="placeholder"
      :disabled="saving || optimizing"
    />
    <div class="text-edit-footer">
      <Button
        v-if="optimize"
        size="small"
        :loading="optimizing"
        :disabled="saving"
        @click="handleOptimize"
      >{{ $t('common.optimize') }}</Button>
      <Button
        v-if="optimize && optimize.recover && optimizeSnapshot !== ''"
        size="small"
        :disabled="saving || optimizing"
        @click="handleRecoverOptimize"
      >{{ $t('projectConfig.recover_before_optimize') }}</Button>
      <span class="footer-spacer" />
      <Button size="small" :disabled="saving || optimizing" @click="$emit('cancel')">{{ $t('common.cancel') }}</Button>
      <Button size="small" type="primary" :loading="saving" :disabled="optimizing" @click="handleSave">{{ $t('common.save') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Textarea, message } from '../ui'
import { optimizeAgentPrompt } from '../../api/config'

const props = defineProps({
  // 初始内容（打开时的值；编辑期间为组件内部状态）
  content: { type: String, default: '' },
  placeholder: { type: String, default: '' },
  // 优化入口（null = 不显示该按钮）：{ title, useCase } 原样透传 gui.prompt-optimise
  optimize: { type: Object, default: null },
  // 保存回调（**父组件负责落库与关闭弹框**）；抛错 = 保持弹框打开（父已给可见失败提示）
  onSave: { type: Function, required: true },
})

defineEmits(['cancel'])

const { t } = useI18n()

const text = ref(props.content || '')
const saving = ref(false)
const optimizing = ref(false)
// 优化前内容快照（仅 optimize.recover = true 时使用；弹框内「恢复优化」回填，**不落库**）
const optimizeSnapshot = ref('')

async function handleSave() {
  if (saving.value || optimizing.value) return
  saving.value = true
  try {
    await props.onSave(text.value)
  } catch (_) {
    // 父组件已 message.error（内容不丢，弹框不关）
  } finally {
    saving.value = false
  }
}

// 内嵌优化：流式回显（以最后一次 done 为准）→ 结果直接落库（与既有总结提示词优化同口径）
function handleOptimize() {
  if (optimizing.value || saving.value) return
  if (!text.value.trim()) {
    message.warning(t('common.input_required'))
    return
  }
  if (props.optimize.recover) optimizeSnapshot.value = text.value
  optimizing.value = true
  optimizeAgentPrompt(
    { title: props.optimize.title, useCase: props.optimize.useCase, prompt: text.value },
    (chunk) => { text.value += chunk },
    async (prompt) => {
      optimizing.value = false
      text.value = prompt || text.value
      await handleSave()
    },
    (err) => {
      optimizing.value = false
      message.error(t('common.optimize_failed') + ': ' + err)
    },
  )
}

// 恢复优化：回填优化前内容（草稿态；是否落库由用户点【保存】决定）
function handleRecoverOptimize() {
  if (optimizeSnapshot.value === '') return
  text.value = optimizeSnapshot.value
  optimizeSnapshot.value = ''
}
</script>

<style scoped>
.text-edit-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 12px 16px 0;
}
.text-edit-input {
  flex: 1;
  min-height: 0;
  height: 100%;
  resize: none;
}
.text-edit-footer {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 0;
}
.footer-spacer {
  flex: 1;
}
</style>
