<template>
  <div />
</template>

<script setup>
import { ref, reactive, h, defineComponent } from 'vue'
import {
  getTechInfo,
  generatePrompts,
  getProjectAgents,
  saveProjectAgents,
} from '../../api/config'
import { Button, Input, Textarea, Select, Tabs, message } from '../ui'
import Icon from '../icon/Icon.vue'
import MarkdownRender from '@ashlesss/markstream-vue'
import '@ashlesss/markstream-vue/index.css'
import { dialog } from '../dialog'

let handle = null
let streamAbortController = null

function open() {
  const generating = ref(false)
  const projectTypes = ref([])
  const techOptions = ref({})
  const generatedPrompts = ref([])
  const analysisDone = ref(false)
  const activePromptTab = ref('')
  const editedPrompts = reactive({})
  const promptPreviewMode = ref(false)

  const overrideProjectType = ref('')
  const overrideFrontend = ref([])
  const overrideBackend = ref([])
  const overrideArchitecture = ref([])
  const overrideExtra = ref([])
  const customFrontend = ref('')
  const customBackend = ref('')
  const customArchitecture = ref('')
  const customExtra = ref('')
  const streamRawContent = ref('')

  function toggleCheckbox(list, item) {
    const idx = list.value.indexOf(item)
    if (idx >= 0) {
      list.value = list.value.filter(v => v !== item)
    } else {
      list.value = [...list.value, item]
    }
  }

  function renderCheckboxGroup(options, modelValue, onToggle) {
    return h('div', { class: 'native-checkbox-group' },
      (options || []).map(o =>
        h('label', { class: 'checkbox-item', key: o }, [
          h('input', {
            type: 'checkbox',
            value: o,
            checked: modelValue.value.includes(o),
            onChange: () => onToggle(modelValue, o),
          }),
          h('span', null, o),
        ])
      )
    )
  }

  function renderCustomAddRow(modelValue, customVal, addFn) {
    return h('div', { class: 'custom-add-row' }, [
      h('input', {
        class: 'b-input custom-input',
        value: customVal.value,
        onInput: (e) => { customVal.value = e.target.value },
        placeholder: 'Custom...',
        onKeyup: (e) => { if (e.key === 'Enter') addFn() },
      }),
      h(Button, { size: 'small', onClick: addFn }, () => '+'),
    ])
  }

  function addCustomFrontend() {
    const v = customFrontend.value.trim()
    if (v && !overrideFrontend.value.includes(v)) {
      overrideFrontend.value = [...overrideFrontend.value, v]
    }
    customFrontend.value = ''
  }

  function addCustomBackend() {
    const v = customBackend.value.trim()
    if (v && !overrideBackend.value.includes(v)) {
      overrideBackend.value = [...overrideBackend.value, v]
    }
    customBackend.value = ''
  }

  function addCustomExtra() {
    const v = customExtra.value.trim()
    if (v && !overrideExtra.value.includes(v)) {
      overrideExtra.value = [...overrideExtra.value, v]
    }
    customExtra.value = ''
  }

  function addCustomArchitecture() {
    const v = customArchitecture.value.trim()
    if (v && !overrideArchitecture.value.includes(v)) {
      overrideArchitecture.value = [...overrideArchitecture.value, v]
    }
    customArchitecture.value = ''
  }

  async function loadTechInfo() {
    try {
      const res = await getTechInfo()
      projectTypes.value = res.types || []
      techOptions.value = res.options || {}
      overrideProjectType.value = projectTypes.value[0]?.value || ''
    } catch (e) {
      console.error('Load tech info failed:', e)
      message.error('Failed to load tech options')
    }
  }

  function doGenerate() {
    generating.value = true
    streamRawContent.value = ''
    const payload = {
      analysis: null,
      projectType: overrideProjectType.value,
      frontend: overrideFrontend.value,
      backend: overrideBackend.value,
      architecture: overrideArchitecture.value,
      extra: overrideExtra.value,
    }

    streamAbortController = generatePrompts(payload,
      (token) => {
        streamRawContent.value += token
      },
      (prompts) => {
        generatedPrompts.value = prompts || []
        if (generatedPrompts.value.length > 0) {
          activePromptTab.value = generatedPrompts.value[0].category
          for (const p of generatedPrompts.value) {
            editedPrompts[p.category] = p.prompt
          }
          promptPreviewMode.value = true
          analysisDone.value = true
        }
        generating.value = false
        streamRawContent.value = ''
        streamAbortController = null
      },
      (errMsg) => {
        console.error('Generate failed:', errMsg)
        message.error('Prompt generation failed: ' + errMsg)
        generating.value = false
        streamRawContent.value = ''
        streamAbortController = null
      }
    )
  }

  async function saveAsAgents() {
    try {
      let existingAgents = []
      try {
        const res = await getProjectAgents()
        existingAgents = res.agents || []
      } catch (e) { console.warn('[AnalyzeDialog] Failed to load existing agents:', e) }

      const userAgents = existingAgents.filter(a => a._source !== 'llm')
      const newAgents = generatedPrompts.value.map(p => ({
        title: p.useCase,
        useCase: p.category,
        prompt: editedPrompts[p.category] || p.prompt,
        _source: 'llm',
      }))
      const merged = [...userAgents, ...newAgents]
      await saveProjectAgents({ agents: merged })
      message.success('Prompts saved as agents')
      handle?.close()
    } catch (e) {
      console.error('Save agents failed:', e)
      message.error('Failed to save: ' + (e.message || 'unknown error'))
    }
  }

  function backToStart() {
    analysisDone.value = false
    generatedPrompts.value = []
    generating.value = false
    streamRawContent.value = ''
    if (streamAbortController) {
      streamAbortController.abort()
      streamAbortController = null
    }
  }

  function reset() {
    generating.value = false
    generatedPrompts.value = []
    analysisDone.value = false
    promptPreviewMode.value = false
    streamRawContent.value = ''
    overrideFrontend.value = []
    overrideBackend.value = []
    overrideArchitecture.value = []
    overrideExtra.value = []
    customFrontend.value = ''
    customBackend.value = ''
    customArchitecture.value = ''
    customExtra.value = ''
  }

  loadTechInfo()

  handle = dialog.show(defineComponent({
    setup() {
      return () => h('div', { class: 'ana-body' }, [
        // Manual tech selection
        (!generating.value && !analysisDone.value)
          ? [
              h('div', { class: 'desc' }, [
                h('p', null, 'Select the tech stack of your project and generate agent prompts for development, testing, code review, security, and deployment.'),
              ]),
              // Project type
              h('div', { class: 'override-row' }, [
                h('span', { class: 'override-label' }, 'Type:'),
                h(Select, {
                  modelValue: overrideProjectType.value,
                  'onUpdate:modelValue': (v) => { overrideProjectType.value = v },
                  class: 'override-select',
                  options: projectTypes.value.map(t => ({ label: t.label, value: t.value })),
                }),
              ]),
              // Frontend
              h('div', { class: 'override-row' }, [
                h('span', { class: 'override-label' }, 'Frontend:'),
                h('div', { class: 'checkbox-group-wrap' }, [
                  h('div', { class: 'checkbox-sub-label' }, 'Frameworks'),
                  renderCheckboxGroup(techOptions.value['前端框架'], overrideFrontend, toggleCheckbox),
                  h('div', { class: 'checkbox-sub-label' }, 'Libraries'),
                  renderCheckboxGroup(techOptions.value['前端组件库'], overrideFrontend, toggleCheckbox),
                  renderCustomAddRow(overrideFrontend, customFrontend, addCustomFrontend),
                ]),
              ]),
              // Backend
              h('div', { class: 'override-row' }, [
                h('span', { class: 'override-label' }, 'Backend:'),
                h('div', { class: 'checkbox-group-wrap' }, [
                  h('div', { class: 'checkbox-sub-label' }, 'Languages'),
                  renderCheckboxGroup(techOptions.value['后端语言'], overrideBackend, toggleCheckbox),
                  h('div', { class: 'checkbox-sub-label' }, 'Frameworks'),
                  renderCheckboxGroup(techOptions.value['后端框架'], overrideBackend, toggleCheckbox),
                  renderCustomAddRow(overrideBackend, customBackend, addCustomBackend),
                ]),
              ]),
              // Architecture
              h('div', { class: 'override-row' }, [
                h('span', { class: 'override-label' }, 'Architecture:'),
                h('div', { class: 'checkbox-group-wrap' }, [
                  renderCheckboxGroup(techOptions.value['架构'], overrideArchitecture, toggleCheckbox),
                  renderCustomAddRow(overrideArchitecture, customArchitecture, addCustomArchitecture),
                ]),
              ]),
              // Extra
              h('div', { class: 'override-row' }, [
                h('span', { class: 'override-label' }, 'Extra:'),
                h('div', { class: 'checkbox-group-wrap' }, [
                  h('div', { class: 'checkbox-sub-label' }, 'Databases'),
                  renderCheckboxGroup(techOptions.value['数据库'], overrideExtra, toggleCheckbox),
                  h('div', { class: 'checkbox-sub-label' }, 'Build Tools'),
                  renderCheckboxGroup(techOptions.value['构建工具'], overrideExtra, toggleCheckbox),
                  h('div', { class: 'checkbox-sub-label' }, 'Containers'),
                  renderCheckboxGroup(techOptions.value['容器化'], overrideExtra, toggleCheckbox),
                  renderCustomAddRow(overrideExtra, customExtra, addCustomExtra),
                ]),
              ]),
              // Generate button
              h('div', { class: 'generate-section' }, [
                h(Button, {
                  type: 'primary',
                  loading: generating.value,
                  disabled: generating.value,
                  onClick: doGenerate,
                }, () => [
                  h(Icon, { name: 'magic-stick', size: 14 }),
                  generating.value ? 'Generating...' : 'Generate Prompts',
                ]),
              ]),
            ]
          : null,

        // Streaming markdown
        generating.value
          ? h('div', { class: 'step-prompts' }, [
              h('div', { class: 'section-title' }, 'Generating Prompts...'),
              h('div', { class: 'streaming-preview' }, [
                h(MarkdownRender, { content: streamRawContent.value || '' }),
              ]),
              h('div', { class: 'prompt-actions' }, [
                h(Button, { onClick: backToStart, disabled: generating.value }, () => [
                  h(Icon, { name: 'refresh', size: 14 }),
                  ' Cancel',
                ]),
              ]),
            ])
          : null,

        // Generated prompts
        analysisDone.value
          ? h('div', { class: 'step-prompts' }, [
              h('div', { class: 'section-title' }, 'Generated Prompts'),
              h('div', { class: 'prompt-tabs' }, (() => {
                const tabSlots = {}
                generatedPrompts.value.forEach(p => {
                  tabSlots[p.category] = () =>
                    h('div', { class: 'prompt-card' }, [
                      h('div', { class: 'prompt-desc' }, p.description),
                      h('div', { class: 'prompt-toggle' }, [
                        h(Button, { text: true, size: 'small', type: !promptPreviewMode.value ? 'primary' : '', onClick: () => { promptPreviewMode.value = false } }, () => 'Code'),
                        h(Button, { text: true, size: 'small', type: promptPreviewMode.value ? 'primary' : '', onClick: () => { promptPreviewMode.value = true } }, () => 'Preview'),
                      ]),
                      !promptPreviewMode.value
                        ? h(Textarea, {
                            modelValue: editedPrompts[activePromptTab.value],
                            'onUpdate:modelValue': (v) => { editedPrompts[activePromptTab.value] = v },
                            rows: 12,
                            class: 'prompt-textarea',
                          })
                        : h('div', { class: 'prompt-preview' }, [
                            h(MarkdownRender, { content: editedPrompts[activePromptTab.value] || '' }),
                          ]),
                    ])
                })
                return h(Tabs, {
                  tabs: generatedPrompts.value.map(p => ({ name: p.category, label: p.useCase })),
                  modelValue: activePromptTab.value,
                  'onUpdate:modelValue': (v) => { activePromptTab.value = v },
                }, tabSlots)
              })()),
              h('div', { class: 'prompt-actions' }, [
                h(Button, { onClick: backToStart }, () => [
                  h(Icon, { name: 'refresh', size: 14 }),
                  ' Modify & Regenerate',
                ]),
                h(Button, { type: 'primary', onClick: saveAsAgents }, () => [
                  h(Icon, { name: 'folder-opened', size: 14 }),
                  ' Save as Agents',
                ]),
              ]),
            ])
          : null,
      ])
    }
  }), {
    title: 'Initialize',
    width: 760,
    closable: true,
    bodyClass: 'analyze-dialog-body',
    onAction: (action) => {
      if (action === 'close' || action === 'cancel') {
        if (streamAbortController) {
          streamAbortController.abort()
          streamAbortController = null
        }
        reset()
        handle.close()
      }
    }
  })
}

