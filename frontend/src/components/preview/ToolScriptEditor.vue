<template>
  <div ref="containerRef" class="monaco-tool-container" />
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'

const props = defineProps({
  modelValue: { type: String, default: '' },
  language: { type: String, default: 'python' },
})

const emit = defineEmits(['update:modelValue'])
const containerRef = ref(null)
let editor = null

function getMonacoLanguage(lang) {
  const map = { python: 'python', js: 'javascript', powershell: 'powershell', sh: 'shell', bat: 'bat' }
  return map[lang] || 'plaintext'
}

onMounted(async () => {
  if (!containerRef.value) return
  try {
    const monaco = await import('monaco-editor')
    editor = monaco.editor.create(containerRef.value, {
      value: props.modelValue || '',
      language: getMonacoLanguage(props.language),
      readOnly: false,
      fontSize: 13,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      lineNumbers: 'on',
      renderWhitespace: 'selection',
      theme: document.documentElement.getAttribute('data-theme') === 'dark' ? 'vs-dark' : 'vs',
      automaticLayout: true,
    })
    editor.onDidChangeModelContent(() => {
      emit('update:modelValue', editor.getValue())
    })
  } catch (e) {
    console.error('[ToolScriptEditor] Monaco init failed:', e)
  }
})

onUnmounted(() => {
  if (editor) {
    editor.dispose()
    editor = null
  }
})

// Update editor content when modelValue changes externally
watch(() => props.modelValue, (val) => {
  if (editor && val !== editor.getValue()) {
    editor.setValue(val || '')
  }
})

// Update language when type changes
watch(() => props.language, (lang) => {
  if (editor) {
    const monacoLang = getMonacoLanguage(lang)
    const model = editor.getModel()
    if (model) {
      import('monaco-editor').then(monaco => {
        monaco.editor.setModelLanguage(model, monacoLang)
      })
    }
  }
})
</script>

<style scoped>
.monaco-tool-container {
  width: 100%;
  height: 300px;
  border: 1px solid var(--border);
  border-radius: 4px;
  overflow: hidden;
}
</style>
