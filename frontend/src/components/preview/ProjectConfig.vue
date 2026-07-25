<template>
  <div ref="configRoot" class="ide-config project-config-panel">
    <!-- List view: show tabs -->
    <template v-if="!editingType">
      <Tabs :tabs="tabsConfig" v-model="activeTab" class="cfg-tabs">
        <template #agents>
          <AgentList
            :agents="agentList"
            :max-height="tableMaxHeight"
            @add="startAdd('agents')"
            @edit="(i) => startEdit('agents', i)"
            @delete="(i) => deleteItem('agents', i)"
            @refresh="loadAgents"
            @open-analyze="openAnalyzeDialog"
          />
        </template>

        <template #tools>
          <ToolList
            :tools="toolList"
            :max-height="tableMaxHeight"
            @add="startAdd('tools')"
            @edit="(i) => startEdit('tools', i)"
            @delete="(i) => deleteItem('tools', i)"
          />
        </template>

        <template #notes>
          <NoteList
            :notes="notes"
            :max-height="tableMaxHeight"
            @add="noteStartAdd"
            @edit="(i) => noteStartEdit(notes[i])"
            @delete="(i) => notes.splice(i, 1)"
            @refresh="notesLoad"
          />
        </template>

        <template #security>
          <SecurityConfig :max-height="tableMaxHeight" />
        </template>

        <template #context>
          <form class="b-form context-form">
            <div class="form-grid-2col">
              <div class="form-item" style="flex-direction:column;align-items:stretch">
                <label class="form-label" style="width:auto;text-align:left;margin-bottom:4px">保留完整对话内容的轮次（默认6）</label>
                <Input v-model.number="keepFullTurns" type="number" :min="1" :max="50" :step="1" @blur="handleKeepFullTurnsChange" />
              </div>
              <div class="form-item" style="flex-direction:column;align-items:stretch">
                <label class="form-label" style="width:auto;text-align:left;margin-bottom:4px">简化区 Token 压缩阈值（默认 80000）</label>
                <Input v-model.number="compressTokenThreshold" type="number" :min="10000" :max="500000" :step="10000" @blur="handleCompressThresholdChange" />
              </div>
            </div>
            <p class="form-description">
              保留最近的 <strong>N</strong> 轮为完整内容 → 之前的轮次简化为工具摘要 → 当简化区总 token 数超过阈值时，自动压缩为 LLM 摘要。
              简化区的计算方式为：简化区所有消息字符数 / 4 + 已有摘要字符数 / 4。<br>
              示例：N=6, 阈值=80000 → 第 1-6 轮完整保留，第 7+ 轮简化，简化区超过约 32 万字符时触发压缩。
              压缩在每轮结束后异步执行，同一会话的压缩互斥进行，不会重复触发。
            </p>
          </form>
        </template>

        <template #prompts>
          <div class="prompts-container">
            <div class="tab-toolbar">
            <div class="prompt-tab-links">
              <div class="b-btn-group">
                <Button :type="promptSubTab === 'system' ? 'primary' : 'default'" size="small" @click="promptSubTab = 'system'">系统提示词</Button>
                <Button :type="promptSubTab === 'tool' ? 'primary' : 'default'" size="small" @click="promptSubTab = 'tool'">工具使用说明</Button>
                <Button :type="promptSubTab === 'summary' ? 'primary' : 'default'" size="small" @click="promptSubTab = 'summary'">摘要提示词</Button>
              </div>
            </div>
            <div class="tab-actions">
              <Button size="small" type="primary" @click="saveCurrentPrompt">保存</Button>
              <Button size="small" @click="resetCurrentPrompt">恢复默认</Button>
            </div>
          </div>
          <div class="prompt-edit-area">
            <p class="prompt-desc">{{ promptDesc }}</p>
            <Textarea
              v-model="currentPromptText"
              placeholder="加载中..."
              class="prompt-editor"
              :rows="100"
            />
          </div>
          </div>
        </template>
      </Tabs>
    </template>

    <!-- Agent Editor -->
    <AgentEditor
      v-else-if="editingType === 'agents'"
      :data="editingData"
      :is-new="editingIndex === -1"
      :llm-options="llmSelectOptions"
      @save="confirmEdit"
      @cancel="cancelEdit"
      @refresh="loadAgents"
    />

    <!-- Tool Editor (inline) -->
    <div v-else-if="editingType === 'tools'" class="edit-view">
      <div class="edit-header tool-edit-header">
        <div class="header-left">
          <Button text size="small" @click="cancelEdit">
            <Icon name="arrow-left" /> 返回列表
          </Button>
          <div class="sub-tabs">
            <span class="sub-tab" :class="{ active: toolEditTab === 'basic' }" @click="toolEditTab = 'basic'">基本信息</span>
            <span class="sub-tab" :class="{ active: toolEditTab === 'params' }" @click="toolEditTab = 'params'">参数说明</span>
            <span class="sub-tab" :class="{ active: toolEditTab === 'script' }" @click="toolEditTab = 'script'">脚本内容</span>
          </div>
        </div>
        <div class="header-center">
          <span class="edit-title">{{ editingIndex === -1 ? '添加工具' : '编辑工具' }}</span>
        </div>
        <div class="header-right">
          <Button size="small" type="primary" @click="confirmEdit">保存</Button>
        </div>
      </div>
      <div class="tool-edit-body">
        <!-- 基本信息 -->
        <div v-show="toolEditTab === 'basic'" class="tab-pane-content">
          <div class="basic-fields">
            <div class="form-item">
              <label class="form-label">名称</label>
              <Input v-model="toolEditData.name" placeholder="工具名称" />
            </div>
            <div class="form-item">
              <label class="form-label">功能描述</label>
              <Textarea v-model="toolEditData.description" :rows="2" placeholder="描述此工具的功能" />
            </div>
            <div class="form-item">
              <label class="form-label">脚本类型</label>
              <Select v-model="toolEditData.type" :options="toolTypeOptions" />
            </div>
            <template v-if="toolEditData.type === 'mcp'">
              <div class="form-item">
                <label class="form-label">MCP Server</label>
                <Select v-model="toolEditData.server_url" :options="mcpServerOptions" placeholder="选择已配置的 MCP Server" />
                <span v-if="mcpServerList.length === 0" class="form-hint">请先在 设置 → 全局 → MCP 中配置 Server</span>
              </div>
              <div class="form-item">
                <label class="form-label">MCP 工具名</label>
                <Input v-model="toolEditData.mcp_tool_name" placeholder="MCP Server 暴露的工具名，如 query_database" />
                <span class="form-hint">工具名称由 MCP Server 定义</span>
              </div>
            </template>
          </div>
        </div>
        <!-- 参数说明 -->
        <div v-show="toolEditTab === 'params'" class="tab-pane-content">
          <Textarea
            v-model="toolEditData.parameters"
            placeholder="参数说明和使用样例"
            class="tool-params-editor"
            :rows="100"
          />
        </div>
        <!-- 脚本内容 -->
        <div v-show="toolEditTab === 'script'" class="tab-pane-content">
          <div v-if="toolEditData.type === 'mcp'" class="script-empty-hint">
            MCP 工具不需要填写脚本内容，请在基本信息中配置 MCP Server 和工具名称。
          </div>
          <div v-else-if="toolEditData.type === 'executable'" class="file-select-row">
            <Input v-model="toolEditData.command" placeholder="选择可执行文件..." readonly />
            <Button @click="selectExecutableFile">选择文件</Button>
          </div>
          <div v-else-if="toolEditData.type === 'api'" class="script-full-editor">
            <Textarea
              v-model="toolEditData.command"
              :rows="2"
              placeholder="接口 URL（参数说明中填写 param 和 body 值）"
              class="tool-params-editor"
            />
          </div>
          <ToolScriptEditor
            v-else
            v-model="toolEditData.command"
            :language="toolEditData.type"
            :key="'toolscript-' + toolEditData.type"
            class="tool-script-full"
          />
        </div>
      </div>
    </div>

    <!-- Note Editor (inline) -->
    <div v-else-if="editingType === 'notes'" class="edit-view">
      <div class="edit-header">
        <Button text size="small" @click="cancelEdit">
          <Icon name="arrow-left" /> 返回列表
        </Button>
        <span class="edit-title">{{ editingIndex === -1 ? '添加笔记' : '编辑笔记' }}</span>
        <div class="edit-header-actions">
          <Button v-if="noteEditData.content" size="small" :loading="optimizingNote" :disabled="optimizingNote" @click="aiOptimizeNote">
            <Icon name="lightning" /> AI 优化
          </Button>
          <Button size="small" type="primary" :loading="noteSaving" @click="noteSave">保存</Button>
        </div>
      </div>
      <div class="agent-edit-body">
        <Input v-model="noteEditData.title" placeholder="标题" class="note-title-input" />
        <Textarea
          v-model="noteEditData.content"
          :rows="100"
          placeholder="在此输入笔记内容..."
          class="note-content-editor"
        />
      </div>
    </div>

    <!-- AnalyzeDialog (AI Assist) -->
    <AnalyzeDialog ref="analyzeDialogRef" />

    <!-- PromptOptimizer (hidden utility) -->
    <PromptOptimizer ref="promptOptimizerRef" />
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import Icon from '../icon/Icon.vue'
import { Input, Textarea, Select, Button, Tabs } from '../ui'
import { message, confirm } from '../ui'
import {
  getProjectAgents, saveProjectAgents,
  getProjectTools, saveProjectTools,
  getPrompt, setPrompt, optimizeAgentPrompt,
  getUserMCP, loadMissingAgentsFromResource,
  getAllConfig, setConfig,
} from '../../api/config'
import { getNotes, saveNote, deleteNote } from '../../api/note'
import { PickExecutableFile } from '../../../wailsjs/go/main/App'
import AnalyzeDialog from '../config/AnalyzeDialog.vue'
import ToolScriptEditor from './ToolScriptEditor.vue'
import AgentList from './projectconfig/AgentList.vue'
import AgentEditor from './projectconfig/AgentEditor.vue'
import ToolList from './projectconfig/ToolList.vue'
import NoteList from './projectconfig/NoteList.vue'
import SecurityConfig from './projectconfig/SecurityConfig.vue'
import CodeIndexConfig from './projectconfig/CodeIndexConfig.vue'
import PromptOptimizer from './projectconfig/PromptOptimizer.vue'
import { config as cg, updateConfig } from '../../utils/configStore'

