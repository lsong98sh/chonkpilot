<template>
  <el-dialog v-model="visible" title="Initialize" width="760px" class="ana-dialog" draggable @open="loadTechInfo" @closed="reset">
    <div class="ana-body">

    <!-- Manual tech selection -->
    <div v-if="!generating && !analysisDone" class="step-analyze">
      <div class="desc">
        <p>Select the tech stack of your project and generate agent prompts for development, testing, code review, security, and deployment.</p>
      </div>

      <!-- Project type override -->
      <div class="override-row">
        <span class="override-label">Type:</span>
        <el-select v-model="overrideProjectType" class="override-select">
          <el-option
            v-for="t in projectTypes"
            :key="t.value"
            :label="t.label"
            :value="t.value"
          />
        </el-select>
      </div>

      <div class="override-row">
        <span class="override-label">Frontend:</span>
        <div class="checkbox-group-wrap">
          <div class="checkbox-sub-label">Frameworks</div>
          <el-checkbox-group v-model="overrideFrontend">
            <el-checkbox v-for="o in techOptions['前端框架'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="checkbox-sub-label">Libraries</div>
          <el-checkbox-group v-model="overrideFrontend">
            <el-checkbox v-for="o in techOptions['前端组件库'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="custom-add-row">
            <el-input v-model="customFrontend" size="small" placeholder="Custom..." class="custom-input" @keyup.enter="addCustomFrontend" />
            <el-button size="small" @click="addCustomFrontend">+</el-button>
          </div>
        </div>
      </div>
      <div class="override-row">
        <span class="override-label">Backend:</span>
        <div class="checkbox-group-wrap">
          <div class="checkbox-sub-label">Languages</div>
          <el-checkbox-group v-model="overrideBackend">
            <el-checkbox v-for="o in techOptions['后端语言'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="checkbox-sub-label">Frameworks</div>
          <el-checkbox-group v-model="overrideBackend">
            <el-checkbox v-for="o in techOptions['后端框架'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="custom-add-row">
            <el-input v-model="customBackend" size="small" placeholder="Custom..." class="custom-input" @keyup.enter="addCustomBackend" />
            <el-button size="small" @click="addCustomBackend">+</el-button>
          </div>
        </div>
      </div>
      <div class="override-row">
        <span class="override-label">Architecture:</span>
        <div class="checkbox-group-wrap">
          <el-checkbox-group v-model="overrideArchitecture">
            <el-checkbox v-for="o in techOptions['架构'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="custom-add-row">
            <el-input v-model="customArchitecture" size="small" placeholder="Custom..." class="custom-input" @keyup.enter="addCustomArchitecture" />
            <el-button size="small" @click="addCustomArchitecture">+</el-button>
          </div>
        </div>
      </div>
      <div class="override-row">
        <span class="override-label">Extra:</span>
        <div class="checkbox-group-wrap">
          <div class="checkbox-sub-label">Databases</div>
          <el-checkbox-group v-model="overrideExtra">
            <el-checkbox v-for="o in techOptions['数据库'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="checkbox-sub-label">Build Tools</div>
          <el-checkbox-group v-model="overrideExtra">
            <el-checkbox v-for="o in techOptions['构建工具'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="checkbox-sub-label">Containers</div>
          <el-checkbox-group v-model="overrideExtra">
            <el-checkbox v-for="o in techOptions['容器化'] || []" :key="o" :label="o" :value="o" size="small" />
          </el-checkbox-group>
          <div class="custom-add-row">
            <el-input v-model="customExtra" size="small" placeholder="Custom..." class="custom-input" @keyup.enter="addCustomExtra" />
            <el-button size="small" @click="addCustomExtra">+</el-button>
          </div>
        </div>
      </div>

      <!-- Generate button -->
      <div class="generate-section">
        <el-button
          type="primary"
          :loading="generating"
          :disabled="generating"
          @click="doGenerate"
        >
          <el-icon><MagicStick /></el-icon>
          {{ generating ? 'Generating...' : 'Generate Prompts' }}
        </el-button>
      </div>
    </div>

    <!-- Streaming markdown (during LLM generation) -->
    <div v-if="generating" class="step-prompts">
      <div class="section-title">Generating Prompts...</div>
      <div class="streaming-preview">
        <MarkdownRender :content="streamRawContent || ''" />
      </div>
      <div class="prompt-actions">
        <el-button @click="backToStart" :disabled="generating">
          <el-icon><Refresh /></el-icon> Cancel
        </el-button>
      </div>
    </div>

    <!-- Step 2: Generated prompts -->
    <div v-if="analysisDone" class="step-prompts">
      <div class="section-title">Generated Prompts</div>
      <div class="prompt-tabs">
        <el-tabs v-model="activePromptTab" type="border-card">
          <el-tab-pane
            v-for="p in generatedPrompts"
            :key="p.category"
            :label="p.useCase"
            :name="p.category"
          >
            <div class="prompt-card">
              <div class="prompt-desc">{{ p.description }}</div>
              <div class="prompt-toggle">
                <el-button
                  text
                  size="small"
                  :type="!promptPreviewMode ? 'primary' : ''"
                  @click="promptPreviewMode = false"
                >Code</el-button>
                <el-button
                  text
                  size="small"
                  :type="promptPreviewMode ? 'primary' : ''"
                  @click="promptPreviewMode = true"
                >Preview</el-button>
              </div>
              <el-input
                v-if="!promptPreviewMode"
                v-model="editedPrompts[activePromptTab]"
                type="textarea"
                :rows="12"
                class="prompt-textarea"
              />
              <div v-else class="prompt-preview">
                <MarkdownRender :content="editedPrompts[activePromptTab] || ''" />
              </div>
            </div>
          </el-tab-pane>
        </el-tabs>
      </div>

      <div class="prompt-actions">
        <el-button @click="backToStart">
          <el-icon><Refresh /></el-icon> Modify &amp; Regenerate
        </el-button>
        <el-button type="primary" @click="saveAsAgents">
          <el-icon><FolderOpened /></el-icon> Save as Agents
        </el-button>
      </div>
    </div>
  </div>
</el-dialog>
</template>

<script setup>
import { ref, reactive } from 'vue'
import {
  getTechInfo,
  generatePrompts,
  getProjectAgents,
  saveProjectAgents,
} from '../../api/config'
import { ElMessage } from 'element-plus'
import MarkdownRender from '@ashlesss/markstream-vue'
import '@ashlesss/markstream-vue/index.css'

const visible = ref(false)
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
const streamingProgress = ref('')
const streamRawContent = ref('')
let streamAbortController = null

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
    ElMessage.error('Failed to load tech options')
  }
}

function doGenerate() {
  generating.value = true
  streamingProgress.value = 'Generating...'
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
      streamingProgress.value = streamingProgress.value.length > 60
        ? 'Generating... ' + token.slice(0, 40)
        : streamingProgress.value + token
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
      streamingProgress.value = ''
      streamRawContent.value = ''
      streamAbortController = null
    },
    (errMsg) => {
      console.error('Generate failed:', errMsg)
      ElMessage.error('Prompt generation failed: ' + errMsg)
      generating.value = false
      streamingProgress.value = ''
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

    ElMessage.success('Prompts saved as agents')
    visible.value = false
  } catch (e) {
    console.error('Save agents failed:', e)
    ElMessage.error('Failed to save: ' + (e.message || 'unknown error'))
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

function open() {
  visible.value = true
}

defineExpose({ open })
</script>

<style scoped>
.ana-dialog :deep(.el-dialog__body) {
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

.prompt-toggle .el-button + .el-button {
  margin-left: 0;
}

.prompt-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 8px;
}
</style>
