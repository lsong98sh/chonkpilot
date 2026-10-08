<template>
  <!-- 通用「单块文本编辑」弹框内容（A1/A2/A4，2026-09-24 用户口径）：
       多行编辑 + （可选）内嵌【优化】按钮 + 【保存】即落库并由父组件关闭弹框。
       内容撑满弹框、底部不留白（bodyClass = text-edit-dialog-body，见 global.css）。 -->
  <div class="text-edit-body">
    <!-- 来源/口径提示（可选）：如「当前为内置默认」/「当前为自定义」——由父组件按读取结果给 -->
    <div v-if="hint" class="text-edit-hint">{{ hint }}</div>
    <Textarea
      ref="inputRef"
      v-model="text"
      class="text-edit-input"
      :placeholder="placeholder"
      :disabled="saving || optimizing"
    />
    <!-- 底部按钮区（固定在编辑区之外，不随内容滚动）：左侧「变量 / 优化 / 恢复优化 / 恢复默认」，右侧「取消 / 保存」 -->
    <div class="text-edit-footer">
      <PromptVariablesButton
        v-if="variables"
        :disabled="saving || optimizing"
        @insert="insert"
      />
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
      <!-- 「恢复默认」（可选）：清除自定义值回落内置默认，由父组件落库并关闭弹框 -->
      <Button
        v-if="reset"
        size="small"
        :disabled="saving || optimizing"
        @click="handleReset"
      >{{ reset.label }}</Button>
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
import PromptVariablesButton from './PromptVariablesButton.vue'
import { useVariableInsert } from '../../composables/usePromptVariables'

const props = defineProps({
  // 初始内容（打开时的值；编辑期间为组件内部状态）
  content: { type: String, default: '' },
  placeholder: { type: String, default: '' },
  // 优化入口（null = 不显示该按钮）：{ title, useCase } 原样透传 gui.prompt-optimise
  optimize: { type: Object, default: null },
  // 来源/口径提示（空 = 不显示）：如「当前为内置默认（保存后覆盖）/ 当前为自定义提示词」
  hint: { type: String, default: '' },
  // 「恢复默认」入口（null = 不显示该按钮）：{ label, onClick }；onClick 由父组件落库并关闭弹框
  reset: { type: Object, default: null },
  // 保存回调（**父组件负责落库与关闭弹框**）；抛错 = 保持弹框打开（父已给可见失败提示）
  onSave: { type: Function, required: true },
  // 「变量插入」入口（OP-12）：true → 底部显示变量按钮（插入 {{...}} 到光标处）；缺省不显示。
  variables: { type: Boolean, default: false },
})

defineEmits(['cancel'])

const { t } = useI18n()

const text = ref(props.content || '')
const saving = ref(false)
const optimizing = ref(false)
// 优化前内容快照（仅 optimize.recover = true 时使用；弹框内「恢复优化」回填，**不落库**）
const optimizeSnapshot = ref('')
// 变量插入（OP-12）：把 {{...}} 插入到编辑框光标处（Textarea 根节点即 textarea → ref.$el）。
const inputRef = ref(null)
const { insert } = useVariableInsert({
  getEl: () => inputRef.value?.$el || null,
  getValue: () => text.value,
  setValue: (v) => { text.value = v },
})

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

// 恢复默认：清除自定义值（父组件落库 + 关闭弹框）；进行中不重入
async function handleReset() {
  if (saving.value || optimizing.value || !props.reset || !props.reset.onClick) return
  saving.value = true
  try {
    await props.reset.onClick()
  } catch (_) {
    // 父组件已给可见失败提示（弹框保持打开，内容不丢）
  } finally {
    saving.value = false
  }
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
/* 来源/口径提示（非编辑区）：单行小字，不占满剩余高度 */
.text-edit-hint {
  flex-shrink: 0;
  padding-bottom: 8px;
  font-size: 12px;
  color: var(--fg-secondary);
  line-height: 1.6;
}
.text-edit-input {
  flex: 1;
  min-height: 0;
  height: 100%;
  resize: none;
}
/* 底部按钮区：左侧「优化 / 恢复优化 / 恢复默认」+ 右侧「取消 / 保存」，固定在编辑区之外不随内容滚动 */
.text-edit-footer {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 0;
}
.footer-spacer {
  flex: 1;
}
</style>