const activeTab = ref('agents')
const agentList = ref([])
const toolList = ref([])
const notes = ref([])
const mcpServerList = ref([])
const systemPromptText = ref('')
const toolUsageText = ref('')
const summaryPromptText = ref('')
const promptSubTab = ref('system')
const analyzeDialogRef = ref(null)
const promptOptimizerRef = ref(null)
const optimizingNote = ref(false)
const noteSaving = ref(false)
const configRoot = ref(null)
const tableMaxHeight = ref(600)

const tabsConfig = [
  { label: '智能体', name: 'agents' },
  { label: '工具', name: 'tools' },
  { label: '笔记', name: 'notes' },
  { label: '安全', name: 'security' },
  { label: '上下文压缩', name: 'context' },
  { label: '提示词', name: 'prompts' },
]

const toolTypeOptions = [
  { label: 'Python', value: 'python' },
  { label: 'JavaScript', value: 'js' },
  { label: 'PowerShell', value: 'powershell' },
  { label: 'Shell', value: 'sh' },
  { label: 'Batch', value: 'bat' },
  { label: '可执行文件', value: 'executable' },
  { label: 'API 接口', value: 'api' },
  { label: 'MCP', value: 'mcp' },
]

// Tool editor tab
const toolEditTab = ref('basic')

