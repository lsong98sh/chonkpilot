<template>
  <div ref="configRoot" class="ide-config">
    <!-- 列表视图：不在编辑时显示 -->
    <template v-if="!editing.type">
      <el-tabs v-model="activeTab" type="border-card" class="cfg-tabs">
        <el-tab-pane label="智能体" name="agents">
          <div class="tab-toolbar">
            <span class="tab-title">智能体</span>
            <div class="tab-actions">
              <el-button size="small" @click="loadMissingAgents">
                <el-icon><Refresh /></el-icon> 从资源加载
              </el-button>
              <el-button size="small" @click="openAnalyzeDialog">
                <el-icon><MagicStick /></el-icon> AI 辅助
              </el-button>
              <el-button size="small" type="primary" @click="startAdd('agents')">
                <el-icon><Plus /></el-icon> 添加
              </el-button>
            </div>
          </div>
          <div class="table-wrap">
            <el-table :data="agentList" size="small" style="width:100%" empty-text="暂无智能体" :max-height="tableMaxHeight">
              <el-table-column label="#" type="index" width="40" />
              <el-table-column label="标题" min-width="140">
                <template #default="{ row }">
                  <span class="agent-title-cell">
                    <el-icon v-if="row._source === 'system'" class="agent-system-icon" :size="14"><StarFilled /></el-icon>
                    <el-icon v-else-if="row._source === 'llm'" class="agent-llm-icon" :size="14"><MagicStick /></el-icon>
                    <span>{{ row.title }}</span>
                  </span>
                </template>
              </el-table-column>
              <el-table-column label="使用场景" prop="useCase" min-width="200" show-overflow-tooltip />
              <el-table-column label="来源" width="70" align="center">
                <template #default="{ row }">
                  <el-tag v-if="row._source === 'llm'" size="small" type="warning">模型</el-tag>
                  <el-tag v-else-if="row._source === 'system'" size="small" type="success">系统</el-tag>
                  <el-tag v-else size="small" type="info">用户</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="150" align="center">
                <template #default="{ $index }">
                  <el-button text size="small" @click="startEdit('agents', $index)">编辑</el-button>
                  <el-button text size="small" type="danger" @click="deleteItem('agents', $index)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </el-tab-pane>

        <el-tab-pane label="工具" name="tools">
          <div class="tab-toolbar">
            <span class="tab-title">自定义工具</span>
            <div class="tab-actions">
              <el-button size="small" type="primary" @click="startAdd('tools')">
                <el-icon><Plus /></el-icon> 添加
              </el-button>
            </div>
          </div>
          <div class="table-wrap">
            <el-table :data="toolList" size="small" style="width:100%" empty-text="暂无工具" :max-height="tableMaxHeight">
              <el-table-column label="#" type="index" width="40" />
              <el-table-column label="名称" prop="name" min-width="140" />
              <el-table-column label="类型" prop="type" width="90" />
              <el-table-column label="命令" prop="command" min-width="160" />
              <el-table-column label="来源" width="70" align="center">
                <template #default="{ row }">
                  <el-tag v-if="row._source === 'llm'" size="small" type="warning">模型</el-tag>
                  <el-tag v-else size="small" type="info">用户</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="150" align="center">
                <template #default="{ $index }">
                  <el-button text size="small" @click="startEdit('tools', $index)">编辑</el-button>
                  <el-button text size="small" type="danger" @click="deleteItem('tools', $index)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </el-tab-pane>

        <!-- Notes tab -->
        <el-tab-pane label="笔记" name="notes">
          <div class="tab-toolbar">
            <span class="tab-title">笔记</span>
            <div class="tab-actions">
              <el-button size="small" type="primary" @click="noteStartAdd">
                <el-icon><Plus /></el-icon> 添加
              </el-button>
            </div>
          </div>

          <!-- Note list -->
          <div class="notes-list table-wrap">
            <el-table :data="notes" size="small" style="width:100%" empty-text="暂无笔记" :max-height="tableMaxHeight">
              <el-table-column label="#" type="index" width="50" />
              <el-table-column label="标题" prop="title" min-width="160" />
              <el-table-column label="预览" min-width="240">
                <template #default="{ row }">
                  {{ noteContentPreview(row.content) }}
                </template>
              </el-table-column>
              <el-table-column label="操作" width="150" align="center">
                <template #default="{ $index }">
                  <el-button text size="small" @click="noteStartEdit(notes[$index])">编辑</el-button>
                  <el-button text size="small" type="danger" @click="noteDelete($index)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </el-tab-pane>

        <!-- Security tab -->
        <el-tab-pane label="安全" name="security">
          <div class="tab-toolbar">
            <span class="tab-title">信任目录</span>
            <div class="tab-actions">
              <el-button size="small" type="primary" @click="addSecurityEntry">
                <el-icon><Plus /></el-icon> 添加
              </el-button>
            </div>
          </div>
          <div class="table-wrap">
            <el-table :data="securityList" size="small" style="width:100%" empty-text="暂无信任目录" :max-height="tableMaxHeight">
              <el-table-column label="No" type="index" width="50" />
              <el-table-column label="信任目录" min-width="300">
                <template #default="{ $index }">
                  <div class="security-dir-row">
                    <el-input v-model="securityList[$index].dir" size="small" placeholder="C:\path\to\trusted" />
                    <el-button size="small" @click="selectSecurityDir($index)">...</el-button>
                  </div>
                </template>
              </el-table-column>
              <el-table-column label="读写" width="80" align="center">
                <template #default="{ $index }">
                  <el-checkbox v-model="securityList[$index].writable" size="small" />
                </template>
              </el-table-column>
              <el-table-column label="操作" width="70" align="center">
                <template #default="{ $index }">
                  <el-button text size="small" type="danger" @click="deleteSecurityEntry($index)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </el-tab-pane>

        <!-- 代码索引 -->
        <el-tab-pane label="代码索引" name="codebase">
          <div class="tab-toolbar">
            <span class="tab-title">代码索引</span>
          </div>
          <el-form label-position="top" size="small">
            <el-form-item>
              <div class="codebase-toggle">
                <span>启用代码索引（消耗 Token）</span>
                <el-switch v-model="codebaseIndexEnabled" />
              </div>
              <div class="codebase-reminder">启用后 write_file/replace 自动触发 LLM 分析，并注入 query_codebase 工具</div>
            </el-form-item>
            <el-form-item label="索引文件扩展名">
              <el-input v-model="codebaseIndexExtensions" placeholder=".go,.js,.ts,.vue" />
              <span class="form-hint">逗号分隔，支持 .c .cpp .h .java 等</span>
            </el-form-item>
            <el-divider />
            <el-form-item>
              <div class="codebase-status">
                <div class="status-numbers">
                  <span>待索引: <strong>{{ pending }}</strong></span>
                  <span>已完成: <strong>{{ files }}</strong></span>
                  <span>合计: <strong>{{ total }}</strong></span>
                  <span v-if="failed > 0" class="status-failed">重试中: <strong>{{ failed }}</strong></span>
                  <span v-if="failedExhausted > 0" class="status-exhausted">错误: <strong>{{ failedExhausted }}</strong></span>
                </div>
                <el-progress
                  :percentage="progress"
                  :status="ok ? 'success' : (failedExhausted > 0 ? 'exception' : undefined)"
                  :stroke-width="16"
                  :text-inside="false"
                  :show-text="false"
                />
                <div class="status-meta">
                  <span v-if="cbLoading" class="status-loading">获取中...</span>
                  <span v-else-if="!ok || failedExhausted > 0" class="status-active">
                    ⏳ {{ indexing }} 正在索引 · {{ pending }} 待处理
                    <template v-if="failed > 0"> · {{ failed }} 重试中</template>
                    <template v-if="failedExhausted > 0"> · ❌ {{ failedExhausted }} 错误</template>
                  </span>
                  <span v-else class="status-done">✓ {{ files }} 个文件已索引</span>
                </div>
              </div>
            </el-form-item>
            <el-form-item>
              <el-button type="danger" size="small" @click="clearCodebaseIndex" :disabled="!codebaseIndexEnabled">
                清空索引
              </el-button>
              <el-button type="primary" size="small" @click="reindexCodebase" :disabled="!codebaseIndexEnabled" :loading="reindexing">
                重新索引
              </el-button>
              <el-button v-if="failed > 0 || failedExhausted > 0" type="warning" size="small" @click="resetFailedItems">
                重试失败项
              </el-button>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 上下文压缩 -->
        <el-tab-pane label="上下文压缩" name="context">
          <el-form label-position="top" size="small" class="context-form">
            <el-row :gutter="12">
              <el-col :span="8">
                <el-form-item label="保留完整对话内容的轮次（默认6）">
                  <el-input-number v-model="keepFullTurns" :min="1" :max="50" :step="1" />
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="简化区 Token 压缩阈值（默认 80000）">
                  <el-input-number v-model="compressTokenThreshold"
                    :min="10000" :max="500000" :step="10000" />
                </el-form-item>
              </el-col>
            </el-row>
            <p class="form-description">
              保留最近的 <strong>N</strong> 轮为完整内容 → 之前的轮次简化为工具摘要 → 当简化区总 token 数超过阈值时，自动压缩为 LLM 摘要。
              简化区的计算方式为：简化区所有消息字符数 / 4 + 已有摘要字符数 / 4。<br>
              示例：N=6, 阈值=80000 → 第 1-6 轮完整保留，第 7+ 轮简化，简化区超过约 32 万字符时触发压缩。
              压缩在每轮结束后异步执行，同一会话的压缩互斥进行，不会重复触发。
            </p>
          </el-form>
        </el-tab-pane>

        <!-- Prompts tab (merged: System Prompt / Tool Usage / Summary) -->
        <el-tab-pane label="提示词" name="prompts">
          <div class="tab-toolbar">
            <div class="prompt-tab-links">
              <el-radio-group v-model="promptSubTab" size="small">
                <el-radio-button value="system">系统提示词</el-radio-button>
                <el-radio-button value="tool">工具使用说明</el-radio-button>
                <el-radio-button value="summary">摘要提示词</el-radio-button>
              </el-radio-group>
            </div>
            <div class="tab-actions">
              <el-button size="small" type="primary" @click="saveCurrentPrompt">保存</el-button>
              <el-button size="small" @click="resetCurrentPrompt">恢复默认</el-button>
            </div>
          </div>
          <div class="prompt-edit-area">
            <p class="prompt-desc">{{ promptDesc }}</p>
            <el-input
              v-model="currentPromptText"
              type="textarea"
              placeholder="加载中..."
              class="prompt-editor"
            />
          </div>
        </el-tab-pane>
      </el-tabs>
    </template>

    <!-- 编辑表单视图：编辑时显示，撑满100% -->
    <div v-else class="edit-view">
      <!-- Agents 编辑表单 -->
      <template v-if="editing.type === 'agents'">
        <div class="edit-header">
          <el-button text size="small" @click="cancelEdit">
            <el-icon><ArrowLeft /></el-icon> 返回列表
          </el-button>
          <span class="edit-title">{{ editing.index === -1 ? '添加智能体' : '编辑智能体' }}</span>
          <div class="edit-header-actions">
            <el-button v-if="editing.index !== -1 && editing.data._source" size="small" @click="restoreAgentById(editing.index)">
              <el-icon><Refresh /></el-icon> 恢复
            </el-button>
            <el-button size="small" :loading="optimizing" :disabled="optimizing" @click="aiOptimizeAgent">
              <el-icon><Lightning /></el-icon> AI 优化
            </el-button>
            <el-button size="small" type="primary" @click="confirmEdit">保存</el-button>
          </div>
        </div>
        <div class="agent-edit-body">
          <el-row :gutter="12" class="agent-edit-row">
            <el-col :span="6">
              <el-input v-model="editing.data.title" placeholder="标题" size="small" />
            </el-col>
            <el-col :span="8">
              <el-input v-model="editing.data.useCase" placeholder="使用场景" size="small" />
            </el-col>
            <el-col :span="10">
              <el-select v-model="editing.data.llmRef" placeholder="LLM（继承父进程）" size="small" clearable style="width:100%">
                <el-option label="（继承父进程）" value="" />
                <el-option v-for="llm in llmOptions" :key="llm.name" :label="llm.label" :value="llm.name" />
              </el-select>
            </el-col>
          </el-row>
          <el-input
            v-model="editing.data.prompt"
            type="textarea"
            placeholder="You are an architecture expert..."
            class="agent-prompt-editor"
          />
        </div>
      </template>

      <!-- Tools 编辑表单 -->
      <template v-if="editing.type === 'tools'">
        <div class="edit-header">
          <el-button text size="small" @click="cancelEdit">
            <el-icon><ArrowLeft /></el-icon> 返回列表
          </el-button>
          <span class="edit-title">{{ editing.index === -1 ? '添加工具' : '编辑工具' }}</span>
          <div class="edit-header-actions">
            <el-button size="small" type="primary" @click="confirmEdit">保存</el-button>
          </div>
        </div>
        <div class="edit-body">
          <el-form label-position="top" size="small">
            <el-form-item label="名称">
              <el-input v-model="editing.data.name" placeholder="工具名称" />
            </el-form-item>
            <el-form-item label="功能描述">
              <el-input v-model="editing.data.description" type="textarea" :rows="3" placeholder="描述此工具的功能" />
            </el-form-item>
            <el-form-item label="参数说明及样例">
              <el-input v-model="editing.data.parameters" type="textarea" :rows="3" placeholder="参数说明和使用样例" />
            </el-form-item>
            <el-form-item label="脚本类型">
              <el-select v-model="editing.data.type" style="width:100%">
                <el-option label="Python" value="python" />
                <el-option label="JavaScript" value="js" />
                <el-option label="PowerShell" value="powershell" />
                <el-option label="Shell" value="sh" />
                <el-option label="Batch" value="bat" />
                <el-option label="可执行文件" value="executable" />
                <el-option label="API 接口" value="api" />
                <el-option label="MCP" value="mcp" />
              </el-select>
            </el-form-item>
            <template v-if="editing.data.type === 'mcp'">
              <el-form-item label="MCP Server">
                <el-select v-model="editing.data.server_url" style="width:100%" placeholder="选择已配置的 MCP Server">
                  <el-option
                    v-for="srv in mcpServerList"
                    :key="srv.url"
                    :label="srv.name + (srv.url ? ' (' + srv.url + ')' : '')"
                    :value="srv.url"
                  />
                </el-select>
                <span v-if="mcpServerList.length === 0" class="form-hint">请先在 设置 → 全局 → MCP 中配置 Server</span>
              </el-form-item>
              <el-form-item label="MCP 工具名称">
                <el-input v-model="editing.data.mcp_tool_name" placeholder="MCP Server 暴露的工具名，如 query_database" />
                <span class="form-hint">工具名称由 MCP Server 定义，需联系 Server 文档确认</span>
              </el-form-item>
            </template>
            <template v-else>
              <el-form-item label="脚本内容">
                <!-- executable: file selector -->
                <div v-if="editing.data.type === 'executable'" class="file-select-row">
                  <el-input v-model="editing.data.command" placeholder="选择可执行文件..." readonly />
                  <el-button @click="selectExecutableFile">选择文件</el-button>
                </div>
                <!-- api: URL input -->
                <el-input
                  v-else-if="editing.data.type === 'api'"
                  v-model="editing.data.command"
                  type="textarea"
                  :rows="2"
                  placeholder="接口 URL（参数说明中填写 param 和 body 值）"
                />
                <!-- other types: Monaco editor -->
                <ToolScriptEditor
                  v-if="editing.data.type !== 'executable' && editing.data.type !== 'api'"
                  v-model="editing.data.command"
                  :language="editing.data.type"
                  :key="'toolscript-' + editing.data.type"
                />
              </el-form-item>
            </template>
          </el-form>
        </div>
      </template>

      <!-- Notes 编辑表单 -->
      <template v-if="editing.type === 'notes'">
        <div class="edit-header">
          <el-button text size="small" @click="noteCancel">
            <el-icon><ArrowLeft /></el-icon> 返回列表
          </el-button>
          <span class="edit-title">{{ editing.index === -1 ? '添加笔记' : '编辑笔记' }}</span>
          <div class="edit-header-actions">
            <el-button v-if="editing.data.content" size="small" :loading="optimizingNote" :disabled="optimizingNote" @click="aiOptimizeNote">
              <el-icon><Lightning /></el-icon> AI 优化
            </el-button>
            <el-button size="small" type="primary" :loading="noteSaving" @click="noteSave">保存</el-button>
          </div>
        </div>
        <div class="agent-edit-body">
          <el-input v-model="editing.data.title" placeholder="标题" size="small" class="note-title-input" />
          <el-input
            v-model="editing.data.content"
            type="textarea"
            :rows="15"
            placeholder="在此输入笔记内容..."
            class="note-content-editor"
          />
        </div>
      </template>
    </div>

    <!-- AnalyzeDialog (AI Assist) -->
    <AnalyzeDialog ref="analyzeDialogRef" />
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { Plus, MagicStick, Lightning, ArrowLeft, Refresh, StarFilled } from '@element-plus/icons-vue'
import { useCodebaseStatus } from '../../composables/useCodebaseStatus'
import { useConfig } from '../../composables/useConfig'
import {
  getProjectAgents, saveProjectAgents,
  getProjectTools, saveProjectTools,
  getProjectSecurity, saveProjectSecurity,
  getPrompt, setPrompt, optimizeAgentPrompt,
  getUserMCP, restoreAgent, loadMissingAgentsFromResource,
  getAllConfig, setConfig,
} from '../../api/config'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getNotes, saveNote, deleteNote } from '../../api/note'
import { PickFolder, PickExecutableFile } from '../../../wailsjs/go/main/App'
import AnalyzeDialog from '../config/AnalyzeDialog.vue'
import ToolScriptEditor from './ToolScriptEditor.vue'

