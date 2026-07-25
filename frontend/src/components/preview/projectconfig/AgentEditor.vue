<template>
  <div class="edit-view">
    <div class="edit-header">
      <div class="header-left">
        <Button text size="small" @click="$emit('cancel')">
          <Icon name="arrow-left" /> 返回列表
        </Button>
        <div class="sub-tabs">
          <span class="sub-tab" :class="{ active: agentEditTab === 'basic' }" @click="agentEditTab = 'basic'">基本信息</span>
          <span class="sub-tab" :class="{ active: agentEditTab === 'prompt' }" @click="agentEditTab = 'prompt'">提示词</span>
          <span class="sub-tab" :class="{ active: agentEditTab === 'tools' }" @click="agentEditTab = 'tools'">可用工具</span>
        </div>
      </div>
      <div class="header-center">
        <span class="edit-title">{{ isNew ? '添加智能体' : '编辑智能体' }}</span>
      </div>
      <div class="header-right">
        <Button v-if="!isNew && data._source" size="small" @click="handleRestore">
          <Icon name="refresh" /> 恢复
        </Button>
        <Button size="small" :loading="optimizing" :disabled="optimizing" @click="aiOptimize">
          <Icon name="lightning" /> AI 优化
        </Button>
        <Button size="small" type="primary" @click="$emit('save', localData)">保存</Button>
      </div>
    </div>
    <div class="agent-edit-body">
      <!-- 基本信息 -->
      <div v-show="agentEditTab === 'basic'" class="tab-pane-content basic-panel">
        <div class="basic-fields">
          <div class="form-item">
            <label class="form-label">名称</label>
            <Input v-model="localData.title" placeholder="Agent 名称" />
          </div>
          <div class="form-item">
            <label class="form-label">使用场景</label>
            <Input v-model="localData.useCase" placeholder="描述使用场景" />
          </div>
          <div class="form-item">
            <label class="form-label">LLM</label>
            <Select v-model="localData.llmRef" :options="llmOptions" placeholder="LLM（继承父进程）" />
          </div>
        </div>
      </div>
      <!-- 提示词 -->
      <div v-show="agentEditTab === 'prompt'" class="tab-pane-content">
        <Textarea
          v-model="localData.prompt"
          placeholder="You are an architecture expert..."
          class="agent-prompt-editor"
          :rows="100"
        />
      </div>
      <!-- 可用工具 -->
      <div v-show="agentEditTab === 'tools'" class="tab-pane-content">
        <div class="tool-select-area">
          <div class="tool-select-header">
            <label class="b-checkbox">
              <input type="checkbox" v-model="allToolsSelected"
                :indeterminate="isIndeterminate"
                @change="onToggleAll" />
              <span class="b-checkbox__label">全选/取消全选</span>
            </label>
            <span class="tool-select-count">{{ checkedCount }}/{{ totalTools }} 个工具</span>
          </div>
          <div class="tool-group-list">
            <div v-for="group in toolGroups" :key="group.name" class="tool-group">
              <div class="tool-group-header" @click="group.expanded = !group.expanded">
                <Icon :name="group.expanded ? 'arrow-down' : 'arrow-right'" />
                <span class="tool-group-name">{{ group.label }}</span>
                <span class="tool-group-info">{{ group.checkedCount }}/{{ group.tools.length }}</span>
              </div>
              <div v-show="group.expanded" class="tool-group-items">
                <div v-for="tool in group.tools" :key="tool.name" class="tool-item">
                  <label class="b-checkbox">
                    <input type="checkbox" v-model="tool.checked" @change="onToolCheckChange" />
                    <span class="b-checkbox__label">{{ tool.name }}</span>
                  </label>
                  <span class="tool-desc">{{ tool.description }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onUpdated } from 'vue'
import Icon from '../../icon/Icon.vue'
import { Input, Textarea, Select, Button } from '../../ui'
import { message } from '../../ui'
import { restoreAgent, optimizeAgentPrompt } from '../../../api/config'

const props = defineProps({
  data: { type: Object, default: () => ({ title: '', useCase: '', prompt: '', _source: '', llmRef: '', blockedTools: [] }) },
  isNew: { type: Boolean, default: false },
  llmOptions: { type: Array, default: () => [] },
})

const emit = defineEmits(['save', 'cancel', 'refresh'])

const localData = reactive({ ...props.data })
let prevData = null
onUpdated(() => {
  if (props.data !== prevData) {
    Object.assign(localData, props.data)
  }
  prevData = props.data
})

const agentEditTab = ref('basic')
const allToolsSelected = ref(true)
const isIndeterminate = ref(false)
const optimizing = ref(false)

const toolGroups = computed(() => {
  const builtinTools = getBuiltinToolGroups()
  const blocked = new Set(localData.blockedTools || [])
  return builtinTools.map(group => {
    const tools = group.tools.map(t => ({
      ...t,
      checked: !blocked.has(t.name),
    }))
    const checkedCount = tools.filter(t => t.checked).length
    return { ...group, tools, checkedCount, expanded: true }
  })
})