// Context compression settings
const keepFullTurns = ref(6)
const compressTokenThreshold = ref(80000)

// Editing state
const editingType = ref('') // 'agents' | 'tools' | 'notes' | ''
const editingIndex = ref(-1)
const editingData = ref({})
const toolEditData = ref({})
const noteEditData = ref({})

const config = cg

const llmSelectOptions = computed(() => {
  const opts = [{ label: '（继承父进程）', value: '' }]
  if (config.value?.llms) {
    config.value.llms.forEach(llm => {
      if (llm.name) {
        opts.push({ label: `${llm.name} (${llm.protocol || llm.model || ''})`, value: llm.name })
      }
    })
  }
  return opts
})

const mcpServerOptions = computed(() => {
  return mcpServerList.value.map(srv => ({
    label: srv.name + (srv.url ? ' (' + srv.url + ')' : ''),
    value: srv.url,
  }))
})

const promptDesc = computed(() => {
  const descs = {
    system: '定义 AI 助手的角色和行为。',
    tool: '指导模型如何有效使用工具。',
    summary: '用于生成会话摘要的提示词，供上下文压缩使用。',
  }
  return descs[promptSubTab.value] || ''
})

const currentPromptText = computed({
  get: () => {
    switch (promptSubTab.value) {
      case 'system': return systemPromptText.value
      case 'tool': return toolUsageText.value
      case 'summary': return summaryPromptText.value
      default: return ''
    }
  },
  set: (val) => {
    switch (promptSubTab.value) {
      case 'system': systemPromptText.value = val; break
      case 'tool': toolUsageText.value = val; break
      case 'summary': summaryPromptText.value = val; break
    }
  },
})