const activeTab = ref('agents')
const agentList = ref([])
const toolList = ref([])
const mcpServerList = ref([])
const securityList = ref([])
const systemPromptText = ref('')
const toolUsageText = ref('')
const summaryPromptText = ref('')
const promptSubTab = ref('system')
const analyzeDialogRef = ref(null)
const optimizing = ref(false)

// 代码索引 & 上下文压缩 settings (project-level)
const codebaseIndexEnabled = ref(false)
const codebaseIndexExtensions = ref('.go,.js,.ts,.jsx,.tsx,.vue,.py,.rs,.java,.c,.cpp,.h,.hpp,.cs,.rb,.php,.swift,.kt')
const keepFullTurns = ref(6)
const compressTokenThreshold = ref(80000)
const tableMaxHeight = ref(600)
const configRoot = ref(null)

// Codebase status — reactive, auto-updated by backend push
const {
  files, pending, indexing, failed, failedExhausted,
  total, progress, ok, loading: cbLoading, teardown: cbTeardown, resetFailed,
} = useCodebaseStatus()

// Project config — singleton with config:refresh bridge event
const { config, teardown: configTeardown } = useConfig()
const reindexing = ref(false)

async function reindexCodebase() {
  try {
    reindexing.value = true
    const count = await window.go.main.App.ReindexCodebase()
    ElMessage.success(`重新索引完成，已入队 ${count} 个文件`)
  } catch (e) {
    ElMessage.error('重新索引失败: ' + (e.message || ''))
  } finally {
    reindexing.value = false
  }
}

