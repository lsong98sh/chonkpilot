<script setup>
/**
 * PromptOptimizer — provides AI optimization for prompts and notes.
 * Used as a utility via expose.
 */
import { ref } from 'vue'
import { message } from '../../ui'
import { optimizeAgentPrompt } from '../../../api/config'

const optimizing = ref(false)

function optimizePrompt(data, onUpdate, onDone) {
  if (!data.prompt) {
    message.warning('请先输入内容')
    return
  }
  if (optimizing.value) return
  optimizing.value = true
  message.info('正在优化...')
  let optimizedText = ''
  optimizeAgentPrompt(
    { title: data.title || 'Content', useCase: data.useCase || 'Improve this content', prompt: data.prompt },
    (content) => {
      optimizedText += content
      onUpdate(optimizedText)
    },
    (result) => {
      optimizing.value = false
      if (result) {
        onDone(result)
        message.success('优化成功')
      }
    },
    (errMsg) => {
      optimizing.value = false
      message.error('优化失败: ' + errMsg)
    },
  )
}

defineExpose({ optimizePrompt, optimizing })
</script>

<template>
  <div style="display:none"></div>
</template>