const promptKeyMap = {
  system: 'system_prompt',
  tool: 'tool_usage_prompt',
  summary: 'summary_prompt',
}
const promptTextMap = {
  system: systemPromptText,
  tool: toolUsageText,
  summary: summaryPromptText,
}

// ====== Data loading ======

async function loadAgents() {
  try {
    const res = await getProjectAgents()
    agentList.value = res.agents || []
  } catch (_) { agentList.value = [] }
}

async function loadTools() {
  try {
    const res = await getProjectTools()
    toolList.value = res.tools || []
  } catch (_) { toolList.value = [] }
}

async function loadAll() {
  await Promise.all([loadAgents(), loadTools(), notesLoad()])
  // Load prompts
  try {
    const sp = await getPrompt('system_prompt')
    systemPromptText.value = sp.value || ''
  } catch (e) { console.error('[ProjectConfig] getPrompt tool_usage_prompt error:', e) }
  try {
    const tp = await getPrompt('tool_usage_prompt')
    toolUsageText.value = tp.value || ''
  } catch (e) { console.error('[ProjectConfig] getPrompt summary_prompt error:', e) }
  try {
    const sp = await getPrompt('summary_prompt')
    summaryPromptText.value = sp.value || ''
  } catch (e) { console.error('[ProjectConfig] getPrompt summary_prompt error:', e) }
  // Load context compression settings
  try {
    const res = await getAllConfig()
    const c = res.config || res
    if (c['keep_full_turns'] !== undefined) keepFullTurns.value = parseInt(c['keep_full_turns']) || 6
    if (c['compress_token_threshold'] !== undefined) compressTokenThreshold.value = parseInt(c['compress_token_threshold']) || 80000
  } catch (e) { console.error('[ProjectConfig] getAllConfig error:', e) }
}

async function notesLoad() {
  try {
    const res = await getNotes()
    notes.value = (res && res.notes) || []
  } catch (_) { notes.value = [] }
}

async function loadMCPServers() {
  try {
    const servers = await getUserMCP()
    mcpServerList.value = servers || []
  } catch (_) { mcpServerList.value = [] }
}

// ====== Agent operations ======

function startAdd(type) {
  cancelEdit()
  if (type === 'agents') {
    editingData.value = { title: '', useCase: '', prompt: '', _source: '', llmRef: '', blockedTools: [] }
    editingIndex.value = -1
  } else if (type === 'tools') {
    loadMCPServers()
    toolEditData.value = { name: '', description: '', parameters: '', type: 'python', command: '', _source: 'user' }
    editingIndex.value = -1
  }
  editingType.value = type
}

function startEdit(type, index) {
  editingType.value = type
  editingIndex.value = index
  if (type === 'agents') {
    editingData.value = { ...agentList.value[index] }
    if (!editingData.value.blockedTools) editingData.value.blockedTools = []
  } else if (type === 'tools') {
    loadMCPServers()
    const d = { ...toolList.value[index] }
    if (d.parameters !== undefined && d.parameters !== null && typeof d.parameters !== 'string') {
      d.parameters = JSON.stringify(d.parameters, null, 2)
    }
    if (!d.parameters) d.parameters = ''
    if (!d.description) d.description = ''
    toolEditData.value = d
  }
}

function cancelEdit() {
  editingType.value = ''
  editingIndex.value = -1
  editingData.value = {}
  toolEditData.value = {}
  noteEditData.value = {}
}

function confirmEdit(data) {
  const d = data || editingData.value
  if (editingType.value === 'agents') {
    if (editingIndex.value === -1) agentList.value.push(d)
    else agentList.value[editingIndex.value] = d
    saveAgents()
  } else if (editingType.value === 'tools') {
    const td = toolEditData.value
    if (editingIndex.value === -1) toolList.value.push(td)
    else toolList.value[editingIndex.value] = td
    saveTools()
  }
  cancelEdit()
  message.success('已保存')
}

async function deleteItem(type, index) {
  const label = type === 'agents' ? '智能体' : '工具'
  try {
    await confirm(`删除此${label}？`)
  } catch { return }
  if (type === 'agents') {
    agentList.value.splice(index, 1)
    saveAgents()
  } else {
    toolList.value.splice(index, 1)
    saveTools()
  }
  if (editingType.value === type && editingIndex.value === index) cancelEdit()
  message.success('已删除')
}