defineExpose({ open })
</script>

<style>
.analyze-dialog-body {
  padding-top: 8px;
  overflow: hidden;
}

.ana-body {
  max-height: 70vh;
  overflow-y: auto;
  padding-right: 4px;
}

.desc {
  margin-bottom: 16px;
  color: var(--text-secondary);
  font-size: 13px;
}

.section-title {
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 12px;
  color: var(--text-primary);
}

.override-row {
  display: flex;
  gap: 8px;
  margin-bottom: 10px;
}

.override-label {
  font-size: 13px;
  color: var(--text-secondary);
  white-space: nowrap;
  width: 80px;
  flex-shrink: 0;
  padding-top: 4px;
}

.override-select {
  flex: 1;
}

.checkbox-group-wrap {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.checkbox-sub-label {
  font-size: 11px;
  color: var(--text-muted);
  margin-top: 4px;
}

.checkbox-sub-label:first-child {
  margin-top: 0;
}

.native-checkbox-group {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 12px;
}

.checkbox-item {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 12px;
  color: var(--text-primary);
  cursor: pointer;
  user-select: none;
}

.checkbox-item input[type="checkbox"] {
  cursor: pointer;
}

.custom-add-row {
  display: flex;
  gap: 4px;
  margin-top: 4px;
}

.custom-input {
  width: 140px;
}

.generate-section {
  text-align: center;
  margin: 20px 0 8px;
}

.step-prompts {
  min-height: 200px;
}

.prompt-tabs {
  margin-bottom: 16px;
}

.prompt-card {
  padding: 4px 0;
}

.prompt-desc {
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 8px;
}

.prompt-textarea {
  font-family: 'Consolas', 'Courier New', monospace;
  font-size: 12px;
}

.prompt-preview {
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  padding: 12px 16px;
  max-height: 400px;
  overflow-y: auto;
  background: #fff;
}

.streaming-preview {
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  padding: 12px 16px;
  max-height: 50vh;
  overflow-y: auto;
  background: #fff;
  margin-bottom: 12px;
}

.prompt-toggle {
  display: flex;
  gap: 0;
  margin-bottom: 8px;
}

.prompt-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 8px;
}
</style>