const totalTools = computed(() => toolGroups.value.reduce((s, g) => s + g.tools.length, 0))
const checkedCount = computed(() => toolGroups.value.reduce((s, g) => s + g.tools.filter(t => t.checked).length, 0))

function onToggleAll() {
  const val = allToolsSelected.value
  for (const group of toolGroups.value) {
    for (const tool of group.tools) {
      tool.checked = val
    }
  }
  updateBlockedTools()
  isIndeterminate.value = false
}

function onToolCheckChange() {
  updateBlockedTools()
  const total = toolGroups.value.reduce((s, g) => s + g.tools.length, 0)
  const checked = toolGroups.value.reduce((s, g) => s + g.tools.filter(t => t.checked).length, 0)
  allToolsSelected.value = checked === total
  isIndeterminate.value = checked > 0 && checked < total
}

function updateBlockedTools() {
  const blocked = []
  for (const group of toolGroups.value) {
    for (const tool of group.tools) {
      if (!tool.checked) {
        blocked.push(tool.name)
      }
    }
  }
  localData.blockedTools = blocked
}

function restoreToolSelection() {
  const blocked = localData.blockedTools || []
  const total = toolGroups.value.reduce((s, g) => s + g.tools.length, 0)
  const checked = toolGroups.value.reduce((s, g) => s + g.tools.filter(t => t.checked).length, 0)
  allToolsSelected.value = blocked.length === 0 || checked === total
  isIndeterminate.value = checked > 0 && checked < total
}

async function handleRestore() {
  if (!localData._source || !localData.id) return
  try {
    const res = await restoreAgent(localData.id)
    message.success(`智能体 "${res.title || ''}" 已从资源恢复`)
    emit('refresh')
  } catch (e) {
    message.error('恢复失败: ' + (e.message || ''))
  }
}

function aiOptimize() {
  if (!localData.prompt) {
    message.warning('请先输入提示词')
    return
  }
  if (optimizing.value) return
  optimizing.value = true
  message.info('正在优化提示词...')
  let optimizedText = ''
  optimizeAgentPrompt(
    { title: localData.title, useCase: localData.useCase, prompt: localData.prompt },
    (content) => {
      optimizedText += content
      localData.prompt = optimizedText
    },
    (prompt) => {
      optimizing.value = false
      if (prompt) {
        localData.prompt = prompt
        message.success('提示词优化成功')
      }
    },
    (errMsg) => {
      optimizing.value = false
      message.error('优化失败: ' + errMsg)
    },
  )
}