async function saveAgents() {
  try {
    await saveProjectAgents({ agents: agentList.value })
  } catch (e) {
    message.error('保存智能体失败: ' + (e.message || ''))
  }
}

async function saveTools() {
  try {
    await saveProjectTools(toolList.value)
  } catch (e) {
    message.error('保存工具失败: ' + (e.message || ''))
  }
}

// ====== Notes operations ======

function noteStartAdd() {
  cancelEdit()
  editingType.value = 'notes'
  editingIndex.value = -1
  noteEditData.value = { title: '', content: '' }
}

function noteStartEdit(note) {
  cancelEdit()
  editingType.value = 'notes'
  editingIndex.value = notes.value.findIndex(n => n.title === note.title && n.content === note.content)
  noteEditData.value = { title: note.title, content: note.content }
}

async function noteSave() {
  if (!noteEditData.value.title.trim()) {
    message.warning('标题不能为空')
    return
  }
  noteSaving.value = true
  try {
    await saveNote(noteEditData.value.title.trim(), noteEditData.value.content || '')
    message.success('已保存')
    cancelEdit()
    await notesLoad()
  } catch (e) {
    message.error('保存失败: ' + (e.message || ''))
  } finally {
    noteSaving.value = false
  }
}

// ====== Prompt operations ======

async function saveCurrentPrompt() {
  const key = promptKeyMap[promptSubTab.value]
  const textRef = promptTextMap[promptSubTab.value]
  if (!key || !textRef) return
  try {
    await setPrompt(key, textRef.value)
    message.success('提示词已保存')
  } catch (e) {
    message.error('保存失败: ' + (e.message || ''))
  }
}

async function resetCurrentPrompt() {
  const key = promptKeyMap[promptSubTab.value]
  const textRef = promptTextMap[promptSubTab.value]
  if (!key || !textRef) return
  try {
    await setPrompt(key, '')
    const sp = await getPrompt(key)
    textRef.value = sp.value || ''
    message.success('已恢复默认提示词')
  } catch (e) {
    message.error('恢复失败: ' + (e.message || ''))
  }
}

// ====== Tool operations ======

async function selectExecutableFile() {
  try {
    const res = await PickExecutableFile()
    if (res?.path) {
      toolEditData.value.command = res.path
    }
  } catch (err) {
    console.error('[IDEConfig] File dialog bridge error:', err)
    const path = prompt('输入可执行文件路径:')
    if (path) toolEditData.value.command = path
  }
}

function openAnalyzeDialog() {
  analyzeDialogRef.value?.open()
}

function aiOptimizeNote() {
  const note = noteEditData.value
  if (!note || !note.content) {
    message.warning('请先输入笔记内容')
    return
  }
  if (optimizingNote.value) return
  optimizingNote.value = true
  message.info('正在优化笔记...')
  let optimizedText = ''
  optimizeAgentPrompt(
    { title: note.title || 'Note', useCase: 'Improve this note', prompt: note.content },
    (content) => {
      optimizedText += content
      noteEditData.value.content = optimizedText
    },
    (prompt) => {
      optimizingNote.value = false
      if (prompt) {
        note.content = prompt
        noteEditData.value.content = prompt
        message.success('笔记优化成功')
      }
    },
    (errMsg) => {
      optimizingNote.value = false
      message.error('优化失败: ' + errMsg)
    },
  )
}

// ====== Lifecycle ======

let resObs = null

onMounted(() => {
  loadAll()
  if (configRoot.value) {
    resObs = new ResizeObserver(([entry]) => {
      const h = entry.contentRect.height - 96
      if (h > 100) tableMaxHeight.value = Math.floor(h)
    })
    resObs.observe(configRoot.value)
  }
})

// Auto-save context compression settings (use events instead of watch)
function handleKeepFullTurnsChange() {
  setConfig('keep_full_turns', String(keepFullTurns.value)).catch(e => console.warn('[ProjectConfig] setConfig error:', e))
}

function handleCompressThresholdChange() {
  setConfig('compress_token_threshold', String(compressTokenThreshold.value)).catch(e => console.warn('[ProjectConfig] setConfig error:', e))
}

onUnmounted(() => {
  try {
    if (resObs) resObs.disconnect()
    configTeardown()
  } catch (e) { console.error('[ProjectConfig] onUnmounted error:', e) }
})
</script>

<style scoped>
.ide-config {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 0;
  background: var(--bg-primary);
}

/* Edit views */
.edit-view {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.edit-header {
  flex-shrink: 0;
  padding: 6px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 0;
}

.edit-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}