async function resetFailedItems() {
  try {
    await resetFailed()
    ElMessage.success('失败项已重置为待索引')
  } catch (e) {
    ElMessage.error('重置失败: ' + (e.message || ''))
  }
}

async function clearCodebaseIndex() {
  try {
    await window.go.main.App.ClearCodebaseIndex()
    ElMessage.success('索引已清空')
  } catch (e) {
    ElMessage.error('清空失败: ' + (e.message || ''))
  }
}

async function saveCodebaseConfig() {
  try {
    await setConfig('codebase_index.enabled', String(codebaseIndexEnabled.value))
    await setConfig('codebase_index.extensions', codebaseIndexExtensions.value)
  } catch (e) {
    console.warn('[IDEConfig] Failed to save codebase config:', e)
  }
}

async function saveContextConfig() {
  try {
    await setConfig('keep_full_turns', String(keepFullTurns.value))
    await setConfig('compress_token_threshold', String(compressTokenThreshold.value))
  } catch (e) {
    console.warn('[IDEConfig] Failed to save context config:', e)
  }
}

const llmOptions = computed(() => {
  if (!config.value?.llms) return []
  return config.value.llms.map(llm => ({
    name: llm.name || '',
    label: llm.name ? `${llm.name} (${llm.protocol || llm.model || ''})` : '',
  })).filter(l => l.name)
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

const emptyAgent = () => ({
  title: '', useCase: '', prompt: '', _source: '', llmRef: '',
})

const emptyTool = () => ({
  name: '', description: '', parameters: '', type: 'python', command: '', _source: 'user',
})

const editing = reactive({
  type: '', // 'agents' | 'tools' | ''
  index: -1,
  data: {},
})

// Notes state
const notes = ref([])
const noteSaving = ref(false)
const optimizingNote = ref(false)

async function notesLoad() {
  try {
    const res = await getNotes()
    notes.value = (res && res.notes) || []
  } catch (_) { notes.value = [] }
}

function noteStartAdd() {
  cancelEdit()
  editing.type = 'notes'
  editing.index = -1
  editing.data = { title: '', content: '' }
}

function noteStartEdit(note) {
  cancelEdit()
  const idx = notes.value.findIndex(n => n.title === note.title && n.content === note.content)
  editing.type = 'notes'
  editing.index = idx
  editing.data = { title: note.title, content: note.content }
}

function noteCancel() {
  cancelEdit()
}

async function noteSave() {
  if (!editing.data.title.trim()) {
    ElMessage.warning('标题不能为空')
    return
  }
  noteSaving.value = true
  try {
    await saveNote(editing.data.title.trim(), editing.data.content || '')
    ElMessage.success('已保存')
    noteCancel()
    await notesLoad()
  } catch (e) {
    ElMessage.error('保存失败: ' + (e.message || ''))
  } finally {
    noteSaving.value = false
  }
}

async function noteDelete(index) {
  const note = notes.value[index]
  if (!note) return
  try {
    await ElMessageBox.confirm('删除此笔记？', '确认', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteNote(note.title)
    notes.value.splice(index, 1)
    ElMessage.success('已删除')
  } catch (e) {
    ElMessage.error('删除失败: ' + (e.message || ''))
  }
}

function noteContentPreview(text) {
  if (!text) return ''
  return text.length > 80 ? text.slice(0, 80) + '...' : text
}

async function loadAll() {
  try {
    const agentRes = await getProjectAgents()
    agentList.value = agentRes.agents || []
  } catch (_) { agentList.value = [] }
  try {
    const toolRes = await getProjectTools()
    toolList.value = toolRes.tools || []
  } catch (_) { toolList.value = [] }
  try {
    const secRes = await getProjectSecurity()
    securityList.value = secRes.entries || []
  } catch (_) { securityList.value = [] }
  // Load prompts
  try {
    const sp = await getPrompt('system_prompt')
    systemPromptText.value = sp.value || ''
  } catch (e) { console.warn('[IDEConfig] Failed to load system_prompt:', e) }
  try {
    const tp = await getPrompt('tool_usage_prompt')
    toolUsageText.value = tp.value || ''
  } catch (e) { console.warn('[IDEConfig] Failed to load tool_usage:', e) }
  try {
    const sp = await getPrompt('summary_prompt')
    summaryPromptText.value = sp.value || ''
  } catch (e) { console.warn('[IDEConfig] Failed to load summary:', e) }
  // Load project-level configs (codebase index, context compression)
  try {
    const res = await getAllConfig()
    const c = res.config || res
    if (c['codebase_index.enabled'] !== undefined) codebaseIndexEnabled.value = c['codebase_index.enabled'] === 'true'
    if (c['codebase_index.extensions']) codebaseIndexExtensions.value = c['codebase_index.extensions']
    if (c['keep_full_turns'] !== undefined) keepFullTurns.value = parseInt(c['keep_full_turns']) || 6
    if (c['compress_token_threshold'] !== undefined) compressTokenThreshold.value = parseInt(c['compress_token_threshold']) || 80000
  } catch (_) {}
}

async function saveAgents() {
  try {
    await saveProjectAgents({ agents: agentList.value })
  } catch (e) {
    ElMessage.error('保存智能体失败: ' + (e.message || ''))
  }
}

async function saveTools() {
  try {
    await saveProjectTools(toolList.value)
  } catch (e) {
    ElMessage.error('保存工具失败: ' + (e.message || ''))
  }
}

async function saveSecurity() {
  try {
    await saveProjectSecurity(securityList.value)
  } catch (e) {
    ElMessage.error('保存安全配置失败: ' + (e.message || ''))
  }
}

function addSecurityEntry() {
  securityList.value.push({ dir: '', writable: false })
  saveSecurity()
}

async function selectSecurityDir(index) {
  try {
    const res = await PickFolder()
    if (res?.path) {
      securityList.value[index].dir = res.path
      saveSecurity()
    }
  } catch (_) {
    const path = prompt('输入目录路径:')
    if (path) {
      securityList.value[index].dir = path
      saveSecurity()
    }
  }
}

async function deleteSecurityEntry(index) {
  try {
    await ElMessageBox.confirm('删除此信任目录？', '确认', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  securityList.value.splice(index, 1)
  saveSecurity()
}

async function loadMCPServers() {
  try {
    const servers = await getUserMCP()
    mcpServerList.value = servers || []
  } catch (_) {
    mcpServerList.value = []
  }
}

async function saveCurrentPrompt() {
  const key = promptKeyMap[promptSubTab.value]
  const textRef = promptTextMap[promptSubTab.value]
  if (!key || !textRef) return
  try {
    await setPrompt(key, textRef.value)
    ElMessage.success('提示词已保存')
  } catch (e) {
    ElMessage.error('保存失败: ' + (e.message || ''))
  }
}

async function resetCurrentPrompt() {
  const key = promptKeyMap[promptSubTab.value]
  const textRef = promptTextMap[promptSubTab.value]
  if (!key || !textRef) return
  try {
    // Write empty string to DB, then reload default from server
    await setPrompt(key, '')
    const sp = await getPrompt(key)
    textRef.value = sp.value || ''
    ElMessage.success('已恢复默认提示词')
  } catch (e) {
    ElMessage.error('恢复失败: ' + (e.message || ''))
  }
}

function startAdd(type) {
  cancelEdit()
  if (type === 'agents') editing.data = emptyAgent()
  else {
    if (type === 'tools') loadMCPServers()
    editing.data = emptyTool()
  }
  editing.type = type
  editing.index = -1
}

function startEdit(type, index) {
  editing.type = type
  editing.index = index
  if (type === 'agents') editing.data = { ...agentList.value[index] }
  else {
    if (type === 'tools') loadMCPServers()
    const d = { ...toolList.value[index] }
    // Normalize parameters to string for textarea
    if (d.parameters !== undefined && d.parameters !== null && typeof d.parameters !== 'string') {
      d.parameters = JSON.stringify(d.parameters, null, 2)
    }
    if (!d.parameters) d.parameters = ''
    if (!d.description) d.description = ''
    editing.data = d
  }
}

function cancelEdit() {
  editing.type = ''
  editing.index = -1
  editing.data = {}
  delete editing._testing
  delete editing._testResult
  delete editing._testOk
}

function confirmEdit() {
  const d = { ...editing.data }
  if (editing.type === 'agents') {
    if (editing.index === -1) agentList.value.push(d)
    else agentList.value[editing.index] = d
    saveAgents()
  } else {
    if (editing.index === -1) toolList.value.push(d)
    else toolList.value[editing.index] = d
    saveTools()
  }
  cancelEdit()
  ElMessage.success('已保存')
}

async function deleteItem(type, index) {
  const label = type === 'agents' ? '智能体' : '工具'
  try {
    await ElMessageBox.confirm(`删除此${label}？`, '确认', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return // cancelled
  }
  if (type === 'agents') {
    agentList.value.splice(index, 1)
    saveAgents()
  } else {
    toolList.value.splice(index, 1)
    saveTools()
  }
  if (editing.type === type && editing.index === index) cancelEdit()
  ElMessage.success('已删除')
}

async function restoreAgentById(index) {
  const agent = agentList.value[index]
  if (!agent || !agent._source || !agent.id) return
  try {
    const res = await restoreAgent(agent.id)
    ElMessage.success(`智能体 "${res.title || ''}" 已从资源恢复`)
    await loadAll()
  } catch (e) {
    ElMessage.error('恢复失败: ' + (e.message || ''))
  }
}

async function loadMissingAgents() {
  try {
    const res = await loadMissingAgentsFromResource()
    const count = res.inserted || 0
    if (count > 0) {
      await loadAll()
      ElMessage.success(`已从资源加载 ${count} 个缺失的智能体`)
    } else {
      ElMessage.info('所有资源智能体已存在')
    }
  } catch (e) {
    ElMessage.error('加载失败: ' + (e.message || ''))
  }
}

async function selectExecutableFile() {
  try {
    const res = await PickExecutableFile()
    if (res?.path) {
      editing.data.command = res.path
    } else {
      console.warn('[IDEConfig] File dialog returned no path:', res)
    }
  } catch (err) {
    console.error('[IDEConfig] File dialog bridge error:', err)
    alert('文件选择对话框出错: ' + (err.message || err))
    const path = prompt('输入可执行文件路径:')
    if (path) editing.data.command = path
  }
}

function openAnalyzeDialog() {
  analyzeDialogRef.value?.open()
}

function aiOptimizeAgent() {
  if (!editing.data || !editing.data.prompt) {
    ElMessage.warning('请先输入提示词')
    return
  }
  if (optimizing.value) return
  optimizing.value = true

  ElMessage.info('正在优化提示词...')

  let optimizedText = ''
  optimizeAgentPrompt(
    { title: editing.data.title, useCase: editing.data.useCase, prompt: editing.data.prompt },
    // onToken: append streaming content to editor
    (content) => {
      optimizedText += content
      editing.data.prompt = optimizedText
    },
    // onDone: update prompt (no auto-save)
    (prompt) => {
      optimizing.value = false
      if (prompt) {
        editing.data.prompt = prompt
        ElMessage.success('提示词优化成功')
      }
    },
    // onError
    (errMsg) => {
      optimizing.value = false
      ElMessage.error('优化失败: ' + errMsg)
    },
  )
}

function aiOptimizeNote() {
  const note = editing.data
  if (!note || !note.content) {
    ElMessage.warning('请先输入笔记内容')
    return
  }
  if (optimizingNote.value) return
  optimizingNote.value = true

  ElMessage.info('正在优化笔记...')

  let optimizedText = ''
  optimizeAgentPrompt(
    { title: note.title || 'Note', useCase: 'Improve this note', prompt: note.content },
    (content) => {
      optimizedText += content
      editing.data.content = optimizedText
    },
    (prompt) => {
      optimizingNote.value = false
      if (prompt) {
        note.content = prompt
        editing.data.content = prompt
        ElMessage.success('笔记优化成功')
      }
    },
    (errMsg) => {
      optimizingNote.value = false
      ElMessage.error('优化失败: ' + errMsg)
    },
  )
}

// Listen for config:refresh events (via useConfig singleton)
let unsub = null
let resObs = null
onMounted(() => {
  try {
    loadAll()
    // When project config updates (from useConfig singleton), reload all
    unsub = watch(config, () => {
      loadAll()
    }, { deep: true })

    // Observe the root for height changes to set table max-height
    if (configRoot.value) {
      resObs = new ResizeObserver(([entry]) => {
        // content height minus tabs header (~40px) minus toolbar (~40px) minus padding (16px)
        const h = entry.contentRect.height - 96
        if (h > 100) tableMaxHeight.value = Math.floor(h)
      })
      resObs.observe(configRoot.value)
    }
  } catch (err) {
    console.error('[IDEConfig] onMounted error:', err)
  }
})

// Load Notes when their tab is selected
watch(activeTab, (tab) => {
  if (tab === 'notes') {
    notesLoad()
  }
})

// Auto-save codebase index settings on change
watch(codebaseIndexEnabled, async (val) => {
  await saveCodebaseConfig()
  // Start the indexer worker if enabling
  if (val && window.go?.main?.App?.StartCodebaseIndex) {
    window.go.main.App.StartCodebaseIndex().catch(() => {})
  }
})
watch(codebaseIndexExtensions, () => {
  saveCodebaseConfig()
})

// Auto-save context compression settings on change
watch(keepFullTurns, () => {
  saveContextConfig()
})
watch(compressTokenThreshold, () => {
  saveContextConfig()
})

onUnmounted(() => {
  try {
    if (unsub) unsub()
    if (resObs) resObs.disconnect()
    cbTeardown()
    configTeardown()
  } catch (_) {}
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

.ide-config :deep(.el-tabs--border-card) {
  border: none;
  box-shadow: none;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.ide-config :deep(.el-tabs--border-card > .el-tabs__content) {
  flex: 1;
  overflow: hidden;
  padding: 0 8px;
}

.ide-config :deep(.el-tabs--border-card > .el-tabs__header) {
  background: var(--bg-tertiary);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

/* Each tab-pane fills remaining height as flex column */
.ide-config :deep(.el-tab-pane) {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* Table wrapper fills remaining space, constraints el-table height */
.table-wrap {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/* Prompts tab: textarea fills remaining space */
.prompt-edit-area {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.prompt-edit-area .prompt-desc {
  flex-shrink: 0;
}
.prompt-edit-area .prompt-editor {
  flex: 1;
  min-height: 0;
}
.prompt-edit-area :deep(.el-textarea__inner) {
  height: 100% !important;
  resize: vertical;
}

/* Context compression tab: unified font */
.context-form .form-description {
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-secondary);
}

.tab-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  gap: 8px;
  flex-shrink: 0;
}

.tab-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}

.tab-actions {
  display: flex;
  gap: 6px;
}

.header-spacer {
  flex: 1;
  min-width: 0;
}

/* 编辑视图 - 撑满100% */
.edit-view {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.edit-header {
  flex-shrink: 0;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  display: flex;
  align-items: center;
  justify-content: space-between;
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

/* Agent edit body — flex column like notes editor */
.agent-edit-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 16px;
  overflow: hidden;
}
.agent-edit-row {
  flex-shrink: 0;
}
.agent-prompt-editor {
  flex: 1;
  min-height: 0;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 13px;
}
.agent-prompt-editor :deep(.el-textarea__inner) {
  height: 100% !important;
  resize: vertical;
}

.file-select-row {
  display: flex;
  gap: 8px;
}

.file-select-row .el-input {
  flex: 1;
}

.security-dir-row {
  display: flex;
  gap: 4px;
  align-items: center;
}

.security-dir-row .el-input {
  flex: 1;
}

.prompt-tab-links {
  display: flex;
  align-items: center;
}

.prompt-tab-links :deep(.el-radio-group) {
  gap: 0;
}

.prompt-tab-links :deep(.el-radio-button__inner) {
  font-size: 12px;
  padding: 4px 12px;
  border-radius: 0;
  border: 1px solid var(--border);
}

.prompt-tab-links :deep(.el-radio-button:first-child .el-radio-button__inner) {
  border-radius: 4px 0 0 4px;
}

.prompt-tab-links :deep(.el-radio-button:last-child .el-radio-button__inner) {
  border-radius: 0 4px 4px 0;
}

.prompt-edit-area {
  padding: 8px 0;
}

.prompt-desc {
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 8px;
}

.test-ok { color: var(--el-color-success); font-weight: 500; }
.test-fail { color: var(--el-color-danger); font-weight: 500; }

.agent-title-cell {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.agent-system-icon {
  color: var(--el-color-warning);
}
.agent-llm-icon {
  color: var(--el-color-primary);
}

.codebase-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
  font-weight: 500;
}
.codebase-toggle span {
  white-space: nowrap;
}
.codebase-reminder {
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
}
.codebase-status {
  width: 100%;
}
.codebase-status .status-numbers {
  display: flex;
  gap: 16px;
  font-size: 13px;
  margin-bottom: 6px;
}
.codebase-status .status-numbers span {
  color: var(--text-muted);
}
.codebase-status .status-numbers strong {
  color: var(--text-primary);
}
.codebase-status .status-meta {
  font-size: 12px;
  margin-top: 4px;
}
.status-loading { color: var(--text-muted); }
.status-active  { color: var(--el-color-warning); }
.status-done    { color: var(--el-color-success); }
.status-failed  { color: var(--el-color-warning); font-weight: 500; }
.status-exhausted { color: var(--el-color-danger); font-weight: 500; }

.prompt-editor {
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 13px;
}

/* Notes editor styles */
.notes-list {
  flex: 1;
  min-height: 0;
}

.note-title-input {
  flex-shrink: 0;
}

.note-content-editor {
  flex: 1;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 13px;
}
.note-content-editor :deep(.el-textarea__inner) {
  height: 100% !important;
  resize: vertical;
}
</style>