function getBuiltinToolGroups() {
  return [
    { name: 'read', label: '读文件/搜索', tools: [
      { name: 'read_file', description: '读取文件内容' },
      { name: 'grep', description: '搜索文件内容' },
      { name: 'list_directory', description: '列出目录' },
      { name: 'query_codebase', description: '查询代码库索引' },
      { name: 'fetch', description: 'HTTP 获取' },
    ]},
    { name: 'write', label: '写文件/编辑', tools: [
      { name: 'write_file', description: '写入文件' },
      { name: 'replace', description: '替换内容' },
      { name: 'patch', description: '应用 patch' },
      { name: 'diff', description: '文件差异' },
      { name: 'rename', description: '重命名' },
      { name: 'remove', description: '删除文件/目录' },
      { name: 'make_directory', description: '创建目录' },
    ]},
    { name: 'execute', label: '命令执行', tools: [
      { name: 'execute_command', description: '执行命令' },
      { name: 'execute_python_script', description: '执行 Python' },
    ]},
    { name: 'task', label: '任务管理', tools: [
      { name: 'run_tasks', description: '运行任务组' },
      { name: 'process_task_status', description: '任务状态查询' },
      { name: 'process_task_stop', description: '停止任务' },
      { name: 'process_wait', description: '等待任务' },
      { name: 'query_task', description: '查询任务' },
    ]},
    { name: 'web', label: 'Web 自动化', tools: [
      { name: 'web_start', description: '启动浏览器' },
      { name: 'web_open', description: '打开网页' },
      { name: 'web_close', description: '关闭浏览器' },
      { name: 'web_click', description: '点击元素' },
      { name: 'web_type', description: '输入文本' },
      { name: 'web_screenshot', description: '截图' },
      { name: 'web_evaluate', description: '执行 JS' },
      { name: 'web_get_console', description: '获取控制台日志' },
      { name: 'web_get_requests', description: '获取网络请求' },
      { name: 'web_get_html', description: '获取元素 HTML' },
      { name: 'web_get_text', description: '获取元素文本' },
      { name: 'web_get_title', description: '获取页面标题' },
      { name: 'web_get_url', description: '获取当前 URL' },
      { name: 'web_get_style', description: '获取样式' },
      { name: 'web_hover', description: '悬浮元素' },
      { name: 'web_scroll_to', description: '滚动到位置' },
      { name: 'web_scroll_wheel', description: '滚动滚轮' },
      { name: 'web_set_viewport', description: '设置视口' },
      { name: 'web_set_geolocation', description: '设置地理位置' },
      { name: 'web_wait_navigation', description: '等待导航' },
      { name: 'web_wait_selector', description: '等待元素' },
      { name: 'web_grant_permission', description: '授予权限' },
      { name: 'web_drag', description: '拖拽' },
      { name: 'web_mouse_down', description: '按下鼠标' },
      { name: 'web_mouse_up', description: '释放鼠标' },
      { name: 'web_mouse_move', description: '移动鼠标' },
    ]},
    { name: 'desktop', label: '桌面自动化', tools: [
      { name: 'find_window', description: '查找窗口' },
      { name: 'focus_window', description: '聚焦窗口' },
      { name: 'list_windows', description: '列出窗口' },
      { name: 'mouse_move', description: '移动鼠标' },
      { name: 'mouse_click', description: '鼠标点击' },
      { name: 'mouse_down', description: '按下鼠标' },
      { name: 'mouse_up', description: '释放鼠标' },
      { name: 'scroll_wheel', description: '滚动滚轮' },
      { name: 'key_press', description: '按键' },
      { name: 'key_down', description: '按下按键' },
      { name: 'key_up', description: '释放按键' },
      { name: 'type_text', description: '输入文本' },
      { name: 'screenshot', description: '桌面截图' },
      { name: 'get_window_rect', description: '获取窗口位置' },
      { name: 'set_window_rect', description: '设置窗口位置' },
      { name: 'minimize_window', description: '最小化窗口' },
      { name: 'maximize_window', description: '最大化窗口' },
      { name: 'restore_window', description: '恢复窗口' },
    ]},
    { name: 'llm', label: 'LLM/Agent', tools: [
      { name: 'call_llm', description: '调用子 LLM' },
      { name: 'batch_llm', description: '批量 LLM 任务' },
      { name: 'add_agent', description: '创建/修改 Agent' },
      { name: 'delete_agent', description: '删除 Agent' },
      { name: 'list_agent', description: '列出 Agent' },
      { name: 'add_tool', description: '注册自定义工具' },
      { name: 'delete_tool', description: '删除自定义工具' },
      { name: 'list_tool', description: '列出自定义工具' },
      { name: 'get_tool', description: '查看工具详情' },
      { name: 'ask_user', description: '询问用户' },
    ]},
    { name: 'notes', label: '持久化存储', tools: [
      { name: 'note_write', description: '写入笔记' },
      { name: 'note_read', description: '读取笔记' },
      { name: 'note_list', description: '列出笔记' },
      { name: 'note_delete', description: '删除笔记' },
    ]},
    { name: 'history', label: '文件版本管理', tools: [
      { name: 'history_put', description: '保存版本快照' },
      { name: 'history_list', description: '列出版本历史' },
      { name: 'history_take', description: '预览版本内容' },
      { name: 'history_diff', description: '对比版本差异' },
      { name: 'history_rollback', description: '回滚到版本' },
    ]},
  ]
}

// Initialize tool selection
restoreToolSelection()
</script>

<style scoped>
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
.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.header-center {
  flex: 1;
  text-align: center;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 6px;
}
.edit-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}
.sub-tabs {
  display: flex;
  align-items: center;
  gap: 0;
}
.sub-tab {
  padding: 4px 12px;
  font-size: 13px;
  color: var(--text-secondary);
  cursor: pointer;
  user-select: none;
  transition: color 0.15s;
  border-bottom: 2px solid transparent;
}
.sub-tab:hover {
  color: var(--text-primary);
}
.sub-tab.active {
  color: var(--accent);
  font-weight: 500;
  border-bottom-color: var(--accent);
}
.agent-edit-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.agent-prompt-editor {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 13px;
}
.agent-prompt-editor :deep(textarea) {
  flex: 1;
  height: 100% !important;
  min-height: 100%;
  resize: none;
}
.tool-select-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.tool-select-header {
  padding: 8px 0;
  border-bottom: 1px solid var(--border);
  margin-bottom: 4px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.tool-group-list {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}
.tool-group {
  margin-bottom: 4px;
}
.tool-group-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 0;
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}
.tool-group-name {
  flex: 1;
}
.tool-group-info {
  font-size: 11px;
  color: var(--text-muted);
}
.tool-group-items {
  padding-left: 22px;
}
.tool-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 2px 0;
  font-size: 12px;
}
.tool-desc {
  color: var(--text-muted);
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.b-checkbox {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  font-size: 13px;
}
.b-checkbox input[type="checkbox"] {
  accent-color: var(--accent, #409eff);
}
.tab-pane-content {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 4px 16px;
}
.basic-panel {
  padding: 8px 32px;
}
.basic-fields {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.form-item {
  display: flex;
  gap: 8px;
}
.form-label {
  flex-shrink: 0;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
  min-width: 56px;
}
.form-item .b-input,
.form-item .b-select {
  flex: 1;
}
</style>