.edit-header-actions {
  display: flex;
  gap: 6px;
}

.edit-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px;
}

.agent-edit-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 16px;
  overflow: hidden;
}

/* Tab toolbar */
.tab-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  gap: 8px;
  flex-shrink: 0;
}

.tab-actions {
  display: flex;
  gap: 6px;
}

/* Prompts container */
.prompts-container {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

/* Prompt tab */
.prompt-edit-area {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding-top: 12px;
}
.prompt-edit-area .prompt-desc {
  flex-shrink: 0;
  margin-bottom: 8px;
}
.prompt-edit-area .prompt-editor {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.prompt-edit-area .prompt-editor :deep(textarea) {
  flex: 1;
  height: 100% !important;
  min-height: 100%;
  resize: none;
}

.prompt-desc {
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 8px;
}

.prompt-editor {
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 13px;
}

.prompt-tab-links {
  display: flex;
  align-items: center;
}

/* Context compression */
.context-form {
  padding-top: 16px;
}
.context-form .form-description {
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-secondary);
}

/* Tool editor */
.tool-edit-header {
  padding: 6px 12px;
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 0;
}
.tool-edit-header .header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tool-edit-header .header-center {
  flex: 1;
  text-align: center;
}
.tool-edit-header .header-right {
  display: flex;
  align-items: center;
  gap: 6px;
}
.tool-edit-header .sub-tabs {
  display: flex;
  align-items: center;
  gap: 0;
}
.tool-edit-header .sub-tab {
  padding: 4px 12px;
  font-size: 13px;
  color: var(--text-secondary);
  cursor: pointer;
  user-select: none;
  transition: color 0.15s;
  border-bottom: 2px solid transparent;
}
.tool-edit-header .sub-tab:hover {
  color: var(--text-primary);
}
.tool-edit-header .sub-tab.active {
  color: var(--accent);
  font-weight: 500;
  border-bottom-color: var(--accent);
}
.tool-edit-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.tool-edit-body .tab-pane-content {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 4px 16px;
}
.tool-edit-body .basic-fields {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 8px 0;
  max-width: 700px;
}
.tool-edit-body .form-item {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}
.tool-edit-body .form-label {
  flex-shrink: 0;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
  min-width: 72px;
  line-height: 28px;
  text-align: left;
}
.tool-edit-body .form-item .b-input,
.tool-edit-body .form-item .b-select {
  flex: 1;
}
.tool-params-editor {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 13px;
}
.tool-params-editor :deep(textarea) {
  flex: 1;
  height: 100% !important;
  min-height: 100%;
  resize: none;
}
.script-empty-hint {
  color: var(--text-muted);
  font-size: 13px;
  padding: 24px;
  text-align: center;
}
.script-full-editor {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.tool-script-full {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* Form layout */
.b-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.form-item {
  display: flex;
  align-items: center;
  gap: 8px;
}
.form-label {
  flex-shrink: 0;
  font-size: 13px;
  color: var(--text-primary);
  text-align: right;
}
.form-hint {
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
}

.note-title-input {
  flex-shrink: 0;
}

.note-content-editor {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 13px;
}
.note-content-editor :deep(textarea) {
  flex: 1;
  height: 100% !important;
  min-height: 100%;
  resize: none;
}

.file-select-row {
  display: flex;
  gap: 8px;
}

.b-btn-group {
  display: inline-flex;
  gap: 0;
}
.b-btn-group .b-btn {
  border-radius: 0;
  border: 1px solid var(--border, #d9d9d9);
  border-right-width: 0;
}
.b-btn-group .b-btn:first-child {
  border-radius: 4px 0 0 4px;
}
.b-btn-group .b-btn:last-child {
  border-radius: 0 4px 4px 0;
  border-right-width: 1px;
}

/* Tabs body content */
.project-config-panel .cfg-tabs :deep(.b-tabs-body) {
  padding: 0.5em 16px 0;
  max-height: 680px;
  overflow-y: auto;
}

/* Context form: 2-column grid */
.form-grid-2col {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

/* 统一字体大小 */
.project-config-panel :deep(.b-input),
.project-config-panel :deep(.b-textarea),
.project-config-panel :deep(.b-select__native),
.project-config-panel :deep(.b-btn),
.project-config-panel :deep(.b-tag),
.project-config-panel :deep(.b-table-inline),
.project-config-panel :deep(.b-checkbox) {
  font-size: 13px;
}
</style>